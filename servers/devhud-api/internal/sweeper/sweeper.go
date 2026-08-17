package sweeper

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
)

const MaximumBatchSize = 500
const advisoryUnlockTimeout = time.Second

type Sweeper struct {
	repository  domain.Repository
	coordinator domain.SweepCoordinator
	purgers     []domain.AccountPurgeAdapter
	clock       domain.Clock
	logger      *slog.Logger
	batchSize   int
	staging     domain.UploadStagingSweeper
}

type Result struct {
	LockAcquired          bool
	AccountsClaimed       int
	AccountsPurged        int
	RequestLogsDeleted    int64
	AuditEventsDeleted    int64
	StagingObjectsDeleted int
}

type Option func(*Sweeper)

func WithUploadStaging(staging domain.UploadStagingSweeper) Option {
	return func(sweeper *Sweeper) { sweeper.staging = staging }
}

func New(repository domain.Repository, coordinator domain.SweepCoordinator, purgers []domain.AccountPurgeAdapter, clock domain.Clock, logger *slog.Logger, batchSize int, options ...Option) (*Sweeper, error) {
	if batchSize < 1 || batchSize > MaximumBatchSize {
		return nil, fmt.Errorf("sweeper batch size must be between 1 and %d", MaximumBatchSize)
	}
	worker := &Sweeper{repository: repository, coordinator: coordinator, purgers: purgers, clock: clock, logger: logger, batchSize: batchSize}
	for _, option := range options {
		option(worker)
	}
	return worker, nil
}

func (s *Sweeper) RunOnce(ctx context.Context) (result Result, returnErr error) {
	unlock, acquired, err := s.coordinator.TryLock(ctx)
	if err != nil {
		return result, fmt.Errorf("acquire sweep lock: %w", err)
	}
	if !acquired {
		return result, nil
	}
	result.LockAcquired = true
	defer func() {
		unlockContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), advisoryUnlockTimeout)
		defer cancel()
		if err := unlock(unlockContext); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("release sweep lock: %w", err)
		}
	}()

	now := s.clock.Now()
	if s.staging != nil {
		for {
			deleted, err := s.staging.SweepExpiredUploads(ctx, now, s.batchSize)
			if err != nil {
				return result, fmt.Errorf("sweep expired staging: %w", err)
			}
			result.StagingObjectsDeleted += deleted
			if deleted < s.batchSize {
				break
			}
		}
	}
	accounts, err := s.repository.ClaimPurgeBatch(ctx, now, s.batchSize)
	if err != nil {
		return result, fmt.Errorf("claim purge batch: %w", err)
	}
	result.AccountsClaimed = len(accounts)
	for _, account := range accounts {
		failed := false
		for _, purger := range s.purgers {
			if err := purger.PurgeAccount(ctx, account); err != nil {
				s.logger.WarnContext(ctx, "account purge adapter failed", "error", err)
				failed = true
				break
			}
		}
		if failed {
			continue
		}
		if err := s.repository.CompleteAccountPurge(ctx, account, now); err != nil {
			s.logger.WarnContext(ctx, "account purge finalization failed", "error", err)
			continue
		}
		result.AccountsPurged++
	}
	for {
		retention, err := s.repository.PruneRetention(ctx, now, s.batchSize)
		if err != nil {
			return result, fmt.Errorf("prune retention: %w", err)
		}
		result.RequestLogsDeleted += retention.RequestLogsDeleted
		result.AuditEventsDeleted += retention.AuditEventsDeleted
		if retention.RequestLogsDeleted < int64(s.batchSize) && retention.AuditEventsDeleted < int64(s.batchSize) {
			break
		}
	}
	return result, nil
}
