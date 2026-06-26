package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"aihop.io/ainode/internal/billing"
	"aihop.io/ainode/internal/db"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BillingTaskProcessor 负责处理所有的计费异步任务
type BillingTaskProcessor struct {
	queries *db.Queries
	pool    *pgxpool.Pool
}

func NewBillingTaskProcessor(queries *db.Queries, pool *pgxpool.Pool) *BillingTaskProcessor {
	return &BillingTaskProcessor{
		queries: queries,
		pool:    pool,
	}
}

// splitActual3 把实际消费金额按消费顺序 sub → grant → cash 拆分到三池。
// 纯函数，便于单测:实际费用从「预扣各池」的头部依次消耗(退款是从尾部退,故保留的实付在头部)。
func splitActual3(actualCost, subDeducted, grantDeducted, cashDeducted int64) (sub, grant, cash int64) {
	rem := actualCost
	take := func(deducted int64) int64 {
		if rem <= 0 || deducted <= 0 {
			return 0
		}
		t := deducted
		if t > rem {
			t = rem
		}
		rem -= t
		return t
	}
	sub = take(subDeducted)
	grant = take(grantDeducted)
	cash = take(cashDeducted)
	return sub, grant, cash
}

// HandleRecordBillingLog 处理写入流水和扣减 DB 余额的任务。
//
// 关键不变量：幂等检查 + 两个余额扣减 + 写流水必须在同一个数据库事务内完成，
// 否则任务重试时（如扣完 cash 后写流水前失败）会因幂等标记尚未落库而重复扣减余额。
func (processor *BillingTaskProcessor) HandleRecordBillingLog(ctx context.Context, t *asynq.Task) error {
	var req billing.SettlementRequest
	if err := json.Unmarshal(t.Payload(), &req); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry) // 序列化失败无需重试
	}

	reqID := pgtype.Text{String: req.RequestID, Valid: req.RequestID != ""}

	tx, err := processor.pool.Begin(ctx)
	if err != nil {
		log.Printf("Worker ERROR: Failed to begin tx for request %s: %v", req.RequestID, err)
		return err // DB 错误，触发重试
	}
	// 默认回滚；成功路径会显式 Commit，Commit 之后 Rollback 变为 no-op。
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := processor.queries.WithTx(tx)

	// 1. 幂等性检查：判断该 RequestID 是否已经处理过（与扣减/写流水在同一事务内才有意义）
	if req.RequestID != "" {
		exists, err := qtx.CheckBillingLogExists(ctx, reqID)
		if err != nil {
			log.Printf("Worker ERROR: Failed to check existence for request %s: %v", req.RequestID, err)
			return err // DB 错误，触发重试
		}
		if exists {
			log.Printf("Worker INFO: Request %s already processed, skipping to ensure idempotency", req.RequestID)
			return nil // 已处理过，事务回滚（无副作用），不重试
		}
	}

	// 2. 计算实际扣减 DB 三池余额 (消费顺序 sub → grant → cash)
	actualSub, actualGrant, actualCash := splitActual3(req.ActualCostCents, req.SubDeducted, req.GrantDeducted, req.CashDeducted)

	if err := qtx.UpdateUserSubBalance(ctx, req.UserID, -actualSub); err != nil {
		log.Printf("Worker ERROR: Failed to update DB sub balance for user %d: %v", req.UserID, err)
		return err
	}

	if err := qtx.UpdateUserTopupBalance(ctx, db.UpdateUserTopupBalanceParams{
		ID:          req.UserID,
		CashBalance: pgtype.Int8{Int64: -actualCash, Valid: true},
	}); err != nil {
		log.Printf("Worker ERROR: Failed to update DB cash balance for user %d: %v", req.UserID, err)
		return err
	}

	if err := qtx.UpdateUserGrantBalance(ctx, db.UpdateUserGrantBalanceParams{
		ID:           req.UserID,
		GrantBalance: pgtype.Int8{Int64: -actualGrant, Valid: true},
	}); err != nil {
		log.Printf("Worker ERROR: Failed to update DB grant balance for user %d: %v", req.UserID, err)
		return err
	}

	// 3. 写入流水表（幂等标记）
	channelID := pgtype.Int4{Int32: req.ChannelID, Valid: req.ChannelID > 0}
	promptTokens := pgtype.Int4{Int32: req.PromptTokens, Valid: true}
	compTokens := pgtype.Int4{Int32: req.CompletionTokens, Valid: true}
	cacheHitTokens := pgtype.Int4{Int32: req.CacheHitTokens, Valid: true}
	cacheMissTokens := pgtype.Int4{Int32: req.CacheMissTokens, Valid: true}

	logID, _ := uuid.NewRandom()
	var pgUUID pgtype.UUID
	pgUUID.Bytes = logID
	pgUUID.Valid = true

	if _, err := qtx.CreateBillingLog(ctx, db.CreateBillingLogParams{
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
	}); err != nil {
		log.Printf("Worker ERROR: Failed to create billing log for request %s: %v", req.RequestID, err)
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("Worker ERROR: Failed to commit billing tx for request %s: %v", req.RequestID, err)
		return err
	}

	// 4. 递增 API Key 配额使用量（事务外的尽力而为：失败不影响已落库的计费）
	if req.ApiKeyID > 0 {
		if err := processor.queries.IncrementAPIKeyQuotaUsed(ctx, db.IncrementAPIKeyQuotaUsedParams{
			ID:        req.ApiKeyID,
			QuotaUsed: pgtype.Int8{Int64: req.ActualCostCents, Valid: true},
		}); err != nil {
			log.Printf("Worker ERROR: Failed to increment API Key quota for key %d: %v", req.ApiKeyID, err)
			// 不触发重试——账单已写入，配额递增失败不影响计费准确性，下次请求会重新检查 DB
		}
	}

	log.Printf("Worker SUCCESS: DB synchronized for Request %s", req.RequestID)
	return nil
}
