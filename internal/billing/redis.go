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
