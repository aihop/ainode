# Changelog

## 2026-03-27: 阶段一：基础脚手架初始化

**架构决策与依赖变更：**
- 初始化了 Go 模块 `ainode-gateway`。
- 创建了严格遵循 `PROMPT.md` 的项目目录结构（包括 `cmd/api`, `internal/proxy`, `internal/adapter`, `internal/billing`, `internal/channel`, `internal/db`, `internal/middleware`, `internal/config`, `docs/ai/logs`, `docs/ai/knowledge`）。
- 使用 `sqlc` (`github.com/sqlc-dev/sqlc`) 结合 `pgx/v5` 引擎作为 PostgreSQL 数据库交互层。
- 定义了完整的 `schema.sql` 和基础的 `query.sql` 并成功生成了 `internal/db` 相关的代码，保证类型安全的数据库操作。
- 引入了 `github.com/go-chi/chi/v5` 作为核心路由框架。
- 引入了 `github.com/redis/go-redis/v9` 作为 Redis 客户端依赖，为后续的预扣费与限流功能做准备。

**待办：**
系统基础版本已全部完工，接下来可以进入测试和部署阶段。

## 2026-03-27: 引入 Asynq 重构计费写入架构

**架构优化与漏洞修复：**
- **解耦数据库强依赖**: 引入了 `github.com/hibiken/asynq`。移除了 `Settle` 函数中直接起 Goroutine 写 PostgreSQL 的逻辑。
- **任务生产 (Producer)**: 现在的 `Settle` 只负责在 Redis 中进行扣费，随后将计费数据序列化为 JSON，作为一个 `billing:record_log` 任务推送到 Redis 的任务队列中。
- **任务消费 (Worker)**: 在 `internal/worker/billing.go` 中实现了专门的消费者，负责从队列拉取任务，安全地执行 DB 的 Update 和 Insert 操作。
- **容错与重试机制**: 如果写数据库发生超时或报错，任务会留在队列中按指数退避策略自动重试（最多 5 次），彻底解决了高并发下数据库连接池被打满导致账单丢失的严重隐患。

**架构决策与变动：**
- **移除 `golang-migrate`**: 为了降低项目初期的心智负担和维护成本，废弃了基于 `up/down` 脚本的迁移工具。
- **恢复单文件 Schema**: 重新启用了根目录下的 `schema.sql`，并为所有的 `CREATE TABLE` 和 `CREATE INDEX` 添加了 `IF NOT EXISTS` 防御性语句。
- **启动时自动建表**: 在 `cmd/api/main.go` 初始化的流程中，加入了自动读取并执行 `schema.sql` 的逻辑。现在，部署时无需手动执行建表脚本，服务启动即可自动完成数据库初始化。

**架构优化与漏洞修复：**
- **泛解析路由支持 (`main.go`)**: 将路由注册由写死的 `/v1/chat/completions` 改为了 `/*`，使网关能无缝接管 `/v1/models`、`/v1/images/generations` 等所有周边接口。
- **动态计费鉴别**: 在 Auth 中间件中通过识别 `URL.Path`，智能区分“需要预扣费的生成请求”和“免费/透传的元数据请求”，既保证了计费安全，又实现了最大程度的协议兼容。
- **预扣费异常退款 (Refund)**: 增加了资金安全底线。在限流中间件 (`rate_limit.go`) 触发或代理发生彻底不可用 (`StatusBadGateway`) 时，系统会自动调用 `billing.Refund` 将用户已经预扣的钱**全额退还**至 Redis。
- **非流式响应计费**: 针对 `stream: false` 的普通请求，`ModifyResponse` 拦截器现在能自动读取上游返回的 JSON，解析其中的 `usage.completion_tokens`，并调用 `Settle` 完成准确的计费结算。
- **前端友好性 (CORS & Error Format)**: 
  - 移除了原有的 60 秒全局超时，防止长时间的 AI 生成任务被意外掐断。
  - 引入了 `github.com/go-chi/cors` 中间件，允许任意前端（如 NextChat 等网页版应用）跨域直连网关。
  - 新增 `internal/utils/error.go`，把所有的 HTTP 拦截错误包装成了标准 OpenAI 格式的 JSON，防止客户端 SDK 解析报错。

**架构决策与业务逻辑：**
- **鉴权与解析中间件 (`AuthAndPreDeductMiddleware`)**: 实现了请求体拦截和解析，粗略估算用户的 Prompt Tokens。在中间件中直接触发了我们在第三阶段编写的 `billing.PreDeduct`，如果余额不足则直接拦截，防止非法请求打到后端。将核心的上下文变量（如 `user_id`, `request_id` 等）注入 `context` 中贯穿整个生命周期。
- **路由组装 (`cmd/api/main.go`)**: 
  - 使用 `go-chi/chi` 将所有模块连接起来。
  - 按顺序挂载了：基础日志/恢复中间件 -> 鉴权预扣费中间件 -> RPM/TPM限流中间件 -> `GatewayProxy`。
- **优雅启停 (Graceful Shutdown)**: 监听了 `SIGINT` 和 `SIGTERM` 信号。给予系统 10 秒的缓冲时间，确保所有正在流式输出的 HTTP 请求能够被正常断开，并触发 `TallyReader` 的 `Close()` 方法，保证所有异步的计费结算 Goroutine 都能将最终数据安全写入 PostgreSQL。


## 2026-03-27: 阶段四：代理与流式拦截 (包含跨协议重构)

**架构决策与业务逻辑：**
- **模型精确路由 (Model-based Routing)**: 
  - 重构了 `schema.sql`，给 `channels` 表增加了 `models` 字段（支持逗号分隔列表和 `*` 通配符）。
  - 修改了 `channel.Manager.GetNextChannel(modelName)`，现在路由不仅做负载均衡，还会精确匹配客户端请求中指定的模型，实现了**按需寻址**。
- **One API 协议适配器 (`internal/adapter`)**: 
  - 引入了 `ProviderAdapter` 接口，实现了基于厂商的协议转换设计。
  - 实现了 `AnthropicAdapter`。它能在 `ReverseProxy` 转发前，将客户端发来的 OpenAI 格式 JSON 和 `/v1/chat/completions` 路径，动态“翻译”成 Claude 原生的 `/v1/messages` 格式。
  - **SSE 双向翻译**: 修改了 `TallyReader`，现在它会先调用 `Adapter.TransformSSEEvent`，将上游各种奇奇怪怪的流式事件（如 `content_block_delta`）实时翻译回纯正的 OpenAI `choices[0].delta` 格式。
- **SSE 断流止损拦截器 (`TallyReader`)**: 
  - 实现了 `internal/proxy/tally_reader.go`。它包装了上游返回的 `http.Response.Body`，能够在流式响应中实时解析 `text/event-stream`。
  - 策略：优先读取最后一块 JSON 里的 `usage` 字段获取官方精准消耗；如果不存在，则作为兜底使用 `tiktoken-go` (或单词切分) 来实时估算 Token。
  - **断流止损**: 当客户端突然关闭连接，Go 的 `http` 框架会调用 `Close()`。`TallyReader` 在 `Close()` 被调用时，会立即触发 `OnComplete` 回调，将已消耗的 Token 送去结算，**绝不浪费上游额度**。
- **高可用重试代理 (`FallbackTransport`)**: 
  - 实现了 `internal/proxy/reverse_proxy.go` 中的 `FallbackTransport`。
  - 当上游渠道返回 `429 Too Many Requests` 或 `5xx` 错误时，拦截错误并静默地从 `channel.GlobalManager` 获取下一个可用渠道，替换 `Authorization` 头后重新发起请求，对客户端保持完全透明。
  - 在 `ModifyResponse` 中注入了与第三阶段 `billing.Settle` 的对接逻辑，实现了**代理-拦截-计费**的完整闭环。


## 2026-03-27: 阶段三：计费与限流引擎

**架构决策与业务逻辑：**
- **防超卖预扣费**: 编写了 `internal/billing/lua/pre_deduct.lua`，并使用 Go 的 `go:embed` 嵌入到代码中。实现了原子化的预扣费逻辑。当 Redis 中缺失用户余额时，系统会回源到 PostgreSQL 并更新 Redis（防止冷启动或缓存过期导致服务不可用）。
- **多退少补结算 (Settlement)**: 在 `internal/billing/settlement.go` 中实现了结算逻辑。请求结束后，根据真实的 `ActualCostCents` 计算差额，并利用 `IncrBy` 原子地修正 Redis 余额。同时，开启 Goroutine 异步将账单流水（`billing_logs`）写入 PostgreSQL，并对 DB 中的长期余额做最终同步。
- **高并发限流**: 在 `internal/middleware/lua/rate_limit.lua` 中实现了基于 Redis 的滑动/固定窗口计数器。在 `internal/middleware/rate_limit.go` 中封装了 `RPMAndTPMMiddleware`，支持同时限制单用户的 `RPM`（每分钟请求数）和 `TPM`（每分钟预估 Token 数），直接拦截异常并发。


## 2026-03-27: 阶段二：渠道与配置缓存

**架构决策与依赖变更：**
- 引入了 `github.com/spf13/viper` 用于配置加载，支持从 `config.yaml` 和环境变量中读取服务端口、数据库 DSN、Redis 连接信息。
- 在 `internal/channel` 中实现了渠道池管理器 `Manager`，支持从数据库加载激活渠道，并实现了基础的 Round-Robin 轮询获取。
- 在 `internal/config` 中实现了模型管理器 `ModelManager`，它作为内存缓存动态提供模型定价（未命中则查询 DB）。
- 在 `internal/config` 中增加了 `StartBackgroundSync` 函数，这是一个异步协程，用于定期（如每 5 分钟）同步 DB 中的最新渠道和配置到内存中。

