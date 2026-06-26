package billing

import (
	"context"
	"log"
	"time"

	"aihop.io/ainode/internal/db"

	"github.com/hibiken/asynq"
)

const (
	outboxRelayInterval = 30 * time.Second
	outboxRelayBatch    = 100
)

// StartOutboxRelay 启动后台 relay：定时扫描 settlement_outbox 中未投递的结算，
// 重新推送到 asynq。投递成功后标记 processed；asynq 仍不可用则保留待下次重试。
// 实际写库由账单 Worker 完成，且按 request_id 幂等，故重投是安全的。
func StartOutboxRelay(ctx context.Context, queries *db.Queries) {
	if queries == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(outboxRelayInterval)
		defer ticker.Stop()
		log.Println("📦 Settlement outbox relay started")
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				drainSettlementOutbox(ctx, queries)
			}
		}
	}()
}

func drainSettlementOutbox(ctx context.Context, queries *db.Queries) {
	rows, err := queries.ListPendingSettlementOutbox(ctx, outboxRelayBatch)
	if err != nil {
		log.Printf("Outbox relay ERROR: failed to list pending settlements: %v", err)
		return
	}
	if len(rows) == 0 {
		return
	}

	delivered := 0
	for _, row := range rows {
		task := asynq.NewTask(TaskRecordBillingLog, row.Payload)
		if _, err := AsynqClient.EnqueueContext(ctx, task, asynq.Queue("ainode_billing"), asynq.MaxRetry(5)); err != nil {
			// asynq/Redis 仍不可用：记一次重试，保留待下个周期
			_ = queries.IncrementSettlementOutboxAttempts(ctx, row.ID)
			log.Printf("Outbox relay WARN: re-enqueue failed for request %s: %v", row.RequestID, err)
			continue
		}
		if err := queries.MarkSettlementOutboxProcessed(ctx, row.ID); err != nil {
			// 已成功投递但标记失败：Worker 幂等会兜住重复，下次仍可能再投一次（无害）
			log.Printf("Outbox relay WARN: failed to mark processed for request %s: %v", row.RequestID, err)
			continue
		}
		delivered++
	}
	if delivered > 0 {
		log.Printf("Outbox relay: re-delivered %d/%d pending settlements", delivered, len(rows))
	}
}
