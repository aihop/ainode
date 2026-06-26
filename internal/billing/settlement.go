package billing

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"

	"aihop.io/ainode/internal/db"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

//go:embed lua/refund.lua
var refundLuaScript string
var refundScript *redis.Script

func init() {
	refundScript = redis.NewScript(refundLuaScript)
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
	GrantDeducted    int64  `json:"grant_deducted"`
	CashDeducted     int64  `json:"cash_deducted"`
	ActualCostCents  int64  `json:"actual_cost_cents"`
	RequestID        string `json:"request_id"`
}

// Refund 退还预扣费 (发生系统错误或完全未消费时调用)
func Refund(ctx context.Context, queries *db.Queries, userID int32, amountCents int64, grantDeducted int64, cashDeducted int64, requestID string) {
	if amountCents <= 0 {
		return
	}

	grantKey := fmt.Sprintf("grant_balance:%d", userID)
	cashKey := fmt.Sprintf("cash_balance:%d", userID)
	keys := []string{grantKey, cashKey}
	args := []interface{}{amountCents, grantDeducted, cashDeducted}

	if err := refundScript.Run(ctx, RedisClient, keys, args...).Err(); err != nil {
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
		grantKey := fmt.Sprintf("grant_balance:%d", req.UserID)
		cashKey := fmt.Sprintf("cash_balance:%d", req.UserID)
		keys := []string{grantKey, cashKey}
		args := []interface{}{diff, req.GrantDeducted, req.CashDeducted}

		err := refundScript.Run(ctx, RedisClient, keys, args...).Err()
		if err != nil {
			log.Printf("ERROR: Failed to compensate redis balance for user %d: %v", req.UserID, err)
		}
	} else if diff < 0 {
		// 补扣逻辑：理论上极少发生，优先遵循模型余额策略。
		billingPolicy := "both"
		if queries != nil {
			if modelInfo, err := queries.GetModelByName(ctx, req.ModelName); err == nil && modelInfo.BillingPolicy != "" {
				billingPolicy = modelInfo.BillingPolicy
			}
		}

		switch billingPolicy {
		case "grant_only":
			grantKey := fmt.Sprintf("grant_balance:%d", req.UserID)
			RedisClient.DecrBy(ctx, grantKey, -diff)
		default:
			cashKey := fmt.Sprintf("cash_balance:%d", req.UserID)
			RedisClient.DecrBy(ctx, cashKey, -diff)
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
		log.Printf("ERROR: Failed to enqueue billing task: %v", err)
		return err
	}

	log.Printf("Settlement completed for Request %s in Redis. Enqueued billing task %s", req.RequestID, info.ID)
	return nil
}
