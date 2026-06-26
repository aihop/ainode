-- KEYS[1]: 订阅余额Key (grant_balance:user_id)
-- KEYS[2]: 充值余额Key (cash_balance:user_id)
-- ARGV[1]: 预估最大扣费金额 (amount_cents)
-- ARGV[2]: 计费策略 (both | cash_only | grant_only)

local grant_key = KEYS[1]
local cash_key = KEYS[2]
local cost = tonumber(ARGV[1])
local billing_policy = ARGV[2] or "both"

local grant_bal_str = redis.call("GET", grant_key)
local cash_bal_str = redis.call("GET", cash_key)

-- 如果缓存中没有余额信息，返回 -2 要求服务回源查 DB
if not grant_bal_str or not cash_bal_str then
    return {-2, 0, 0}
end

local grant_bal = tonumber(grant_bal_str)
local cash_bal = tonumber(cash_bal_str)

if billing_policy == "cash_only" then
    if cash_bal < cost then
        return {-1, 0, 0}
    end

    redis.call("DECRBY", cash_key, cost)
    return {1, 0, cost}
end

if billing_policy == "grant_only" then
    if grant_bal < cost then
        return {-1, 0, 0}
    end

    redis.call("DECRBY", grant_key, cost)
    return {1, cost, 0}
end

if (grant_bal + cash_bal) < cost then
    return {-1, 0, 0} -- 余额不足
end

if grant_bal >= cost then
    -- 订阅余额充足，全部从订阅扣
    redis.call("DECRBY", grant_key, cost)
    return {1, cost, 0}
else
    -- 订阅余额不足，先扣完订阅，剩下的扣充值余额
    local remain_cost = cost - grant_bal
    redis.call("SET", grant_key, 0)
    redis.call("DECRBY", cash_key, remain_cost)
    return {1, grant_bal, remain_cost}
end
