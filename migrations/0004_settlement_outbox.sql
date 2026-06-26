-- 结算账单 outbox 兜底表：当结算无法投递到 asynq（如 Redis 故障）时持久化到此表，
-- 由后台 relay 定时重投，保证“Redis 已扣费但账单不丢”。
CREATE TABLE IF NOT EXISTS settlement_outbox (
    id BIGSERIAL PRIMARY KEY,
    request_id VARCHAR(100) NOT NULL UNIQUE,
    payload JSONB NOT NULL,
    attempts INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_settlement_outbox_pending
ON settlement_outbox (created_at)
WHERE processed_at IS NULL;
