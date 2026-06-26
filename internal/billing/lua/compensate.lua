-- 三池补扣：结算实际 > 预扣（diff<0）时按消费正序补扣 sub → grant → cash。
-- KEYS[1]=sub_balance:uid  KEYS[2]=grant_balance:uid  KEYS[3]=cash_balance:uid
-- ARGV[1]=amount(>0)  ARGV[2]=policy(all|cash_only|grant_only)
-- 返回 {sub_taken, grant_taken, cash_taken}
-- 以各池当前余额为下限，最多扣到 0(绝不为负);不足部分视为坏账放弃(极少发生)。

local amount = tonumber(ARGV[1])
if not amount or amount <= 0 then
    return {0, 0, 0}
end
local policy = ARGV[2] or "all"

local sp = tonumber(redis.call("GET", KEYS[1]) or "0")
local gr = tonumber(redis.call("GET", KEYS[2]) or "0")
local ca = tonumber(redis.call("GET", KEYS[3]) or "0")

local use_sub = policy ~= "cash_only"
local use_grant = policy ~= "cash_only"
local use_cash = policy ~= "grant_only"

local rem = amount
local function take(key, bal, enabled)
    if not enabled or bal <= 0 or rem <= 0 then
        return 0
    end
    local t = bal
    if t > rem then t = rem end
    redis.call("DECRBY", key, t)
    rem = rem - t
    return t
end

local p = take(KEYS[1], sp, use_sub)
local g = take(KEYS[2], gr, use_grant)
local c = take(KEYS[3], ca, use_cash)
return {p, g, c}
