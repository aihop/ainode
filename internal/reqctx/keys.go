// Package reqctx 定义请求级 context 的类型安全 key。
//
// 直接用裸字符串作为 context.WithValue 的 key 会被 staticcheck 标记为 SA1029，
// 且存在跨包碰撞风险。这里用私有类型 Key 收口所有 key，底层值与历史字符串保持一致，
// 因此迁移不改变任何运行时行为，只是把 key 变成类型安全的常量。
package reqctx

// Key 是 context 值的私有 key 类型，避免与其它包的 key 冲突。
type Key string

const (
	KeyRequestStartTime  Key = "request_start_time"
	KeyUserID            Key = "user_id"
	KeyAPIKeyID          Key = "api_key_id"
	KeyIsBillingRoute    Key = "is_billing_route"
	KeyModelName         Key = "model_name"
	KeyPublicModelName   Key = "public_model_name"
	KeyUpstreamModelName Key = "upstream_model_name"
	KeyRequestType       Key = "request_type"
	KeyPromptTokens      Key = "prompt_tokens"
	KeyPreDeductedCents  Key = "pre_deducted_cents"
	KeySubDeducted       Key = "sub_deducted"
	KeyGrantDeducted     Key = "grant_deducted"
	KeyCashDeducted      Key = "cash_deducted"
	KeyBillingUnits      Key = "billing_units"
	KeyEstimatedTokens   Key = "estimated_tokens"
	KeyRequestID         Key = "request_id"
	KeyCurrentChannelID  Key = "current_channel_id"
	KeyCurrentProvider   Key = "current_provider"
)
