package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"aihop.io/node-api/internal/billing"
	"aihop.io/node-api/internal/db"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"
)

// BillingTaskProcessor 负责处理所有的计费异步任务
type BillingTaskProcessor struct {
	queries *db.Queries
}

func NewBillingTaskProcessor(queries *db.Queries) *BillingTaskProcessor {
	return &BillingTaskProcessor{
		queries: queries,
	}
}

// HandleRecordBillingLog 处理写入流水和扣减 DB 余额的任务
func (processor *BillingTaskProcessor) HandleRecordBillingLog(ctx context.Context, t *asynq.Task) error {
	var req billing.SettlementRequest
	if err := json.Unmarshal(t.Payload(), &req); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry) // 序列化失败无需重试
	}

	reqID := pgtype.Text{String: req.RequestID, Valid: req.RequestID != ""}

	// 1. 幂等性检查：判断该 RequestID 是否已经处理过
	if req.RequestID != "" {
		exists, err := processor.queries.CheckBillingLogExists(ctx, reqID)
		if err != nil {
			log.Printf("Worker ERROR: Failed to check existence for request %s: %v", req.RequestID, err)
			return err // DB错误，触发重试
		}
		if exists {
			log.Printf("Worker INFO: Request %s already processed, skipping to ensure idempotency", req.RequestID)
			return nil // 已经处理过，直接返回成功，不重试
		}
	}

	// 2. 计算实际扣减 DB 余额 (优先扣减 Grant，其次 Cash)
	actualGrant := int64(0)
	actualCash := int64(0)
	if req.ActualCostCents <= req.GrantDeducted {
		actualGrant = req.ActualCostCents
	} else {
		actualGrant = req.GrantDeducted
		actualCash = req.ActualCostCents - req.GrantDeducted
	}

	err := processor.queries.UpdateUserTopupBalance(ctx, db.UpdateUserTopupBalanceParams{
		ID:          req.UserID,
		CashBalance: pgtype.Int8{Int64: -actualCash, Valid: true},
	})
	if err != nil {
		log.Printf("Worker ERROR: Failed to update DB cash balance for user %d: %v", req.UserID, err)
		return err // 返回 error 触发 asynq 自动重试
	}

	err = processor.queries.UpdateUserGrantBalance(ctx, db.UpdateUserGrantBalanceParams{
		ID:           req.UserID,
		GrantBalance: pgtype.Int8{Int64: -actualGrant, Valid: true},
	})
	if err != nil {
		log.Printf("Worker ERROR: Failed to update DB grant balance for user %d: %v", req.UserID, err)
		return err // 返回 error 触发 asynq 自动重试
	}

	// 3. 写入流水表
	channelID := pgtype.Int4{Int32: req.ChannelID, Valid: req.ChannelID > 0}
	promptTokens := pgtype.Int4{Int32: req.PromptTokens, Valid: true}
	compTokens := pgtype.Int4{Int32: req.CompletionTokens, Valid: true}
	cacheHitTokens := pgtype.Int4{Int32: req.CacheHitTokens, Valid: true}
	cacheMissTokens := pgtype.Int4{Int32: req.CacheMissTokens, Valid: true}

	logID, _ := uuid.NewRandom()
	var pgUUID pgtype.UUID
	pgUUID.Bytes = logID
	pgUUID.Valid = true

	_, err = processor.queries.CreateBillingLog(ctx, db.CreateBillingLogParams{
		ID:               pgUUID,
		UserID:           pgtype.Int4{Int32: req.UserID, Valid: true},
		ChannelID:        channelID,
		ModelName:        req.ModelName,
		PromptTokens:     promptTokens,
		CompletionTokens: compTokens,
		CacheHitTokens:   cacheHitTokens,
		CacheMissTokens:  cacheMissTokens,
		AmountCents:      req.ActualCostCents,
		RequestID:        reqID,
	})

	if err != nil {
		log.Printf("Worker ERROR: Failed to create billing log for request %s: %v", req.RequestID, err)
		return err // 返回 error 触发 asynq 自动重试
	}

	log.Printf("Worker SUCCESS: DB synchronized for Request %s", req.RequestID)
	return nil
}
