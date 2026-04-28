-- KEYS[1]: 订阅余额Key (grant_balance:user_id)
-- KEYS[2]: 充值余额Key (cash_balance:user_id)
-- ARGV[1]: 需要退还的金额 (amount_cents)
-- ARGV[2]: 当初扣款时，从订阅池扣了多少 (grant_deducted)

local grant_key = KEYS[1]
local cash_key = KEYS[2]
local refund_amount = tonumber(ARGV[1])
local grant_deducted = tonumber(ARGV[2])

if refund_amount <= 0 then
    return 1
end

-- 优先退还到充值余额（因为充值余额不过期，对用户最有利）
-- 假设本次请求总共退款 X。
-- 我们当初扣款的总金额 = cost。
-- 其中从订阅扣了 S，从充值扣了 T (cost = S + T)。
-- 退款时，最多只能往充值池里退回 T，剩下的 (X - T) 退回订阅池。

local cash_deducted = tonumber(ARGV[3]) -- 当初扣款时，从充值池扣了多少

if cash_deducted > 0 then
    if refund_amount <= cash_deducted then
        -- 充值池扣的钱够退
        redis.call("INCRBY", cash_key, refund_amount)
    else
        -- 充值池全退，剩下的退回订阅池
        redis.call("INCRBY", cash_key, cash_deducted)
        local remain_refund = refund_amount - cash_deducted
        redis.call("INCRBY", grant_key, remain_refund)
    end
else
    -- 当初全是从订阅池扣的，直接全退给订阅池
    redis.call("INCRBY", grant_key, refund_amount)
end

return 1