# AI Node: 高性能 AI 模型网关与计费系统

> **变更日志 (Changelog)**
> `2026-06-26`: 厂商扩展内核从 `internal/adapter` 重构为 `internal/provider`，主链直接依赖 provider registry / strategy / model mapping，见 Section 2 与 Section 7。
> `2026-06-26`: 新增 `internal/api/webhook/events.go` 通用事件入口，兼容 APayShop 的 HMAC 事件包并映射到内部 `transactions`；`transactions` 增加 `event_id` 幂等键，见 Section 2、Section 3 与 Section 4。
> `2026-06-26`: 新增 `transactions` 统一资金总账，并让管理员直充同时写入 `transactions + balance_logs`，见 Section 3 与 Section 6。
> `2026-06-26`: 新增 `balance_logs` 余额流水表与管理员直充审计链路，后台充值改为“写流水 + 同步 Redis 缓存”，见 Section 3 与 Section 6。
> `2026-06-26`: 新增多模态网关演进约定，统一媒体输入抽象与图生视频异步任务设计边界，见 Section 4.6。
> `2026-06-26`: 扩展 models/channels 多模态元数据字段，并引入 request 计费模式与图像生成入口约定，见 Section 3 与 Section 4.6。
> `2026-06-26`: 新增 async_tasks 表与视频异步任务路由骨架，见 Section 3、Section 4.6 与 Section 6。
> `2026-06-26`: 新增 `cmd/migrate` 与 `scripts/migrate.sh` 手动迁移入口，见 Section 2 与 Section 3。
> `2026-06-26`: 移除 schema.sql 中硬编码的 2026 年 billing_logs 分区，改为 `internal/billing/partition.go` 动态创建，启动时自动生成当前月 + 未来 6 个月分区，后台每日定时维护，见 Section 6。
> `2026-06-26`: 激活 `api_keys.quota_limit / quota_used` Key 级硬配额：鉴权中间件在预扣费前检查 `quota_used + 预估费 > quota_limit`，结算 Worker 在写账单后递增 `quota_used`；配额单位与金额同纬（cents），0 表示不限制，见 Section 3 与 Section 4.1。
> `2026-06-26`: 管理员 API 鉴权从硬编码 `"admin-secret-key"` 改为读取 `internal.token` 配置，见 Section 6。

## 1. 项目定位与核心架构

AI Node 是一个**生产就绪的 AI 模型网关**，对外提供统一的 OpenAI 兼容接口，对内支持动态路由到多个上游提供商（OpenAI、Anthropic、Gemini 等）。核心特色是**原子化预扣费计费引擎 + 流式 Token 实时统计 + 渠道故障自动切换**。

### 核心技术栈

- **核心框架**: `go-chi/chi/v5` (轻量级、100% 兼容标准库)
- **持久层**: **PostgreSQL** (存储流水/账单/配置) + **sqlc** (类型安全代码生成)
- **缓存/实时计费**: **Redis** (存储余额、RPM/TPM 限流、执行 Lua 原子扣费脚本)
- **转发引擎**: `net/http/httputil.ReverseProxy` 结合自定义 Transport
- **异步任务**: **Asynq** (账单异步持久化，指数退避重试)
- **监控**: **Prometheus** 指标 (请求数、延迟、Token 消耗)
- **并发模型**: 充分利用 Goroutine 异步处理 DB 写入，确保关键路径（转发）零阻塞

### 商业定位

AI Node 是 APayShop 生态中的**AI 计费网关**，与 APayShop 前端（ainode 主题）深度集成。用户通过 APayShop 下单购买 API 额度 → AI Node 负责鉴权、限流、转发、计量、计费结算。

---

## 2. 目录结构约定

AI 请严格按照以下结构组织代码：

```text
.
├── cmd/api/main.go          # 程序入口，组装路由与依赖注入
├── cmd/migrate/main.go      # 手动迁移执行器，顺序执行 schema.sql 与 migrations/*.sql
├── internal/
│   ├── proxy/               # ReverseProxy 核心逻辑、重试机制 (Fallback)
│   │   ├── reverse_proxy.go # 自定义 Transport、ModifyResponse、ErrorHandler
│   │   └── tally_reader.go  # SSE 流式拦截、Token 统计、断流止损
│   ├── provider/            # Provider 内核、注册中心、策略与厂商实现
│   │   ├── contract.go      # Provider 契约、能力声明、异步任务接口
│   │   ├── registry.go      # Provider 注册与获取
│   │   ├── strategy.go      # 鉴权策略与错误翻译
│   │   ├── model_mapping.go # 公共模型名 -> 上游模型名映射
│   │   ├── openai/          # OpenAI Provider 与共享异步任务适配器
│   │   ├── aimlapi/         # AI/ML API Provider (OpenAI-like)
│   │   ├── anthropic/       # Anthropic Provider
│   │   ├── cohere/          # Cohere Provider (OpenAI-like)
│   │   ├── deepinfra/       # DeepInfra Provider (OpenAI-like)
│   │   ├── deepseek/        # DeepSeek Provider (OpenAI-like)
│   │   ├── fireworks/       # Fireworks AI Provider (OpenAI-like)
│   │   ├── gemini/          # Gemini Provider
│   │   ├── grok/            # Grok (xAI) Provider (OpenAI-like)
│   │   ├── groq/            # Groq (LPU Inference) Provider (OpenAI-like)
│   │   ├── ideogram/        # Ideogram Provider (OpenAI-like)
│   │   ├── mistral/         # Mistral AI Provider (OpenAI-like)
│   │   ├── openrouter/      # OpenRouter Provider (OpenAI-like)
│   │   ├── perplexity/      # Perplexity Provider (OpenAI-like)
│   │   ├── qwen/            # Qwen (Alibaba Cloud) Provider (OpenAI-like)
│   │   └── together/        # Together AI Provider (OpenAI-like)
│   ├── billing/             # 计费业务核心
│   │   ├── lua/
│   │   │   ├── pre_deduct.lua  # 预扣费原子脚本
│   │   │   └── refund.lua      # 退款脚本
│   │   ├── deduct.go        # 预扣费调用入口
│   │   ├── settlement.go    # 多退少补结算逻辑
│   │   └── redis.go         # Redis 余额操作
│   ├── channel/             # 上游渠道池管理
│   │   ├── manager.go       # 内存缓存 + 轮询调度 + capability 过滤
│   │   └── circuit_breaker.go # 渠道断路器状态机与熔断恢复
│   ├── db/                  # sqlc 生成的代码 (Querier, Models)
│   │   ├── db.go
│   │   ├── models.go
│   │   └── query.sql.go
│   ├── middleware/          # 中间件
│   │   ├── lua/
│   │   │   └── rate_limit.lua  # 滑动窗口限流脚本
│   │   ├── auth.go          # 鉴权 + 预扣费中间件
│   │   ├── rate_limit.go    # RPM/TPM 限流中间件
│   │   └── model_concurrency.go # 模型级并发占位与释放
│   ├── api/                 # HTTP 接口
│   │   ├── admin/           # 管理员 API (渠道/模型 CRUD、账单查询)
│   │   ├── site/            # 用户 API (仪表盘、统计、Key 管理)
│   │   └── webhook/         # 内部交易事件 Webhook (APayShop 入账等)
│   ├── config/              # 应用级配置
│   │   ├── config.go        # viper 配置加载
│   │   ├── model_manager.go # 模型价格内存缓存
│   │   └── sync.go          # 后台配置同步 + Pub/Sub 刷新
│   ├── metrics/             # Prometheus 埋点
│   │   └── prometheus.go
│   ├── worker/              # Asynq 异步任务
│   │   └── billing.go       # 账单持久化 Worker
│   └── utils/               # 工具
│       └── error.go         # OpenAI 兼容错误格式封装
├── schema.sql               # PostgreSQL 表结构定义（也作为手动迁移基线）
├── migrations/              # 增量 SQL 迁移目录
├── scripts/migrate.sh       # 用户可直接执行的手动迁移脚本
├── query.sql                # sqlc 查询语句定义
├── sqlc.yaml                # sqlc 配置文件
├── config.yaml              # 默认配置
├── defaults.yaml          # 业务默认值（推荐模型、默认地址等）
├── README.md                # 英文文档
├── README.zh.md             # 中文文档
├── AGENT.md                 # AI 协作规范 (本文件)
└── PROMPT.md                # 旧版开发规范 (已归档)
```

---

## 3. 数据表结构 (PostgreSQL)

本项目采用单文件 `schema.sql` 进行数据库管理。

**自动同步机制**: 在 `cmd/api/main.go` 启动时，系统会自动执行 `schema.sql` 脚本，借助 `IF NOT EXISTS` 语句确保数据库表结构安全地初始化。

**手动迁移机制**:

- `cmd/migrate/main.go` 会先执行根目录 `schema.sql` 作为基线，再按字典序执行 `migrations/*.sql`
- 已执行版本记录在 `schema_migrations` 表
- 推荐线上部署优先使用 `./scripts/migrate.sh` 或 `make migrate`，而不是完全依赖启动自动迁移

### 核心表概览

| 表名 | 用途 | 关键字段 |
|------|------|---------|
| `users` | 用户余额与订阅等级 | `cash_balance`, `grant_balance`, `tier_level`, `grant_expires_at` |
| `api_keys` | 网关调用凭证 | `key_string`, `user_id`, `allowed_models`, `quota_limit`, `quota_used` |
| `models` | 模型定价、模态、计费模式、倍率与并发上限 | `model_name`, `modality`, `pricing_mode`, `pricing_config`, `billing_policy`, `multiplier`, `max_concurrency` |
| `channels` | 上游渠道配置 | `provider`, `base_url`, `api_key`, `models`, `protocol_type`, `upload_mode`, `model_mapping`, `supports_async`, `weight` |
| `async_tasks` | 异步媒体任务状态与预扣费跟踪 | `request_id`, `task_type`, `provider`, `model_name`, `status`, `upstream_task_id`, `pre_deducted_cents`, `actual_cost_cents` |
| `billing_logs` | 计费流水 | `user_id`, `model_name`, `amount_cents`, `prompt_tokens`, `completion_tokens` |
| `transactions` | 统一资金总账 | `user_id`, `event_id`, `type`, `balance_type`, `direction`, `amount_cents`, `before_balance_cents`, `after_balance_cents`, `source_type`, `source_id` |
| `balance_logs` | 余额变更流水 | `user_id`, `balance_type`, `action_type`, `amount_cents`, `before_balance_cents`, `after_balance_cents`, `operator_name`, `remark` |

### 双余额体系设计

- **`cash_balance`** (充值余额): 用户自主充值获得，永不过期。金额放大 10⁸ 倍存储。
- **`grant_balance`** (订阅赠送余额): 按月订阅周期赠送，到期重置。是否允许消耗此余额，由模型的 `billing_policy` 决定。

### 计费流水分区

`billing_logs` 按月分区（`billing_logs_YYYYMM`），分区表不再硬编码在 schema.sql 中，改为**启动时动态创建**：`internal/billing/partition.go` 确保当前月 + 未来 6 个月的分区已存在，并启动后台协程每日检查维护。

---

## 4. 核心逻辑实现指南

### 4.1 计费逻辑：多退少补与防并发透支

**流程**: 请求拦截 → 估算 Token → Redis Lua 预扣 → 转发 → 实际用量统计 → 结算退款

```text
Client Request
     │
     ▼
┌─────────────────┐
│  Auth Middleware │  ← 解析 Bearer Key → 查询用户 → 估算 prompt_tokens + max_tokens
│  + Pre-Deduct   │  ← Redis Lua: 原子预扣最大可能金额
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  Rate Limiter   │  ← RPM/TPM 滑动窗口检查，超限则退款
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  ReverseProxy   │  ← 选择渠道 → Protocol Adapter → 转发上游
│  + TallyReader  │  ← SSE 拦截 → 统计实际 Token → 结算 (多退少补)
└─────────────────┘
         │
         ▼
┌─────────────────┐
│  Asynq Worker   │  ← 异步写入 PostgreSQL (指数退避重试)
└─────────────────┘
```

**预扣费 Lua 脚本 (`internal/billing/lua/pre_deduct.lua`)**:

```lua
-- KEYS[1]: grant_balance:user_id
-- KEYS[2]: cash_balance:user_id
-- ARGV[1]: 预估最大扣费金额
-- ARGV[2]: 计费策略 (both | cash_only | grant_only)
local cost = tonumber(ARGV[1])
local billing_policy = ARGV[2] or "both"
```

> **结算退款**也必须遵循级联逻辑：优先退还到 `cash_balance`，超出部分再退还到 `grant_balance`。

### 4.1.1 模型余额策略 (`billing_policy`)

- `models.billing_policy` 用于区分订阅套餐额度与 API 充值余额的可用范围。
- 当前支持：
  - `both`: 套餐额度和充值余额都可用，预扣时优先扣 `grant_balance`，不足再扣 `cash_balance`
  - `cash_only`: 仅允许使用 `cash_balance`
  - `grant_only`: 仅允许使用 `grant_balance`
- 后台模型管理页新增 `Billing Policy` 选择项，默认值为 `both`，保证旧模型兼容。

### 4.1.2 模型倍率 (`multiplier`) 生效规则

- `models.input_price_cents / output_price_cents / cache_hit_price_cents / cache_miss_price_cents` 存放基础单价。
- `models.multiplier` 是用户侧真实收费倍率，必须同时参与：
  - **预扣费**：基础预估金额算出后，按倍率放大并**向上取整**，避免低估预扣。
  - **最终结算**：基础实际金额算出后，按倍率放大并**四舍五入**，保证最终账单稳定。
- 如果运营需要直接录入最终卖价，可将 `multiplier` 保持为 `1.0`。

### 4.2 订阅周期清零与重置

当主站 (APayShop) 触发订阅续费成功时：

1. 更新用户的 `tier_level` 和 `grant_expires_at`
2. 将 `grant_balance` **重置（覆盖）**为固定额度，绝不累加（Use it or lose it）
3. 同步重置 Redis 中的 `grant_balance:user_id`
4. `cash_balance`（充值余额）保持不变

### 4.3 SSE 流式拦截与断流止损 (`internal/proxy/tally_reader.go`)

`TallyReader` 包装 `io.ReadCloser`，实现：

1. **精准统计**: 解析 `text/event-stream`，优先提取最后一块的 `usage` 字段。无 `usage` 时用 `tiktoken-go` 兜底累计 `choices[0].delta.content`。
2. **断流止损**: 监听 `http.Request.Context().Done()`。客户端断开时立即中止上游请求，对已生成的 Token 结算扣费。
3. **异步结算**: 在 `Read` 捕获 `io.EOF` 或断开信号时触发回调，将实际消耗写入 PostgreSQL 并修正 Redis 余额。

### 4.4 渠道池重试与协议转换 (Fallback & Adapter)

`ReverseProxy` 的 `FallbackTransport` 实现：

1. **精确路由**: 解析 `model` 字段，从渠道池挑选支持该模型且状态正常的渠道。
2. **智能重试**: 上游返回 429/5xx 时无缝重试下一个可用渠道。
3. **协议适配**: 通过 `ProviderAdapter` 接口，在转发前改写请求体/路径，在响应时翻译 SSE 事件为 OpenAI 格式。

### 4.5 频率控制与 QoS

在 `internal/middleware/` 中基于 Redis 实现：

1. **RPM/TPM 限流**: 滑动窗口，限制单用户每分钟请求数和 Token 数。触发限流时自动退款预扣。
2. **模型级并发限制**: 根据 `models.max_concurrency` 在 Redis 中做模型并发占位，超限时直接拒绝并退款预扣。
3. **优先级路由**: 根据 `api_keys.tier_level`，在高并发时优先保证订阅用户请求。

### 4.6 多模态网关演进约定

- `图生文` 继续复用 OpenAI 兼容同步主链，统一入口仍优先采用 `POST /v1/chat/completions`。
- 媒体输入必须统一抽象为 `MediaInput`，对外允许 `url | base64 | file` 三种形态，对内统一在解析层归一化后再交给 Provider Adapter。
- 第一阶段允许 `file_id` 仅作为协议预留；在未引入文件服务前，适配器应优先支持 `http(s)` 与 `data:` 图片输入。
- `图生视频`、`视频生视频` 等长任务绝不能硬塞进当前同步 ReverseProxy 结算链，必须设计为独立的异步任务接口与任务级结算流程。
- 多模态扩展时，优先保持“对外兼容 OpenAI 生态、对内保持计费主链纯净”的原则，避免将视频任务逻辑污染现有文本/视觉同步热路径。
- `models.pricing_mode` 当前至少支持：
  - `token`: 沿用输入/输出 Token 价格模型
  - `request`: 按次计费，价格从 `pricing_config.request_price_cents` 读取
- `POST /v1/images/generations` 可作为第二阶段的图像生成入口，优先复用现有鉴权、预扣费、限流和代理主链；当前更适合 OpenAI-compatible 图像渠道。
- `POST /v1/video/generations`、`GET /v1/tasks/{task_id}`、`POST /v1/tasks/{task_id}/cancel` 已作为第三阶段的最小异步链路落地：
  - 提交阶段复用鉴权、预扣费、RPM/TPM 与模型并发中间件
  - 任务状态持久化到 `async_tasks`
  - 终态 `succeeded` 走结算，`failed/canceled` 走退款
  - 当前优先兼容 OpenAI-like 异步任务协议，后续再补厂商专属适配器
- 异步视频任务的上游接入应统一经过 `AsyncTaskAdapter`，禁止在 `internal/api/gateway` 的 handler 中继续硬编码某家厂商的提交、轮询、取消 HTTP 细节。

---

## 5. 计费关键链路 (代码路径)

| 阶段 | 文件 | 说明 |
|------|------|------|
| 鉴权+预扣 | [internal/middleware/auth.go](ainode/internal/middleware/auth.go#L32-L141) | 解析 Key、估算 Token、Redis Lua 预扣 |
| 限流 | [internal/middleware/rate_limit.go](ainode/internal/middleware/rate_limit.go#L1-L106) | RPM/TPM 滑动窗口，超限退款 |
| 模型并发 | [internal/middleware/model_concurrency.go](ainode/internal/middleware/model_concurrency.go) | 按模型全局并发占位，超限退款并返回 429 |
| 代理 | [internal/proxy/reverse_proxy.go](ainode/internal/proxy/reverse_proxy.go#L1-L341) | 渠道选择、协议适配、请求转发、响应拦截 |
| 流式计量 | [internal/proxy/tally_reader.go](ainode/internal/proxy/tally_reader.go#L1-L160) | SSE 拦截、Token 统计、断流止损 |
| 结算 | [internal/billing/settlement.go](ainode/internal/billing/settlement.go#L42-L104) | 多退少补、异步推送账单任务 |
| 异步写库 | [internal/worker/billing.go](ainode/internal/worker/billing.go#L1-L110) | Asynq 消费、幂等写入 PostgreSQL |

---

## 6. API 接口清单

### OpenAI 兼容代理 (外部客户端)

| 路由 | 中间件 | 说明 |
|------|--------|------|
| `GET /v1/models` | Auth (不计费) | 列出可用模型 |
| `POST /v1/chat/completions` | Auth + PreDeduct + RateLimit | 对话补全 (流式/非流式) |
| `POST /v1/completions` | Auth + PreDeduct + RateLimit | 文本补全 (流式/非流式) |
| `POST /v1/video/generations` | Auth + PreDeduct + RateLimit + ModelConcurrency | 创建视频异步任务 |
| `GET /v1/tasks/{task_id}` | Auth | 查询异步任务状态 |
| `POST /v1/tasks/{task_id}/cancel` | Auth | 取消异步任务 |
| `/*` | Auth + PreDeduct + RateLimit | 其他 OpenAI 兼容路由 |

### 管理 API (管理员)

| 方法 | 路由 | 说明 |
|------|------|------|
| GET | `/api/admin/providers` | 返回当前已注册 Provider 列表及默认元信息 |
| GET/POST | `/api/admin/channels` | 渠道列表/创建 |
| PUT/DELETE | `/api/admin/channels/{id}` | 渠道更新/删除 |
| GET/POST | `/api/admin/models` | 模型列表/创建 |
| PUT/DELETE | `/api/admin/models/{model_name}` | 模型更新/删除 |
| GET | `/api/admin/billing_logs` | 计费日志查询 (分页) |
| GET | `/api/admin/users` | 用户余额/Token/请求量聚合列表 |
| POST | `/api/admin/users/{id}/balance` | 管理员直充/赠送，并写入 `transactions + balance_logs` |
| GET | `/api/admin/users/{id}/balance-logs` | 查询指定用户余额流水 |

### Provider Catalog 配置

- `GET /api/admin/providers` 返回给前端的 Provider 展示元信息，采用"代码默认值 + defaults.yaml 覆盖"的方式。
- 高变化字段如 `default_base_url`、`recommended_models`、`recommended_model_presets`、`recommended_model_mapping` 应维护在 `defaults.yaml`，无需重新编译。
- `internal/provider/*` 子包保留稳定的协议能力、鉴权策略、请求改写与错误翻译，不建议为了更新推荐模型频繁改代码。

### 用户 API (由 APayShop 服务端代理调用)

| 方法 | 路由 | 说明 |
|------|------|------|
| GET | `/api/site/dashboard` | 用户仪表盘概览 |
| GET | `/api/site/stats` | 详细使用统计 |
| GET | `/api/site/billing-logs/list` | 用户账单记录 |
| GET/POST | `/api/site/api-keys/list\|create\|delete\|status\|rotate` | API Key 全生命周期管理 |
| GET | `/api/site/models/groups` | 推荐模型分组 |

### 内部 Webhook API

| 方法 | 路由 | 说明 |
|------|------|------|
| POST | `/internal/webhooks/events` | 通用事件入口；兼容 `Authorization: Bearer <internal.token>` 或 APayShop HMAC 签名，并按 `transactions.event_id` 幂等入账 |

---

## 7. 厂商 Provider 内核

### `ProviderAdapter` 接口 (`internal/provider/contract.go`)

```go
type ProviderAdapter interface {
    // RewriteRequest 在转发前改写请求体、路径和头
    RewriteRequest(req *http.Request, modelName string) error
    // TransformSSEEvent 将上游的 SSE 事件翻译为 OpenAI 格式
    TransformSSEEvent(event []byte) ([]byte, error)
}
```

### 当前结构

- `internal/provider/contract.go`: Provider 契约、能力声明、异步任务接口
- `internal/provider/registry.go`: Provider 注册与获取
- `internal/provider/strategy.go`: 鉴权策略与错误翻译
- `internal/provider/model_mapping.go`: 公共模型名与上游模型名映射
- `internal/provider/openai/`: OpenAI Provider 实现与共享异步适配器
- `internal/provider/aimlapi/`: AI/ML API Provider 实现（OpenAI-like）
- `internal/provider/anthropic/`: Anthropic Provider 实现
- `internal/provider/deepseek/`: DeepSeek Provider 实现（OpenAI-like）
- `internal/provider/gemini/`: Gemini Provider 实现
- `internal/provider/grok/`: Grok (xAI) Provider 实现（OpenAI-like）
- `internal/provider/groq/`: Groq (LPU Inference) Provider 实现（OpenAI-like）
- `internal/provider/mistral/`: Mistral AI Provider 实现（OpenAI-like）
- `internal/provider/openrouter/`: OpenRouter Provider 实现（OpenAI-like）
- `internal/provider/qwen/`: Qwen (Alibaba Cloud) Provider 实现（OpenAI-like）
- `internal/provider/together/`: Together AI Provider 实现（OpenAI-like）
- `docs/ai/provider-extension.zh.md`: 新增厂商 Provider 的标准接入模板与检查清单

### 已实现 Provider

| 厂商 | 文件 | 说明 |
|------|------|------|
| OpenAI | [adapter.go](file:///Users/hugh/code/aihop/ainode/internal/provider/openai/adapter.go) / [async.go](file:///Users/hugh/code/aihop/ainode/internal/provider/openai/async.go) | 透传请求，提供共享异步任务适配器 |
| Anthropic | [adapter.go](file:///Users/hugh/code/aihop/ainode/internal/provider/anthropic/adapter.go) | 请求转 `/v1/messages`，SSE 翻译 `content_block_delta` → `choices[0].delta` |
| DeepSeek | [adapter.go](file:///Users/hugh/code/aihop/ainode/internal/provider/deepseek/adapter.go) | 复用 OpenAI-like 请求改写与 Bearer 鉴权，默认声明 Chat / Stream 能力 |
| Gemini | [adapter.go](file:///Users/hugh/code/aihop/ainode/internal/provider/gemini/adapter.go) | 请求转 `/v1beta/models`，SSE 翻译原生格式 → OpenAI chunk |
| Grok (xAI) | [adapter.go](file:///Users/hugh/code/aihop/ainode/internal/provider/grok/adapter.go) | 复用 OpenAI-like 请求改写与 Bearer 鉴权，声明 Chat / Stream 能力 |
| Cohere | [adapter.go](file:///Users/hugh/code/aihop/ainode/internal/provider/cohere/adapter.go) | OpenAI-like，企业级嵌入与 RAG 模型 |
| DeepInfra | [adapter.go](file:///Users/hugh/code/aihop/ainode/internal/provider/deepinfra/adapter.go) | OpenAI-like，极致低价开源模型推理 |
| Fireworks AI | [adapter.go](file:///Users/hugh/code/aihop/ainode/internal/provider/fireworks/adapter.go) | OpenAI-like，LPU 级低延迟开源模型推理 |
| Perplexity | [adapter.go](file:///Users/hugh/code/aihop/ainode/internal/provider/perplexity/adapter.go) | OpenAI-like，搜索增强对话 |
| Ideogram | [adapter.go](file:///Users/hugh/code/aihop/ainode/internal/provider/ideogram/adapter.go) | OpenAI-like，文字渲染最强的图片生成 |
| Qwen | [adapter.go](file:///Users/hugh/code/aihop/ainode/internal/provider/qwen/adapter.go) | OpenAI-like，默认地址 DashScope |
| Mistral | [adapter.go](file:///Users/hugh/code/aihop/ainode/internal/provider/mistral/adapter.go) | OpenAI-like，EU 数据合规 |
| Together AI | [adapter.go](file:///Users/hugh/code/aihop/ainode/internal/provider/together/adapter.go) | OpenAI-like，开源模型托管 |
| OpenRouter | [adapter.go](file:///Users/hugh/code/aihop/ainode/internal/provider/openrouter/adapter.go) | OpenAI-like，统一多模型接入 |
| Groq (LPU) | [adapter.go](file:///Users/hugh/code/aihop/ainode/internal/provider/groq/adapter.go) | OpenAI-like，LPU 超低延迟推理 |
| AI/ML API | [adapter.go](file:///Users/hugh/code/aihop/ainode/internal/provider/aimlapi/adapter.go) | OpenAI-like，400+ 模型聚合 |

---

## 8. 开发规范与约束

### 8.1 代码行数硬约束

- **单文件上限**: 任何 `.go`, `.lua` 文件行数不得超过 **500 行**。
- **组件拆分**: 超过 **400 行** 必须按业务维度拆分子文件。
- **AI 行为**: 发现文件接近 800 行时，禁止直接添加功能，必须先提出瘦身重构方案。

### 8.2 共享逻辑封装原则 (DRY)

- 严禁在多个文件中复制相似逻辑。
- 纯逻辑、无状态的函数（时间处理、加密、字符串格式化）必须放入 `internal/utils/`。

### 8.3 数据库操作规范

- 所有数据库查询必须通过 sqlc 生成的类型安全接口。
- 修改 `schema.sql` 或 `query.sql` 后必须执行 `sqlc generate`。
- **热路径上禁止直接查询 PostgreSQL**——余额和配置必须通过 Redis/内存缓存。

### 8.4 关键中间件顺序

路由挂载顺序不可随意调整（定义于 `cmd/api/main.go`）：

```text
Logger → Recoverer → CORS → Auth+PreDeduct → RateLimit → GatewayProxy
```

Auth 必须在 RateLimit 之前，因为限流需要 `user_id`（从 Auth 中间件注入 context）。

### 8.5 结算退款原则

- 限流触发退款：`rate_limit.go` → `billing.Refund()` — 全额退还预扣
- 代理异常退款：`reverse_proxy.go ErrorHandler` → `billing.Refund()` — 全额退还预扣
- 正常结算：`reverse_proxy.go ModifyResponse` → `billing.Settle()` — 多退少补
- 结算退款必须遵循级联顺序：先退 `cash_balance`，超出部分退 `grant_balance`

### 8.6 错误格式规范

所有 HTTP 错误必须返回 OpenAI 兼容格式（`internal/utils/error.go`）：

```json
{
    "error": {
        "message": "...",
        "type": "invalid_request_error",
        "code": "invalid_api_key"
    }
}
```

---

## 9. AI 自我进化与协作协议 (Self-Audit)

本文件是项目的**唯一事实来源 (Single Source of Truth)**。

### AI 强制触发更新条件

当发生以下行为后，AI **必须主动**更新本 `AGENT.md` 文件：

1. 增删改了数据库 Schema（同步更新 Section 3）。
2. 新增了厂商适配器（同步更新 Section 7 表格）。
3. 新增了独立的功能模块（同步更新 Section 2 目录结构）。
4. 解决了具有代表性的环境兼容性 Bug（提炼至开发规范中）。
5. 修改了关键中间件顺序或结算流程（同步更新 Section 8）。

### 自我审计提问 (每次输出前)

> “我刚才的代码变更是否引入了新的架构模式或表结构？如果是，我是否已经将其记录到了 `AGENT.md` 的对应章节中？”

### 演进日志

架构决策、依赖变更、重大重构必须写入 `docs/ai/logs/changelog.md`。这不仅是给人看的，更是为了后续接手的 AI 能建立正确的历史时间线记忆，避免重复踩坑或推翻之前的正确决策。
