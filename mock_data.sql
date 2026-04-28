-- Generate 30 days of mock billing data for user_id = 1 with strictly matching costs
DO $$
DECLARE
    curr_date TIMESTAMP;
    end_date TIMESTAMP := NOW();
    -- Keep within the 2026-04 partition to avoid partition missing errors
    start_date TIMESTAMP := '2026-04-01 00:00:00';
    
    -- Model names
    m_gpt4o VARCHAR := 'gpt-4o';
    m_claude VARCHAR := 'claude-3-opus';
    m_gemini VARCHAR := 'gemini-1.5-pro';
    m_gpt35 VARCHAR := 'gpt-3.5-turbo';
    
    model_arr VARCHAR[] := ARRAY[m_gpt4o, m_claude, m_gemini, m_gpt35];
    channel_arr INT[] := ARRAY[1, 2, 3];
    
    i INT;
    daily_requests INT;
    chosen_model VARCHAR;
    p_tokens INT;
    c_tokens INT;
    hit_tokens INT;
    miss_tokens INT;
    
    -- Price variables (per 1M tokens, stored as cents * 10^8)
    -- E.g. $5.00 per 1M tokens = 500 cents = 500 * 100,000,000 = 50,000,000,000
    price_in BIGINT;
    price_out BIGINT;
    price_hit BIGINT;
    price_miss BIGINT;
    
    amount BIGINT;
BEGIN
    -- 1. Ensure user 1 exists
    IF NOT EXISTS (SELECT 1 FROM users WHERE id = 1) THEN
        INSERT INTO users (id, email, password_hash, nickname, cash_balance) 
        VALUES (1, 'user1@example.com', 'hash', 'Test User', 10000000000);
    END IF;

    -- 2. Ensure channels exist
    IF NOT EXISTS (SELECT 1 FROM channels WHERE id = 1) THEN
        INSERT INTO channels (id, name, provider, base_url, api_key) VALUES (1, 'OpenAI-1', 'openai', 'x', 'x');
        INSERT INTO channels (id, name, provider, base_url, api_key) VALUES (2, 'Anthropic-1', 'anthropic', 'x', 'x');
        INSERT INTO channels (id, name, provider, base_url, api_key) VALUES (3, 'Gemini-1', 'gemini', 'x', 'x');
    END IF;

    -- 3. Ensure models exist with realistic pricing
    -- GPT-4o: $5.00 in / $15.00 out per 1M (500M cents in, 1500M cents out)
    INSERT INTO models (model_name, input_price_cents, output_price_cents, cache_hit_price_cents, cache_miss_price_cents)
    VALUES (m_gpt4o, 50000000000, 150000000000, 25000000000, 50000000000)
    ON CONFLICT (model_name) DO UPDATE SET 
        input_price_cents = EXCLUDED.input_price_cents, output_price_cents = EXCLUDED.output_price_cents;

    -- Claude-3-Opus: $15.00 in / $75.00 out per 1M
    INSERT INTO models (model_name, input_price_cents, output_price_cents, cache_hit_price_cents, cache_miss_price_cents)
    VALUES (m_claude, 150000000000, 750000000000, 0, 0)
    ON CONFLICT (model_name) DO NOTHING;

    -- Gemini 1.5 Pro: $3.50 in / $10.50 out per 1M
    INSERT INTO models (model_name, input_price_cents, output_price_cents, cache_hit_price_cents, cache_miss_price_cents)
    VALUES (m_gemini, 35000000000, 105000000000, 0, 0)
    ON CONFLICT (model_name) DO NOTHING;

    -- GPT-3.5-Turbo: $0.50 in / $1.50 out per 1M
    INSERT INTO models (model_name, input_price_cents, output_price_cents, cache_hit_price_cents, cache_miss_price_cents)
    VALUES (m_gpt35, 5000000000, 15000000000, 0, 0)
    ON CONFLICT (model_name) DO NOTHING;


    -- 4. Clean up existing mock data for user 1 to avoid duplicates
    DELETE FROM billing_logs WHERE user_id = 1;

    curr_date := start_date;
    
    WHILE curr_date <= end_date LOOP
        -- Random number of requests per day (30 to 80 for more volume)
        daily_requests := floor(random() * 50 + 30)::INT;
        
        FOR i IN 1..daily_requests LOOP
            chosen_model := model_arr[floor(random() * 4 + 1)::INT];
            
            -- Fetch actual prices for the chosen model
            SELECT input_price_cents, output_price_cents, cache_hit_price_cents, cache_miss_price_cents 
            INTO price_in, price_out, price_hit, price_miss
            FROM models WHERE model_name = chosen_model;
            
            -- Generate realistic token counts
            p_tokens := floor(random() * 2000 + 100)::INT;
            c_tokens := floor(random() * 800 + 50)::INT;
            
            -- Simulate caching for models that support it
            IF price_hit > 0 THEN
                hit_tokens := floor(random() * (p_tokens / 2))::INT;
                miss_tokens := p_tokens - hit_tokens;
                p_tokens := 0; -- If using cache, regular prompt tokens are 0 for that portion
            ELSE
                hit_tokens := 0;
                miss_tokens := 0;
            END IF;
            
            -- CRITICAL: Calculate EXACT cost based on tokens and prices
            -- Formula: (tokens * price_per_1M) / 1,000,000
            amount := (
                (p_tokens::BIGINT * price_in) +
                (c_tokens::BIGINT * price_out) +
                (hit_tokens::BIGINT * price_hit) +
                (miss_tokens::BIGINT * price_miss)
            ) / 1000000;
            
            -- Ensure minimum charge of 1 cent (100000000 in internal format) if calculated is 0
            IF amount = 0 THEN
                amount := 100000000;
            END IF;

            INSERT INTO billing_logs (
                id, 
                user_id, 
                channel_id, 
                model_name, 
                prompt_tokens, 
                completion_tokens, 
                cache_hit_tokens,
                cache_miss_tokens,
                amount_cents, 
                request_id, 
                created_at
            ) VALUES (
                gen_random_uuid(),
                1,
                channel_arr[floor(random() * 3 + 1)::INT],
                chosen_model,
                p_tokens,
                c_tokens,
                hit_tokens,
                miss_tokens,
                amount,
                'req-' || md5(random()::text),
                curr_date + (random() * 24 * 60 || ' minutes')::interval
            );
        END LOOP;
        
        curr_date := curr_date + INTERVAL '1 day';
    END LOOP;
END $$;