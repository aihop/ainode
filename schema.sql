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
    grant_expires_at TIMESTAMP
    WITH
        TIME ZONE DEFAULT NULL, -- 订阅周期赠送到期时间
        status INT DEFAULT 1, -- 1: 正常, 0: 禁用
        last_login_at TIMESTAMP
    WITH
        TIME ZONE DEFAULT NULL,
        created_at TIMESTAMP
    WITH
        TIME ZONE DEFAULT NOW()
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
    request_id VARCHAR(100),
    created_at TIMESTAMP
    WITH
        TIME ZONE DEFAULT NOW(),
        PRIMARY KEY (id, created_at)
)
PARTITION BY
    RANGE (created_at);

-- 为 request_id 添加约束（如果需要，必须包含分区键，或者在业务层保证唯一性，这里我们移除 UNIQUE，靠应用层保证）

-- 创建 2026 年各月的分区表
CREATE TABLE IF NOT EXISTS billing_logs_2026_04 PARTITION OF billing_logs FOR
VALUES
FROM ('2026-04-01') TO ('2026-05-01');

CREATE TABLE IF NOT EXISTS billing_logs_2026_05 PARTITION OF billing_logs FOR
VALUES
FROM ('2026-05-01') TO ('2026-06-01');

CREATE TABLE IF NOT EXISTS billing_logs_2026_06 PARTITION OF billing_logs FOR
VALUES
FROM ('2026-06-01') TO ('2026-07-01');

CREATE TABLE IF NOT EXISTS billing_logs_2026_07 PARTITION OF billing_logs FOR
VALUES
FROM ('2026-07-01') TO ('2026-08-01');

CREATE TABLE IF NOT EXISTS billing_logs_2026_08 PARTITION OF billing_logs FOR
VALUES
FROM ('2026-08-01') TO ('2026-09-01');

CREATE TABLE IF NOT EXISTS billing_logs_2026_09 PARTITION OF billing_logs FOR
VALUES
FROM ('2026-09-01') TO ('2026-10-01');

CREATE TABLE IF NOT EXISTS billing_logs_2026_10 PARTITION OF billing_logs FOR
VALUES
FROM ('2026-10-01') TO ('2026-11-01');

CREATE TABLE IF NOT EXISTS billing_logs_2026_11 PARTITION OF billing_logs FOR
VALUES
FROM ('2026-11-01') TO ('2026-12-01');

CREATE TABLE IF NOT EXISTS billing_logs_2026_12 PARTITION OF billing_logs FOR
VALUES
FROM ('2026-12-01') TO ('2027-01-01');

-- 添加索引
CREATE INDEX IF NOT EXISTS idx_api_keys_key_string ON api_keys (key_string);

CREATE INDEX IF NOT EXISTS idx_billing_created_at ON billing_logs (created_at);

CREATE INDEX IF NOT EXISTS idx_billing_user_id ON billing_logs (user_id);
