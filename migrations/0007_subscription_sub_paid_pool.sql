-- 订阅三池:新增「订阅实付」池。grant_balance 复用为订阅赠送,cash 为充值余额。
ALTER TABLE users ADD COLUMN IF NOT EXISTS sub_paid_balance BIGINT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN IF NOT EXISTS sub_expires_at TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS idx_users_sub_expires_at ON users (sub_expires_at) WHERE sub_expires_at IS NOT NULL;
