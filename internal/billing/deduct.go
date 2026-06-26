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

// Deduction 记录一次扣费在三池中各扣了多少(10^8 放大整数)。
type Deduction struct {
	SubPaid int64 // 订阅实付池
	Grant   int64 // 订阅赠送池
	Cash    int64 // 充值余额池
}

// Total 返回三池合计。
func (d Deduction) Total() int64 { return d.SubPaid + d.Grant + d.Cash }

// PreDeduct 执行三池有序预扣(sub_paid → grant → cash)。
// 若 Redis 缓存中不存在用户余额，会从 DB 加载后重试。
func PreDeduct(ctx context.Context, queries *db.Queries, userID int32, estimatedCostCents int64, billingPolicy string) (Deduction, error) {
	if billingPolicy == "" {
		billingPolicy = "all"
	}
	keys := []string{SubPaidBalanceKey(userID), GrantBalanceKey(userID), CashBalanceKey(userID)}
	args := []interface{}{estimatedCostCents, billingPolicy}

	run := func() ([]interface{}, error) {
		result, err := preDeductScript.Run(ctx, RedisClient, keys, args...).Result()
		if err != nil {
			return nil, err
		}
		arr, ok := result.([]interface{})
		if !ok || len(arr) < 4 {
			return nil, fmt.Errorf("unexpected pre-deduct result: %v", result)
		}
		return arr, nil
	}

	arr, err := run()
	if err != nil {
		return Deduction{}, fmt.Errorf("failed to execute pre-deduct script: %w", err)
	}

	if arr[0].(int64) == -2 {
		// 缓存缺失，从 DB 回源后重试
		log.Printf("Balance cache miss for user %d, loading from DB", userID)
		if err := LoadBalanceToCache(ctx, queries, userID); err != nil {
			return Deduction{}, fmt.Errorf("failed to load balance from db: %w", err)
		}
		arr, err = run()
		if err != nil {
			return Deduction{}, fmt.Errorf("failed to execute pre-deduct script on retry: %w", err)
		}
	}

	if arr[0].(int64) == -1 {
		return Deduction{}, fmt.Errorf("insufficient balance")
	}

	d := Deduction{
		SubPaid: arr[1].(int64),
		Grant:   arr[2].(int64),
		Cash:    arr[3].(int64),
	}
	log.Printf("User %d pre-deducted %d cents (sub_paid:%d grant:%d cash:%d)", userID, estimatedCostCents, d.SubPaid, d.Grant, d.Cash)
	return d, nil
}

// LoadBalanceToCache 从 DB 读取用户三池余额并写入 Redis。
func LoadBalanceToCache(ctx context.Context, queries *db.Queries, userID int32) error {
	subPaid, grant, cash, err := queries.GetUserBalances(ctx, userID)
	if err != nil {
		return fmt.Errorf("user not found: %w", err)
	}
	if err := SyncUserBalanceCache(ctx, userID, subPaid, grant, cash); err != nil {
		return fmt.Errorf("failed to set balance in redis: %w", err)
	}
	log.Printf("Loaded balance for user %d: sub_paid=%d grant=%d cash=%d cents", userID, subPaid, grant, cash)
	return nil
}
