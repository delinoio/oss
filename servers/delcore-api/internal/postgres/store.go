// SPDX-License-Identifier: Apache-2.0
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/delinoio/oss/servers/delcore-api/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ Pool *pgxpool.Pool }

func Open(ctx context.Context, url string) (*Store, error) {
	p, e := pgxpool.New(ctx, url)
	if e != nil {
		return nil, e
	}
	if e = p.Ping(ctx); e != nil {
		p.Close()
		return nil, e
	}
	return &Store{p}, nil
}
func (s *Store) CheckSchema(ctx context.Context) error {
	var version int
	err := s.Pool.QueryRow(ctx, "SELECT COALESCE(max(version),0) FROM delcore_schema").Scan(&version)
	if err != nil {
		return fmt.Errorf("schema unavailable: run migrate: %w", err)
	}
	if version != migrations.Version {
		return fmt.Errorf("incompatible schema version %d; expected %d", version, migrations.Version)
	}
	return nil
}
func (s *Store) Migrate(ctx context.Context) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(41820001)"); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS delcore_schema(version integer PRIMARY KEY, digest text NOT NULL, applied_at timestamptz NOT NULL)"); e != nil {
		return e
	}
	data, e := migrations.Files.ReadFile("00001_foundation.sql")
	if e != nil {
		return e
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	var version int
	if e = tx.QueryRow(ctx, "SELECT COALESCE(max(version),0) FROM delcore_schema").Scan(&version); e != nil {
		return e
	}
	if version > migrations.Version {
		return errors.New("database schema is newer than this binary")
	}
	if version == 0 {
		if _, e = tx.Exec(ctx, string(data)); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, "INSERT INTO delcore_schema VALUES(1,$1,$2)", digest, time.Now().UTC()); e != nil {
			return e
		}
	} else {
		var old string
		if e = tx.QueryRow(ctx, "SELECT digest FROM delcore_schema WHERE version=1").Scan(&old); e != nil {
			return e
		}
		if old != digest {
			return errors.New("migration checksum mismatch")
		}
	}
	return tx.Commit(ctx)
}

// AcquireInstance owns a dedicated session for the lifetime of the process.
// A lost session must cancel the server before another instance can safely work.
func (s *Store) AcquireInstance(ctx context.Context) (*pgxpool.Conn, error) {
	c, e := s.Pool.Acquire(ctx)
	if e != nil {
		return nil, e
	}
	var ok bool
	if e = c.QueryRow(ctx, "SELECT pg_try_advisory_lock(41820002)").Scan(&ok); e != nil || !ok {
		c.Release()
		return nil, errors.New("delcore instance lock unavailable")
	}
	return c, nil
}
func (s *Store) Transaction(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, e := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	// Single-instance v1 serializes state transitions. External requests never run
	// under this lock. Remove only with per-aggregate locking and concurrency tests.
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(41820003)"); e != nil {
		return e
	}
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
