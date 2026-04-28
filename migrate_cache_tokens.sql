-- Migration script to add cache token fields to billing_logs table
DO $$
BEGIN
    -- Check if cache_hit_tokens exists, if not add it
    IF NOT EXISTS (
        SELECT 1 
        FROM information_schema.columns 
        WHERE table_name='billing_logs' AND column_name='cache_hit_tokens'
    ) THEN
        ALTER TABLE billing_logs ADD COLUMN cache_hit_tokens INT DEFAULT 0;
        RAISE NOTICE 'Added cache_hit_tokens column';
    END IF;

    -- Check if cache_miss_tokens exists, if not add it
    IF NOT EXISTS (
        SELECT 1 
        FROM information_schema.columns 
        WHERE table_name='billing_logs' AND column_name='cache_miss_tokens'
    ) THEN
        ALTER TABLE billing_logs ADD COLUMN cache_miss_tokens INT DEFAULT 0;
        RAISE NOTICE 'Added cache_miss_tokens column';
    END IF;
END $$;
