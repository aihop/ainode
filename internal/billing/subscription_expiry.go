package billing

import (
	"context"
	"fmt"
	"log"
	"time"

	"aihop.io/ainode/internal/db"
	"aihop.io/ainode/internal/utils"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const subscriptionExpiryInterval = 1 * time.Hour

// StartSubscriptionExpirySweep 启动后台任务,定期清理已过期订阅:
// 把过期用户的订阅赠送清零、订阅实付剩余结转充值余额(等价于一次取消)。
// 这是「APayShop 未发取消事件」时的兜底,确保 use-it-or-lose-it 真正生效。
func StartSubscriptionExpirySweep(ctx context.Context, pool *pgxpool.Pool, queries *db.Queries) {
	utils.SafeGo(ctx, "subscription-expiry-sweep", func() {
		ticker := time.NewTicker(subscriptionExpiryInterval)
		defer ticker.Stop()
		// 启动后先跑一次
		sweepExpiredSubscriptions(ctx, pool, queries)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweepExpiredSubscriptions(ctx, pool, queries)
			}
		}
	})
}

func sweepExpiredSubscriptions(ctx context.Context, pool *pgxpool.Pool, queries *db.Queries) {
	ids, err := queries.ListExpiredSubscriptionUsers(ctx)
	if err != nil {
		log.Printf("subscription expiry sweep: list failed: %v", err)
		return
	}
	if len(ids) == 0 {
		return
	}
	day := time.Now().UTC().Format("20060102")
	cleared := 0
	for _, uid := range ids {
		applied, aerr := ApplySubscription(ctx, pool, queries, SubscriptionApply{
			UserID:     uid,
			NewPaid:    0,
			NewGrant:   0,
			ExpiresAt:  pgtype.Timestamptz{}, // 置空
			Tier:       0,
			EventID:    fmt.Sprintf("sub:expire:%d:%s", uid, day),
			Type:       "sub_expire",
			SourceType: "system",
			SourceID:   "expiry-sweep",
			Remark:     "订阅到期自动清理(赠送清零、实付剩余转余额)",
		})
		if aerr != nil {
			log.Printf("subscription expiry sweep: apply for user %d failed: %v", uid, aerr)
			continue
		}
		if applied {
			cleared++
		}
	}
	log.Printf("subscription expiry sweep: processed %d expired users, cleared %d", len(ids), cleared)
}
