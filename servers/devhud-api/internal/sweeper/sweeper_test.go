package sweeper

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
)

func TestRunOnceIsBoundedCoordinatedAndRetryable(t *testing.T) {
	repository := &fakeRepository{accounts: []domain.User{{ID: "one"}, {ID: "two"}}, retention: domain.RetentionResult{RequestLogsDeleted: 3, AuditEventsDeleted: 4}}
	coordinator := &fakeCoordinator{acquired: true}
	purger := &failingOncePurger{}
	worker, err := New(repository, coordinator, []domain.AccountPurgeAdapter{purger}, fixedClock{}, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), 2)
	if err != nil {
		t.Fatal(err)
	}
	result, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.AccountsClaimed != 2 || result.AccountsPurged != 1 || result.RequestLogsDeleted != 3 || result.AuditEventsDeleted != 4 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if repository.limit != 2 || repository.retentionLimit != 2 || coordinator.unlocks != 1 {
		t.Fatalf("coordination/bound mismatch: account_limit=%d retention_limit=%d unlocks=%d", repository.limit, repository.retentionLimit, coordinator.unlocks)
	}

	result, err = worker.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.AccountsPurged != 1 || len(repository.accounts) != 0 {
		t.Fatalf("retry did not complete the remaining idempotent purge: %+v", result)
	}
}

func TestLockContentionSkipsWork(t *testing.T) {
	repository := &fakeRepository{}
	worker, err := New(repository, &fakeCoordinator{}, nil, fixedClock{}, slog.Default(), 1)
	if err != nil {
		t.Fatal(err)
	}
	result, err := worker.RunOnce(context.Background())
	if err != nil || result.LockAcquired || repository.claims != 0 {
		t.Fatalf("unexpected contention result: %+v, err=%v", result, err)
	}
}

func TestRunOnceBoundsUnlockAfterCallerCancellation(t *testing.T) {
	var unlockErr error
	var unlockDeadline time.Time
	coordinator := &fakeCoordinator{
		acquired: true,
		unlock: func(ctx context.Context) error {
			unlockErr = ctx.Err()
			unlockDeadline, _ = ctx.Deadline()
			return nil
		},
	}
	worker, err := New(&fakeRepository{}, coordinator, nil, fixedClock{}, slog.Default(), 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if unlockErr != nil {
		t.Fatalf("unlock context was already canceled: %v", unlockErr)
	}
	remaining := time.Until(unlockDeadline)
	if unlockDeadline.IsZero() || remaining <= 0 || remaining > advisoryUnlockTimeout {
		t.Fatalf("unlock deadline remaining = %v", remaining)
	}
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC) }

type fakeCoordinator struct {
	acquired bool
	unlocks  int
	unlock   func(context.Context) error
}

func (coordinator *fakeCoordinator) TryLock(context.Context) (func(context.Context) error, bool, error) {
	return func(ctx context.Context) error {
		coordinator.unlocks++
		if coordinator.unlock != nil {
			return coordinator.unlock(ctx)
		}
		return nil
	}, coordinator.acquired, nil
}

type failingOncePurger struct {
	mu     sync.Mutex
	failed bool
}

func (purger *failingOncePurger) PurgeAccount(_ context.Context, _ domain.User) error {
	purger.mu.Lock()
	defer purger.mu.Unlock()
	if !purger.failed {
		purger.failed = true
		return errors.New("temporary failure")
	}
	return nil
}

type fakeRepository struct {
	accounts       []domain.User
	retention      domain.RetentionResult
	limit          int
	retentionLimit int
	claims         int
}

func (*fakeRepository) SchemaCurrent(context.Context) (bool, error) { return true, nil }
func (*fakeRepository) Ping(context.Context) error                  { return nil }
func (*fakeRepository) ProvisionUser(context.Context, domain.Identity) (domain.User, error) {
	return domain.User{}, nil
}
func (*fakeRepository) GetSettings(context.Context, string) (*domain.Settings, error) {
	return nil, nil
}
func (*fakeRepository) ReplaceSettings(context.Context, string, uint32, []byte, uint64, time.Time) (domain.Settings, error) {
	return domain.Settings{}, nil
}
func (*fakeRepository) GetAccount(context.Context, string) (domain.User, error) {
	return domain.User{}, nil
}
func (*fakeRepository) DeleteAccount(context.Context, string, time.Time) (domain.User, error) {
	return domain.User{}, nil
}
func (*fakeRepository) RestoreAccount(context.Context, string, time.Time) (domain.User, error) {
	return domain.User{}, nil
}
func (*fakeRepository) RecordRequest(context.Context, domain.RequestLog) error { return nil }
func (*fakeRepository) RecordAudit(context.Context, domain.AuditEvent) error   { return nil }
func (repository *fakeRepository) ClaimPurgeBatch(_ context.Context, _ time.Time, limit int) ([]domain.User, error) {
	repository.limit = limit
	repository.claims++
	return append([]domain.User(nil), repository.accounts...), nil
}
func (repository *fakeRepository) CompleteAccountPurge(_ context.Context, user domain.User, _ time.Time) error {
	for index, account := range repository.accounts {
		if account.ID == user.ID {
			repository.accounts = append(repository.accounts[:index], repository.accounts[index+1:]...)
			break
		}
	}
	return nil
}
func (repository *fakeRepository) PruneRetention(_ context.Context, _ time.Time, limit int) (domain.RetentionResult, error) {
	repository.retentionLimit = limit
	return repository.retention, nil
}
