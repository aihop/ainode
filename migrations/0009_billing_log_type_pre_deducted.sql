-- 账单记录增加 log_type (consumption/refund) 和预扣金额字段,
-- 使得全额退费场景也有账单可查,退费金额 = pre_deducted_cents - amount_cents.

-- 修复分区 ownership: billing_logs 是分区表,子分区可能由应用运行时
-- 其他 role 创建,ALTER TABLE 父表时 PG 需要锁所有子分区,
-- 若 owner 不一致则报 42501。先将所有子分区 owner 统一为 current_user。
DO $$
DECLARE
    r RECORD;
BEGIN
    FOR r IN
        SELECT c.relname AS tablename
        FROM pg_class c
        JOIN pg_inherits i ON c.oid = i.inhrelid
        JOIN pg_class p ON i.inhparent = p.oid
        WHERE p.relname = 'billing_logs'
          AND pg_get_userbyid(c.relowner) != current_user
    LOOP
        EXECUTE format('ALTER TABLE %I OWNER TO %I', r.tablename, current_user);
        RAISE NOTICE 'Changed owner of % to %', r.tablename, current_user;
    END LOOP;
END $$;

ALTER TABLE billing_logs ADD COLUMN IF NOT EXISTS log_type VARCHAR(20) NOT NULL DEFAULT 'consumption';
ALTER TABLE billing_logs ADD COLUMN IF NOT EXISTS pre_deducted_cents BIGINT NOT NULL DEFAULT 0;
