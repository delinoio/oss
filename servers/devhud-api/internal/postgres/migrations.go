package postgres

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/delinoio/oss/servers/devhud-api/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

const migrationAdvisoryLock int64 = 0x6465766875646d67

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer connection.Release()

	if _, err := connection.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationAdvisoryLock); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	defer func() {
		_, _ = connection.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", migrationAdvisoryLock)
	}()

	if _, err := connection.Exec(ctx, `CREATE TABLE IF NOT EXISTS devhud_schema_migrations (
        version text PRIMARY KEY,
        applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
    )`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}

	entries, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(entries)
	for _, version := range entries {
		var applied bool
		if err := connection.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM devhud_schema_migrations WHERE version = $1)", version).Scan(&applied); err != nil {
			return fmt.Errorf("read migration %s status: %w", version, err)
		}
		if applied {
			continue
		}
		sql, err := migrations.Files.ReadFile(version)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", version, err)
		}
		tx, err := connection.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", version, err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", version, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO devhud_schema_migrations (version) VALUES ($1)", version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %s: %w", version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", version, err)
		}
	}
	return nil
}

func expectedMigrationCount() int {
	entries, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry, ".sql") {
			count++
		}
	}
	return count
}
