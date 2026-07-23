// Package database owns delibase's PostgreSQL pool and transaction boundary.
package database

import (
	"context"
	"errors"

	"github.com/delinoio/oss/servers/delibase/db/migrations"
	"github.com/delinoio/oss/servers/delibase/internal/catalog"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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

// SyncCatalog applies a validated checked-in catalog as one transaction.
// Entries omitted from the current file are disabled, while immutable price
// versions remain retained for reservations and historical usage.
func (store *Store) SyncCatalog(
	ctx context.Context,
	specification catalog.Specification,
) error {
	err := store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		if err := queries.DisableServiceMeterAllowlists(ctx); err != nil {
			return err
		}
		if err := queries.DeleteUnusedPolarMeterMappings(ctx); err != nil {
			return err
		}
		if err := queries.DisableCatalogMeters(ctx); err != nil {
			return err
		}
		if err := queries.DisableCatalogApps(ctx); err != nil {
			return err
		}
		if err := queries.DisableServiceIdentities(ctx); err != nil {
			return err
		}
		for _, app := range specification.Apps {
			id, err := catalogUUID(app.ID)
			if err != nil {
				return err
			}
			if err := queries.UpsertCatalogApp(ctx, dbgen.UpsertCatalogAppParams{
				ID:          id,
				Slug:        app.Slug,
				Name:        app.Name,
				Summary:     app.Summary,
				Description: app.Description,
				IconUrl:     app.IconURL,
				Enabled:     *app.Enabled,
			}); err != nil {
				return err
			}
		}
		for _, meter := range specification.Meters {
			id, err := catalogUUID(meter.ID)
			if err != nil {
				return err
			}
			appID, err := catalogUUID(meter.AppID)
			if err != nil {
				return err
			}
			if err := queries.UpsertCatalogMeter(ctx, dbgen.UpsertCatalogMeterParams{
				ID:                    id,
				AppID:                 appID,
				MeterKey:              meter.Key,
				Name:                  meter.Name,
				Description:           meter.Description,
				UnitName:              meter.UnitName,
				UnitPrecision:         int32(*meter.UnitPrecision),
				ReservationTtlSeconds: meter.ReservationTTLSeconds,
				Enabled:               *meter.Enabled,
			}); err != nil {
				return err
			}
		}
		for _, price := range specification.Prices {
			if price.EffectiveUntil == nil {
				continue
			}
			id, err := catalogUUID(price.ID)
			if err != nil {
				return err
			}
			if err := queries.CloseCatalogPriceVersion(
				ctx,
				dbgen.CloseCatalogPriceVersionParams{
					ID: id,
					EffectiveUntil: pgtype.Timestamptz{
						Time:  *price.EffectiveUntil,
						Valid: true,
					},
				},
			); err != nil {
				return err
			}
		}
		for _, price := range specification.Prices {
			id, err := catalogUUID(price.ID)
			if err != nil {
				return err
			}
			meterID, err := catalogUUID(price.MeterID)
			if err != nil {
				return err
			}
			effectiveUntil := pgtype.Timestamptz{}
			if price.EffectiveUntil != nil {
				effectiveUntil = pgtype.Timestamptz{
					Time:  *price.EffectiveUntil,
					Valid: true,
				}
			}
			affected, err := queries.EnsureCatalogPriceVersion(
				ctx,
				dbgen.EnsureCatalogPriceVersionParams{
					ID:               id,
					MeterID:          meterID,
					UsdMicrosPerUnit: price.USDMicrosPerUnit,
					EffectiveFrom: pgtype.Timestamptz{
						Time:  price.EffectiveFrom,
						Valid: true,
					},
					EffectiveUntil: effectiveUntil,
				},
			)
			if err != nil {
				return err
			}
			if affected != 1 {
				return errors.New("database: catalog price version conflict")
			}
		}
		for _, service := range specification.Services {
			id, err := catalogUUID(service.ID)
			if err != nil {
				return err
			}
			if err := queries.UpsertServiceIdentity(ctx, dbgen.UpsertServiceIdentityParams{
				ID:            id,
				LogtoClientID: service.LogtoClientID,
				Name:          service.Name,
				Enabled:       *service.Enabled,
			}); err != nil {
				return err
			}
			for _, allowedMeterID := range service.AllowedMeterIDs {
				meterID, err := catalogUUID(allowedMeterID)
				if err != nil {
					return err
				}
				if err := queries.UpsertServiceMeterAllowlist(
					ctx,
					dbgen.UpsertServiceMeterAllowlistParams{
						ServiceIdentityID: id,
						MeterID:           meterID,
					},
				); err != nil {
					return err
				}
			}
		}
		if err := queries.DeleteDisabledServiceMeterAllowlists(ctx); err != nil {
			return err
		}
		for _, mapping := range specification.PolarMeters {
			meterID, err := catalogUUID(mapping.MeterID)
			if err != nil {
				return err
			}
			affected, err := queries.EnsurePolarMeterMapping(
				ctx,
				dbgen.EnsurePolarMeterMappingParams{
					MeterID:      meterID,
					PolarMeterID: mapping.PolarMeterID,
				},
			)
			if err != nil {
				return err
			}
			if affected != 1 {
				return errors.New("database: active Polar meter mapping conflict")
			}
		}
		return nil
	})
	if err != nil {
		return errors.New("database: catalog synchronization failed")
	}
	return nil
}

func catalogUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, errors.New("database: invalid catalog identifier")
	}
	return pgtype.UUID{Bytes: [16]byte(parsed), Valid: true}, nil
}

// Close releases all pooled connections.
func (store *Store) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}
