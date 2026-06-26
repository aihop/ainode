-- 三池有序预扣：sub_paid(订阅实付) → grant(订阅赠送) → cash(充值余额)
-- KEYS[1]=sub_paid_balance:uid  KEYS[2]=grant_balance:uid  KEYS[3]=cash_balance:uid
-- ARGV[1]=cost  ARGV[2]=policy(all | cash_only | grant_only)
-- 返回 {status, sub_paid_deducted, grant_deducted, cash_deducted}
--   status: 1 成功 / -1 余额不足 / -2 缓存缺失(回源 DB 后重试)

local sp_str = redis.call("GET", KEYS[1])
local gr_str = redis.call("GET", KEYS[2])
local ca_str = redis.call("GET", KEYS[3])
-- 任一池缓存缺失 → 要求回源
if not sp_str or not gr_str or not ca_str then
    return {-2, 0, 0, 0}
end

local sp = tonumber(sp_str)
local gr = tonumber(gr_str)
local ca = tonumber(ca_str)
local cost = tonumber(ARGV[1])
local policy = ARGV[2] or "all"

-- 计费策略决定哪些池参与:
--   cash_only  → 仅 cash
--   grant_only → 订阅池(sub_paid + grant),不含 cash
--   其它(all)  → 三池全用
local use_sub_paid = policy ~= "cash_only"
local use_grant = policy ~= "cash_only"
local use_cash = policy ~= "grant_only"

local avail = 0
if use_sub_paid then avail = avail + sp end
if use_grant then avail = avail + gr end
if use_cash then avail = avail + ca end
if avail < cost then
    return {-1, 0, 0, 0}
end

local rem = cost
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

local p = take(KEYS[1], sp, use_sub_paid)
local g = take(KEYS[2], gr, use_grant)
local c = take(KEYS[3], ca, use_cash)
return {1, p, g, c}
