package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"aihop.io/ainode/internal/billing"
	"aihop.io/ainode/internal/db"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"
)

// RequestLogProcessor 处理请求审计日志的异步写入
type RequestLogProcessor struct {
	queries *db.Queries
}

func NewRequestLogProcessor(queries *db.Queries) *RequestLogProcessor {
	return &RequestLogProcessor{
		queries: queries,
	}
}

// HandleRecordRequestLog 处理写入 request_logs 的任务
func (p *RequestLogProcessor) HandleRecordRequestLog(ctx context.Context, t *asynq.Task) error {
	var req billing.RequestLogRequest
	if err := json.Unmarshal(t.Payload(), &req); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	channelID := pgtype.Int4{Int32: req.ChannelID, Valid: req.ChannelID > 0}
	apiKeyID := pgtype.Int4{Int32: req.APIKeyID, Valid: req.APIKeyID > 0}
	provider := req.Provider
	if provider == "" {
		provider = "-"
	}
	upstreamModel := req.UpstreamModelName
	if upstreamModel == "" {
		upstreamModel = req.PublicModelName
	}

	if err := p.queries.CreateRequestLog(ctx, db.CreateRequestLogParams{
		RequestID:         req.RequestID,
		UserID:            req.UserID,
		ApiKeyID:          apiKeyID,
		ChannelID:         channelID,
		Provider:          provider,
		RequestType:       req.RequestType,
		PublicModelName:   req.PublicModelName,
		UpstreamModelName: upstreamModel,
		InputPayload:      req.InputPayload,
		PromptTokens:      req.PromptTokens,
		CompletionTokens:  req.CompletionTokens,
		CacheHitTokens:    req.CacheHitTokens,
		CacheMissTokens:   req.CacheMissTokens,
		AmountCents:       req.AmountCents,
		PreDeductedCents:  req.PreDeductedCents,
		StatusCode:        req.StatusCode,
		IsStream:          req.IsStream,
		IsSuccess:         req.IsSuccess,
	}); err != nil {
		log.Printf("RequestLog ERROR: failed to insert for %s: %v", req.RequestID, err)
		return err
	}

	return nil
}
