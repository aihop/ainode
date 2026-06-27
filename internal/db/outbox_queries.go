package db

import "context"

const insertSettlementOutbox = `-- name: InsertSettlementOutbox :exec
INSERT INTO settlement_outbox (request_id, payload)
VALUES ($1, $2)
ON CONFLICT (request_id) DO NOTHING
`

// InsertSettlementOutbox 持久化一条待投递的结算；request_id 冲突时幂等忽略。
func (q *Queries) InsertSettlementOutbox(ctx context.Context, requestID string, payload []byte) error {
	_, err := q.db.Exec(ctx, insertSettlementOutbox, requestID, payload)
	return err
}

const listPendingSettlementOutbox = `-- name: ListPendingSettlementOutbox :many
SELECT id, request_id, payload, attempts
FROM settlement_outbox
WHERE processed_at IS NULL
ORDER BY created_at
LIMIT $1
`

// ListPendingSettlementOutbox 返回尚未投递成功的结算，按创建时间升序。
func (q *Queries) ListPendingSettlementOutbox(ctx context.Context, limit int32) ([]SettlementOutbox, error) {
	rows, err := q.db.Query(ctx, listPendingSettlementOutbox, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []SettlementOutbox
	for rows.Next() {
		var i SettlementOutbox
		if err := rows.Scan(&i.ID, &i.RequestID, &i.Payload, &i.Attempts); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

const markSettlementOutboxProcessed = `-- name: MarkSettlementOutboxProcessed :exec
UPDATE settlement_outbox SET processed_at = NOW() WHERE id = $1
`

// MarkSettlementOutboxProcessed 标记某条结算已成功投递。
func (q *Queries) MarkSettlementOutboxProcessed(ctx context.Context, id int64) error {
	_, err := q.db.Exec(ctx, markSettlementOutboxProcessed, id)
	return err
}

const incrementSettlementOutboxAttempts = `-- name: IncrementSettlementOutboxAttempts :exec
UPDATE settlement_outbox SET attempts = attempts + 1 WHERE id = $1
`

// IncrementSettlementOutboxAttempts 记录一次投递重试，便于排查长期失败的结算。
func (q *Queries) IncrementSettlementOutboxAttempts(ctx context.Context, id int64) error {
	_, err := q.db.Exec(ctx, incrementSettlementOutboxAttempts, id)
	return err
}
