// Package database owns delibase's PostgreSQL pool and transaction boundary.
package database

import (
	"context"
	"errors"

	"github.com/delinoio/oss/servers/delibase/db/migrations"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store exposes generated sqlc queries while retaining transaction ownership.
type Store struct {
	pool    *pgxpool.Pool
	queries *dbgen.Queries
}

// Open parses configuration, establishes PostgreSQL connectivity, and runs all
// pending migrations before returning a usable store.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("database: invalid connection configuration")
	}
	poolConfig.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, errors.New("database: connection pool initialization failed")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("database: connectivity check failed")
	}
	if err := migrations.Run(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool, queries: dbgen.New(pool)}, nil
}

// Ping checks readiness through generated sqlc access.
func (store *Store) Ping(ctx context.Context) error {
	if store == nil || store.queries == nil {
		return errors.New("database: store is unavailable")
	}
	if _, err := store.queries.Ping(ctx); err != nil {
		return errors.New("database: readiness check failed")
	}
	return nil
}

// Queries returns generated read-only access for service implementations.
// Mutations spanning more than one statement must use WithinTransaction.
func (store *Store) Queries() dbgen.Querier {
	if store == nil {
		return nil
	}
	return store.queries
}

// WithinTransaction commits only when callback returns nil. Any database or
// callback failure rolls back, making mutations fail closed.
func (store *Store) WithinTransaction(
	ctx context.Context,
	options pgx.TxOptions,
	callback func(*dbgen.Queries) error,
) error {
	if store == nil || store.pool == nil {
		return errors.New("database: store is unavailable")
	}
	if callback == nil {
		return errors.New("database: transaction callback is required")
	}
	transaction, err := store.pool.BeginTx(ctx, options)
	if err != nil {
		return errors.New("database: transaction could not start")
	}
	defer func() { _ = transaction.Rollback(context.WithoutCancel(ctx)) }()
	if err := callback(store.queries.WithTx(transaction)); err != nil {
		return err
	}
	if err := transaction.Commit(ctx); err != nil {
		return errors.New("database: transaction commit failed")
	}
	return nil
}

// Close releases all pooled connections.
func (store *Store) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}
