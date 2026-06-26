package billing

import (
	"context"
	"fmt"
	"strconv"

	"aihop.io/ainode/internal/db"
)

// GetUserBalance 返回用户当前的 grant/cash 余额（10^8 放大的整数）。
//
// 优先读 Redis 实时缓存——它反映每次请求即时扣减后的真实可用余额；
// 任一余额缓存缺失/不可用时回源 DB（GetUserByID），并顺带回填缓存。
func GetUserBalance(ctx context.Context, queries *db.Queries, userID int32) (grant int64, cash int64, err error) {
	grantKey := fmt.Sprintf("grant_balance:%d", userID)
	cashKey := fmt.Sprintf("cash_balance:%d", userID)

	if RedisClient != nil {
		if vals, gerr := RedisClient.MGet(ctx, grantKey, cashKey).Result(); gerr == nil &&
			len(vals) == 2 && vals[0] != nil && vals[1] != nil {
			g, e1 := strconv.ParseInt(fmt.Sprint(vals[0]), 10, 64)
			c, e2 := strconv.ParseInt(fmt.Sprint(vals[1]), 10, 64)
			if e1 == nil && e2 == nil {
				return g, c, nil
			}
		}
	}

	// 缓存缺失/不可用 → 回源 DB
	user, derr := queries.GetUserByID(ctx, userID)
	if derr != nil {
		return 0, 0, derr
	}
	grant = user.GrantBalance.Int64
	cash = user.CashBalance.Int64
	// 顺带回填缓存（失败不影响返回）
	_ = SyncUserBalanceCache(ctx, userID, grant, cash)
	return grant, cash, nil
}
