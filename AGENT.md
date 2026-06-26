# AI Node: 高性能 AI 模型网关与计费系统

> **变更日志 (Changelog)**

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
├── internal/
│   ├── proxy/               # ReverseProxy 核心逻辑、重试机制 (Fallback)
│   │   ├── reverse_proxy.go # 自定义 Transport、ModifyResponse、ErrorHandler
│   │   └── tally_reader.go  # SSE 流式拦截、Token 统计、断流止损
│   ├── adapter/             # 多协议转换适配器
│   │   ├── adapter.go       # ProviderAdapter 接口定义
│   │   ├── openai.go        # OpenAI 透传
│   │   ├── anthropic.go     # Claude 请求改写 + SSE 双向翻译
│   │   └── gemini.go        # Gemini 请求改写 + SSE 双向翻译
│   ├── billing/             # 计费业务核心
│   │   ├── lua/
│   │   │   ├── pre_deduct.lua  # 预扣费原子脚本
│   │   │   └── refund.lua      # 退款脚本
│   │   ├── deduct.go        # 预扣费调用入口
│   │   ├── settlement.go    # 多退少补结算逻辑
│   │   └── redis.go         # Redis 余额操作
│   ├── channel/             # 上游渠道池管理
│   │   └── manager.go       # 内存缓存 + 加权轮询 + Pub/Sub 热刷新
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
│   │   └── site/            # 用户 API (仪表盘、统计、Key 管理)
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
├── schema.sql               # PostgreSQL 表结构定义
├── query.sql                # sqlc 查询语句定义
├── sqlc.yaml                # sqlc 配置文件
├── config.yaml              # 默认配置
├── README.md                # 英文文档
├── README.zh.md             # 中文文档
├── AGENT.md                 # AI 协作规范 (本文件)
└── PROMPT.md                # 旧版开发规范 (已归档)
```

---

## 3. 数据表结构 (PostgreSQL)

本项目采用单文件 `schema.sql` 进行数据库管理。

**自动同步机制**: 在 `cmd/api/main.go` 启动时，系统会自动执行 `schema.sql` 脚本，借助 `IF NOT EXISTS` 语句确保数据库表结构安全地初始化。

### 核心表概览

| 表名 | 用途 | 关键字段 |
|------|------|---------|
| `users` | 用户余额与订阅等级 | `cash_balance`, `grant_balance`, `tier_level`, `grant_expires_at` |
| `api_keys` | 网关调用凭证 | `key_string`, `user_id`, `allowed_models`, `quota_limit` |
| `models` | 模型定价、倍率与并发上限 | `model_name`, `input_price_cents`, `output_price_cents`, `multiplier`, `max_concurrency` |
| `channels` | 上游渠道配置 | `provider`, `base_url`, `api_key`, `models`, `weight` |
| `billing_logs` | 计费流水 | `user_id`, `model_name`, `amount_cents`, `prompt_tokens`, `completion_tokens` |

### 双余额体系设计

- **`cash_balance`** (充值余额): 用户自主充值获得，永不过期。金额放大 10⁸ 倍存储。
- **`grant_balance`** (订阅赠送余额): 按月订阅周期赠送，到期重置。扣费优先消耗此余额。

### 计费流水分区

`billing_logs` 按月分区（`billing_logs_YYYYMM`），需运维任务自动创建后续月份分区。

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
local cost = tonumber(ARGV[1])
local grant_bal = tonumber(redis.call("GET", KEYS[1]) or "0")
local cash_bal = tonumber(redis.call("GET", KEYS[2]) or "0")

if (grant_bal + cash_bal) < cost then
    return -1 -- 余额不足
end

if grant_bal >= cost then
    redis.call("DECRBY", KEYS[1], cost)
else
    local remain_cost = cost - grant_bal
    redis.call("SET", KEYS[1], 0)
    redis.call("DECRBY", KEYS[2], remain_cost)
end
return 1
```

> **结算退款**也必须遵循级联逻辑：优先退还到 `cash_balance`，超出部分再退还到 `grant_balance`。

### 4.1.1 模型倍率 (`multiplier`) 生效规则

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

---

## 5. 计费关键链路 (代码路径)

| 阶段 | 文件 | 说明 |
|------|------|------|
| 鉴权+预扣 | [internal/middleware/auth.go](file:///Users/hugh/code/aihop/ainode/internal/middleware/auth.go#L32-L141) | 解析 Key、估算 Token、Redis Lua 预扣 |
| 限流 | [internal/middleware/rate_limit.go](file:///Users/hugh/code/aihop/ainode/internal/middleware/rate_limit.go#L1-L106) | RPM/TPM 滑动窗口，超限退款 |
| 模型并发 | [internal/middleware/model_concurrency.go](file:///Users/hugh/code/aihop/ainode/internal/middleware/model_concurrency.go) | 按模型全局并发占位，超限退款并返回 429 |
| 代理 | [internal/proxy/reverse_proxy.go](file:///Users/hugh/code/aihop/ainode/internal/proxy/reverse_proxy.go#L1-L341) | 渠道选择、协议适配、请求转发、响应拦截 |
| 流式计量 | [internal/proxy/tally_reader.go](file:///Users/hugh/code/aihop/ainode/internal/proxy/tally_reader.go#L1-L160) | SSE 拦截、Token 统计、断流止损 |
| 结算 | [internal/billing/settlement.go](file:///Users/hugh/code/aihop/ainode/internal/billing/settlement.go#L42-L104) | 多退少补、异步推送账单任务 |
| 异步写库 | [internal/worker/billing.go](file:///Users/hugh/code/aihop/ainode/internal/worker/billing.go#L1-L110) | Asynq 消费、幂等写入 PostgreSQL |

---

## 6. API 接口清单

### OpenAI 兼容代理 (外部客户端)

| 路由 | 中间件 | 说明 |
|------|--------|------|
| `GET /v1/models` | Auth (不计费) | 列出可用模型 |
| `POST /v1/chat/completions` | Auth + PreDeduct + RateLimit | 对话补全 (流式/非流式) |
| `POST /v1/completions` | Auth + PreDeduct + RateLimit | 文本补全 (流式/非流式) |
| `/*` | Auth + PreDeduct + RateLimit | 其他 OpenAI 兼容路由 |

### 管理 API (管理员)

| 方法 | 路由 | 说明 |
|------|------|------|
| GET/POST | `/api/admin/channels` | 渠道列表/创建 |
| PUT/DELETE | `/api/admin/channels/{id}` | 渠道更新/删除 |
| GET/POST | `/api/admin/models` | 模型列表/创建 |
| PUT/DELETE | `/api/admin/models/{model_name}` | 模型更新/删除 |
| GET | `/api/admin/billing_logs` | 计费日志查询 (分页) |

### 用户 API (由 APayShop 服务端代理调用)

| 方法 | 路由 | 说明 |
|------|------|------|
| GET | `/api/site/dashboard` | 用户仪表盘概览 |
| GET | `/api/site/stats` | 详细使用统计 |
| GET | `/api/site/billing-logs/list` | 用户账单记录 |
| GET/POST | `/api/site/api-keys/list\|create\|delete\|status\|rotate` | API Key 全生命周期管理 |
| GET | `/api/site/models/groups` | 推荐模型分组 |

---

## 7. 厂商适配器接口

### `ProviderAdapter` 接口 (`internal/adapter/adapter.go`)

```go
type ProviderAdapter interface {
    // RewriteRequest 在转发前改写请求体、路径和头
    RewriteRequest(req *http.Request, body []byte) ([]byte, error)
    // TransformSSEEvent 将上游的 SSE 事件翻译为 OpenAI 格式
    TransformSSEEvent(event []byte) ([]byte, error)
}
```

### 已实现适配器

| 厂商 | 文件 | 说明 |
|------|------|------|
| OpenAI | [openai.go](file:///Users/hugh/code/aihop/ainode/internal/adapter/openai.go) | 透传，无改写 |
| Anthropic | [anthropic.go](file:///Users/hugh/code/aihop/ainode/internal/adapter/anthropic.go#L1-L198) | 请求转 `/v1/messages`，SSE 翻译 `content_block_delta` → `choices[0].delta` |
| Gemini | [gemini.go](file:///Users/hugh/code/aihop/ainode/internal/adapter/gemini.go#L1-L208) | 请求转 `/v1beta/models`，SSE 翻译原生格式 → OpenAI chunk |

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
