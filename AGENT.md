# AI Node: 高性能 AI 模型网关与计费系统

> **变更日志 (Changelog)**
> `2026-06-27`: 订阅三池计费落地。新增「订阅实付」池 `users.sub_balance`(+`sub_expires_at`),`grant_balance` 复用为订阅赠送,消费顺序 sub→grant→cash。预扣/退款/补扣 Lua 改三池(逆序退款、正序补扣、下限保护),结算与 Worker 按三池拆分(`splitActual3`),钱包接口返回三池明细。新增统一状态机 `billing.ApplySubscription`(订阅/续费/升级/降级/取消/过期同一入口:旧实付剩余→cash、赠送清零、覆盖新额度,event_id 幂等),接入 webhook 事件 `subscription.apply`/`subscription.cancel`,并加每日订阅过期清理兜底。设计见 docs/ai/subscription-3pool-design.md。
> `2026-06-27`: 修复 webhook 充值只写 `transactions` 漏写 `balance_logs`，导致「资金/余额流水」看不到 webhook 入账；现 `processTransaction` 同事务补写 balance_logs，与管理员直充口径一致，见 Section 8.3.1。webhook 路由对齐 `/api/*` 命名：新增 `/api/webhooks/events`，旧 `/internal/webhooks/events` 过渡期双注册。性能:`pgxpool` 显式配置连接池(默认 MaxConns=20)、新增 `api_keys(user_id,created_at)` 与 `billing_logs(user_id,created_at)` 索引、admin 用户汇总拆为 `/api/admin/users/summary`。
> `2026-06-27`: 修复缓存计价少收：无缓存明细时 prompt 按 `input_price_cents`（而非 cacheMiss=0），cacheMiss 未配回退 inputPrice；`multiplier<=0` 按 1 处理防白嫖；结算计价抽成纯函数 `proxy/pricing.go::computeActualCost` 并加单测；模型并发占位 TTL 2m→15m 防长流式中途丢槽，见 Section 4.1.2 / 4.1.3。
> `2026-06-27`: 新增独立钱包接口 `GET /api/site/wallet`（进账 funded / 用了 spent / 还剩 available + 现金/赠金明细），dashboard 移除钱包块回归活动概览，stats 去除重复的累计消耗（累计消耗唯一出口为 wallet.spent），见 Section 2 与 Section 6。
> `2026-06-27`: 新增结算 outbox 兜底：`settlement_outbox` 表 + `internal/billing/outbox_relay.go`，asynq 投递失败时落库由后台 relay 重投，保证“Redis 已扣费但账单不丢”，见 Section 3 与 Section 4.1。
> `2026-06-27`: 账单结算 Worker 改为单事务（幂等检查+双余额扣减+写流水同事务），修复重试重复扣 DB 余额；充值缓存改用原子 `INCRBY`（`lua/credit_cache.lua` + `CreditBalanceCache`），避免覆盖在途扣减导致超额消费，见 Section 4.1。
> `2026-06-27`: 预扣不足补扣改用原子 `lua/compensate.lua`（带余额下限，绝不为负）；后台常驻协程统一经 `utils.SafeGo` 兜底 panic（防单点 panic 拖垮进程）；context key 收口到 `internal/reqctx`（消除 SA1029）；tiktoken 编码器按模型缓存（`utils.GetTokenizer`）。
> `2026-06-27`: 限流阈值改为可配置（`server.rpm_limit` / `server.tpm_limit`，默认调高为 600 / 2,000,000）；金额展示新增 `centsToMoneyPrecise`（8 位精度，与 10^8 刻度对齐），账单/统计接口的消费金额改用高精度并附原始 `*Cents` 整数，避免小额被四舍五入成 0。
> `2026-06-27`: `GET /api/site/models/groups` 从硬编码列表改为从 `GlobalModelManager`（models 表 active 模型）动态生成、按厂商分组并展示真实单价，见 Section 6。
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
> `2026-06-27`: 新增时间与时区规范（Section 8.8）。所有 API 出口点的时间格式化必须 `.UTC()` 后输出 RFC3339；修复 stats.go trend label 遗漏 `.UTC()` 的一致性问题。
> `2026-06-27`: RPM/TPM 限流升级为按订阅等级差异化（`config.tier_limits`）+ 新增用户+模型级 ModelRPM 限制（防单用户打满某模型配额）。废弃全局固定阈值模式。`reqctx` 新增 `KeyTierLevel`，auth 中间件将 `user_tier` 注入 context，限流中间件读取 tier 后通过 `config.Server.ResolveTierLimit` 获取对应限流参数。tier 未配置的字段自动回落至全局默认，完全向后兼容。见 Section 4.5 与 Section 8.4。
> `2026-06-27`: 新增可配置的请求审计日志（`request_log.enabled`，默认关闭）。新增 `request_logs` 表（`request_id` 唯一幂等）、`internal/billing/request_log.go`（入队）、`internal/worker/request_log.go`（落库）。auth 中间件存原始 body 到 context，`ModifyResponse` 的结算点（成功 / 失败 / 代理错误路径）统一经 Asynq 异步写入，热路径只入队不写库。见 Section 3、Section 4.1 与 Section 7。

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
│   ├── reqctx/              # 请求级 context 的类型安全 key（消除裸字符串 key / SA1029）
│   │   └── keys.go
│   ├── proxy/               # ReverseProxy 核心逻辑、重试机制 (Fallback)
│   │   ├── reverse_proxy.go # 自定义 Transport、ModifyResponse、ErrorHandler
│   │   ├── failure_log.go   # 渠道/模型失败日志与错误分类（从 reverse_proxy 拆出）
│   │   ├── stream_options.go# 给流式请求注入 stream_options.include_usage
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
│   │   │   ├── pre_deduct.lua   # 预扣费原子脚本
│   │   │   ├── refund.lua       # 退款脚本
│   │   │   ├── compensate.lua   # 预扣不足补扣（带余额下限，绝不为负）
│   │   │   └── credit_cache.lua # 充值缓存原子 INCRBY（仅 key 存在时）
│   │   ├── deduct.go        # 预扣费调用入口
│   │   ├── settlement.go    # 多退少补结算 + asynq 入队（失败落 outbox）
│   │   ├── outbox_relay.go  # settlement_outbox 后台 relay 重投
│   │   ├── credit_cache.go  # CreditBalanceCache：充值对缓存做相对增减
│   │   ├── balance.go       # GetUserBalance：优先 Redis、回源 DB
│   │   ├── partition.go     # billing_logs 分区动态创建 + 维护
│   │   └── redis.go         # Redis 余额操作
│   ├── channel/             # 上游渠道池管理
│   │   ├── manager.go       # 内存缓存 + 轮询调度 + capability 过滤
│   │   └── circuit_breaker.go # 渠道断路器状态机与熔断恢复
│   ├── db/                  # sqlc 生成的代码 (Querier, Models)
│   │   ├── db.go
│   │   ├── models.go
│   │   ├── query.sql.go
│   │   ├── outbox_queries.go   # 手写：settlement_outbox 增删查（sqlc 风格）
│   │   └── billing_queries.go  # 手写：累计消耗 / 累计入账汇总（sqlc 风格）
│   ├── middleware/          # 中间件
│   │   ├── lua/
│   │   │   └── rate_limit.lua  # 滑动窗口限流脚本
│   │   ├── auth.go          # 鉴权 + 预扣费中间件
│   │   ├── internal_token.go # 服务间 Internal Token 鉴权（admin/site 共用，常量时间比较）
│   │   ├── rate_limit.go    # RPM/TPM 限流中间件（阈值来自 config）
│   │   └── model_concurrency.go # 模型级并发占位与释放
│   ├── api/                 # HTTP 接口
│   │   ├── admin/           # 管理员 API (渠道/模型 CRUD、账单查询)
│   │   ├── site/            # 用户 API (钱包/仪表盘/统计/Key/模型分组)
│   │   │   └── wallet.go    # 钱包接口：进账/用了/还剩
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
│       ├── error.go         # OpenAI 兼容错误格式封装
│       ├── pricing.go       # 倍率换算（ApplyMultiplier）
│       ├── safego.go        # SafeGo：后台协程 panic 兜底 + 重启
│       └── tokenizer.go     # GetTokenizer：按模型缓存 tiktoken 编码器
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
| `users` | 用户余额与订阅等级 | `cash_balance`, `grant_balance`, `sub_balance`, `tier_level`, `sub_expires_at` |
| `api_keys` | 网关调用凭证 | `key_string`, `user_id`, `allowed_models`, `quota_limit`, `quota_used` |
| `models` | 模型定价、模态、计费模式、倍率与并发上限 | `model_name`, `modality`, `pricing_mode`, `pricing_config`, `billing_policy`, `multiplier`, `max_concurrency` |
| `channels` | 上游渠道配置 | `provider`, `base_url`, `api_key`, `models`, `protocol_type`, `upload_mode`, `model_mapping`, `supports_async`, `weight` |
| `async_tasks` | 异步媒体任务状态与预扣费跟踪 | `request_id`, `task_type`, `provider`, `model_name`, `status`, `upstream_task_id`, `pre_deducted_cents`, `actual_cost_cents` |
| `billing_logs` | 计费流水 | `user_id`, `model_name`, `amount_cents`, `prompt_tokens`, `completion_tokens` |
| `transactions` | 统一资金总账 | `user_id`, `event_id`, `type`, `balance_type`, `direction`, `amount_cents`, `before_balance_cents`, `after_balance_cents`, `source_type`, `source_id` |
| `balance_logs` | 余额变更流水 | `user_id`, `balance_type`, `action_type`, `amount_cents`, `before_balance_cents`, `after_balance_cents`, `operator_name`, `remark` |
| `settlement_outbox` | 结算投递兜底（asynq 投递失败时落库，由 relay 重投） | `request_id`(唯一), `payload`(jsonb), `attempts`, `processed_at` |
| `request_logs` | 请求审计日志（由 `request_log.enabled` 控制） | `request_id`(唯一), `user_id`, `provider`, `public_model_name`, `input_payload`, `prompt_tokens`, `completion_tokens`, `amount_cents`, `is_success`, `created_at` |

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
- **`multiplier <= 0` 一律按 `1.0` 处理**（防止漏配倍率导致费用被乘成 0 即白嫖）；预扣/结算/模型展示三处口径一致。

### 4.1.3 缓存计价规则（prompt 计价口径，预扣与结算必须一致）

结算计价已收口为纯函数 `internal/proxy/pricing.go` 的 `computeActualCost`（含单测）：

- **无缓存命中/未命中明细时**（多数上游不返回 `usage` 的缓存拆分），prompt **全部按 `input_price_cents`** 计——与预扣口径一致，**不得**当成「全部未命中」去套 `cache_miss_price_cents`（未配=0 会系统性少收）。
- **有明细时**：命中按 `cache_hit_price_cents`（折扣）；未命中本质是全价，按 `cache_miss_price_cents`，**未配（≤0）则回退 `input_price_cents`**。
- 修改结算计价逻辑后，必须同步更新 `computeActualCost` 的单测。

### 4.2 订阅周期清零与重置

当主站 (APayShop) 触发订阅续费成功时：

1. 更新用户的 `tier_level` 和 `sub_expires_at`
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
   - 支持 **按订阅等级（tier_level）差异化**：通过 `config.tier_limits` 配置各 tier 的 RPM/TPM/ModelRPM，
     未配置的 tier 回落到全局 `rpm_limit`/`tpm_limit`，见 [config.go](internal/config/config.go) 的 `ResolveTierLimit`。
   - 已废弃旧的全局固定阈值模式。
2. **模型级并发限制**: 根据 `models.max_concurrency` 在 Redis 中做模型并发占位，超限时直接拒绝并退款预扣。
3. **用户+模型级 RPM 限制**: 通过 `tier_limits.<tier>.model_rpm` 配置，防止单用户打满某模型配额。
4. **优先级路由（规划中，未实现）**: 计划根据 `api_keys.tier_level` 在高并发时优先保证订阅用户请求。

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
| GET | `/api/site/wallet` | 钱包概览：进账 `funded` / 用了 `spent` / 还剩 `available` + 现金/赠金明细（累计消耗的唯一出口；金额均附 `*Cents` 原始整数） |
| GET | `/api/site/dashboard` | 用户仪表盘概览（活动维度：热力图、调用数、用量、配额、近 30 天支出；不含钱包） |
| GET | `/api/site/stats` | 详细使用统计（range 内支出 `summary.totalCost`、趋势、模型分布） |
| GET | `/api/site/billing-logs/list` | 用户账单记录（每条含高精度 `cost` 与原始 `amountCents`） |
| GET/POST | `/api/site/api-keys/list\|create\|delete\|status\|rotate` | API Key 全生命周期管理 |
| GET | `/api/site/models/groups` | 可用模型分组（来自 models 表 active 模型，按厂商动态生成 + 真实单价） |

> 接口职责划分：钱包余额相关只在 `/api/site/wallet`（变化快、可高频刷新）；dashboard/stats 专注活动与统计，避免金额口径重复。

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
- 无 sqlc 工具链时新增查询可手写在独立文件（`db/outbox_queries.go`、`db/billing_queries.go`），遵循 sqlc 风格；切回 sqlc 生成时需迁移到 `query.sql` 并删除手写文件，避免方法重复定义。
- **资金/审计的多步写必须包在同一 `pgx.Tx` 事务内**（幂等检查 + 余额扣减 + 写流水），否则重试会重复扣减（见 `worker/billing.go`）。

### 8.3.1 并发与金额约定

- **后台常驻协程必须经 `utils.SafeGo` 启动**：裸 `go func` 的未捕获 panic 会让整个进程退出；SafeGo 捕获 panic 并退避重启。
- **计费/结算/审计等需在客户端断流后仍完成的写**，必须脱离请求 context（用带超时上限的 `context.Background()`，见 `reverse_proxy.go` 的 `newBillingWriteCtx`），不能用 `req.Context()`，否则断流会取消结算/退款导致资金不一致。
- **金额对外展示**：余额等大额可用 `centsToMoney`（2 位）；单次/小额消费必须用 `centsToMoneyPrecise`（8 位，与 10^8 刻度对齐）并附原始 `*Cents` 整数，前端累加/对账一律用整数，避免小额被舍成 0 或浮点误差。
- **缓存余额的增量操作用相对（DECRBY/INCRBY），禁止用绝对 SET 覆盖**（订阅重置这种本就是覆盖语义的除外），否则会覆盖在途扣减。
- **任何改用户余额的入账/扣账（充值、套餐发放、管理员直充、webhook 入账等）必须在同一 DB 事务内同时写 `transactions`（资金总账）和 `balance_logs`（余额流水）**，并更新 `users` 余额。三者口径要一致——「资金/余额流水」展示页读的是 `balance_logs`，只写 `transactions` 会导致流水里看不到这笔（webhook 曾漏写 balance_logs 即此坑）。参考实现:`admin/users.go AdjustUserBalance` 与 `webhook/processor.go processTransaction`。

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

### 8.7 命名规范（统一,勿混用）

- **三个资金池**统一命名为 `sub`(订阅实付) / `grant`(订阅赠送) / `cash`(充值),消费顺序 sub → grant → cash:
  - DB 列:`sub_balance` / `grant_balance` / `cash_balance`;异步任务预扣记 `sub_deducted`。
  - Redis key:`sub_balance:<uid>` / `grant_balance:<uid>` / `cash_balance:<uid>`(用 `billing.SubBalanceKey/GrantBalanceKey/CashBalanceKey`,勿手拼)。
  - Go:`billing.Deduction{Sub,Grant,Cash}`、`SettlementRequest.SubDeducted`、`reqctx.KeySubDeducted`。
  - **禁止再出现 `sub_paid` 旧名**。
- **订阅到期时间**统一用 `sub_expires_at`(已废弃 `grant_expires_at`)。
- **所有对外 HTTP API 的 JSON 字段一律 camelCase**(`userId`/`eventId`/`balanceType`/`sourceId`/`amountCents`…),禁止 snake_case。这适用于 admin / site / webhook 全部接口与事件(含 `order.paid` 的 `integration.transaction`、`subscription.apply`/`subscription.cancel`)。
- **金额**对外字段:高精度浮点(`centsToMoneyPrecise`)+ 原始整数 `xxxCents` 并存,累加/对账用整数。

### 8.8 时间与时区规范 (UTC Output)

> **核心原则**: 所有对外 API 返回的时间字段**必须**先转为 UTC 再格式化输出。数据库层使用 PostgreSQL `TIMESTAMP WITH TIME ZONE`（本质存 UTC）。Go 后端不存储用户级时区配置——前端由 APayShop 根据 `settings.timezone` 渲染。

#### 统一格式化入口

- **唯一出口**: `internal/utils/time.go` 提供的 `utils.FormatTime(value any) string` 是**全项目唯一的 UTC 时间格式化函数**。
- **支持类型**: `time.Time` 和 `pgtype.Timestamptz` 等实现了 `Value() (time.Time, bool)` 接口的类型。
- **输出格式**: RFC3339 UTC（如 `"2026-06-27T08:30:00Z"`）。
- **绝对禁止**在各包中重复定义 `formatTime()` 或手写 `.UTC().Format(time.RFC3339)` 内联调用。
- **已由该函数覆盖的包**: `admin`, `site`, `webhook`。

#### 与 APayShop 的分工

- **Go 后端职责**: 存储 UTC、输出 UTC（统一通过 `utils.FormatTime`）。
- **APayShop 前端职责**: 按 `settings.timezone` 配置渲染为本地时区显示（`useFormatTime()` composable）。
- 当前 Go 后端不存储用户时区配置，也不做时区转换——这一层由 APayShop 的服务端代理层与前端组合完成。

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
