package billing

import (
	"context"
	"encoding/json"
	"log"

	"github.com/hibiken/asynq"
)

// TaskRecordRequestLog 是推送到队列的 Task 名称
const TaskRecordRequestLog = "billing:record_request_log"

// RequestLogRequest 请求审计日志参数
type RequestLogRequest struct {
	UserID           int32  `json:"user_id"`
	APIKeyID         int32  `json:"api_key_id"`
	ChannelID        int32  `json:"channel_id"`
	Provider         string `json:"provider"`
	RequestType      string `json:"request_type"`
	PublicModelName  string `json:"public_model_name"`
	UpstreamModelName string `json:"upstream_model_name"`
	InputPayload     []byte `json:"input_payload,omitempty"`
	PromptTokens     int32  `json:"prompt_tokens"`
	CompletionTokens int32  `json:"completion_tokens"`
	CacheHitTokens   int32  `json:"cache_hit_tokens"`
	CacheMissTokens  int32  `json:"cache_miss_tokens"`
	AmountCents      int64  `json:"amount_cents"`
	PreDeductedCents int64  `json:"pre_deducted_cents"`
	StatusCode       int32  `json:"status_code"`
	RequestID        string `json:"request_id"`
	IsStream         bool   `json:"is_stream"`
	IsSuccess        bool   `json:"is_success"`
}

// EnqueueRequestLog 将请求审计日志推送到 Asynq 队列。
// 该操作是尽力而为的，失败不阻塞主请求链（仅记一条 warn 日志）。
func EnqueueRequestLog(ctx context.Context, req RequestLogRequest) {
	if AsynqClient == nil {
		return
	}

	payload, err := json.Marshal(req)
	if err != nil {
		log.Printf("WARN: Failed to marshal request log for %s: %v", req.RequestID, err)
		return
	}

	task := asynq.NewTask(TaskRecordRequestLog, payload)
	if _, err := AsynqClient.EnqueueContext(ctx, task, asynq.Queue("ainode_billing"), asynq.MaxRetry(3)); err != nil {
		log.Printf("WARN: Failed to enqueue request log for %s: %v", req.RequestID, err)
	}
}
