// Package database owns RealQA's PostgreSQL pool and sqlc transaction boundary.
package database

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"

	"github.com/delinoio/oss/servers/devhud-realqa/db/migrations"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database/dbgen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool        *pgxpool.Pool
	queries     *dbgen.Queries
	identityKey []byte
}

func Open(ctx context.Context, databaseURL string, identityKey []byte) (*Store, error) {
	if len(identityKey) < 32 {
		return nil, errors.New("realqa database: identity hash key is required")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("realqa database: invalid connection configuration")
	}
	config.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("realqa database: pool initialization failed")
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("realqa database: connectivity check failed")
	}
	if err = migrations.Run(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{
		pool: pool, queries: dbgen.New(pool),
		identityKey: append([]byte(nil), identityKey...),
	}, nil
}

func (store *Store) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}

func (store *Store) Ping(ctx context.Context) error {
	if store == nil || store.queries == nil {
		return errors.New("realqa database: store unavailable")
	}
	if _, err := store.queries.Ping(ctx); err != nil {
		return errors.New("realqa database: readiness check failed")
	}
	return nil
}

func (store *Store) Queries() dbgen.Querier {
	if store == nil {
		return nil
	}
	return store.queries
}

func (store *Store) SubjectDigest(subject string) ([]byte, error) {
	if store == nil || len(store.identityKey) < 32 || subject == "" {
		return nil, errors.New("realqa database: subject unavailable")
	}
	hash := hmac.New(sha256.New, store.identityKey)
	_, _ = hash.Write([]byte(subject))
	return hash.Sum(nil), nil
}

func (store *Store) WithinTransaction(
	ctx context.Context,
	options pgx.TxOptions,
	callback func(*dbgen.Queries) error,
) error {
	if store == nil || store.pool == nil || callback == nil {
		return errors.New("realqa database: transaction unavailable")
	}
	transaction, err := store.pool.BeginTx(ctx, options)
	if err != nil {
		return errors.New("realqa database: transaction start failed")
	}
	defer func() { _ = transaction.Rollback(context.WithoutCancel(ctx)) }()
	if err = callback(store.queries.WithTx(transaction)); err != nil {
		return err
	}
	if err = transaction.Commit(ctx); err != nil {
		return errors.New("realqa database: transaction commit failed")
	}
	return nil
}
