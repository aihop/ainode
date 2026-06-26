package billing

import (
	"context"
	"errors"

	"aihop.io/ainode/internal/db"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SubscriptionApply 是一次订阅状态转移的入参(订阅/续费/升级/降级/取消/过期统一)。
// 取消/过期时 NewPaid=NewGrant=0、ExpiresAt 置空。
type SubscriptionApply struct {
	UserID     int32
	NewPaid    int64 // 本期订阅实付(10^8 放大)
	NewGrant   int64 // 本期订阅赠送(10^8 放大)
	ExpiresAt  pgtype.Timestamptz
	Tier       int32
	EventID    string // 幂等键(写入 transactions.event_id)
	Type       string // 事务类型:sub_apply | sub_cancel
	SourceType string // apayshop | system
	SourceID   string
	Remark     string
}

// ApplySubscription 原子应用订阅状态转移:
//
//	旧 sub_paid 剩余 → cash;覆盖写入新 sub_paid/grant/到期/等级;写 transactions + balance_logs(同事务);
//	提交后失效三池 Redis 缓存(下次请求从已更新 DB 重载),避免缓存与 DB 不一致。
//
// 通过 transactions.event_id 唯一约束保证幂等:重复事件不会重复转移。
// 返回 applied=false 表示该 event_id 已处理过(幂等命中)。
func ApplySubscription(ctx context.Context, pool *pgxpool.Pool, queries *db.Queries, p SubscriptionApply) (applied bool, err error) {
	eventID := pgtype.Text{String: p.EventID, Valid: p.EventID != ""}

	// 预检幂等(快速短路;真正的保证是下面的唯一约束)
	if p.EventID != "" {
		if _, e := queries.GetTransactionByEventID(ctx, eventID); e == nil {
			return false, nil
		}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := queries.WithTx(tx)

	// 旧三池(用于审计 before/after)
	oldPaid, oldGrant, oldCash, err := qtx.GetUserBalances(ctx, p.UserID)
	if err != nil {
		return false, err
	}

	// DB 状态转移:cash += 旧 sub_paid;覆盖 sub_paid/grant/到期/等级。返回结转额(=旧 sub_paid)。
	movedToCash, err := qtx.ApplySubscriptionDB(ctx, p.UserID, p.NewPaid, p.NewGrant, p.ExpiresAt, pgtype.Int4{Int32: p.Tier, Valid: true})
	if err != nil {
		return false, err
	}

	txType := p.Type
	if txType == "" {
		txType = "sub_apply"
	}
	// 主事务记录(承载 event_id 幂等):记新订阅额度发放(实付+赠送)
	newTotal := p.NewPaid + p.NewGrant
	transaction, err := qtx.CreateTransaction(ctx, db.CreateTransactionParams{
		UserID:             p.UserID,
		EventID:            eventID,
		Type:               txType,
		BalanceType:        "grant",
		Direction:          "credit",
		AmountCents:        newTotal,
		BeforeBalanceCents: oldPaid + oldGrant,
		AfterBalanceCents:  newTotal,
		SourceType:         nonEmpty(p.SourceType, "system"),
		SourceID:           p.SourceID,
		Status:             "completed",
		Remark:             p.Remark,
		Metadata:           []byte("{}"),
	})
	if err != nil {
		// 唯一约束冲突 = 并发下已处理 → 幂等返回
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return false, nil
		}
		return false, err
	}

	txID := pgtype.Int8{Int64: transaction.ID, Valid: true}

	// 余额流水:订阅额度变更(新发放/清零)
	if err := qtx.CreateBalanceLog(ctx, db.CreateBalanceLogParams{
		TransactionID:      txID,
		UserID:             p.UserID,
		BalanceType:        "subscription",
		ActionType:         txType,
		AmountCents:        newTotal,
		BeforeBalanceCents: oldPaid + oldGrant,
		AfterBalanceCents:  newTotal,
		OperatorAdminID:    pgtype.Int4{},
		OperatorName:       nonEmpty(p.SourceType, "system"),
		Remark:             p.Remark,
	}); err != nil {
		return false, err
	}

	// 余额流水:旧实付剩余结转到 cash
	if movedToCash > 0 {
		if err := qtx.CreateBalanceLog(ctx, db.CreateBalanceLogParams{
			TransactionID:      txID,
			UserID:             p.UserID,
			BalanceType:        "cash",
			ActionType:         "sub_paid_to_cash",
			AmountCents:        movedToCash,
			BeforeBalanceCents: oldCash,
			AfterBalanceCents:  oldCash + movedToCash,
			OperatorAdminID:    pgtype.Int4{},
			OperatorName:       nonEmpty(p.SourceType, "system"),
			Remark:             "订阅实付剩余结转充值余额",
		}); err != nil {
			return false, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, err
	}

	// 失效三池缓存,下次请求从已更新的 DB 重载(避免覆盖/竞态)。
	if RedisClient != nil {
		_ = RedisClient.Del(ctx, SubPaidBalanceKey(p.UserID), GrantBalanceKey(p.UserID), CashBalanceKey(p.UserID)).Err()
	}

	return true, nil
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
