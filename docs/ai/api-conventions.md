# ainode API 规范（状态码 / 分页 / 返回结构）

> 本规范是 ainode 所有 HTTP 接口的统一约定。分两类 API,**约定不同,不要混用**:
> - **A. 网关代理 `/v1/*`**:OpenAI 兼容,格式由 OpenAI 生态决定,**保持原样**。
> - **B. 管理/站点 API `/api/*`**(site / admin):ainode 自有,**统一遵循本规范**。
> - **C. Webhook 回调 `/api/webhooks/*`**:服务端↔服务端回调,**豁免** B 类信封,返回扁平结果对象(如 `{applied,alreadyProcessed}` / `{ignored,event}`),契约见 `subscription-billing-contract.md`。

---

## 1. 通用约定

- **字段命名**:一律 `camelCase`。
- **金额**:对外同时给「高精度浮点 + 原始整数 `xxxCents`(10^8 放大)」;累加/对账用整数。
- **时间**:一律 **RFC3339 UTC 字符串**(如 `2026-06-27T10:00:00Z`),禁止本地化格式(`2006-01-02 15:04:05`)。
- **Content-Type**:`application/json; charset=utf-8`。
- **空数组**:列表为空返回 `[]`,**禁止 `null`**。

---

## 2. B 类(`/api/*`)统一返回结构

### 2.1 信封(所有响应,成功与失败都用)

```json
{ "code": 0, "msg": "success", "data": <object | null> }
```

- `code`:`0` = 成功;非 0 = 错误业务码(见 §4)。
- `msg`:人类可读说明(成功为 `"success"`,失败为原因)。
- `data`:成功时为业务对象(或分页对象,见 §3);失败时为 `null`。

### 2.2 HTTP 状态码 与 code 的关系

**两者都用,各司其职**:
- **HTTP status** = 传输/类别层(供反代、监控、客户端粗粒度判断)。
- **`code`** = 业务细分(供前端精确分支)。

成功:`HTTP 200` + `code:0`。
失败:`HTTP 4xx/5xx` + 对应 `code`(非 0)+ `msg` + `data:null`。

---

## 3. 分页规范(B 类列表接口)

### 3.1 请求参数（query）

| 参数 | 类型 | 默认 | 约束 |
|---|---|---|---|
| `page` | int | 1 | ≥ 1 |
| `pageSize` | int | 20 | 1–100(超出取边界) |

> 统一用 `pageSize`(**废弃 `limit` / `page_size`**)。

### 3.2 响应结构

列表数据放在 `data` 里,固定形如:

```json
{
  "code": 0, "msg": "success",
  "data": {
    "list": [ /* 当前页记录 */ ],
    "page": 1,
    "pageSize": 20,
    "total": 123
  }
}
```

- `list`:数组,空则 `[]`。
- `total`:符合条件的总记录数(用于前端算总页数)。
- 字段名固定:`list` / `page` / `pageSize` / `total`(**废弃 `items` / `data`(数组) / `limit` / `page_size`**)。

---

## 4. 状态码 与 业务码

### 4.1 HTTP 状态码（B 类通用）

| HTTP | 含义 |
|---|---|
| 200 | 成功 |
| 400 | 参数错误/请求非法 |
| 401 | 未认证(缺少/无效的 token / session / 头) |
| 402 | 计费相关(余额/配额不足) |
| 403 | 已认证但无权限 |
| 404 | 资源不存在 |
| 409 | 冲突(如唯一约束、重复) |
| 429 | 触发限流 |
| 500 | 服务内部错误 |
| 502 / 503 / 504 | 上游/依赖不可用 |

### 4.2 业务码 `code`

- `0`:成功。
- **通用错误**:`code` 直接取对应 **HTTP 状态码数字**(如 `400`/`401`/`404`/`500`),`msg` 给原因。
- **需要前端专门分支的业务场景**:用 5 位保留码(前 3 位 = HTTP 类别):

| code | HTTP | 含义 |
|---|---|---|
| 40201 | 402 | 余额不足 |
| 40202 | 402 | API Key 配额超限 |
| 42900 | 429 | RPM/TPM 限流 |
| 42901 | 429 | 模型并发超限 |

> 前端规则:`code === 0` 成功;否则按 `code` 分支(专用码优先,否则按 HTTP 类别兜底)。

---

## 5. A 类(`/v1/*`)OpenAI 兼容(保持原样)

网关代理接口**不套 B 类信封**,遵循 OpenAI 约定:

- 成功:原样透传上游(含 SSE 流式 `text/event-stream`)。
- 失败:
```json
{ "error": { "message": "...", "type": "invalid_request_error", "code": "invalid_api_key" } }
```
- 状态码:`400`/`401`/`403`/`404`/`429`/`402(insufficient_quota)`/`500`/`502`。
- 由 `utils.WriteOpenAIError` 统一输出,**不要改成 B 类信封**(客户端/SDK 依赖此格式)。

---

## 6. 对齐进度

| # | 偏差 | 状态 |
|---|---|---|
| 1 | 信封不统一(admin 裸返回) | ✅ 已对齐:admin `jsonResponse` 统一套 `{code,msg,data}`;webhook 按 C 类豁免 |
| 2 | 错误体 `{"error": msg}` | ✅ 已对齐:site/admin 错误统一走 `httpx.Err` → `{code,msg,data:null}` |
| 3 | 分页参数 `limit` / `page_size` | ✅ 已对齐:统一 `pageSize`(`httpx.ParsePage`) |
| 4 | 列表结构混乱 | ✅ 已对齐:统一 `data.{list,page,pageSize,total}`(`httpx.Page`) |
| 5 | 时间格式 `2006-01-02 15:04:05` | ⏳ 待办:仍有本地化格式,后续统一 RFC3339 UTC(前端目前自行格式化,影响小) |
| 6 | 金额助手不一致 | ⏳ 待办:展示统一 `centsToMoneyPrecise` + `xxxCents`(列表已用精确版) |

---

## 7. 实现(已落地)

统一 helper 在 `internal/api/httpx/`,site / admin 共用:

```go
httpx.OK(w, data)                          // 200 {code:0,msg:"success",data}
httpx.Page(w, list, page, pageSize, total) // 200 {code:0,...,data:{list,page,pageSize,total}}
httpx.Err(w, httpStatus, code, msg)        // 4xx/5xx {code,msg,data:null}
httpx.ParsePage(r) (page, pageSize, offset int)  // 统一解析 page/pageSize
```

- site 的 `respondError`、admin 的 `errorResponse` 委托给 `httpx.Err`;admin 的 `jsonResponse` 统一套成功信封;列表接口统一 `httpx.Page`。
- `/v1/*` 继续用 `utils.WriteOpenAIError`,不并入 httpx。
- webhook(C 类)保留自有扁平响应,不接入 httpx。

> 前端对接变更:分页参数传 `pageSize`(不再用 `limit`/`page_size`);列表读 `data.list` + `data.total` + `data.pageSize`;错误读 `code`/`msg`(不再读 `error`);admin 响应现在统一外层 `{code,msg,data}`。
