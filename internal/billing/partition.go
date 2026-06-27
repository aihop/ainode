package billing

import (
	"context"
	"fmt"
	"log"
	"time"

	"aihop.io/ainode/internal/utils"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// EnsurePartitionsMonthsAhead 确保未来 N 个月的分区已创建
	EnsurePartitionsMonthsAhead = 6
	// partitionCheckInterval 定时检查间隔
	partitionCheckInterval = 24 * time.Hour
)

// EnsureBillingLogPartitions 确保 billing_logs 分区表已创建。
// 包含当前月 + 未来 monthsAhead 个月，总计 monthsAhead+1 个分区。
// 已存在的分区会被 CREATE TABLE IF NOT EXISTS 自动跳过。
func EnsureBillingLogPartitions(ctx context.Context, pool *pgxpool.Pool, monthsAhead int) error {
	if pool == nil {
		return fmt.Errorf("pool is nil")
	}

	now := time.Now().UTC()

	for i := 0; i <= monthsAhead; i++ {
		partitionDate := now.AddDate(0, i, 0)
		year, month := partitionDate.Year(), partitionDate.Month()

		partitionName := fmt.Sprintf("billing_logs_%04d_%02d", year, int(month))

		startDate := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
		endDate := startDate.AddDate(0, 1, 0)

		startStr := startDate.Format("2006-01-02")
		endStr := endDate.Format("2006-01-02")

		sql := fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s PARTITION OF billing_logs FOR VALUES FROM ('%s') TO ('%s')`,
			partitionName, startStr, endStr,
		)

		if _, err := pool.Exec(ctx, sql); err != nil {
			return fmt.Errorf("failed to create partition %s: %w", partitionName, err)
		}

		// 确保分区 owner 与当前连接用户一致,防止后续 ALTER TABLE 父表时
		// 因 owner 不一致导致 "must be owner of table" (SQLSTATE 42501)。
		ownerSQL := fmt.Sprintf(`ALTER TABLE %s OWNER TO current_user`, partitionName)
		if _, err := pool.Exec(ctx, ownerSQL); err != nil {
			log.Printf("WARNING: failed to set owner for partition %s: %v", partitionName, err)
		}

		log.Printf("Ensured billing_logs partition: %s (%s ~ %s)", partitionName, startStr, endStr)
	}

	return nil
}

// StartPartitionMaintenance 启动分区维护后台任务。
// 定期检查并创建未来月份的分区。
func StartPartitionMaintenance(ctx context.Context, pool *pgxpool.Pool) {
	utils.SafeGo(ctx, "partition-maintenance", func() {
		ticker := time.NewTicker(partitionCheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				log.Println("Partition maintenance stopped")
				return
			case <-ticker.C:
				log.Println("Running periodic partition maintenance check...")
				if err := EnsureBillingLogPartitions(ctx, pool, EnsurePartitionsMonthsAhead); err != nil {
					log.Printf("ERROR: Periodic partition maintenance failed: %v", err)
				}
			}
		}
	})
}
