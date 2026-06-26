CREATE TABLE IF NOT EXISTS transactions (
    id BIGSERIAL PRIMARY KEY,
    user_id INT NOT NULL REFERENCES users (id),
    type VARCHAR(50) NOT NULL,
    balance_type VARCHAR(20) NOT NULL,
    direction VARCHAR(20) NOT NULL,
    amount_cents BIGINT NOT NULL,
    before_balance_cents BIGINT NOT NULL,
    after_balance_cents BIGINT NOT NULL,
    source_type VARCHAR(50) NOT NULL DEFAULT 'admin',
    source_id VARCHAR(100) NOT NULL DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'completed',
    remark TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_transactions_user_created_at
ON transactions (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_transactions_type_created_at
ON transactions (type, created_at DESC);

ALTER TABLE balance_logs
ADD COLUMN IF NOT EXISTS transaction_id BIGINT REFERENCES transactions (id);
