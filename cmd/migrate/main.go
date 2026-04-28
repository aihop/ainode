package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5"
)

func main() {
	dsn := "postgres://datapaas:8kdxEFH8zztfz7QE@192.168.1.117:5432/datapaas?sslmode=disable"
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		log.Fatalf("Unable to connect to database: %v\n", err)
	}
	defer conn.Close(context.Background())

	sqlBytes, err := os.ReadFile("migrate_cache_tokens.sql")
	if err != nil {
		log.Fatalf("Failed to read sql file: %v\n", err)
	}

	_, err = conn.Exec(context.Background(), string(sqlBytes))
	if err != nil {
		log.Fatalf("Failed to execute migration script: %v\n", err)
	}

	fmt.Println("Migration executed successfully!")
}