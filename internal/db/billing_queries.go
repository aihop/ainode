package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

// 手写 sqlc 风格查询：用户累计(全时段)消费总额。
// 若后续切到 sqlc 生成，请把 SQL 迁到 query.sql 并删除本文件，避免重复方法定义。

const getUserTotalSpend = `-- name: GetUserTotalSpend :one
SELECT COALESCE(SUM(amount_cents), 0)::bigint
FROM billing_logs
WHERE user_id = $1
`

// GetUserTotalSpend 返回该用户全时段的累计消费（10^8 放大的整数）。
func (q *Queries) GetUserTotalSpend(ctx context.Context, userID int32) (int64, error) {
	row := q.db.QueryRow(ctx, getUserTotalSpend, userID)
	var total int64
	err := row.Scan(&total)
	return total, err
}

const getUserTotalCredited = `-- name: GetUserTotalCredited :one
SELECT COALESCE(SUM(amount_cents), 0)::bigint
FROM transactions
WHERE user_id = $1 AND direction = 'credit' AND type <> 'refund'
`

// GetUserTotalCredited 返回该用户累计「有效入账」总额（10^8 放大的整数）：
// 充值/购买(topup) + 套餐赠送(grant_issue/grant_reset) + 管理员直充(admin_adjust) 等 credit 流水，
// 排除退款(refund) 冲正。
func (q *Queries) GetUserTotalCredited(ctx context.Context, userID int32) (int64, error) {
	row := q.db.QueryRow(ctx, getUserTotalCredited, userID)
	var total int64
	err := row.Scan(&total)
	return total, err
}

const getUserBalances = `-- name: GetUserBalances :one
SELECT
    COALESCE(sub_paid_balance, 0)::bigint,
    COALESCE(grant_balance, 0)::bigint,
    COALESCE(cash_balance, 0)::bigint
FROM users WHERE id = $1
`

// SetAsyncTaskSubPaidDeducted 记录异步任务预扣中来自 sub_paid 池的金额(创建后补写)。
func (q *Queries) SetAsyncTaskSubPaidDeducted(ctx context.Context, taskID string, subPaid int64) error {
	_, err := q.db.Exec(ctx, `UPDATE async_tasks SET sub_paid_deducted = $2 WHERE id = $1`, taskID, subPaid)
	return err
}

// GetAsyncTaskSubPaidDeducted 读取异步任务的 sub_paid 预扣额(退款/结算时按池退回所需)。
func (q *Queries) GetAsyncTaskSubPaidDeducted(ctx context.Context, taskID string) (int64, error) {
	var v int64
	err := q.db.QueryRow(ctx, `SELECT COALESCE(sub_paid_deducted,0)::bigint FROM async_tasks WHERE id = $1`, taskID).Scan(&v)
	return v, err
}

const listExpiredSubscriptionUsers = `-- name: ListExpiredSubscriptionUsers :many
SELECT id FROM users
WHERE sub_expires_at IS NOT NULL AND sub_expires_at < now()
  AND (sub_paid_balance > 0 OR grant_balance > 0)
`

// ListExpiredSubscriptionUsers 返回订阅已过期但仍有 sub_paid/grant 余额的用户(待清理)。
func (q *Queries) ListExpiredSubscriptionUsers(ctx context.Context) ([]int32, error) {
	rows, err := q.db.Query(ctx, listExpiredSubscriptionUsers)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int32
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetUserBalances 返回用户三池余额(sub_paid, grant, cash;均为 10^8 放大整数)。
func (q *Queries) GetUserBalances(ctx context.Context, userID int32) (subPaid int64, grant int64, cash int64, err error) {
	row := q.db.QueryRow(ctx, getUserBalances, userID)
	err = row.Scan(&subPaid, &grant, &cash)
	return subPaid, grant, cash, err
}

const updateUserSubPaidBalance = `-- name: UpdateUserSubPaidBalance :exec
UPDATE users SET sub_paid_balance = sub_paid_balance + $2 WHERE id = $1
`

// UpdateUserSubPaidBalance 对订阅实付池做增量(传负数为扣减)。
func (q *Queries) UpdateUserSubPaidBalance(ctx context.Context, userID int32, delta int64) error {
	_, err := q.db.Exec(ctx, updateUserSubPaidBalance, userID, delta)
	return err
}

// ApplySubscriptionDB 在 DB 内原子完成订阅状态转移:
// 旧 sub_paid 剩余并入 cash、覆盖写入新 sub_paid/grant/到期/等级。返回结转金额(旧实付剩余)。
func (q *Queries) ApplySubscriptionDB(ctx context.Context, userID int32, newPaid, newGrant int64, expiresAt pgtype.Timestamptz, tier pgtype.Int4) (movedToCash int64, err error) {
	// 先取旧 sub_paid 作为结转额(更新后无法直接拿到旧值)
	if err = q.db.QueryRow(ctx, `SELECT COALESCE(sub_paid_balance,0)::bigint FROM users WHERE id=$1`, userID).Scan(&movedToCash); err != nil {
		return 0, err
	}
	_, err = q.db.Exec(ctx, `
UPDATE users SET
    cash_balance     = cash_balance + sub_paid_balance,
    sub_paid_balance = $2,
    grant_balance    = $3,
    sub_expires_at   = $4,
    tier_level       = $5
WHERE id = $1`, userID, newPaid, newGrant, expiresAt, tier)
	if err != nil {
		return 0, err
	}
	return movedToCash, nil
}
