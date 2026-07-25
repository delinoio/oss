package service

import (
	"context"
	"errors"
	"time"

	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const defaultUsageExpirationInterval = time.Second

// UsageExpirationWorker returns catalog-TTL holds without depending on Polar.
// Every batch uses the same organization-first lock order as usage mutations
// and team deletion.
type UsageExpirationWorker struct {
	dependencies Dependencies
	interval     time.Duration
}

func NewUsageExpirationWorker(
	dependencies Dependencies,
	interval time.Duration,
) (*UsageExpirationWorker, error) {
	dependencies = dependencies.withDefaults()
	if dependencies.Store == nil {
		return nil, errors.New("usage expiration: database store is required")
	}
	if interval <= 0 {
		interval = defaultUsageExpirationInterval
	}
	return &UsageExpirationWorker{
		dependencies: dependencies,
		interval:     interval,
	}, nil
}

func (worker *UsageExpirationWorker) Run(ctx context.Context) error {
	if worker == nil {
		return errors.New("usage expiration: worker is required")
	}
	ticker := time.NewTicker(worker.interval)
	defer ticker.Stop()
	for {
		if _, err := worker.ProcessBatch(ctx); err != nil &&
			ctx.Err() == nil {
			worker.dependencies.Logger.Error(
				"usage expiration batch failed",
				"event", "usage_expiration_failure",
			)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (worker *UsageExpirationWorker) ProcessBatch(
	ctx context.Context,
) (int, error) {
	if worker == nil || worker.dependencies.Store == nil {
		return 0, errors.New("usage expiration: worker is unavailable")
	}
	candidates, err := worker.dependencies.Store.Queries().
		ListExpiredUsageReservationCandidates(ctx, usageExpirationBatchSize)
	if err != nil {
		return 0, err
	}
	processed := 0
	seenOrganizations := make(map[uuid.UUID]struct{}, len(candidates))
	for _, candidate := range candidates {
		organizationID := uuid.UUID(candidate.OrganizationID.Bytes)
		if _, seen := seenOrganizations[organizationID]; seen {
			continue
		}
		seenOrganizations[organizationID] = struct{}{}
		err = worker.dependencies.Store.WithinTransaction(
			ctx,
			pgx.TxOptions{},
			func(queries *dbgen.Queries) error {
				if _, lockErr := queries.LockOrganizationForBilling(
					ctx, candidate.OrganizationID,
				); lockErr != nil {
					if errors.Is(lockErr, pgx.ErrNoRows) {
						return nil
					}
					return lockErr
				}
				count, expirationErr := expireOrganizationReservations(
					ctx, worker.dependencies, queries, organizationID,
				)
				processed += count
				return expirationErr
			},
		)
		if err != nil {
			return processed, err
		}
	}
	return processed, nil
}
