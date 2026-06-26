ALTER TABLE transactions
ADD COLUMN IF NOT EXISTS event_id VARCHAR(120);

CREATE UNIQUE INDEX IF NOT EXISTS idx_transactions_event_id_unique
ON transactions (event_id)
WHERE event_id IS NOT NULL AND event_id <> '';
