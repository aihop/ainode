package billing

import (
	"context"
	"fmt"
	"log"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// RedisClient 是全局共享的 Redis 客户端
var RedisClient *redis.Client

// AsynqClient 是全局共享的异步任务推送客户端
var AsynqClient *asynq.Client

// InitRedis 初始化 Redis 连接和 Asynq 客户端
func InitRedis(addr, password string, db int) error {
	RedisClient = redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	// 测试连接
	ctx := context.Background()
	if err := RedisClient.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("failed to connect to redis: %w", err)
	}

	log.Printf("Connected to Redis at %s", addr)

	// 初始化 Asynq Client
	AsynqClient = asynq.NewClient(asynq.RedisClientOpt{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	return nil
}

// SyncUserBalanceCache rewrites the user's grant/cash balance cache in Redis.
// 三池 Redis key 助手(统一命名,避免散落字符串拼接)。
func SubPaidBalanceKey(userID int32) string { return fmt.Sprintf("sub_paid_balance:%d", userID) }
func GrantBalanceKey(userID int32) string   { return fmt.Sprintf("grant_balance:%d", userID) }
func CashBalanceKey(userID int32) string    { return fmt.Sprintf("cash_balance:%d", userID) }

// SyncUserBalanceCache 覆盖写入用户三池余额缓存(sub_paid / grant / cash)。
func SyncUserBalanceCache(ctx context.Context, userID int32, subPaidBalance, grantBalance, cashBalance int64) error {
	if RedisClient == nil {
		return fmt.Errorf("redis client is not initialized")
	}

	pipe := RedisClient.Pipeline()
	pipe.Set(ctx, SubPaidBalanceKey(userID), subPaidBalance, 0)
	pipe.Set(ctx, GrantBalanceKey(userID), grantBalance, 0)
	pipe.Set(ctx, CashBalanceKey(userID), cashBalance, 0)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("failed to sync balance cache: %w", err)
	}

	return nil
}
