<p align="right">
  <a href="README.md">English</a> | <a href="README.zh.md">中文</a>
</p>

# Node API — AI 模型网关

[![Go Version](https://img.shields.io/badge/Go-1.25.1-blue)](https://go.dev/)
[![License](https://img.shields.io/badge/License-AGPLv3-red.svg)](LICENSE)
[![PostgreSQL](https://img.shields.io/badge/DB-PostgreSQL-336791)](https://postgresql.org)
[![Redis](https://img.shields.io/badge/Cache-Redis-DC382D)](https://redis.io)

**Node API** 是一个高性能、生产就绪的 AI 模型网关，**核心是一套健壮的计费引擎**。对外提供统一的 API 接口，对内将请求动态路由到多个上游提供商（OpenAI、Anthropic、Gemini 等）。与简单的代理不同，Node API 拥有原子预扣费+多退少补结算、实时流式 Token 统计+断连保护、双余额体系（grant + cash）、自动渠道故障切换和 Redis 驱动的限流——一切设计都是为了防止透支、杜绝漏计并确保高可用。

属于 [AIHop](https://github.com/aihop) 生态系统。

---

## 特性

### 🎯 统一 API 接口

对外暴露单一 OpenAI 兼容端点（`/v1/chat/completions`、`/v1/models` 等），后端透明路由到不同上游提供商。任何兼容 OpenAI SDK 的客户端都能无缝接入。

### 💰 原子计费引擎——不透支、不漏计

- **预扣费机制**：转发请求前，通过 Redis Lua 脚本原子地预扣一笔预估最大费用。
- **多退少补**：请求结束后根据实际用量结算——多扣的退还，少扣的补收。
- **双余额体系**：`grant_balance`（订阅赠送额度，有有效期）和 `cash_balance`（充值余额，永不过期），扣费时优先消耗赠送额度。
- **异步持久化**：账单通过 [Asynq](https://github.com/hibiken/asynq) 异步写入 PostgreSQL，支持指数退避自动重试。

### 🔀 多厂商协议适配

| 厂商 | 请求改写 | SSE 翻译 |
|------|:--------:|:--------:|
| OpenAI | 透传 | 透传 |
| Anthropic | → `/v1/messages` | `content_block_delta` → `choices[0].delta` |
| Gemini | → `/v1beta/models` | 原生 → OpenAI chunk 格式 |

添加新厂商只需实现 `ProviderAdapter` 接口。

### ⚡ 流式 Token 计量

`TallyReader` 拦截 SSE 流，实现：
1. 优先从最后一个 chunk 的 `usage` 字段获取官方准确用量（`stream_options: {"include_usage": true}`）
2. 兜底使用 `tiktoken-go` 实时分词统计
3. **客户端断连自动止损**：客户端断开后立即取消上游请求，已产生的 token 照常结算——零浪费

### 🛡️ 渠道池与自动故障切换

- 支持加权轮询调度多个上游 API Key
- 上游返回 `429` 或 `5xx` 时，透明重试下一个可用渠道
- 通过 Redis Pub/Sub 热更新渠道和模型配置（无需重启）

### 🚦 Redis 限流

- **RPM**（每分钟请求数）— 滑动窗口
- **TPM**（每分钟 Token 数）— 滑动窗口
- Lua 原子计数器保证高并发下的正确性

### 📊 Prometheus 监控

内置 Prometheus 指标：请求数、延迟分布、Token 消耗量。

---

## 架构

```
┌──────────────┐     ┌─────────────────────────────────────┐     ┌──────────────┐
│   客户端      │────▶│         Node API 网关              │────▶│   上游厂商    │
│ (OpenAI SDK) │     │                                     │     │              │
└──────────────┘     │  ┌─────────┐  ┌──────────┐  ┌────┐ │     │ ┌──────────┐ │
                     │  │ 鉴权    │─▶│ 限流     │─▶│    │ │     │ │ OpenAI   │ │
                     │  │ + 预扣费│  │ (RPM/   │  │    │ │────▶│ ├──────────┤ │
                     │  └─────────┘  │  TPM)   │  │    │ │     │ │Anthropic │ │
                     │               └──────────┘  │    │ │────▶│ ├──────────┤ │
                     │                             │    │ │     │ │ Gemini   │ │
                     │  ┌─────────┐  ┌──────────┐  │    │ │────▶│ ├──────────┤ │
                     │  │ Tally   │◀─│ 反向代理 │◀─│    │ │     │ │  ...     │ │
                     │  │ Reader  │  │ + 故障   │  │    │ │     │ └──────────┘ │
                     │  │ (SSE)   │  │ 切换     │  └────┘ │     └──────────────┘
                     │  └─────────┘  └──────────┘         │
                     │                                     │
                     │  ┌──────────┐  ┌──────────────────┐ │
                     │  │ Asynq    │  │  Redis           │ │
                     │  │ Worker   │  │  (余额、限流、   │ │
                     │  │ (计费)   │  │   任务队列)      │ │
                     │  └──────────┘  └──────────────────┘ │
                     └─────────────────────────────────────┘
                                  │
                                  ▼
                          ┌──────────────┐
                          │  PostgreSQL   │
                          │  (账单、配置)  │
                          └──────────────┘
```

### 项目结构

```
├── cmd/api/main.go          # 程序入口，依赖注入，路由组装
├── cmd/migrate/main.go      # 手动迁移执行器
├── internal/
│   ├── adapter/             # 协议适配器（Anthropic/Gemini → OpenAI 格式）
│   ├── billing/             # Redis Lua 脚本、预扣费/退款/结算
│   ├── channel/             # 上游渠道池管理与负载均衡
│   ├── db/                  # sqlc 生成的数据访问层（类型安全）
│   ├── middleware/          # 鉴权、限流、请求拦截
│   ├── config/              # 应用配置（viper + YAML + 环境变量）
│   ├── metrics/             # Prometheus 埋点
│   ├── worker/              # 异步账单写入（Asynq）
│   └── utils/               # OpenAI 兼容错误格式封装
├── schema.sql               # PostgreSQL 表结构（启动时自动建表/手动迁移基线）
├── migrations/              # 增量 SQL 迁移目录
├── scripts/migrate.sh       # 傻瓜式手动迁移脚本
├── query.sql                # sqlc 查询定义
├── sqlc.yaml                # sqlc 配置
└── config.yaml              # 默认配置
```

---

## 快速开始

### 前置依赖

- Go 1.25+
- PostgreSQL 15+
- Redis 7+

### 启动服务

```bash
git clone https://github.com/aihop/ainode.git
cd ainode
cp config.yaml config.local.yaml
# 编辑 config.local.yaml，填入数据库和 Redis 凭据

go run cmd/api/main.go
```

服务启动时会自动创建数据库表。

### 手动迁移

如果你不想依赖“服务启动时自动迁移”，可以直接执行：

```bash
cd ainode
./scripts/migrate.sh
```

或使用 Makefile：

```bash
make migrate
```

查看迁移状态：

```bash
./scripts/migrate.sh status
make migrate-status
```

脚本会按以下顺序执行：

- 先执行根目录的 `schema.sql`，并记录为基线迁移 `0000_schema.sql`
- 再按文件名字典序执行 `migrations/*.sql`
- 已执行过的版本会记录到 `schema_migrations` 表，不会重复执行

如果你使用环境变量方式传数据库连接，可以这样：

```bash
DATABASE_URL="postgres://user:pass@host:5432/ainode?sslmode=disable" ./scripts/migrate.sh
```

### 验证

```bash
curl http://localhost:5900/v1/models
```

---

## API 参考

### 多模态文档

- 开发方案与使用方式见 [docs/ai/multimodal-gateway.zh.md](https://github.com/aihop/ainode/docs/ai/multimodal-gateway.zh.md)

### OpenAI 兼容端点

| 端点 | 说明 |
|------|------|
| `GET /v1/models` | 列出可用模型 |
| `POST /v1/chat/completions` | 对话补全（支持流式） |
| `POST /v1/completions` | 文本补全（支持流式） |
| `POST /v1/images/generations` | 图像生成（第二阶段，当前优先适配 OpenAI-compatible 渠道） |
| `POST /v1/video/generations` | 视频异步任务创建（第三阶段） |
| `GET /v1/tasks/{task_id}` | 查询异步任务状态 |
| `POST /v1/tasks/{task_id}/cancel` | 取消异步任务 |

使用任何 OpenAI SDK，指向你的网关地址和 API Key：

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:5900/v1",
    api_key="sk-ainode-<your-api-key>"
)
```

### 管理 API

基础路径：`/api/admin`（需要管理员密钥）

| 方法 | 端点 | 说明 |
|------|------|------|
| GET | `/api/admin/channels` | 列出所有上游渠道 |
| POST | `/api/admin/channels` | 创建渠道 |
| PUT | `/api/admin/channels/{id}` | 更新渠道 |
| DELETE | `/api/admin/channels/{id}` | 删除渠道 |
| GET | `/api/admin/models` | 列出所有模型及定价 |
| POST | `/api/admin/models` | 创建模型 |
| PUT | `/api/admin/models/{model_name}` | 更新模型定价 |
| DELETE | `/api/admin/models/{model_name}` | 删除模型 |
| GET | `/api/admin/billing_logs` | 查询计费日志（分页） |

### 用户 API

基础路径：`/api/site`（需要内部用户 ID 请求头——由 APayShop 服务端代理调用）

| 方法 | 端点 | 说明 |
|------|------|------|
| GET | `/api/site/dashboard` | 用户仪表盘 |
| GET | `/api/site/stats` | 详细使用统计 |
| GET | `/api/site/billing-logs/list` | 用户账单记录 |
| GET | `/api/site/api-keys/list` | 列出用户 API Key |
| POST | `/api/site/api-keys/create` | 创建 API Key |
| POST | `/api/site/api-keys/delete` | 删除 API Key |
| POST | `/api/site/api-keys/status` | 启用/禁用 API Key |
| POST | `/api/site/api-keys/rotate` | 轮换 API Key |
| GET | `/api/site/models/groups` | 推荐模型分组 |

---

## 配置

### config.yaml

```yaml
server:
  port: 5900

db:
  dsn: "postgres://user:pass@localhost:5432/ainode?sslmode=disable"

redis:
  addr: "localhost:6379"
  password: ""
  db: 0
```

所有配置均可通过环境变量覆盖：
- `SERVER_PORT`
- `DB_DSN`
- `REDIS_ADDR`
- `REDIS_PASSWORD`
- `REDIS_DB`

---

## 性能

Node API 专为高吞吐 AI API 代理设计，开销极低：

- **热路径零数据库访问**——余额和限流数据全在 Redis，PostgreSQL 仅用于异步账单持久化
- **sqlc 编译期代码生成**——零反射开销，相比运行时 ORM 性能更优
- **Lua 原子预扣费**——无表锁，并发安全
- **异步计费**——请求转发不会被数据库写入阻塞

> 网关开销（< 5ms）相比上游 LLM 延迟（500ms-30s）可以忽略不计。

---

## 可观测性

### Prometheus 指标

`GET /metrics` 可获取：

- `requests_total` — 按模型和状态统计的请求总数
- `request_duration_ms` — 请求延迟分布
- `tokens_total` — 输入/输出 Token 数量

### 日志

标准 `log` 包输出，可与任意日志聚合工具配合使用。

---

## 开发

### 生成数据库代码

修改 `schema.sql` 或 `query.sql` 后：

```bash
sqlc generate
```

### 编译

```bash
go build -o ainode ./cmd/api
```

### 热重载（开发环境）

安装 [air](https://github.com/air-verse/air) 后：

```bash
air
```

---

## 与 One API / New API 对比

| 特性 | Node API | One API / New API |
|------|:--------:|:-----------------:|
| 数据库 | PostgreSQL | MySQL |
| ORM | sqlc（编译期，零反射） | GORM（运行时反射） |
| 热路径 DB 访问 | 无（纯 Redis） | 有（每次请求查 MySQL） |
| 计费引擎 | 预扣费+退款+异步结算 | 简单扣减 |
| 流式计费 | 专用 TallyReader，支持断连保护 | 基础支持 |
| 双余额体系 | ✅ | ❌ |
| 厂商适配 | 干净接口（OpenAI/Anthropic/Gemini） | 50+ 厂商 |
| 管理面板 | Vue（基于 APayShop 主题） | React + Ant Design Pro |
| 协议 | AGPLv3 | MIT |

Node API 最适合**与自有支付和订阅系统深度集成的定制化计费网关**。如果目标是面向外部用户做通用模型市场，New API 的开箱厂商覆盖更广。

---

## 生产就绪状态

> **状态**：可用于受控环境/内部部署。

计费引擎、限流、渠道故障切换和流式计量已在生产环境中验证。系统设计注重优雅降级——上游故障永远不会导致费用丢失或漏计。

大规模外部部署前建议补充：
- 管理端鉴权升级为 JWT + 角色权限
- 渠道健康检查与熔断机制
- 账单分区表自动维护

---

## 开源协议

本项目采用 **GNU Affero General Public License v3.0 (AGPLv3)**。

```
This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
```

---

属于 [APayShop](https://github.com/aihop/APayShop) 生态系统。
