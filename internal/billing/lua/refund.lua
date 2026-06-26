-- 三池退款：按消费逆序退回 cash → grant → sub，各池最多退回其当初扣额。
-- KEYS[1]=sub_balance:uid  KEYS[2]=grant_balance:uid  KEYS[3]=cash_balance:uid
-- ARGV[1]=refund_amount
-- ARGV[2]=sub_deducted  ARGV[3]=grant_deducted  ARGV[4]=cash_deducted
-- 逆序退：尽量把「可退的现金」留给用户。

local amt = tonumber(ARGV[1])
if not amt or amt <= 0 then
    return 1
end
local pd = tonumber(ARGV[2]) or 0
local gd = tonumber(ARGV[3]) or 0
local cd = tonumber(ARGV[4]) or 0

local rem = amt
local function give(key, cap)
    if cap <= 0 or rem <= 0 then
        return
    end
    local t = cap
    if t > rem then t = rem end
    redis.call("INCRBY", key, t)
    rem = rem - t
end

give(KEYS[3], cd) -- 先退 cash
give(KEYS[2], gd) -- 再退 grant
give(KEYS[1], pd) -- 最后退 sub
return 1
