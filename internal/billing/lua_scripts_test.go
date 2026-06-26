package billing

import (
	"context"
	"strconv"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestRedis 启动内存 miniredis 并把全局 RedisClient 指向它,测试结束自动还原。
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

// setBalances 预置用户 1 的三池余额(sub_paid, grant, cash)。
func setBalances(t *testing.T, userID int32, subPaid, grant, cash int64) {
	t.Helper()
	if err := SyncUserBalanceCache(context.Background(), userID, subPaid, grant, cash); err != nil {
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

// assertBalances 校验用户 1 的三池余额。
func assertBalances(t *testing.T, mr *miniredis.Miniredis, wantSubPaid, wantGrant, wantCash int64) {
	t.Helper()
	if p := balOf(t, mr, "sub_paid_balance:1"); p != wantSubPaid {
		t.Fatalf("sub_paid = %d, want %d", p, wantSubPaid)
	}
	if g := balOf(t, mr, "grant_balance:1"); g != wantGrant {
		t.Fatalf("grant = %d, want %d", g, wantGrant)
	}
	if c := balOf(t, mr, "cash_balance:1"); c != wantCash {
		t.Fatalf("cash = %d, want %d", c, wantCash)
	}
}

// ---------- pre_deduct.lua (三池有序 sub_paid → grant → cash) ----------

func TestPreDeduct_SubPaidCoversCost(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 100, 50, 50)
	d, err := PreDeduct(context.Background(), nil, 1, 80, "all")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.SubPaid != 80 || d.Grant != 0 || d.Cash != 0 {
		t.Fatalf("deducted %+v, want 80/0/0", d)
	}
	assertBalances(t, mr, 20, 50, 50)
}

func TestPreDeduct_CascadeSubPaidToGrant(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 30, 100, 50)
	d, err := PreDeduct(context.Background(), nil, 1, 80, "all")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.SubPaid != 30 || d.Grant != 50 || d.Cash != 0 {
		t.Fatalf("deducted %+v, want 30/50/0", d)
	}
	assertBalances(t, mr, 0, 50, 50)
}

func TestPreDeduct_CascadeThroughAllThree(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 30, 30, 100)
	d, err := PreDeduct(context.Background(), nil, 1, 80, "all")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.SubPaid != 30 || d.Grant != 30 || d.Cash != 20 {
		t.Fatalf("deducted %+v, want 30/30/20", d)
	}
	assertBalances(t, mr, 0, 0, 80)
}

func TestPreDeduct_InsufficientLeavesUntouched(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 10, 10, 10)
	if _, err := PreDeduct(context.Background(), nil, 1, 80, "all"); err == nil {
		t.Fatal("expected insufficient balance error")
	}
	assertBalances(t, mr, 10, 10, 10)
}

func TestPreDeduct_CashOnly(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 100, 100, 50)
	d, err := PreDeduct(context.Background(), nil, 1, 40, "cash_only")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.SubPaid != 0 || d.Grant != 0 || d.Cash != 40 {
		t.Fatalf("deducted %+v, want 0/0/40", d)
	}
	assertBalances(t, mr, 100, 100, 10)
}

func TestPreDeduct_GrantOnlyUsesSubscriptionPools(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 100, 100, 50)
	// grant_only = 订阅池(sub_paid + grant),不含 cash;先扣 sub_paid
	d, err := PreDeduct(context.Background(), nil, 1, 40, "grant_only")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.SubPaid != 40 || d.Grant != 0 || d.Cash != 0 {
		t.Fatalf("deducted %+v, want 40/0/0", d)
	}
	assertBalances(t, mr, 60, 100, 50)
}

func TestPreDeduct_GrantOnlyInsufficient(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 10, 20, 1000) // 订阅池仅 30 < 80
	if _, err := PreDeduct(context.Background(), nil, 1, 80, "grant_only"); err == nil {
		t.Fatal("expected insufficient balance error for grant_only")
	}
	assertBalances(t, mr, 10, 20, 1000)
}

// ---------- refund.lua (三池逆序 cash → grant → sub_paid) ----------

func refund(userID int32, amount, sp, gr, ca int64) {
	Refund(context.Background(), nil, userID, amount, Deduction{SubPaid: sp, Grant: gr, Cash: ca}, "req")
}

func TestRefund_WithinCash(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 0, 0, 0)
	refund(1, 30, 0, 0, 50) // 当初全从 cash 扣,退 30 → cash
	assertBalances(t, mr, 0, 0, 30)
}

func TestRefund_SpillsCashToGrant(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 0, 0, 0)
	refund(1, 70, 0, 30, 50) // cash 全退 50,余 20 退 grant
	assertBalances(t, mr, 0, 20, 50)
}

func TestRefund_ReverseOrderAcrossThree(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 0, 0, 0)
	refund(1, 100, 20, 30, 50) // 逆序:cash 50, grant 30, sub_paid 20
	assertBalances(t, mr, 20, 30, 50)
}

func TestRefund_AllFromSubPaid(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 0, 0, 0)
	refund(1, 40, 40, 0, 0)
	assertBalances(t, mr, 40, 0, 0)
}

// ---------- compensate.lua (三池正序 sub_paid → grant → cash, 带下限) ----------

func runCompensate(t *testing.T, amount int64, policy string) {
	t.Helper()
	if err := compensateScript.Run(context.Background(), RedisClient, balanceKeys3(1), amount, policy).Err(); err != nil {
		t.Fatalf("compensate script error: %v", err)
	}
}

func TestCompensate_SubPaidFirst(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 100, 100, 100)
	runCompensate(t, 50, "all")
	assertBalances(t, mr, 50, 100, 100)
}

func TestCompensate_CascadesAcrossThree(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 30, 30, 100)
	runCompensate(t, 80, "all") // sub_paid 30→0, grant 30→0, cash 扣 20
	assertBalances(t, mr, 0, 0, 80)
}

func TestCompensate_FloorsAtZero(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 10, 10, 10)
	runCompensate(t, 80, "all") // 总额仅 30,最多扣到 0,绝不为负
	assertBalances(t, mr, 0, 0, 0)
}

func TestCompensate_CashOnly(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 100, 100, 100)
	runCompensate(t, 30, "cash_only")
	assertBalances(t, mr, 100, 100, 70)
}

func TestCompensate_GrantOnly(t *testing.T) {
	mr := newTestRedis(t)
	setBalances(t, 1, 100, 100, 100)
	runCompensate(t, 30, "grant_only") // 订阅池正序,先扣 sub_paid
	assertBalances(t, mr, 70, 100, 100)
}
