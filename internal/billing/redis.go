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
func SyncUserBalanceCache(ctx context.Context, userID int32, grantBalance, cashBalance int64) error {
	if RedisClient == nil {
		return fmt.Errorf("redis client is not initialized")
	}

	pipe := RedisClient.Pipeline()
	pipe.Set(ctx, fmt.Sprintf("grant_balance:%d", userID), grantBalance, 0)
	pipe.Set(ctx, fmt.Sprintf("cash_balance:%d", userID), cashBalance, 0)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("failed to sync balance cache: %w", err)
	}

	return nil
}
