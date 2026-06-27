-- 异步任务纳入三池:记录预扣中来自订阅实付池(sub)的金额,
-- 以便任务失败/取消退款时正确退回 sub。
ALTER TABLE async_tasks ADD COLUMN IF NOT EXISTS sub_deducted BIGINT NOT NULL DEFAULT 0;
