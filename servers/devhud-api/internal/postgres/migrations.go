package postgres

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/delinoio/oss/servers/devhud-api/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

const migrationAdvisoryLock int64 = 0x6465766875646d67
const migrationAdvisoryUnlockTimeout = time.Second

func Migrate(ctx context.Context, pool *pgxpool.Pool) (returnErr error) {
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}

	if _, err := connection.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationAdvisoryLock); err != nil {
		return errors.Join(fmt.Errorf("lock migrations: %w", err), discardPoolConnection(connection))
	}
	defer func() {
		returnErr = errors.Join(returnErr, releaseMigrationLock(ctx, connection))
	}()

	if _, err := connection.Exec(ctx, `CREATE TABLE IF NOT EXISTS devhud_schema_migrations (
        version text PRIMARY KEY,
        applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
    )`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}

	entries, err := expectedMigrationVersions()
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
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

func releaseMigrationLock(ctx context.Context, connection *pgxpool.Conn) error {
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), migrationAdvisoryUnlockTimeout)
	defer cancel()
	var unlocked bool
	if err := connection.QueryRow(cleanupContext, "SELECT pg_advisory_unlock($1)", migrationAdvisoryLock).Scan(&unlocked); err != nil {
		return errors.Join(fmt.Errorf("unlock migrations: %w", err), connection.Hijack().Close(cleanupContext))
	}
	if !unlocked {
		return errors.Join(errors.New("migration advisory lock was not held"), connection.Hijack().Close(cleanupContext))
	}
	connection.Release()
	return nil
}

func expectedMigrationVersions() ([]string, error) {
	entries, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(entries)
	return entries, nil
}
