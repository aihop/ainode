package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"aihop.io/ainode/internal/config"

	"github.com/jackc/pgx/v5/pgxpool"
)

const bootstrapSchemaVersion = "0000_schema.sql"

type migration struct {
	Version string
	Path    string
}

func main() {
	action := "up"
	if len(os.Args) > 1 {
		action = strings.ToLower(strings.TrimSpace(os.Args[1]))
	}

	if databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL")); databaseURL != "" && os.Getenv("DB_DSN") == "" {
		_ = os.Setenv("DB_DSN", databaseURL)
	}

	config.LoadConfig()
	if config.AppConfig == nil || strings.TrimSpace(config.AppConfig.DB.DSN) == "" {
		log.Fatal("db.dsn is empty; set DB_DSN or DATABASE_URL, or configure config.yaml")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, config.AppConfig.DB.DSN)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer pool.Close()

	rootDir, err := os.Getwd()
	if err != nil {
		log.Fatalf("failed to get current directory: %v", err)
	}

	if ensureErr := ensureMigrationsTable(ctx, pool); ensureErr != nil {
		log.Fatalf("failed to ensure schema_migrations table: %v", ensureErr)
	}

	migrations, err := loadMigrations(rootDir)
	if err != nil {
		log.Fatalf("failed to load migrations: %v", err)
	}

	switch action {
	case "up":
		if err := runUp(ctx, pool, migrations); err != nil {
			log.Fatalf("migration failed: %v", err)
		}
	case "status":
		if err := printStatus(ctx, pool, migrations); err != nil {
			log.Fatalf("failed to check migration status: %v", err)
		}
	default:
		log.Fatalf("unsupported action %q, expected: up or status", action)
	}
}

func ensureMigrationsTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version VARCHAR(255) PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)`)
	return err
}

func loadMigrations(rootDir string) ([]migration, error) {
	migrations := make([]migration, 0)

	schemaPath := filepath.Join(rootDir, "schema.sql")
	if _, err := os.Stat(schemaPath); err == nil {
		migrations = append(migrations, migration{
			Version: bootstrapSchemaVersion,
			Path:    schemaPath,
		})
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	files, err := filepath.Glob(filepath.Join(rootDir, "migrations", "*.sql"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)

	for _, file := range files {
		migrations = append(migrations, migration{
			Version: filepath.Base(file),
			Path:    file,
		})
	}

	return migrations, nil
}

func appliedVersions(ctx context.Context, pool *pgxpool.Pool) (map[string]time.Time, error) {
	rows, err := pool.Query(ctx, `SELECT version, applied_at FROM schema_migrations ORDER BY version ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	versions := make(map[string]time.Time)
	for rows.Next() {
		var version string
		var appliedAt time.Time
		if err := rows.Scan(&version, &appliedAt); err != nil {
			return nil, err
		}
		versions[version] = appliedAt
	}

	return versions, rows.Err()
}

func runUp(ctx context.Context, pool *pgxpool.Pool, migrations []migration) error {
	applied, err := appliedVersions(ctx, pool)
	if err != nil {
		return err
	}

	appliedCount := 0
	for _, item := range migrations {
		if _, ok := applied[item.Version]; ok {
			log.Printf("skip %s (already applied)", item.Version)
			continue
		}

		sqlBytes, err := os.ReadFile(item.Path)
		if err != nil {
			return fmt.Errorf("read %s: %w", item.Path, err)
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("execute %s: %w", item.Version, err)
		}

		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, item.Version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record %s: %w", item.Version, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit %s: %w", item.Version, err)
		}

		appliedCount++
		log.Printf("applied %s", item.Version)
	}

	if appliedCount == 0 {
		log.Println("no pending migrations")
	} else {
		log.Printf("migration completed, applied %d item(s)", appliedCount)
	}

	return nil
}

func printStatus(ctx context.Context, pool *pgxpool.Pool, migrations []migration) error {
	applied, err := appliedVersions(ctx, pool)
	if err != nil {
		return err
	}

	for _, item := range migrations {
		if appliedAt, ok := applied[item.Version]; ok {
			fmt.Printf("APPLIED  %s  %s\n", item.Version, appliedAt.Format(time.RFC3339))
			continue
		}
		fmt.Printf("PENDING  %s\n", item.Version)
	}

	return nil
}
