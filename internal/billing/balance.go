package billing

import (
	"context"
	"fmt"
	"strconv"

	"aihop.io/ainode/internal/db"
)

// GetUserBalance 返回用户三池余额 sub / grant / cash（10^8 放大的整数）。
//
// 优先读 Redis 实时缓存——它反映每次请求即时扣减后的真实可用余额；
// 任一池缓存缺失/不可用时回源 DB，并顺带回填缓存。
func GetUserBalance(ctx context.Context, queries *db.Queries, userID int32) (sub int64, grant int64, cash int64, err error) {
	if RedisClient != nil {
		if vals, gerr := RedisClient.MGet(ctx, SubBalanceKey(userID), GrantBalanceKey(userID), CashBalanceKey(userID)).Result(); gerr == nil &&
			len(vals) == 3 && vals[0] != nil && vals[1] != nil && vals[2] != nil {
			p, e1 := strconv.ParseInt(fmt.Sprint(vals[0]), 10, 64)
			g, e2 := strconv.ParseInt(fmt.Sprint(vals[1]), 10, 64)
			c, e3 := strconv.ParseInt(fmt.Sprint(vals[2]), 10, 64)
			if e1 == nil && e2 == nil && e3 == nil {
				return p, g, c, nil
			}
		}
	}

	// 缓存缺失/不可用 → 回源 DB
	sub, grant, cash, derr := queries.GetUserBalances(ctx, userID)
	if derr != nil {
		return 0, 0, 0, derr
	}
	// 顺带回填缓存（失败不影响返回）
	_ = SyncUserBalanceCache(ctx, userID, sub, grant, cash)
	return sub, grant, cash, nil
}
