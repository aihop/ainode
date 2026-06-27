-- 账单记录增加 log_type (consumption/refund) 和预扣金额字段,
-- 使得全额退费场景也有账单可查,退费金额 = pre_deducted_cents - amount_cents.
ALTER TABLE billing_logs ADD COLUMN IF NOT EXISTS log_type VARCHAR(20) NOT NULL DEFAULT 'consumption';
ALTER TABLE billing_logs ADD COLUMN IF NOT EXISTS pre_deducted_cents BIGINT NOT NULL DEFAULT 0;
