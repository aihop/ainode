# 三池 / 订阅计费 — 对接契约（前端 & APayShop）

> 一页速查。字段一律 **camelCase**;金额对外用元(decimal),并附 `xxxCents`(10^8 放大整数,累加/对账用)。
> 鉴权:服务间接口都用 `Authorization: Bearer <internal.token>`(webhook 也支持 HMAC)。

## 1. 三个资金池

| 池 | 含义 | 消费顺序 | 结束(取消/过期/换套餐) |
|---|---|---|---|
| `sub` | 订阅实付(用户为订阅实际付的钱) | 1(最先) | 剩余 → 转 `cash`(不没收) |
| `grant` | 订阅赠送 | 2 | 清零 |
| `cash` | 充值余额 | 3(最后) | 不变,永不过期 |

扣费顺序 **sub → grant → cash**;退款逆序 cash → grant → sub。

---

## 2. 钱包 `GET /api/site/wallet`(前端)

Header: `Authorization: Bearer <internal.token>`、`X-Internal-User-Id: <uid>`

```json
{
  "code": 0, "msg": "success",
  "data": {
    "funded": 100.0,   "fundedCents": 10000000000,   // 累计有效入账
    "spent": 26.5,     "spentCents": 2650000000,     // 累计消耗
    "available": 73.5, "availableCents": 7350000000, // 还剩(三池之和)
    "sub":   .., "subCents":   ..,                   // 订阅实付余额
    "grant": .., "grantCents": ..,                   // 订阅赠送余额
    "cash":  .., "cashCents":  ..                    // 充值余额
  }
}
```
展示三件套:进账 `funded` / 用了 `spent` / 还剩 `available`。

---

## 3. Webhook `POST /api/webhooks/events`(APayShop → ainode)

Header: `Authorization: Bearer <internal.token>`(或 HMAC:`X-APayShop-Timestamp` + `X-APayShop-Signature = hex(hmac_sha256(token, ts + "." + body))`)
金额单位:**元(decimal)**。幂等键:`eventId`(同一逻辑事件必须稳定唯一)。

### 3.1 订阅生效 / 续费 / 升级 / 降级 → `subscription.apply`
APayShop 把「换套餐后本期最终值」算好发来(补差价/proration 由 APayShop 负责,ainode 只应用结果)。
```json
{
  "event": "subscription.apply",
  "timestamp": "2026-06-27T10:00:00Z",
  "data": {
    "eventId": "sub:apply:<subId>:<cycleSeq>",
    "userId": 123,
    "paidAmount": 100.0,                 // 本期订阅实付 → sub 池
    "grantAmount": 700.0,                // 本期赠送 → grant 池
    "expiresAt": "2026-07-27T00:00:00Z", // 本期到期
    "tier": 2,
    "sourceId": "order-or-sub-id",
    "remark": "Pro 月付续费"
  }
}
```
ainode 行为:旧 sub 剩余 → cash、旧 grant 清零,写入新 sub/grant/到期/等级。

### 3.2 取消订阅 → `subscription.cancel`
```json
{
  "event": "subscription.cancel",
  "timestamp": "...",
  "data": { "eventId": "sub:cancel:<subId>:<ts>", "userId": 123, "sourceId": "<subId>", "remark": "用户取消" }
}
```
ainode 行为:grant 清零、sub 剩余 → cash。
> 自然过期未续费:可不发事件,ainode 每小时兜底清理(按 `sub_expires_at`)。

### 3.3 充值/购买 → `order.paid`(沿用)
```json
{
  "event": "order.paid",
  "data": {
    "id": "order-123", "userId": 123, "amount": 10.0,
    "integration": {
      "transaction": {
        "enabled": true,
        "type": "topup",          // 或 grant_issue
        "balanceType": "cash",    // 或 grant   ← camelCase
        "direction": "credit",
        "amount": 10.0,
        "sourceId": "order-123",  // ← camelCase
        "remark": "..."
      }
    }
  }
}
```
> ⚠️ `integration.transaction` 的 `balanceType` / `sourceId` 现为 **camelCase**(原 snake_case 已废弃)。

### 3.4 响应
```json
{ "applied": true, "alreadyProcessed": false }   // alreadyProcessed:true = 幂等命中(已处理,无副作用)
```
非订阅/无法映射的事件 → `{ "ignored": true, "event": "..." }`。

---

## 4. 契约要点
- **幂等**:webhook 以 `eventId`(写入 `transactions.event_id` 唯一约束)去重;重复推送不会重复入账/转移。重试请用同一 `eventId`。
- **可靠性**:APayShop 应在本地事务提交后发送,失败重试到 2xx;`alreadyProcessed:true` 也算成功,勿无限重试 4xx。
- **职责**:钱的计算(定价/proration/退款金额)在 APayShop;ainode 只管资金池状态机与计量。
- **命名**:全 camelCase;金额传元,ainode 内部按 10^8 放大存储。
