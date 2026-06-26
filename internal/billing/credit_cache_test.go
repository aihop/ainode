package billing

import (
	"context"
	"testing"
)

func TestCreditBalanceCache_IncrementsWhenKeyExists(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 100, 200) // grant=100, cash=200

	// credit cash +50
	if err := CreditBalanceCache(context.Background(), 1, "cash", 50); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertBalances(t, mr, 100, 250)

	// debit grant -30 (delta 为负)
	if err := CreditBalanceCache(context.Background(), 1, "grant", -30); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertBalances(t, mr, 70, 250)
}

func TestCreditBalanceCache_ComposesWithConcurrentDeduction(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 0, 100) // cash=100

	// 模拟请求侧在途扣减：cash -30 -> 70
	if err := RedisClient.DecrBy(context.Background(), "cash_balance:1", 30).Err(); err != nil {
		t.Fatalf("decr failed: %v", err)
	}
	// 充值到账：cash +50（相对增减，必须叠加而非覆盖）
	if err := CreditBalanceCache(context.Background(), 1, "cash", 50); err != nil {
		t.Fatalf("credit failed: %v", err)
	}
	// 期望 100 - 30 + 50 = 120；若用绝对 SET 会变成 150（丢掉 -30 的扣减）
	assertBalances(t, mr, 0, 120)
}

func TestCreditBalanceCache_SkipsWhenKeyMissing(t *testing.T) {
	mr := newTestRedis(t)
	// 不预置余额：key 不存在，应跳过而不是凭空从 0 创建
	if err := CreditBalanceCache(context.Background(), 1, "cash", 50); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mr.Exists("cash_balance:1") {
		t.Fatal("cache key should not be created when absent (lazy-load from DB instead)")
	}
}

func TestCreditBalanceCache_RejectsUnknownBalanceType(t *testing.T) {
	newTestRedis(t)
	if err := CreditBalanceCache(context.Background(), 1, "bonus", 10); err == nil {
		t.Fatal("expected error for unsupported balance type")
	}
}
