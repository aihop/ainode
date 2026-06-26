-- 用户侧统计/账单/仪表盘查询均为 WHERE user_id = ? AND created_at >= ?，
-- 加组合索引避免「单列索引 + 过滤」，分区表会在各分区各建一份。
CREATE INDEX IF NOT EXISTS idx_billing_user_created_at
ON billing_logs (user_id, created_at DESC);
