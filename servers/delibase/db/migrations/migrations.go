// Package migrations embeds and applies delibase's ordered PostgreSQL schema.
package migrations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const advisoryLockID int64 = 7_221_001

var migrationName = regexp.MustCompile(`^([0-9]{6})_[a-z0-9_]+\.sql$`)

//go:embed *.sql
var files embed.FS

type migration struct {
	version  int64
	name     string
	contents string
	checksum [sha256.Size]byte
}

// Run serializes migration execution across server instances and applies each
// file in its own transaction. A failed file is fully rolled back.
func Run(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("migrations: database pool is required")
	}
	ordered, err := load(files)
	if err != nil {
		return err
	}

	connection, err := pool.Acquire(ctx)
	if err != nil {
		return errors.New("migrations: could not acquire database connection")
	}
	defer connection.Release()

	if _, err := connection.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockID); err != nil {
		return errors.New("migrations: could not acquire migration lock")
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = connection.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", advisoryLockID)
	}()

	if _, err := connection.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version bigint PRIMARY KEY,
			name text NOT NULL UNIQUE,
			checksum bytea NOT NULL CHECK (octet_length(checksum) = 32),
			applied_at timestamptz NOT NULL DEFAULT transaction_timestamp()
		)
	`); err != nil {
		return errors.New("migrations: could not initialize migration history")
	}

	appliedRows, err := connection.Query(ctx, "SELECT version, checksum FROM schema_migrations ORDER BY version")
	if err != nil {
		return errors.New("migrations: could not read migration history")
	}
	applied := make(map[int64][]byte)
	for appliedRows.Next() {
		var version int64
		var checksum []byte
		if err := appliedRows.Scan(&version, &checksum); err != nil {
			appliedRows.Close()
			return errors.New("migrations: invalid migration history")
		}
		applied[version] = checksum
	}
	if err := appliedRows.Err(); err != nil {
		appliedRows.Close()
		return errors.New("migrations: could not read migration history")
	}
	appliedRows.Close()

	known := make(map[int64]struct{}, len(ordered))
	for _, item := range ordered {
		known[item.version] = struct{}{}
	}
	for version := range applied {
		if _, ok := known[version]; !ok {
			return fmt.Errorf("migrations: database contains unknown version %06d", version)
		}
	}
	for _, item := range ordered {
		if checksum, ok := applied[item.version]; ok {
			if !bytes.Equal(checksum, item.checksum[:]) {
				return fmt.Errorf("migrations: checksum mismatch for version %06d", item.version)
			}
			continue
		}
		if err := apply(ctx, connection.Conn(), item); err != nil {
			return err
		}
	}
	return nil
}

func apply(ctx context.Context, connection *pgx.Conn, item migration) error {
	transaction, err := connection.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("migrations: could not begin version %06d", item.version)
	}
	defer func() { _ = transaction.Rollback(context.WithoutCancel(ctx)) }()

	results, err := transaction.Conn().PgConn().Exec(ctx, item.contents).ReadAll()
	if err != nil {
		return fmt.Errorf("migrations: version %06d failed", item.version)
	}
	for _, result := range results {
		if result.Err != nil {
			return fmt.Errorf("migrations: version %06d failed", item.version)
		}
	}
	if _, err := transaction.Exec(
		ctx,
		"INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)",
		item.version,
		item.name,
		item.checksum[:],
	); err != nil {
		return fmt.Errorf("migrations: could not record version %06d", item.version)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("migrations: could not commit version %06d", item.version)
	}
	return nil
}

func load(source fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(source, ".")
	if err != nil {
		return nil, errors.New("migrations: could not read embedded files")
	}
	ordered := make([]migration, 0, len(entries))
	seen := make(map[int64]struct{})
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "migrations.go" || entry.Name() == "migrations_test.go" {
			continue
		}
		matches := migrationName.FindStringSubmatch(entry.Name())
		if matches == nil {
			return nil, fmt.Errorf("migrations: invalid filename %q", entry.Name())
		}
		version, err := strconv.ParseInt(matches[1], 10, 64)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("migrations: invalid version in %q", entry.Name())
		}
		if _, duplicate := seen[version]; duplicate {
			return nil, fmt.Errorf("migrations: duplicate version %06d", version)
		}
		contents, err := fs.ReadFile(source, entry.Name())
		if err != nil || len(bytes.TrimSpace(contents)) == 0 {
			return nil, fmt.Errorf("migrations: could not read %q", entry.Name())
		}
		seen[version] = struct{}{}
		ordered = append(ordered, migration{
			version:  version,
			name:     entry.Name(),
			contents: string(contents),
			checksum: sha256.Sum256(contents),
		})
	}
	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left].version < ordered[right].version
	})
	if len(ordered) == 0 {
		return nil, errors.New("migrations: no migration files found")
	}
	for index, item := range ordered {
		expected := int64(index + 1)
		if item.version != expected {
			return nil, fmt.Errorf(
				"migrations: expected version %06d, found %06d",
				expected,
				item.version,
			)
		}
	}
	return ordered, nil
}
