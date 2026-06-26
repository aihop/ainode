-- api_keys 此前仅有 key_string 索引，但大量查询按 user_id 过滤
-- （活跃 Key 计数、用户 Key 列表、admin 按用户聚合），导致全表扫描。
-- 组合索引同时服务「按 user 列表 + 按时间排序」与「按 user 计数」。
CREATE INDEX IF NOT EXISTS idx_api_keys_user_id
ON api_keys (user_id, created_at DESC);
