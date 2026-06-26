package billing

import (
	"context"
	_ "embed"
	"encoding/json"
	"log"

	"aihop.io/ainode/internal/db"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

//go:embed lua/refund.lua
var refundLuaScript string
var refundScript *redis.Script

//go:embed lua/compensate.lua
var compensateLuaScript string
var compensateScript *redis.Script

func init() {
	refundScript = redis.NewScript(refundLuaScript)
	compensateScript = redis.NewScript(compensateLuaScript)
}

// TaskRecordBillingLog 是推送到队列的 Task 名称
const TaskRecordBillingLog = "billing:record_log"

// SettlementRequest 结算请求参数
type SettlementRequest struct {
	UserID           int32  `json:"user_id"`
	ApiKeyID         int32  `json:"api_key_id"`
	ChannelID        int32  `json:"channel_id"`
	ModelName        string `json:"model_name"`
	PromptTokens     int32  `json:"prompt_tokens"`
	CompletionTokens int32  `json:"completion_tokens"`
	CacheHitTokens   int32  `json:"cache_hit_tokens"`
	CacheMissTokens  int32  `json:"cache_miss_tokens"`
	PreDeductedCents int64  `json:"pre_deducted_cents"`
	SubPaidDeducted  int64  `json:"sub_paid_deducted"`
	GrantDeducted    int64  `json:"grant_deducted"`
	CashDeducted     int64  `json:"cash_deducted"`
	ActualCostCents  int64  `json:"actual_cost_cents"`
	RequestID        string `json:"request_id"`
}

// balanceKeys3 返回三池 Redis key(顺序与 Lua 脚本 KEYS 一致:sub_paid, grant, cash)。
func balanceKeys3(userID int32) []string {
	return []string{SubPaidBalanceKey(userID), GrantBalanceKey(userID), CashBalanceKey(userID)}
}

// Refund 退还预扣费 (发生系统错误或完全未消费时调用)。
// 按消费逆序退回 cash→grant→sub_paid,各池最多退回其当初扣额。
func Refund(ctx context.Context, queries *db.Queries, userID int32, amountCents int64, d Deduction, requestID string) {
	if amountCents <= 0 {
		return
	}

	args := []interface{}{amountCents, d.SubPaid, d.Grant, d.Cash}
	if err := refundScript.Run(ctx, RedisClient, balanceKeys3(userID), args...).Err(); err != nil {
		log.Printf("ERROR: Failed to refund redis balance for user %d: %v", userID, err)
	}

	log.Printf("Refunded %d cents to user %d for failed request %s", amountCents, userID, requestID)
}

// Settle 完成流式/普通请求后的结算逻辑（多退少补）
// 并通过 asynq 将账单写入任务推送到后台队列
func Settle(ctx context.Context, queries *db.Queries, req SettlementRequest) error {
	// 1. 计算差额 (多退少补)
	diff := req.PreDeductedCents - req.ActualCostCents

	// 2. 补偿 Redis 余额 (差额 > 0 说明预扣多了，需要退还)
	// 如果 diff < 0，说明预扣少了，目前的 pre_deduct 逻辑是按照 max_tokens 预扣，理论上不应该少。
	// 但如果真的少了，需要补扣 (此处简单处理，后续可扩展补扣 Lua 脚本)
	if diff > 0 {
		// 预扣多了 → 按消费逆序退回(cash→grant→sub_paid),各池最多退回其当初扣额。
		args := []interface{}{diff, req.SubPaidDeducted, req.GrantDeducted, req.CashDeducted}
		if err := refundScript.Run(ctx, RedisClient, balanceKeys3(req.UserID), args...).Err(); err != nil {
			log.Printf("ERROR: Failed to compensate redis balance for user %d: %v", req.UserID, err)
		}
	} else if diff < 0 {
		// 预扣少了 → 按消费正序补扣(sub_paid→grant→cash),带余额下限,绝不为负。
		billingPolicy := "all"
		if queries != nil {
			if modelInfo, err := queries.GetModelByName(ctx, req.ModelName); err == nil && modelInfo.BillingPolicy != "" {
				billingPolicy = modelInfo.BillingPolicy
			}
		}
		args := []interface{}{-diff, billingPolicy}
		if err := compensateScript.Run(ctx, RedisClient, balanceKeys3(req.UserID), args...).Err(); err != nil {
			log.Printf("ERROR: Failed to compensate(under-charge) redis balance for user %d: %v", req.UserID, err)
		}
	}

	// 3. 将流水数据序列化并推送到 asynq 队列
	payload, err := json.Marshal(req)
	if err != nil {
		log.Printf("ERROR: Failed to marshal settlement request: %v", err)
		return err
	}

	task := asynq.NewTask(TaskRecordBillingLog, payload)

	// 将任务推送到 asynq 队列，指定队列名称，避免与其他项目冲突
	info, err := AsynqClient.EnqueueContext(ctx, task, asynq.Queue("ainode_billing"), asynq.MaxRetry(5))
	if err != nil {
		// asynq（依赖 Redis）投递失败时，落入 Postgres outbox 兜底，由后台 relay 重投，
		// 避免“Redis 已扣费但账单永久丢失”。需要 request_id 才能去重/重投。
		if queries != nil && req.RequestID != "" {
			if oerr := queries.InsertSettlementOutbox(ctx, req.RequestID, payload); oerr != nil {
				log.Printf("CRITICAL: enqueue failed AND outbox persist failed for request %s: enqueue=%v outbox=%v", req.RequestID, err, oerr)
				return oerr
			}
			log.Printf("WARN: enqueue billing task failed (%v), persisted settlement %s to outbox for later delivery", err, req.RequestID)
			return nil
		}
		log.Printf("ERROR: Failed to enqueue billing task and cannot fall back to outbox (request_id empty): %v", err)
		return err
	}

	log.Printf("Settlement completed for Request %s in Redis. Enqueued billing task %s", req.RequestID, info.ID)
	return nil
}
