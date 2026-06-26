-- 补扣脚本：当结算实际金额 > 预扣金额（diff < 0）时，原子地补扣差额。
-- KEYS[1]: 订阅余额Key (grant_balance:user_id)
-- KEYS[2]: 充值余额Key (cash_balance:user_id)
-- ARGV[1]: 需要补扣的差额 (extra_cents, > 0)
-- ARGV[2]: 计费策略 (both | cash_only | grant_only)
-- 返回: {从grant补扣, 从cash补扣}
--
-- 设计：
--   * 与预扣顺序一致（both 时先扣 grant 再扣 cash）；
--   * 以当前余额为下限，最多扣到 0，绝不把余额扣成负数；
--   * 补扣不足的部分视为坏账放弃（该路径理论上极少发生），优先保证余额一致性。

local grant_key = KEYS[1]
local cash_key = KEYS[2]
local amount = tonumber(ARGV[1])
local policy = ARGV[2] or "both"

if not amount or amount <= 0 then
    return {0, 0}
end

local grant_bal = tonumber(redis.call("GET", grant_key) or "0")
local cash_bal = tonumber(redis.call("GET", cash_key) or "0")

local function take(key, bal, want)
    local t = math.min(bal, want)
    if t > 0 then
        redis.call("DECRBY", key, t)
    end
    return t
end

if policy == "grant_only" then
    return {take(grant_key, grant_bal, amount), 0}
end

if policy == "cash_only" then
    return {0, take(cash_key, cash_bal, amount)}
end

-- both: 先扣订阅，剩余再扣充值
local g = take(grant_key, grant_bal, amount)
local c = take(cash_key, cash_bal, amount - g)
return {g, c}
