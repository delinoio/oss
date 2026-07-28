// Package migrations embeds and applies RealQA's ordered PostgreSQL schema.
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

const advisoryLockID int64 = 7_570_001

var migrationName = regexp.MustCompile(`^([0-9]{6})_[a-z0-9_]+\.sql$`)

//go:embed *.sql
var files embed.FS

type migration struct {
	version  int64
	name     string
	contents string
	checksum [sha256.Size]byte
}

func Run(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("realqa migrations: database pool is required")
	}
	ordered, err := load(files)
	if err != nil {
		return err
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return errors.New("realqa migrations: could not acquire connection")
	}
	defer connection.Release()
	if _, err = connection.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockID); err != nil {
		return errors.New("realqa migrations: could not acquire lock")
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = connection.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", advisoryLockID)
	}()
	if _, err = connection.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS realqa_schema_migrations (
			version bigint PRIMARY KEY,
			name text NOT NULL UNIQUE,
			checksum bytea NOT NULL CHECK (octet_length(checksum) = 32),
			applied_at timestamptz NOT NULL DEFAULT transaction_timestamp()
		)
	`); err != nil {
		return errors.New("realqa migrations: could not initialize history")
	}
	rows, err := connection.Query(ctx,
		"SELECT version, checksum FROM realqa_schema_migrations ORDER BY version")
	if err != nil {
		return errors.New("realqa migrations: could not read history")
	}
	applied := make(map[int64][]byte)
	for rows.Next() {
		var version int64
		var checksum []byte
		if err = rows.Scan(&version, &checksum); err != nil {
			rows.Close()
			return errors.New("realqa migrations: invalid history")
		}
		applied[version] = checksum
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return errors.New("realqa migrations: invalid history")
	}
	rows.Close()
	known := make(map[int64]struct{}, len(ordered))
	for _, item := range ordered {
		known[item.version] = struct{}{}
	}
	for version := range applied {
		if _, ok := known[version]; !ok {
			return fmt.Errorf("realqa migrations: unknown version %06d", version)
		}
	}
	for _, item := range ordered {
		if checksum, ok := applied[item.version]; ok {
			if !bytes.Equal(checksum, item.checksum[:]) {
				return fmt.Errorf("realqa migrations: checksum mismatch for version %06d", item.version)
			}
			continue
		}
		if err = apply(ctx, connection.Conn(), item); err != nil {
			return err
		}
	}
	return nil
}

func apply(ctx context.Context, connection *pgx.Conn, item migration) error {
	transaction, err := connection.Begin(ctx)
	if err != nil {
		return fmt.Errorf("realqa migrations: begin version %06d", item.version)
	}
	defer func() { _ = transaction.Rollback(context.WithoutCancel(ctx)) }()
	results, err := transaction.Conn().PgConn().Exec(ctx, item.contents).ReadAll()
	if err != nil {
		return fmt.Errorf("realqa migrations: version %06d failed", item.version)
	}
	for _, result := range results {
		if result.Err != nil {
			return fmt.Errorf("realqa migrations: version %06d failed", item.version)
		}
	}
	if _, err = transaction.Exec(ctx,
		"INSERT INTO realqa_schema_migrations (version, name, checksum) VALUES ($1, $2, $3)",
		item.version, item.name, item.checksum[:]); err != nil {
		return fmt.Errorf("realqa migrations: record version %06d", item.version)
	}
	if err = transaction.Commit(ctx); err != nil {
		return fmt.Errorf("realqa migrations: commit version %06d", item.version)
	}
	return nil
}

func load(source fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(source, ".")
	if err != nil {
		return nil, errors.New("realqa migrations: could not read files")
	}
	var ordered []migration
	seen := make(map[int64]struct{})
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "migrations.go" ||
			entry.Name() == "migrations_test.go" {
			continue
		}
		match := migrationName.FindStringSubmatch(entry.Name())
		if match == nil {
			return nil, fmt.Errorf("realqa migrations: invalid filename %q", entry.Name())
		}
		version, parseErr := strconv.ParseInt(match[1], 10, 64)
		if parseErr != nil || version <= 0 {
			return nil, fmt.Errorf("realqa migrations: invalid version in %q", entry.Name())
		}
		if _, exists := seen[version]; exists {
			return nil, fmt.Errorf("realqa migrations: duplicate version %06d", version)
		}
		content, readErr := fs.ReadFile(source, entry.Name())
		if readErr != nil || len(bytes.TrimSpace(content)) == 0 {
			return nil, fmt.Errorf("realqa migrations: unreadable %q", entry.Name())
		}
		seen[version] = struct{}{}
		ordered = append(ordered, migration{
			version: version, name: entry.Name(), contents: string(content),
			checksum: sha256.Sum256(content),
		})
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].version < ordered[j].version })
	if len(ordered) == 0 {
		return nil, errors.New("realqa migrations: no migration files")
	}
	for index, item := range ordered {
		if item.version != int64(index+1) {
			return nil, fmt.Errorf("realqa migrations: expected version %06d, found %06d",
				index+1, item.version)
		}
	}
	return ordered, nil
}
