-- 用户表
CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    email VARCHAR(50) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    nickname VARCHAR(50),
    avatar_url VARCHAR(255),
    cash_balance BIGINT DEFAULT 0, -- 充值余额（永不过期），金额放大 10^8 倍存储
    grant_balance BIGINT DEFAULT 0, -- 订阅周期赠送余额（按周期清零），放大 10^8 倍存储
    tier_level INT DEFAULT 0, -- 订阅等级 (0: Free, 1: Pro, 2: Enterprise)，用于网关高并发优先级控制
    -- 订阅到期时间：sub_expires_at 由下方 ALTER 添加
    status INT DEFAULT 1, -- 1: 正常, 0: 禁用
    last_login_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- API Keys 表 (用户网关调用凭证，独立表)
CREATE TABLE IF NOT EXISTS api_keys (
    id SERIAL PRIMARY KEY,
    name VARCHAR(50) NOT NULL,
    key_string VARCHAR(64) UNIQUE NOT NULL,
    user_id INT REFERENCES users (id),
    -- 订单ID
    order_id VARCHAR(100),
    -- 产品ID
    product_id INT,
    -- tier_level 冗余用户订阅等级，用于网关层快速读取并进行 Priority Routing
    tier_level INT DEFAULT 0,
    -- quota_limit 月度配额
    quota_limit BIGINT DEFAULT 0,
    -- quota_used 已用配额
    quota_used BIGINT DEFAULT 0,
    -- allowed_models 允许访问的模型列表 (JSON 数组 ["gpt-4o", "claude-3-opus-20240229"]，为空或 [] 表示无限制)
    allowed_models JSONB DEFAULT '[]',
    status INT DEFAULT 1, -- 1: 正常, 0: 禁用
    created_at TIMESTAMP
    WITH
        TIME ZONE DEFAULT NOW()
);

-- 模型价格表（区分输入输出价格）
CREATE TABLE IF NOT EXISTS models (
    id SERIAL PRIMARY KEY,
    model_name VARCHAR(50) UNIQUE NOT NULL,
    input_price_cents BIGINT NOT NULL, -- 每 1M Token 的输入价格
    output_price_cents BIGINT NOT NULL, -- 每 1M Token 的输出价格
    cache_hit_price_cents BIGINT NOT NULL DEFAULT 0, -- 每 1M Token 的缓存读取价格
    cache_miss_price_cents BIGINT NOT NULL DEFAULT 0, -- 每 1M Token 的缓存创建价格
    multiplier REAL NOT NULL DEFAULT 1.0, -- 用户端计费倍率 (如 0.60x 表示打6折)
    billing_policy VARCHAR(20) NOT NULL DEFAULT 'both', -- both | cash_only | grant_only
    modality VARCHAR(20) NOT NULL DEFAULT 'text', -- text | vision | image | video | audio
    pricing_mode VARCHAR(20) NOT NULL DEFAULT 'token', -- token | request
    pricing_config JSONB NOT NULL DEFAULT '{}'::jsonb, -- 请求计费等扩展配置
    max_concurrency INT NOT NULL DEFAULT 0, -- 模型级并发限制，0 表示不限制
    status INT DEFAULT 1
);

ALTER TABLE models
ADD COLUMN IF NOT EXISTS max_concurrency INT NOT NULL DEFAULT 0;

ALTER TABLE models
ADD COLUMN IF NOT EXISTS billing_policy VARCHAR(20) NOT NULL DEFAULT 'both';

ALTER TABLE models
ADD COLUMN IF NOT EXISTS modality VARCHAR(20) NOT NULL DEFAULT 'text';

ALTER TABLE models
ADD COLUMN IF NOT EXISTS pricing_mode VARCHAR(20) NOT NULL DEFAULT 'token';

ALTER TABLE models
ADD COLUMN IF NOT EXISTS pricing_config JSONB NOT NULL DEFAULT '{}'::jsonb;

-- 上游渠道池表（高可用配置）
CREATE TABLE IF NOT EXISTS channels (
    id SERIAL PRIMARY KEY,
    name VARCHAR(50) NOT NULL,
    provider VARCHAR(20) NOT NULL, -- openai, anthropic, gemini 等
    base_url VARCHAR(255) NOT NULL,
    api_key VARCHAR(255) NOT NULL,
    models TEXT NOT NULL DEFAULT '', -- 逗号分隔的支持模型列表 (如 "gpt-4,gpt-3.5-turbo")
    protocol_type VARCHAR(20) NOT NULL DEFAULT 'openai', -- openai | anthropic | gemini | custom
    upload_mode VARCHAR(20) NOT NULL DEFAULT 'url', -- url | base64 | multipart | file_id
    model_mapping JSONB NOT NULL DEFAULT '{}'::jsonb, -- 上游模型映射配置
    supports_async BOOLEAN NOT NULL DEFAULT FALSE, -- 是否支持异步任务接口
    weight INT DEFAULT 1, -- 负载均衡权重
    status INT DEFAULT 1 -- 1: 正常, 0: 故障/禁用
);

ALTER TABLE channels
ADD COLUMN IF NOT EXISTS protocol_type VARCHAR(20) NOT NULL DEFAULT 'openai';

ALTER TABLE channels
ADD COLUMN IF NOT EXISTS upload_mode VARCHAR(20) NOT NULL DEFAULT 'url';

ALTER TABLE channels
ADD COLUMN IF NOT EXISTS model_mapping JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE channels
ADD COLUMN IF NOT EXISTS supports_async BOOLEAN NOT NULL DEFAULT FALSE;

-- 异步媒体任务表（视频生成/编辑等）
CREATE TABLE IF NOT EXISTS async_tasks (
    id VARCHAR(36) PRIMARY KEY,
    user_id INT NOT NULL REFERENCES users (id),
    channel_id INT REFERENCES channels (id),
    request_id VARCHAR(100) UNIQUE NOT NULL,
    task_type VARCHAR(32) NOT NULL, -- video_generation | video_edit
    provider VARCHAR(32) NOT NULL DEFAULT '',
    model_name VARCHAR(100) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'queued', -- queued | running | succeeded | failed | canceled
    upstream_task_id VARCHAR(100),
    input_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    output_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    error_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    pre_deducted_cents BIGINT NOT NULL DEFAULT 0,
    grant_deducted BIGINT NOT NULL DEFAULT 0,
    cash_deducted BIGINT NOT NULL DEFAULT 0,
    actual_cost_cents BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    submitted_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    finished_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    canceled_at TIMESTAMP WITH TIME ZONE DEFAULT NULL
);

-- 计费流水表
-- 修改 billing_logs 为按月分区表
CREATE TABLE IF NOT EXISTS billing_logs (
    id UUID NOT NULL,
    user_id INT REFERENCES users (id),
    channel_id INT REFERENCES channels (id),
    model_name VARCHAR(50) NOT NULL,
    prompt_tokens INT DEFAULT 0, -- 输入 Token 数量
    completion_tokens INT DEFAULT 0, -- 输出 Token 数量
    cache_hit_tokens INT DEFAULT 0, -- 缓存读取 Token 数量 (上游返回)
    cache_miss_tokens INT DEFAULT 0, -- 缓存创建 Token 数量 (上游返回)
    amount_cents BIGINT NOT NULL, -- 本次请求实际扣费 (折算倍率后)
    log_type VARCHAR(20) NOT NULL DEFAULT 'consumption', -- consumption | refund
    pre_deducted_cents BIGINT NOT NULL DEFAULT 0, -- 预扣金额 (10^8 放大)
    request_id VARCHAR(100),
    created_at TIMESTAMP
    WITH
        TIME ZONE DEFAULT NOW(),
        PRIMARY KEY (id, created_at)
)
PARTITION BY
    RANGE (created_at);

-- 为 request_id 添加约束（如果需要，必须包含分区键，或者在业务层保证唯一性，这里我们移除 UNIQUE，靠应用层保证）

-- 分区表改为启动时由 partition.go 动态创建，确保始终包含当前月 + 未来 6 个月
-- 每日后台定时维护，避免人为遗忘

-- 统一资金流水总账（先承接后台余额调整，后续扩展到支付/退款/扣费）
CREATE TABLE IF NOT EXISTS transactions (
    id BIGSERIAL PRIMARY KEY,
    user_id INT NOT NULL REFERENCES users (id),
    event_id VARCHAR(120),
    type VARCHAR(50) NOT NULL, -- topup | refund | grant_issue | grant_reset | usage_deduct | admin_adjust
    balance_type VARCHAR(20) NOT NULL, -- cash | grant
    direction VARCHAR(20) NOT NULL, -- credit | debit
    amount_cents BIGINT NOT NULL,
    before_balance_cents BIGINT NOT NULL,
    after_balance_cents BIGINT NOT NULL,
    source_type VARCHAR(50) NOT NULL DEFAULT 'admin', -- order | subscription | billing | admin | system
    source_id VARCHAR(100) NOT NULL DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'completed', -- pending | completed | failed
    remark TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- 余额变更流水（后台直充/赠送等）
CREATE TABLE IF NOT EXISTS balance_logs (
    id BIGSERIAL PRIMARY KEY,
    transaction_id BIGINT REFERENCES transactions (id),
    user_id INT NOT NULL REFERENCES users (id),
    balance_type VARCHAR(20) NOT NULL, -- cash | grant
    action_type VARCHAR(50) NOT NULL DEFAULT 'admin_recharge',
    amount_cents BIGINT NOT NULL, -- 本次变更金额，放大 10^8 倍
    before_balance_cents BIGINT NOT NULL,
    after_balance_cents BIGINT NOT NULL,
    operator_admin_id INT,
    operator_name VARCHAR(100) NOT NULL DEFAULT '',
    remark TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- 渠道失败日志（用于追查上游异常、限流与断路器熔断原因）
CREATE TABLE IF NOT EXISTS channel_failure_logs (
    id BIGSERIAL PRIMARY KEY,
    channel_id INT NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    request_id VARCHAR(100) NOT NULL DEFAULT '',
    model_name VARCHAR(100) NOT NULL DEFAULT '',
    provider VARCHAR(32) NOT NULL DEFAULT '',
    upstream_base_url VARCHAR(255) NOT NULL DEFAULT '',
    error_type VARCHAR(50) NOT NULL DEFAULT '',
    status_code INT NOT NULL DEFAULT 0,
    response_body TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    latency_ms INT NOT NULL DEFAULT 0,
    circuit_state VARCHAR(20) NOT NULL DEFAULT 'closed',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- 用户侧模型失败日志（面向用户展示自己的模型请求失败记录）
CREATE TABLE IF NOT EXISTS model_failure_logs (
    id BIGSERIAL PRIMARY KEY,
    user_id INT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    api_key_id INT REFERENCES api_keys (id) ON DELETE SET NULL,
    request_id VARCHAR(100) NOT NULL DEFAULT '',
    model_name VARCHAR(100) NOT NULL DEFAULT '',
    provider VARCHAR(32) NOT NULL DEFAULT '',
    error_type VARCHAR(50) NOT NULL DEFAULT '',
    error_code VARCHAR(50) NOT NULL DEFAULT '',
    status_code INT NOT NULL DEFAULT 0,
    error_message TEXT NOT NULL DEFAULT '',
    response_body TEXT NOT NULL DEFAULT '',
    latency_ms INT NOT NULL DEFAULT 0,
    is_retryable BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- 添加索引
-- 订阅三池:新增「订阅实付」池 sub_balance(10^8 放大),消费序最高;
-- grant_balance 复用为「订阅赠送」,cash_balance 为充值余额。sub_expires_at 为本订阅周期到期。
ALTER TABLE users ADD COLUMN IF NOT EXISTS sub_balance BIGINT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN IF NOT EXISTS sub_expires_at TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS idx_users_sub_expires_at ON users (sub_expires_at) WHERE sub_expires_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_api_keys_key_string ON api_keys (key_string);

-- api_keys 大量按 user_id 过滤（活跃 Key 计数、用户 Key 列表、admin 按用户聚合），
-- 组合索引同时服务「按 user 列表 + 按时间排序」与「按 user 计数」。
CREATE INDEX IF NOT EXISTS idx_api_keys_user_id ON api_keys (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_billing_created_at ON billing_logs (created_at);

CREATE INDEX IF NOT EXISTS idx_billing_user_id ON billing_logs (user_id);

-- 用户侧统计/账单/仪表盘均按 (user_id, created_at) 过滤，组合索引避免单列索引 + 过滤
CREATE INDEX IF NOT EXISTS idx_billing_user_created_at ON billing_logs (user_id, created_at DESC);

-- 异步任务也纳入三池:记录预扣中来自订阅实付池的金额,失败/取消退款时正确退回 sub。
ALTER TABLE async_tasks ADD COLUMN IF NOT EXISTS sub_deducted BIGINT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_async_tasks_user_created_at ON async_tasks (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_async_tasks_status_created_at ON async_tasks (status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_transactions_user_created_at ON transactions (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_transactions_type_created_at ON transactions (type, created_at DESC);

CREATE UNIQUE INDEX IF NOT EXISTS idx_transactions_event_id_unique ON transactions (event_id)
WHERE event_id IS NOT NULL AND event_id <> '';

CREATE INDEX IF NOT EXISTS idx_balance_logs_user_created_at ON balance_logs (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_balance_logs_operator_created_at ON balance_logs (operator_admin_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_channel_failure_logs_channel_created_at ON channel_failure_logs (channel_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_channel_failure_logs_created_at ON channel_failure_logs (created_at DESC);

CREATE INDEX IF NOT EXISTS idx_model_failure_logs_user_created_at ON model_failure_logs (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_model_failure_logs_user_model_created_at ON model_failure_logs (user_id, model_name, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_model_failure_logs_request_id ON model_failure_logs (request_id);

-- settlement_outbox：结算任务无法投递到 asynq（如 Redis 故障）时的持久化兜底，
-- 由后台 relay 定时重投，保证“Redis 已扣费但账单不丢”。
CREATE TABLE IF NOT EXISTS settlement_outbox (
    id BIGSERIAL PRIMARY KEY,
    request_id VARCHAR(100) NOT NULL UNIQUE,
    payload JSONB NOT NULL,
    attempts INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_settlement_outbox_pending ON settlement_outbox (created_at) WHERE processed_at IS NULL;
