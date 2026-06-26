-- name: GetUserByAPIKey :one
SELECT
    u.id,
    u.email,
    u.password_hash,
    u.nickname,
    u.avatar_url,
    u.cash_balance,
    u.grant_balance,
    u.tier_level AS user_tier,
    u.grant_expires_at,
    u.status,
    u.last_login_at,
    u.created_at,
    ak.id AS key_id,
    ak.name AS key_name,
    ak.key_string,
    ak.order_id,
    ak.product_id,
    ak.tier_level AS key_tier,
    ak.quota_limit,
    ak.quota_used,
    ak.allowed_models,
    ak.status AS key_status
FROM users u
    JOIN api_keys ak ON u.id = ak.user_id
WHERE
    ak.key_string = $1
    AND ak.status = 1
    AND u.status = 1
LIMIT 1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1 LIMIT 1;

-- name: UpdateUserTopupBalance :exec
UPDATE users
SET
    cash_balance = cash_balance + $2
WHERE
    id = $1;

-- name: UpdateUserSubBalance :exec
UPDATE users
SET
    grant_balance = $2,
    grant_expires_at = $3,
    tier_level = $4
WHERE
    id = $1;

-- name: UpdateUserGrantBalance :exec
UPDATE users
SET
    grant_balance = grant_balance + $2
WHERE
    id = $1;

-- name: CheckBillingLogExists :one
SELECT EXISTS (
        SELECT 1
        FROM billing_logs
        WHERE
            request_id = $1
    );

-- name: GetModelByName :one
SELECT
    id,
    model_name,
    input_price_cents,
    output_price_cents,
    cache_hit_price_cents,
    cache_miss_price_cents,
    multiplier,
    billing_policy,
    max_concurrency,
    status
FROM models
WHERE
    model_name = $1
    AND status = 1
LIMIT 1;

-- name: ListActiveModels :many
SELECT
    id,
    model_name,
    input_price_cents,
    output_price_cents,
    cache_hit_price_cents,
    cache_miss_price_cents,
    multiplier,
    billing_policy,
    max_concurrency,
    status
FROM models
WHERE
    status = 1
ORDER BY model_name ASC;

-- name: ListActiveChannels :many
SELECT * FROM channels WHERE status = 1 ORDER BY weight DESC;

-- name: UpdateChannelStatus :exec
UPDATE channels SET status = $2 WHERE id = $1;

-- name: CreateBillingLog :one
INSERT INTO
    billing_logs (
        id,
        user_id,
        channel_id,
        model_name,
        prompt_tokens,
        completion_tokens,
        cache_hit_tokens,
        cache_miss_tokens,
        amount_cents,
        request_id
    )
VALUES (
        $1,
        $2,
        $3,
        $4,
        $5,
        $6,
        $7,
        $8,
        $9,
        $10
    ) RETURNING *;

-- ==========================================
-- Admin API Queries (Admin Panel CRUD)
-- ==========================================

-- name: CreateChannel :one
INSERT INTO
    channels (
        name,
        provider,
        base_url,
        api_key,
        weight,
        models,
        status
    )
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: UpdateChannel :one
UPDATE channels
SET
    name = $2,
    provider = $3,
    base_url = $4,
    api_key = $5,
    weight = $6,
    models = $7,
    status = $8
WHERE
    id = $1 RETURNING *;

-- name: DeleteChannel :exec
DELETE FROM channels WHERE id = $1;

-- name: GetChannelByID :one
SELECT * FROM channels WHERE id = $1 LIMIT 1;

-- name: ListAllChannels :many
SELECT * FROM channels ORDER BY id DESC;

-- name: CreateModel :one
INSERT INTO
    models (
        model_name,
        input_price_cents,
        output_price_cents,
        cache_hit_price_cents,
        cache_miss_price_cents,
        multiplier,
        billing_policy,
        max_concurrency,
        status
    )
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING
    id,
    model_name,
    input_price_cents,
    output_price_cents,
    cache_hit_price_cents,
    cache_miss_price_cents,
    multiplier,
    billing_policy,
    max_concurrency,
    status;

-- name: UpdateModel :one
UPDATE models
SET
    input_price_cents = $2,
    output_price_cents = $3,
    cache_hit_price_cents = $4,
    cache_miss_price_cents = $5,
    multiplier = $6,
    billing_policy = $7,
    max_concurrency = $8,
    status = $9
WHERE
    model_name = $1 RETURNING
    id,
    model_name,
    input_price_cents,
    output_price_cents,
    cache_hit_price_cents,
    cache_miss_price_cents,
    multiplier,
    billing_policy,
    max_concurrency,
    status;

-- name: DeleteModel :exec
DELETE FROM models WHERE model_name = $1;

-- name: ListAllModelsForAdmin :many
SELECT
    id,
    model_name,
    input_price_cents,
    output_price_cents,
    cache_hit_price_cents,
    cache_miss_price_cents,
    multiplier,
    billing_policy,
    max_concurrency,
    status
FROM models
ORDER BY model_name ASC;

-- name: ListBillingLogs :many
SELECT *
FROM billing_logs
ORDER BY created_at DESC
LIMIT $1
OFFSET
    $2;

-- name: CountBillingLogs :one
SELECT COUNT(*) FROM billing_logs;

-- ==========================================
-- Internal API Queries (User Dashboard Stats)
-- ==========================================

-- name: GetUserStatsSummary :one
SELECT 
    COALESCE(SUM(amount_cents), 0)::bigint as total_amount,
    COALESCE(SUM(prompt_tokens), 0)::bigint as total_prompt_tokens,
    COALESCE(SUM(completion_tokens), 0)::bigint as total_completion_tokens,
    COALESCE(SUM(cache_hit_tokens), 0)::bigint as total_cache_hit_tokens,
    COALESCE(SUM(cache_miss_tokens), 0)::bigint as total_cache_miss_tokens
FROM billing_logs
WHERE user_id = $1 AND created_at >= $2;

-- name: GetUserTrendSeries :many
SELECT 
    DATE(created_at) as date,
    COUNT(*) as request_count,
    COALESCE(SUM(amount_cents), 0)::bigint as daily_amount
FROM billing_logs
WHERE user_id = $1 AND created_at >= $2
GROUP BY DATE(created_at)
ORDER BY date ASC;

-- name: GetUserModelStats :many
SELECT 
    model_name,
    COUNT(*) as request_count,
    COALESCE(SUM(amount_cents), 0)::bigint as total_amount
FROM billing_logs
WHERE user_id = $1 AND created_at >= $2
GROUP BY model_name
ORDER BY total_amount DESC;

-- name: GetUserBillingLogs :many
SELECT 
    id,
    created_at,
    model_name,
    prompt_tokens,
    completion_tokens,
    amount_cents
FROM billing_logs
WHERE user_id = sqlc.arg(user_id)
  AND (sqlc.arg(model_name)::varchar = '' OR model_name = sqlc.arg(model_name))
  AND created_at >= sqlc.arg(start_time)
ORDER BY created_at DESC
LIMIT sqlc.arg(limit_val) OFFSET sqlc.arg(offset_val);

-- name: CountUserBillingLogs :one
SELECT COUNT(*)
FROM billing_logs
WHERE user_id = sqlc.arg(user_id)
  AND (sqlc.arg(model_name)::varchar = '' OR model_name = sqlc.arg(model_name))
  AND created_at >= sqlc.arg(start_time);

-- name: CountActiveUserAPIKeys :one
SELECT COUNT(*) FROM api_keys WHERE user_id = $1 AND status = 1;

-- name: GetUserAPIKeys :many
SELECT
    id,
    name,
    key_string,
    order_id,
    product_id,
    tier_level,
    quota_limit,
    quota_used,
    allowed_models,
    status,
    created_at
FROM api_keys
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: CreateAPIKey :one
INSERT INTO api_keys (
    name,
    key_string,
    user_id,
    allowed_models,
    status,
    tier_level
) VALUES (
    $1, $2, $3, $4, $5, $6
) RETURNING id, name, key_string, allowed_models, status, created_at;

-- name: DeleteAPIKey :exec
DELETE FROM api_keys WHERE id = $1 AND user_id = $2;

-- name: UpdateAPIKeyStatus :exec
UPDATE api_keys SET status = $1 WHERE id = $2 AND user_id = $3;

-- name: UpdateAPIKeyName :exec
UPDATE api_keys SET name = $1 WHERE id = $2 AND user_id = $3;

-- name: RotateAPIKey :exec
UPDATE api_keys SET key_string = $1 WHERE id = $2 AND user_id = $3;
