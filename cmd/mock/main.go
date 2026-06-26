package main

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dbURL := "postgres://ainode:8kdxEFH8zztfz7QE@192.168.1.117:5432/ainode"
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		log.Fatalf("Unable to connect to database: %v\n", err)
	}
	defer pool.Close()

	sql := `
	INSERT INTO api_keys (name, key_string, user_id, order_id, product_id, tier_level, quota_limit, quota_used, allowed_models, status, created_at) VALUES 
	('Default Flex Key', 'sk-test-flex-001', 1, NULL, NULL, 0, 0, 0, '[]', 1, NOW() - INTERVAL '10 days'),
	('Pro Sub Key', 'sk-test-pro-002', 1, NULL, 2, 1, 5000000000, 150000000, '["gpt-4o", "claude-3-opus-20240229"]', 1, NOW() - INTERVAL '2 days'),
	('Old Revoked Key', 'sk-test-revoked-003', 1, NULL, NULL, 0, 0, 0, '[]', 0, NOW() - INTERVAL '30 days')
	ON CONFLICT DO NOTHING;
	`

	_, err = pool.Exec(context.Background(), sql)
	if err != nil {
		log.Fatalf("Failed to insert mock keys: %v\n", err)
	}

	fmt.Println("Successfully inserted mock api_keys")
}
