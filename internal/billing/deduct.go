package billing

import (
	"context"
	_ "embed"
	"fmt"
	"log"

	"aihop.io/ainode/internal/db"
	"github.com/redis/go-redis/v9"
)

//go:embed lua/pre_deduct.lua
var preDeductLuaScript string

var preDeductScript *redis.Script

func init() {
	preDeductScript = redis.NewScript(preDeductLuaScript)
}

// PreDeduct 执行预扣费。
// 如果 Redis 缓存中不存在用户余额，会从 DB 加载后重试。
func PreDeduct(ctx context.Context, queries *db.Queries, userID int32, estimatedCostCents int64) (int64, int64, error) {
	grantKey := fmt.Sprintf("grant_balance:%d", userID)
	cashKey := fmt.Sprintf("cash_balance:%d", userID)

	keys := []string{grantKey, cashKey}
	args := []interface{}{estimatedCostCents}

	result, err := preDeductScript.Run(ctx, RedisClient, keys, args...).Result()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to execute pre-deduct script: %w", err)
	}

	arr := result.([]interface{})
	status := arr[0].(int64)

	if status == -2 {
		// 缓存中没有余额，从 DB 回源加载
		log.Printf("Balance cache miss for user %d, loading from DB", userID)
		err = LoadBalanceToCache(ctx, queries, userID)
		if err != nil {
			return 0, 0, fmt.Errorf("failed to load balance from db: %w", err)
		}

		// 重试扣费
		result, err = preDeductScript.Run(ctx, RedisClient, keys, args...).Result()
		if err != nil {
			return 0, 0, fmt.Errorf("failed to execute pre-deduct script on retry: %w", err)
		}
		arr = result.([]interface{})
		status = arr[0].(int64)
	}

	if status == -1 {
		return 0, 0, fmt.Errorf("insufficient balance")
	}

	grantDeducted := arr[1].(int64)
	cashDeducted := arr[2].(int64)

	log.Printf("User %d pre-deducted %d cents (grant: %d, cash: %d)", userID, estimatedCostCents, grantDeducted, cashDeducted)
	return grantDeducted, cashDeducted, nil
}

// LoadBalanceToCache 从 DB 读取用户余额并设置到 Redis
func LoadBalanceToCache(ctx context.Context, queries *db.Queries, userID int32) error {
	user, err := queries.GetUserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("user not found: %w", err)
	}

	grantBalance := user.GrantBalance.Int64 // pgtype.Int8 转换为 int64
	cashBalance := user.CashBalance.Int64

	// 使用 Pipeline 原子性写入两个余额
	pipe := RedisClient.Pipeline()
	pipe.Set(ctx, fmt.Sprintf("grant_balance:%d", userID), grantBalance, 0)
	pipe.Set(ctx, fmt.Sprintf("cash_balance:%d", userID), cashBalance, 0)
	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to set balance in redis: %w", err)
	}

	log.Printf("Loaded balance for user %d: grant=%d, cash=%d cents", userID, grantBalance, cashBalance)
	return nil
}
