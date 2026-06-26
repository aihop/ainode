package billing

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/redis/go-redis/v9"
)

//go:embed lua/credit_cache.lua
var creditCacheLuaScript string
var creditCacheScript *redis.Script

func init() {
	creditCacheScript = redis.NewScript(creditCacheLuaScript)
}

// CreditBalanceCache 对用户某个余额池的 Redis 缓存按 delta 原子增减（delta 可负，用于 debit）。
//
// 仅在缓存 key 已存在时生效——避免用绝对值覆盖请求侧 DECRBY 的实时扣减导致丢扣/超额消费。
// key 不存在时跳过，由下次请求 miss 从（已在同一笔 DB 事务中更新过的）DB 懒加载，最终一致。
func CreditBalanceCache(ctx context.Context, userID int32, balanceType string, delta int64) error {
	if RedisClient == nil {
		return fmt.Errorf("redis client is not initialized")
	}

	var key string
	switch balanceType {
	case "grant":
		key = fmt.Sprintf("grant_balance:%d", userID)
	case "cash":
		key = fmt.Sprintf("cash_balance:%d", userID)
	default:
		return fmt.Errorf("unsupported balance type: %s", balanceType)
	}

	return creditCacheScript.Run(ctx, RedisClient, []string{key}, delta).Err()
}
