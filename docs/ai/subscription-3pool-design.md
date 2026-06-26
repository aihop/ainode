# 订阅三池计费设计稿

> 状态：**已按推荐默认值实现(2026-06-27)**。复用 `grant_balance` 作订阅赠送、新增 `sub_balance`。
> 关键实现:`pre_deduct/refund/compensate.lua`(三池)、`billing/subscription.go`(ApplySubscription 状态机)、
> `billing/subscription_expiry.go`(过期清理)、webhook `subscription.apply`/`subscription.cancel`。
> 标 ⚠️ 的决策点若需调整(尤其消费顺序、过期处理),改对应实现即可。

## 0. 目标

支持「订阅 = 实付 + 赠送」的混合额度，并正确处理 订阅 / 续费 / 升级 / 降级 / 取消 / 过期 全生命周期：

- 例:套餐 100 元/月，赠送 700 元 → 用户本期可用 800。
- 消费顺序:**订阅实付 → 订阅赠送 → 充值余额**。
- 取消/过期:赠送清零;实付剩余转入充值余额(不没收)。
- 升级/降级:旧订阅按上述收尾,再发放新套餐。

---

## 1. 资金池(三池)

| 池 | DB 列 | Redis key | 消费序 | 结束(取消/过期/换套餐)时 |
|---|---|---|---|---|
| 订阅实付 | `sub_balance`(**新增**) | `sub_balance:<uid>` | 1 | 剩余 → 转 `cash_balance` |
| 订阅赠送 | `grant_balance`(**复用现有**) | `grant_balance:<uid>` | 2 | 清零 |
| 充值余额 | `cash_balance`(现有) | `cash_balance:<uid>` | 3 | 不变 |

**迁移友好**:复用现有 `grant_balance` 作为「订阅赠送」(语义本就如此,数据/链路不动),只**新增 `sub_balance`** 作为最高优先级池。即在现有 `grant→cash` 两级前面**插入一级 `sub`**。

新增字段:
- `users.sub_balance BIGINT NOT NULL DEFAULT 0`(10^8 放大,同其它金额)
- `users.sub_tier_level`/复用 `tier_level`

---

## 2. 消费(预扣):三级有序扣减

`pre_deduct.lua` 重写为三池有序扣减,返回各池扣额(供结算/退款对账)。

```lua
-- KEYS[1]=sub  KEYS[2]=grant  KEYS[3]=cash
-- ARGV[1]=cost  ARGV[2]=policy(默认 "all")
-- 返回 {status, sub_deducted, grant_deducted, cash_deducted}
-- status: 1 成功 / -1 余额不足 / -2 缓存缺失(回源 DB 后重试)
local function getn(k) local v=redis.call("GET",k); if not v then return nil end; return tonumber(v) end
local sp, gr, ca = getn(KEYS[1]), getn(KEYS[2]), getn(KEYS[3])
if sp==nil or gr==nil or ca==nil then return {-2,0,0,0} end

local cost = tonumber(ARGV[1])
-- 总额不足直接拒
if sp + gr + ca < cost then return {-1,0,0,0} end

local rem = cost
local function take(key, bal)
  local t = math.min(bal, rem)
  if t > 0 then redis.call("DECRBY", key, t); rem = rem - t end
  return t
end
local p = take(KEYS[1], sp)   -- 先实付
local g = take(KEYS[2], gr)   -- 再赠送
local c = take(KEYS[3], ca)   -- 最后余额
return {1, p, g, c}
```

> ⚠️ 决策#1:消费顺序 = `sub → grant → cash`(已按你说的)。
> billing_policy 仍保留 `cash_only`/`grant_only` 等特例的话,在 Go 侧选择传哪几个 key 即可。

---

## 3. 结算(多退少补):三池拆分

`onComplete` 已知预扣各池扣额 `(p,g,c)` 与实际费用 `actual`:

- `diff = (p+g+c) - actual`
- `diff > 0`(预扣多了,退):按**消费逆序退回**——先退 cash,再 grant,最后 sub(让可退的实付尽量留在用户手里)。
- `diff < 0`(预扣少了,补):按**消费正序补扣**(sub→grant→cash),用带下限的原子补扣(同现有 `compensate.lua` 思路,扩成三池)。

退款/补扣脚本由现有 `refund.lua` / `compensate.lua` 扩成三池版本。

> ⚠️ 决策:退款逆序(cash→grant→sub)是为了「尽量把可退现金留住」。如要严格「从哪扣退哪」,可改为按比例,但逆序更简单且对用户友好。

---

## 4. 统一状态机:`applySubscription`

订阅/续费/升级/降级/取消/过期 **全部走这一个原子操作**:

```text
applySubscription(uid, newPaid, newGrant, newExpiresAt, tier):
  # —— 旧订阅收尾(固定规则)——
  movedToCash = 当前 sub 剩余        # 实付剩余 → 转 cash(不没收)
  cash        += movedToCash
  # grant(赠送)直接被下面覆盖为 newGrant,相当于清零后再发
  # —— 应用新订阅 ——
  sub       = newSub
  grant          = newGrant
  sub_expires_at = newExpiresAt
  tier_level     = tier
  # —— 审计(同事务)——
  写 transactions + balance_logs:
    - grant_reset      : 旧 grant 清零(记录被清额)
    - sub_to_cash : movedToCash(实付转余额)        [movedToCash>0 时]
    - sub_issue   : newSub 发放                    [newSub>0 时]
    - grant_issue      : newGrant 发放                   [newGrant>0 时]
```

各事件入参:

| 事件 | newPaid | newGrant | newExpiresAt |
|---|---|---|---|
| 首次订阅 / 续费 / 升级 / 降级 | 新套餐实付 | 新套餐赠送 | 本期末 |
| 取消 / 过期 | 0 | 0 | null |

> **升级/降级无需专门分支**:APayShop 把「换套餐后的最终 paid/grant/expires」发来,直接 `applySubscription` 即可。proration(补差价/按比例退)由 APayShop 算,ainode 不算钱。

### 4.1 实付剩余转 cash 的并发安全

`movedToCash` 必须取 **Redis 实时 sub**(非滞后 DB),否则在途消费会被重复计。建议用一个 Redis Lua 原子完成:

```lua
-- KEYS[1]=sub  KEYS[2]=grant KEYS[3]=cash
-- ARGV[1]=newSub ARGV[2]=newGrant
-- 返回 movedToCash(旧实付剩余,供 DB 对账/审计)
local moved = tonumber(redis.call("GET", KEYS[1]) or "0")
if moved > 0 then redis.call("INCRBY", KEYS[3], moved) end   -- 实付剩余 → cash(相对增减)
redis.call("SET", KEYS[1], ARGV[1])                          -- 覆盖新实付(重置语义)
redis.call("SET", KEYS[2], ARGV[2])                          -- 覆盖新赠送(重置语义)
return moved
```

DB 侧在同一事务用 `applySubscription` 的列更新 + 审计;Redis 用上面脚本。两者以 `movedToCash` 对齐。

---

## 5. 事件接入(APayShop → ainode)

| APayShop 事件 | ainode 动作 |
|---|---|
| `order.paid`(订阅产品)/ 订阅生效 / 续费 / 升级 / 降级 | `applySubscription(paid, grant, expires, tier)` |
| 订阅取消(**当前未接,必加**) | `applySubscription(0, 0, null, 0)` |
| (无取消事件兜底)每日定时 | `sub_expires_at < now` 的用户 → `applySubscription(0,0,null,0)` |
| 纯充值 `topup` | 现有逻辑(只动 cash) |

> ⚠️ 决策#4:APayShop 的订阅/升级/取消事件需带上**最终的 paid / grant / expiresAt / tier**。这是 ainode 做纯状态机的前提。
> 事件需要扩展 `integration.transaction` 的字段(增加 `paid_amount` / `grant_amount` / `expires_at` / `tier`),或新增事件类型。

---

## 6. 过期处理(每日定时任务)

`utils.SafeGo` 起一个每日任务:
```sql
-- 找出到期未续费的用户
SELECT id FROM users WHERE sub_expires_at < now() AND (sub_balance>0 OR grant_balance>0)
```
对每个执行 `applySubscription(0,0,null,0)`(实付转 cash、赠送清零、审计)。
> ⚠️ 决策#2:过期时实付剩余 → cash(推荐,真金白银不没收);仅赠送清零。

---

## 7. 审计与展示

- 所有池变动都写 `transactions` + `balance_logs`(同事务),`action_type` 区分:`sub_issue` / `grant_issue` / `grant_reset` / `sub_to_cash` / `usage_deduct`。
- 钱包接口 `/api/site/wallet` 增加三池明细:
  ```json
  "sub": .., "subCents": ..,
  "grant":   .., "grantCents": ..,
  "cash":    .., "cashCents": ..,
  "available": (三池之和), "availableCents": ..,
  "subExpiresAt": "..."
  ```

---

## 8. 迁移

1. `users` 加 `sub_balance`、`sub_expires_at`(默认 0/null)。
2. 现有 `grant_balance` 语义不变(= 订阅赠送),无需搬数据。
3. `pre_deduct.lua` / `refund.lua` / `compensate.lua` 扩三池;Go 侧预扣/结算/worker 改三池拆分。
4. Redis:用户首次请求 cache-miss 时按新逻辑加载三个 key。
5. 历史用户 `sub_balance=0`,自然兼容(没有订阅实付池)。

---

## 9. 分阶段落地

- **Phase 1**:三池数据结构 + 三级预扣/结算/退款/补扣 + 钱包展示 + 单测。
- **Phase 2**:`applySubscription` 状态机 + 事件接入(订阅/续费/升级/降级/**取消**)+ 每日过期任务 + 审计。

---

## 10. 待你确认(决策清单)

1. **消费顺序** = `sub → grant → cash`?(已按你说的)
2. **过期/取消时**:`sub` 剩余 → cash(推荐),`grant` 清零?
3. **升级时旧赠送清零**(统一规则)?还是要保留较大赠送(破例,不建议)?
4. **APayShop 事件**能否在 订阅/升级/取消 时带上最终 `paid / grant / expiresAt / tier`?
5. **退款顺序** = 逆序 cash→grant→sub(推荐)?
