package db

import "context"

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
