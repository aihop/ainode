package billing

import (
	"context"
	"strconv"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestRedis 启动一个内存 miniredis 并把全局 RedisClient 指向它，
// 测试结束后自动还原，使三个资金 Lua 脚本可在真实 Redis 语义下验证。
func newTestRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	prev := RedisClient
	RedisClient = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		_ = RedisClient.Close()
		RedisClient = prev
		mr.Close()
	})
	return mr
}

func setBalances(t *testing.T, userID int32, grant, cash int64) {
	t.Helper()
	if err := SyncUserBalanceCache(context.Background(), userID, grant, cash); err != nil {
		t.Fatalf("failed to seed balances: %v", err)
	}
}

func balOf(t *testing.T, mr *miniredis.Miniredis, key string) int64 {
	t.Helper()
	v, err := mr.Get(key)
	if err != nil {
		t.Fatalf("missing key %s: %v", key, err)
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		t.Fatalf("key %s not int: %q", key, v)
	}
	return n
}

func assertBalances(t *testing.T, mr *miniredis.Miniredis, wantGrant, wantCash int64) {
	t.Helper()
	if g := balOf(t, mr, "grant_balance:1"); g != wantGrant {
		t.Fatalf("grant_balance = %d, want %d", g, wantGrant)
	}
	if c := balOf(t, mr, "cash_balance:1"); c != wantCash {
		t.Fatalf("cash_balance = %d, want %d", c, wantCash)
	}
}

// ---------- pre_deduct.lua ----------

func TestPreDeduct_BothGrantCoversCost(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 100, 50)

	g, c, err := PreDeduct(context.Background(), nil, 1, 80, "both")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g != 80 || c != 0 {
		t.Fatalf("deducted grant=%d cash=%d, want 80/0", g, c)
	}
	assertBalances(t, mr, 20, 50)
}

func TestPreDeduct_BothCascadesToCash(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 30, 100)

	g, c, err := PreDeduct(context.Background(), nil, 1, 80, "both")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g != 30 || c != 50 {
		t.Fatalf("deducted grant=%d cash=%d, want 30/50", g, c)
	}
	assertBalances(t, mr, 0, 50)
}

func TestPreDeduct_InsufficientLeavesBalancesUntouched(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 10, 20)

	_, _, err := PreDeduct(context.Background(), nil, 1, 80, "both")
	if err == nil {
		t.Fatal("expected insufficient balance error")
	}
	assertBalances(t, mr, 10, 20) // 余额不足时绝不能扣
}

func TestPreDeduct_CashOnlyIgnoresGrant(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 100, 50)

	g, c, err := PreDeduct(context.Background(), nil, 1, 40, "cash_only")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g != 0 || c != 40 {
		t.Fatalf("deducted grant=%d cash=%d, want 0/40", g, c)
	}
	assertBalances(t, mr, 100, 10)
}

func TestPreDeduct_GrantOnlyIgnoresCash(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 100, 50)

	g, c, err := PreDeduct(context.Background(), nil, 1, 40, "grant_only")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g != 40 || c != 0 {
		t.Fatalf("deducted grant=%d cash=%d, want 40/0", g, c)
	}
	assertBalances(t, mr, 60, 50)
}

func TestPreDeduct_GrantOnlyInsufficient(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 30, 1000)

	if _, _, err := PreDeduct(context.Background(), nil, 1, 80, "grant_only"); err == nil {
		t.Fatal("expected insufficient balance error for grant_only")
	}
	assertBalances(t, mr, 30, 1000)
}

// ---------- refund.lua ----------

func TestRefund_WithinCashDeducted(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 0, 0)

	// 当初扣款：grant 0 / cash 50；现退 30，应全退到 cash。
	Refund(context.Background(), nil, 1, 30, 0, 50, "req-1")
	assertBalances(t, mr, 0, 30)
}

func TestRefund_ExceedsCashSpillsToGrant(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 0, 0)

	// 当初扣款：grant 30 / cash 50；现退 70，应 cash 全退 50，余 20 退 grant。
	Refund(context.Background(), nil, 1, 70, 30, 50, "req-2")
	assertBalances(t, mr, 20, 50)
}

func TestRefund_AllFromGrant(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 0, 0)

	// 当初全从 grant 扣，退款全回 grant。
	Refund(context.Background(), nil, 1, 40, 40, 0, "req-3")
	assertBalances(t, mr, 40, 0)
}

// ---------- compensate.lua (diff < 0 补扣) ----------

func runCompensate(t *testing.T, amount int64, policy string) {
	t.Helper()
	keys := []string{"grant_balance:1", "cash_balance:1"}
	if err := compensateScript.Run(context.Background(), RedisClient, keys, amount, policy).Err(); err != nil {
		t.Fatalf("compensate script error: %v", err)
	}
}

func TestCompensate_BothDeductsGrantFirst(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 100, 100)

	runCompensate(t, 50, "both")
	assertBalances(t, mr, 50, 100)
}

func TestCompensate_BothCascadesToCash(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 30, 100)

	runCompensate(t, 80, "both") // grant 30 -> 0, 余 50 扣 cash
	assertBalances(t, mr, 0, 50)
}

func TestCompensate_FloorsAtZeroNeverNegative(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 10, 20)

	runCompensate(t, 80, "both") // 总额仅 30，最多扣到 0，绝不为负
	assertBalances(t, mr, 0, 0)
}

func TestCompensate_GrantOnly(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 100, 100)

	runCompensate(t, 30, "grant_only")
	assertBalances(t, mr, 70, 100)
}

func TestCompensate_CashOnly(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 100, 100)

	runCompensate(t, 30, "cash_only")
	assertBalances(t, mr, 100, 70)
}
