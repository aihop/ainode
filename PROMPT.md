# DataPaaS AI Gateway: 高性能模型中转与计费系统开发规范

## 项目愿景与架构约束

本项目是 **DataPaaS** 生态下的企业级 AI 流量中转中心，核心目标是打造**最强大、高可用、防透支**的 AI API 网关。

**核心特性要求**：

1. **极低延迟转发与协议统一**：对外提供统一的 OpenAI 接口格式，对内支持向 Anthropic、Gemini 等多协议转换（One API 模式）。
2. **精准计费与防透支**：流式 Token 实时统计，基于 Redis 的原子化“预扣费（多退少补）”机制，杜绝并发白嫖。
3. **高可用渠道池与 Fallback**：支持多上游 API Key 轮询与权重调度，遇到 429/5xx 错误自动无缝重试下一个可用渠道。
4. **极致止损机制**：客户端断开连接（Context Cancel）时，必须毫秒级中断上游请求，防止后台持续生成浪费资金。

**技术栈约束**：

- **核心框架**: `go-chi/chi/v5` (轻量级、100% 兼容标准库)。
- **持久层**: **PostgreSQL** (存储流水/账单/配置) + **sqlc** (类型安全代码生成)。
- **缓存/实时计费**: **Redis** (存储余额、RPM/TPM 限流、执行 Lua 原子扣费脚本)。
- **转发引擎**: `net/http/httputil.ReverseProxy` 结合自定义 Transport。
- **并发模型**: 充分利用 Goroutine 异步处理 DB 写入，确保关键路径（转发）零阻塞。

---

## 目录结构约定 (Project Structure)

AI 请严格按照以下结构组织代码：

```text
.
├── cmd/api/main.go          # 程序入口，组装路由与依赖注入
├── internal/
│   ├── proxy/               # ReverseProxy 核心逻辑、重试机制 (Fallback)
│   ├── adapter/             # 多协议转换适配器 (如 Claude/Gemini 转换为 OpenAI 格式)
│   ├── billing/             # Redis Lua 脚本集成、多退少补计费业务核心
│   ├── channel/             # 上游渠道池管理、负载均衡调度
│   ├── db/                  # sqlc 生成的代码 (Querier, Models)
│   ├── middleware/          # Auth 鉴权、RateLimit 限流、BalanceCheck 预扣费
│   └── config/              # 环境变量与应用级配置
├── sqlc.yaml                # sqlc 配置文件
├── schema.sql               # PostgreSQL 表结构定义
└── query.sql                # sqlc 查询语句定义
```

---

## 3. 数据表结构 (PostgreSQL)

本项目采用单文件 `schema.sql` 进行数据库管理。

**自动同步机制**：在 `cmd/api/main.go` 启动时，系统会自动执行 `schema.sql` 脚本，借助 `IF NOT EXISTS` 语句确保数据库表结构安全地初始化。

### SQL 表结构定义 (`schema.sql`)

```sql
-- 用户表
CREATE TABLE users (
    id SERIAL PRIMARY KEY,
    email VARCHAR(50) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    nickname VARCHAR(50),
    avatar_url VARCHAR(255),
    cash_balance BIGINT DEFAULT 0, -- 充值余额（永不过期），金额放大 10^8 倍存储
    grant_balance BIGINT DEFAULT 0,   -- 订阅周期赠送余额（按周期清零），放大 10^8 倍存储
    tier_level INT DEFAULT 0,             -- 订阅等级 (0: Free, 1: Pro, 2: Enterprise)，用于网关高并发优先级控制
    grant_expires_at TIMESTAMP WITH TIME ZONE DEFAULT NULL, -- 订阅周期赠送到期时间
    status INT DEFAULT 1,                 -- 1: 正常, 0: 禁用
    last_login_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- API Keys 表 (用户网关调用凭证，独立表)
CREATE TABLE api_keys (
    id SERIAL PRIMARY KEY,
    name VARCHAR(50) NOT NULL,
    key_string VARCHAR(64) UNIQUE NOT NULL,
    user_id INT REFERENCES users(id),
    order_id VARCHAR(100),          -- 订单ID
    product_id INT,                 -- 产品ID
    tier_level INT DEFAULT 0,       -- 冗余用户订阅等级，用于网关层快速读取并进行 Priority Routing
    quota_limit BIGINT DEFAULT 0,   -- 月度配额
    quota_used BIGINT DEFAULT 0,    -- 已用配额
    allowed_models JSONB DEFAULT '[]', -- 允许访问的模型列表 (JSON 数组)
    status INT DEFAULT 1, -- 1: 正常, 0: 禁用
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- 模型价格表（区分输入输出价格）
CREATE TABLE models (
    id SERIAL PRIMARY KEY,
    model_name VARCHAR(50) UNIQUE NOT NULL,
    input_price_cents BIGINT NOT NULL,  -- 每 1M Token 的输入价格
    output_price_cents BIGINT NOT NULL, -- 每 1M Token 的输出价格
    cache_hit_price_cents BIGINT NOT NULL DEFAULT 0, -- 每 1M Token 的缓存读取价格
    cache_miss_price_cents BIGINT NOT NULL DEFAULT 0, -- 每 1M Token 的缓存创建价格
    multiplier REAL NOT NULL DEFAULT 1.0,            -- 用户端计费倍率 (如 0.60x)
    status INT DEFAULT 1
);

-- 上游渠道池表（高可用配置）
CREATE TABLE channels (
    id SERIAL PRIMARY KEY,
    name VARCHAR(50) NOT NULL,
    provider VARCHAR(20) NOT NULL,      -- openai, anthropic, gemini 等
    base_url VARCHAR(255) NOT NULL,
    api_key VARCHAR(255) NOT NULL,
    models TEXT NOT NULL DEFAULT '',    -- 逗号分隔的支持模型列表 (如 "gpt-4,gpt-3.5-turbo")
    weight INT DEFAULT 1,               -- 负载均衡权重
    status INT DEFAULT 1                -- 1: 正常, 0: 故障/禁用
);

-- 计费流水表
CREATE TABLE billing_logs (
    id UUID PRIMARY KEY,
    user_id INT REFERENCES users(id),
    channel_id INT REFERENCES channels(id),
    model_name VARCHAR(50) NOT NULL,
    prompt_tokens INT DEFAULT 0,
    completion_tokens INT DEFAULT 0,
    cache_hit_tokens INT DEFAULT 0,  -- 缓存读取 Token 数量 (上游返回)
    cache_miss_tokens INT DEFAULT 0, -- 缓存创建 Token 数量 (上游返回)
    amount_cents BIGINT NOT NULL,           -- 本次请求实际扣费 (折算倍率后)
    request_id VARCHAR(100) UNIQUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_api_keys_key_string ON api_keys(key_string);
CREATE INDEX idx_billing_created_at ON billing_logs(created_at);
```

### sqlc.yaml 配置

```yaml
version: "2"
sql:
  - schema: "db/migrations"
    queries: "query.sql"
    engine: "postgresql"
    gen:
      go:
        package: "db"
        out: "internal/db"
        sql_package: "pgx/v5"
```

---

## 核心逻辑实现指南

### 4.1 计费逻辑：多退少补与防并发透支

在请求进入转发流程前，拦截请求体，计算 `Prompt Tokens`，并结合请求的 `max_tokens`（若无则取模型默认最大值）估算**最大可能消耗金额**进行预扣费。
请求结束后，根据实际使用的 Token 计算真实金额，执行退还或补扣。

**支持“订阅+按量”双轨制的级联扣费**：
扣费顺序必须是：**优先扣除订阅赠送余额 (`grant_balance`)，不足部分再扣除充值余额 (`cash_balance`)**。

**预扣费 Lua 脚本 (`internal/billing/lua/pre_deduct.lua`)**:

```lua
-- KEYS[1]: 订阅余额Key (grant_balance:user_id)
-- KEYS[2]: 充值余额Key (cash_balance:user_id)
-- ARGV[1]: 预估最大扣费金额
local cost = tonumber(ARGV[1])
local grant_bal = tonumber(redis.call("GET", KEYS[1]) or "0")
local cash_bal = tonumber(redis.call("GET", KEYS[2]) or "0")

if (grant_bal + cash_bal) < cost then
    return -1 -- 余额不足
end

if grant_bal >= cost then
    -- 订阅余额充足，全部从订阅扣
    redis.call("DECRBY", KEYS[1], cost)
else
    -- 订阅余额不足，先扣完订阅，剩下的扣充值余额
    local remain_cost = cost - grant_bal
    redis.call("SET", KEYS[1], 0)
    redis.call("DECRBY", KEYS[2], remain_cost)
end

return 1 -- 扣费成功
```

> **注意**：真实结算（退还多扣的金额）时，也必须遵循同样的级联逻辑：优先将额度退还到 `cash_balance`，如果退还金额超出了之前从 `cash_balance` 扣除的部分，剩余部分再退回到 `grant_balance` 中。

### 4.1.2 订阅周期清零与重置逻辑 (Subscription Reset)

系统需提供内部 API / Webhook，当主站 (APayShop) 触发按月订阅续费成功时，网关系统必须：

1. 更新用户的 `tier_level` 和 `grant_expires_at`。
2. 将用户的 `grant_balance` **重置（覆盖）**为该订阅等级包含的固定额度，**绝不累加**（Use it or lose it 机制）。
3. 同步重置 Redis 中的 `grant_balance:user_id`。
4. `cash_balance`（灵活充值余额）保持不变。

### 4.2 SSE 流式拦截与客户端断流控制 (`internal/proxy/tally_reader.go`)

实现包装了 `io.ReadCloser` 的 `TallyReader`：

1. **精准统计**: 解析 `text/event-stream`，优先提取最后一块的 `usage` 字段（如 `stream_options: {"include_usage": true}`）。如果没有 `usage`，兜底使用 `tiktoken-go` 累计 `choices[0].delta.content`。
2. **断流止损**: 必须监听 `http.Request.Context().Done()`。一旦客户端断开，**立即中止上游 HTTP 请求**，并对已生成的 Token 进行结算扣费，绝不浪费上游额度。
3. **异步结算**: 在 `Read` 捕获 `io.EOF` 或断开信号时，触发回调，将实际消耗写入 PostgreSQL 并修正 Redis 余额。

### 4.3 渠道池重试机制与协议转换 (Fallback & Adapter)

`ReverseProxy` 必须实现以下两项高级路由特性，以支持“一域通吃”：

1. **精确路由与智能重试**:
   - 解析客户端请求的 `model`，从 `channels` 表中挑选**包含该模型且状态正常的渠道**发起请求。
   - 请求失败（如上游返回 429 Too Many Requests, 500, 503）时，不能直接报错给客户端，应无缝重试下一个支持该模型的渠道。

2. **跨协议转换 (One API 模式)**:
   - 所有的对外接口必须统一为 OpenAI 格式 (`/v1/chat/completions`)。
   - 在 `internal/adapter/` 中实现各厂商的转换器接口 (如 `ProviderAdapter`)。
   - **请求体转换**: 根据渠道的 `provider` 字段（如 `anthropic`），在请求发出前拦截并修改 JSON 结构和 URL 路径（如转为 `/v1/messages`）。
   - **响应体转换**: 在 `ModifyResponse` 中拦截 SSE 流，将上游私有协议的 SSE 数据（如 Claude 的 `content_block_delta`）实时翻译回 OpenAI 的 `choices[0].delta` 格式再发给客户端。

### 4.4 频率控制与优先级路由 (Rate Limiting & Priority)

在 `internal/middleware/` 中基于 Redis 实现双重限流滑动窗口与分级 QoS：

1. **并发控制 (RPM & TPM)**: 限制单用户每分钟请求次数 (Requests Per Minute) 和每分钟处理的 Token 数量 (Tokens Per Minute)。
2. **优先级路由 (Priority Routing)**:
   - 读取请求所携带的 `api_keys.tier_level`。
   - 在高并发或上游通道出现拥堵时，优先保证 `tier_level > 0` (订阅用户) 的请求进入路由，而对 `tier_level == 0` (免费按量用户) 实施更严格的排队或降级。

---

## 开发者任务清单 (Task List)

- [x] **第一阶段: 基础脚手架** - 初始化项目，编写 `schema.sql` 和 `query.sql`，生成 `internal/db` 代码。
- [x] **第二阶段: 渠道与配置缓存** - 将 PostgreSQL 中的 `models` 和 `channels` 数据加载并同步到内存/Redis 中，实现基础的轮询调度。
- [x] **第三阶段: 计费与限流引擎** - 编写 Redis Lua 中间件，实现 RPM/TPM 限流，以及“多退少补”的原子扣费逻辑。
- [x] **第四阶段: 代理与流式拦截** - 实现 `ReverseProxy`，加入 Fallback 重试逻辑；实现 `TallyReader` 处理 SSE 解析、Token 统计与 Context Cancel 止损。
- [x] **第五阶段: 服务集成与测试** - 在 `main.go` 中组装路由，提供优雅启停（Graceful Shutdown），编写核心场景（断流、高并发预扣费）的单元/集成测试。

## 架构演进与瘦身准则 (Refactoring & Scale)

### 1. 代码行数硬约束 (Line Count Limits)

- **单文件上限**: 任何 `.go`, `.lua` 文件行数不得超过 **500 行**。
- **组件拆分**:
  - 如果 Lua Controller 或 Go Service 超过 **400 行**，必须按业务维度拆分子 Service 或 Trait/Helper。
- **AI 行为**: 当发现目标文件接近或超过 800 行时，**禁止**直接继续添加功能。必须先提出“瘦身重构方案”，将逻辑外迁后再进行开发。

### 2. 共享逻辑封装原则 (DRY - Don't Repeat Yourself)

- **拒绝原地重复**: 严禁在多个 Page/Screen 中复制相似的逻辑（如：地图调起、图片保存、权限检查、金额格式化）。
- **工具类化 (Utils)**:
  - 纯逻辑、无状态的函数（如时间处理、加密、字符串格式化）必须放入 `/utils/` 或 `pkg/utils/`。

### AI 自我进化与记忆机制 (AI Evolution) - **核心要求**

- **任务后复盘**: AI 在每次完成复杂任务（如重构、修复顽固 Bug、实现新核心逻辑）后，必须主动思考：“本次任务中是否发现了现有规则未覆盖的盲区？是否有值得沉淀的通用模式？”
- **强制规则反哺**: 如果发现了新的通用规律（例如：某个特定框架的坑、某种跨端数据通信的必现错误），AI 必须主动提议并将其写入本文件（`PROMPT.md`）或相关的 `docs/ai/knowledge/` 文档中。
- **演进日志 (Changelog) 约束**: 必须将架构决策、依赖变更、重大重构写入 `docs/ai/logs/changelog.md`。这不仅是给人看的，更是为了下一个接手的 AI 能建立正确的**历史时间线记忆**，避免重复踩坑或推翻之前的正确决策。
