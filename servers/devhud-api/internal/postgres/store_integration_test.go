//go:build integration

package postgres

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
	"github.com/delinoio/oss/servers/devhud-api/internal/idgen"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFoundationTransactionsAndRetention(t *testing.T) {
	databaseURL := os.Getenv("DEVHUD_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DEVHUD_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := NewPool(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dropFoundation(t, ctx, pool)
	defer dropFoundation(t, ctx, pool)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	clock := &mutableClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	store := New(pool, idgen.UUIDv7{}, clock)
	if current, err := store.SchemaCurrent(ctx); err != nil || !current {
		t.Fatalf("schema current = %v, err=%v", current, err)
	}

	identity := domain.Identity{
		Issuer: "https://issuer.example", Subject: "subject", DisplayName: "User", Email: "user@example.com",
		Fingerprint: []byte("01234567890123456789012345678901"),
	}
	identity.FingerprintCandidates = [][]byte{identity.Fingerprint}
	user, err := store.ProvisionUser(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot, err := store.GetSettings(ctx, user.ID); err != nil || snapshot != nil {
		t.Fatalf("initial settings = %+v, err=%v", snapshot, err)
	}
	created, err := store.ReplaceSettings(ctx, user.ID, 1, []byte(`{"theme":"system"}`), 0, clock.Now())
	if err != nil || created.Revision != 1 {
		t.Fatalf("create settings = %+v, err=%v", created, err)
	}

	var successes, conflicts int
	var mutex sync.Mutex
	var wait sync.WaitGroup
	for _, body := range [][]byte{[]byte(`{"theme":"dark"}`), []byte(`{"theme":"light"}`)} {
		wait.Add(1)
		go func(body []byte) {
			defer wait.Done()
			_, replaceErr := store.ReplaceSettings(ctx, user.ID, 1, body, 1, clock.Now())
			mutex.Lock()
			defer mutex.Unlock()
			var conflict *domain.RevisionConflict
			if replaceErr == nil {
				successes++
			} else if errors.As(replaceErr, &conflict) {
				conflicts++
			} else {
				t.Errorf("unexpected replace error: %v", replaceErr)
			}
		}(body)
	}
	wait.Wait()
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}

	deleted, err := store.DeleteAccount(ctx, user.ID, clock.Now())
	if err != nil || deleted.RecoverableUntil == nil {
		t.Fatalf("delete account = %+v, err=%v", deleted, err)
	}
	deletedAgain, err := store.DeleteAccount(ctx, user.ID, clock.Now().Add(time.Hour))
	if err != nil || !deletedAgain.RecoverableUntil.Equal(*deleted.RecoverableUntil) {
		t.Fatalf("idempotent delete changed recovery: %+v, err=%v", deletedAgain, err)
	}
	if _, err := store.ReplaceSettings(ctx, user.ID, 1, []byte(`{}`), 2, clock.Now()); err == nil {
		t.Fatal("settings replacement succeeded for deletion-pending account")
	}
	if _, err := pool.Exec(ctx, "UPDATE devhud_users SET administrative_block_state = 2 WHERE user_id = $1", user.ID); err != nil {
		t.Fatal(err)
	}
	restored, err := store.RestoreAccount(ctx, user.ID, clock.Now().Add(24*time.Hour))
	if err != nil || restored.DeletionState != domain.DeletionStateActive || restored.AdministrativeBlockState != domain.AdministrativeBlockStateBlocked {
		t.Fatalf("restore account = %+v, err=%v", restored, err)
	}
	if snapshot, err := store.GetSettings(ctx, user.ID); err != nil || snapshot == nil || snapshot.Revision != 2 {
		t.Fatalf("account backup/restore lost settings: %+v, err=%v", snapshot, err)
	}

	clock.Set(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	if _, err := store.DeleteAccount(ctx, user.ID, clock.Now()); err != nil {
		t.Fatal(err)
	}
	boundary := clock.Now().Add(domain.RecoveryWindow)
	if accounts, err := store.ClaimPurgeBatch(ctx, boundary.Add(-time.Nanosecond), 10); err != nil || len(accounts) != 0 {
		t.Fatalf("purge claimed before boundary: %v, err=%v", accounts, err)
	}
	if _, err := store.RestoreAccount(ctx, user.ID, boundary); err == nil {
		t.Fatal("restore succeeded at expired boundary")
	}
	accounts, err := store.ClaimPurgeBatch(ctx, boundary, 10)
	if err != nil || len(accounts) != 1 || accounts[0].DeletionState != domain.DeletionStatePurgeClaimed {
		t.Fatalf("purge claim = %+v, err=%v", accounts, err)
	}
	if _, err := store.RestoreAccount(ctx, user.ID, boundary); err == nil {
		t.Fatal("restore succeeded after purge claim")
	}
	if err := store.CompleteAccountPurge(ctx, accounts[0], boundary); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteAccountPurge(ctx, accounts[0], boundary); err != nil {
		t.Fatalf("idempotent purge retry failed: %v", err)
	}
	if _, err := store.ProvisionUser(ctx, identity); !errors.Is(err, domain.ErrIdentityPurged) {
		t.Fatalf("purged identity provision error = %v", err)
	}

	oldID, _ := idgen.UUIDv7{}.New()
	oldCorrelation, _ := idgen.UUIDv7{}.New()
	newID, _ := idgen.UUIDv7{}.New()
	newCorrelation, _ := idgen.UUIDv7{}.New()
	if err := store.RecordRequest(ctx, domain.RequestLog{ID: oldID, CorrelationID: oldCorrelation, Procedure: "/healthz", HTTPStatus: 200, CreatedAt: boundary.Add(-domain.RequestLogRetention), ExpiresAt: boundary}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRequest(ctx, domain.RequestLog{ID: newID, CorrelationID: newCorrelation, Procedure: "/healthz", HTTPStatus: 200, CreatedAt: boundary, ExpiresAt: boundary.Add(domain.RequestLogRetention)}); err != nil {
		t.Fatal(err)
	}
	oldAuditID, _ := idgen.UUIDv7{}.New()
	newAuditID, _ := idgen.UUIDv7{}.New()
	if err := store.RecordAudit(ctx, domain.AuditEvent{ID: oldAuditID, Action: domain.AuditActionAccountPurged, CreatedAt: boundary.Add(-domain.AuditRetention), ExpiresAt: boundary}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAudit(ctx, domain.AuditEvent{ID: newAuditID, Action: domain.AuditActionAccountPurged, CreatedAt: boundary, ExpiresAt: boundary.Add(domain.AuditRetention)}); err != nil {
		t.Fatal(err)
	}
	retention, err := store.PruneRetention(ctx, boundary)
	if err != nil || retention.RequestLogsDeleted != 1 || retention.AuditEventsDeleted != 1 {
		t.Fatalf("retention result = %+v, err=%v", retention, err)
	}

	unlock, acquired, err := store.TryLock(ctx)
	if err != nil || !acquired {
		t.Fatalf("first lock acquired=%v err=%v", acquired, err)
	}
	_, secondAcquired, err := store.TryLock(ctx)
	if err != nil || secondAcquired {
		t.Fatalf("second lock acquired=%v err=%v", secondAcquired, err)
	}
	if err := unlock(ctx); err != nil {
		t.Fatal(err)
	}
}

type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *mutableClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *mutableClock) Set(now time.Time) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = now
}

func dropFoundation(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	// This helper is intentionally scoped to the explicitly configured disposable test database.
	_, err := pool.Exec(ctx, `DROP TABLE IF EXISTS devhud_audit_events, devhud_request_logs,
        devhud_settings, devhud_purged_identities, devhud_users, devhud_schema_migrations CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
}
