-- 充值/扣账对余额缓存的原子相对增减。
-- KEYS[1]: 余额 key (grant_balance:uid 或 cash_balance:uid)
-- ARGV[1]: 变动量 delta（可正可负；credit 为正，debit 为负）
--
-- 仅当 key 已存在时才 INCRBY：缓存-aside 语义下，key 存在表示它正反映实时余额
-- （请求侧用 DECRBY 扣减），此时按 delta 相对增减可与并发扣减正确叠加，避免用绝对
-- SET 覆盖掉在途扣减。key 不存在则跳过，由下次请求 miss 时从已更新的 DB 懒加载。
if redis.call("EXISTS", KEYS[1]) == 1 then
    return redis.call("INCRBY", KEYS[1], ARGV[1])
end
return -1
