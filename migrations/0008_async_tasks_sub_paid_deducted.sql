-- 异步任务纳入三池:记录预扣中来自订阅实付池(sub_paid)的金额,
-- 以便任务失败/取消退款时正确退回 sub_paid。
ALTER TABLE async_tasks ADD COLUMN IF NOT EXISTS sub_paid_deducted BIGINT NOT NULL DEFAULT 0;
