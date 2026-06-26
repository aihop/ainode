CREATE TABLE IF NOT EXISTS balance_logs (
    id BIGSERIAL PRIMARY KEY,
    user_id INT NOT NULL REFERENCES users (id),
    balance_type VARCHAR(20) NOT NULL,
    action_type VARCHAR(50) NOT NULL DEFAULT 'admin_recharge',
    amount_cents BIGINT NOT NULL,
    before_balance_cents BIGINT NOT NULL,
    after_balance_cents BIGINT NOT NULL,
    operator_admin_id INT,
    operator_name VARCHAR(100) NOT NULL DEFAULT '',
    remark TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_balance_logs_user_created_at
ON balance_logs (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_balance_logs_operator_created_at
ON balance_logs (operator_admin_id, created_at DESC);
