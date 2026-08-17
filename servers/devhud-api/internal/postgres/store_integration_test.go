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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSchemaCurrentUsesConfiguredSearchPath(t *testing.T) {
	databaseURL := os.Getenv("DEVHUD_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DEVHUD_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	adminPool, err := NewPool(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminPool.Close)
	dropFoundation(t, ctx, adminPool)

	const schemaName = "devhud_search_path_test"
	quotedSchema := pgx.Identifier{schemaName}.Sanitize()
	if _, err := adminPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE"); err != nil {
		t.Fatal(err)
	}
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal(err)
	}

	configuration, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	configuration.ConnConfig.RuntimeParams["search_path"] = schemaName
	pool, err := pgxpool.NewWithConfig(ctx, configuration)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, cleanupErr := adminPool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE"); cleanupErr != nil {
			t.Errorf("drop search-path test schema: %v", cleanupErr)
		}
	})
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := New(pool, idgen.UUIDv7{}, domain.RealClock{})
	if current, err := store.SchemaCurrent(ctx); err != nil || !current {
		t.Fatalf("schema current = %v, err=%v", current, err)
	}
}

func TestMigrationLockCleanupUsesBoundedIndependentContext(t *testing.T) {
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

	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationAdvisoryLock); err != nil {
		connection.Release()
		t.Fatal(err)
	}
	canceledContext, cancel := context.WithCancel(ctx)
	cancel()
	if err := releaseMigrationLock(canceledContext, connection); err != nil {
		t.Fatal(err)
	}
	if acquired := pool.Stat().AcquiredConns(); acquired != 0 {
		t.Fatalf("acquired connections after unlock = %d, want 0", acquired)
	}
}

func TestMigrationLockCleanupDiscardsSessionWhenLockWasNotHeld(t *testing.T) {
	databaseURL := os.Getenv("DEVHUD_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DEVHUD_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	configuration, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	configuration.MaxConns = 1
	configuration.MinConns = 0
	pool, err := pgxpool.NewWithConfig(ctx, configuration)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	discardedPID := connection.Conn().PgConn().PID()
	if err := releaseMigrationLock(ctx, connection); err == nil {
		t.Fatal("cleanup succeeded without a held migration lock")
	}
	replacement, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	replacementPID := replacement.Conn().PgConn().PID()
	replacement.Release()
	if replacementPID == discardedPID {
		t.Fatalf("failed-unlock session was reused: discarded PID=%d replacement PID=%d", discardedPID, replacementPID)
	}
}

func TestMigrationLockAcquisitionErrorDiscardsSession(t *testing.T) {
	databaseURL := os.Getenv("DEVHUD_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DEVHUD_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	configuration, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	configuration.MaxConns = 1
	configuration.MinConns = 0
	cancelAcquisition := func() {}
	var failedSessionPID uint32
	cancelOnAcquire := false
	configuration.BeforeAcquire = func(_ context.Context, connection *pgx.Conn) bool {
		if cancelOnAcquire {
			cancelOnAcquire = false
			failedSessionPID = connection.PgConn().PID()
			cancelAcquisition()
		}
		return true
	}
	pool, err := pgxpool.NewWithConfig(ctx, configuration)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	acquisitionContext, cancel := context.WithCancel(ctx)
	cancelAcquisition = cancel
	cancelOnAcquire = true
	if err := Migrate(acquisitionContext, pool); !errors.Is(err, context.Canceled) {
		t.Fatalf("migration lock acquisition error = %v, want context cancellation", err)
	}
	cancel()

	replacement, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	replacementPID := replacement.Conn().PgConn().PID()
	replacement.Release()
	if failedSessionPID == 0 || replacementPID == failedSessionPID {
		t.Fatalf("migration acquisition-error session was reused: failed PID=%d replacement PID=%d", failedSessionPID, replacementPID)
	}
}

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
	expectedVersions, err := expectedMigrationVersions()
	if err != nil || len(expectedVersions) == 0 {
		t.Fatalf("expected migration versions = %v, err=%v", expectedVersions, err)
	}
	missingVersion := expectedVersions[0]
	if _, err := pool.Exec(ctx, "DELETE FROM devhud_schema_migrations WHERE version = $1", missingVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO devhud_schema_migrations (version) VALUES ('99999_unknown.sql')"); err != nil {
		t.Fatal(err)
	}
	if current, err := store.SchemaCurrent(ctx); err != nil || current {
		t.Fatalf("schema with equal-count ledger drift current = %v, err=%v", current, err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM devhud_schema_migrations WHERE version = '99999_unknown.sql'"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO devhud_schema_migrations (version) VALUES ($1)", missingVersion); err != nil {
		t.Fatal(err)
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
	if _, err := store.RestoreAccount(ctx, user.ID, clock.Now()); !accountFailureIs(err, domain.AccountFailureRecoveryExpired) {
		t.Fatalf("never-deleted account restore error = %v", err)
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
	if retried, err := store.RestoreAccount(ctx, user.ID, clock.Now().Add(48*time.Hour)); err != nil || retried.ID != user.ID {
		t.Fatalf("in-window restore retry = %+v, err=%v", retried, err)
	}
	if _, err := store.RestoreAccount(ctx, user.ID, *deleted.RecoverableUntil); !accountFailureIs(err, domain.AccountFailureRecoveryExpired) {
		t.Fatalf("expired active-account restore error = %v", err)
	}
	var blocked *domain.PermissionError
	if _, err := store.GetSettings(ctx, user.ID); !errors.As(err, &blocked) || blocked.Failure != domain.PermissionFailureAdministrativeBlock {
		t.Fatalf("settings read error = %v, want administrative-block permission failure", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE devhud_users SET administrative_block_state = 1 WHERE user_id = $1", user.ID); err != nil {
		t.Fatal(err)
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
	blocker, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	identityLockKey := identityAdvisoryLockKey(identity.Issuer, identity.Subject)
	if _, err := blocker.Exec(ctx, "SELECT pg_advisory_lock($1)", identityLockKey); err != nil {
		blocker.Release()
		t.Fatal(err)
	}
	lockHeld := true
	defer func() {
		if lockHeld {
			_, _ = blocker.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", identityLockKey)
		}
		blocker.Release()
	}()
	for name, operation := range map[string]func(context.Context) error{
		"purge": func(operationContext context.Context) error {
			return store.CompleteAccountPurge(operationContext, accounts[0], boundary)
		},
		"provision": func(operationContext context.Context) error {
			_, operationErr := store.ProvisionUser(operationContext, identity)
			return operationErr
		},
	} {
		operationContext, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		operationErr := operation(operationContext)
		cancel()
		if !errors.Is(operationErr, context.DeadlineExceeded) {
			t.Fatalf("%s did not wait for the shared identity lock: %v", name, operationErr)
		}
	}
	var unlocked bool
	if err := blocker.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", identityLockKey).Scan(&unlocked); err != nil || !unlocked {
		t.Fatalf("unlock identity: unlocked=%v err=%v", unlocked, err)
	}
	lockHeld = false
	if err := store.CompleteAccountPurge(ctx, accounts[0], boundary); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteAccountPurge(ctx, accounts[0], boundary); err != nil {
		t.Fatalf("idempotent purge retry failed: %v", err)
	}
	if _, err := store.RestoreAccount(ctx, user.ID, boundary); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("restore after purge error = %v, want ErrNotFound", err)
	}
	if _, err := store.DeleteAccount(ctx, user.ID, boundary); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("delete after purge error = %v, want ErrNotFound", err)
	}
	rotatedIdentity := identity
	rotatedIdentity.Fingerprint = []byte("abcdefghijklmnopqrstuvwxyz123456")
	rotatedIdentity.FingerprintCandidates = [][]byte{rotatedIdentity.Fingerprint}
	if _, err := store.ProvisionUser(ctx, rotatedIdentity); !errors.Is(err, domain.ErrIdentityPurged) {
		t.Fatalf("purged identity provision error = %v", err)
	}

	oldID, _ := idgen.UUIDv7{}.New()
	oldCorrelation, _ := idgen.UUIDv7{}.New()
	newID, _ := idgen.UUIDv7{}.New()
	newCorrelation, _ := idgen.UUIDv7{}.New()
	if err := store.RecordRequest(ctx, domain.RequestLog{ID: oldID, CorrelationID: oldCorrelation, Procedure: "/devhud.v1.SettingsService/GetSettings", HTTPStatus: 200, RPCStatusCode: domain.RPCStatusCodeUnauthenticated, CreatedAt: boundary.Add(-domain.RequestLogRetention), ExpiresAt: boundary}); err != nil {
		t.Fatal(err)
	}
	var persistedRPCStatus string
	if err := pool.QueryRow(ctx, "SELECT COALESCE(rpc_status_code, '') FROM devhud_request_logs WHERE request_log_id = $1", oldID).Scan(&persistedRPCStatus); err != nil {
		t.Fatal(err)
	}
	if persistedRPCStatus != string(domain.RPCStatusCodeUnauthenticated) {
		t.Fatalf("persisted RPC status = %q", persistedRPCStatus)
	}
	secondOldID, _ := idgen.UUIDv7{}.New()
	secondOldCorrelation, _ := idgen.UUIDv7{}.New()
	if err := store.RecordRequest(ctx, domain.RequestLog{ID: secondOldID, CorrelationID: secondOldCorrelation, Procedure: "/readyz", HTTPStatus: 200, CreatedAt: boundary.Add(-domain.RequestLogRetention), ExpiresAt: boundary}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT COALESCE(rpc_status_code, '') FROM devhud_request_logs WHERE request_log_id = $1", secondOldID).Scan(&persistedRPCStatus); err != nil {
		t.Fatal(err)
	}
	if persistedRPCStatus != "" {
		t.Fatalf("non-RPC request persisted status %q", persistedRPCStatus)
	}
	if err := store.RecordRequest(ctx, domain.RequestLog{ID: newID, CorrelationID: newCorrelation, Procedure: "/healthz", HTTPStatus: 200, CreatedAt: boundary, ExpiresAt: boundary.Add(domain.RequestLogRetention)}); err != nil {
		t.Fatal(err)
	}
	oldAuditID, _ := idgen.UUIDv7{}.New()
	newAuditID, _ := idgen.UUIDv7{}.New()
	if err := store.RecordAudit(ctx, domain.AuditEvent{ID: oldAuditID, Action: domain.AuditActionAccountPurged, CreatedAt: boundary.Add(-domain.AuditRetention), ExpiresAt: boundary}); err != nil {
		t.Fatal(err)
	}
	secondOldAuditID, _ := idgen.UUIDv7{}.New()
	if err := store.RecordAudit(ctx, domain.AuditEvent{ID: secondOldAuditID, Action: domain.AuditActionAccountPurged, CreatedAt: boundary.Add(-domain.AuditRetention), ExpiresAt: boundary}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAudit(ctx, domain.AuditEvent{ID: newAuditID, Action: domain.AuditActionAccountPurged, CreatedAt: boundary, ExpiresAt: boundary.Add(domain.AuditRetention)}); err != nil {
		t.Fatal(err)
	}
	retention, err := store.PruneRetention(ctx, boundary, 1)
	if err != nil || retention.RequestLogsDeleted != 1 || retention.AuditEventsDeleted != 1 {
		t.Fatalf("retention result = %+v, err=%v", retention, err)
	}
	retention, err = store.PruneRetention(ctx, boundary, 1)
	if err != nil || retention.RequestLogsDeleted != 1 || retention.AuditEventsDeleted != 1 {
		t.Fatalf("second retention result = %+v, err=%v", retention, err)
	}
	retention, err = store.PruneRetention(ctx, boundary, 1)
	if err != nil || retention.RequestLogsDeleted != 0 || retention.AuditEventsDeleted != 0 {
		t.Fatalf("drained retention result = %+v, err=%v", retention, err)
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

	failedUnlock, acquired, err := store.TryLock(ctx)
	if err != nil || !acquired {
		t.Fatalf("failure-case lock acquired=%v err=%v", acquired, err)
	}
	canceledContext, cancel := context.WithCancel(ctx)
	cancel()
	if err := failedUnlock(canceledContext); !errors.Is(err, context.Canceled) {
		t.Fatalf("failed unlock error = %v, want context cancellation", err)
	}
	otherPool, err := NewPool(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer otherPool.Close()
	otherStore := New(otherPool, idgen.UUIDv7{}, clock)
	otherUnlock, acquired, err := otherStore.TryLock(ctx)
	if err != nil || !acquired {
		t.Fatalf("lock after discarded session acquired=%v err=%v", acquired, err)
	}
	if err := otherUnlock(ctx); err != nil {
		t.Fatal(err)
	}

	acquisitionConfiguration, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	acquisitionConfiguration.MaxConns = 1
	acquisitionConfiguration.MinConns = 0
	cancelAcquisition := func() {}
	var failedSessionPID uint32
	cancelOnAcquire := false
	acquisitionConfiguration.BeforeAcquire = func(_ context.Context, connection *pgx.Conn) bool {
		if cancelOnAcquire {
			cancelOnAcquire = false
			failedSessionPID = connection.PgConn().PID()
			cancelAcquisition()
		}
		return true
	}
	acquisitionPool, err := pgxpool.NewWithConfig(ctx, acquisitionConfiguration)
	if err != nil {
		t.Fatal(err)
	}
	defer acquisitionPool.Close()
	if err := acquisitionPool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	acquisitionContext, cancel := context.WithCancel(ctx)
	cancelAcquisition = cancel
	cancelOnAcquire = true
	acquisitionStore := New(acquisitionPool, idgen.UUIDv7{}, clock)
	if _, _, err := acquisitionStore.TryLock(acquisitionContext); !errors.Is(err, context.Canceled) {
		t.Fatalf("failed acquisition error = %v, want context cancellation", err)
	}
	cancel()
	replacement, err := acquisitionPool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	replacementPID := replacement.Conn().PgConn().PID()
	replacement.Release()
	if failedSessionPID == 0 || replacementPID == failedSessionPID {
		t.Fatalf("acquisition-error session was reused: failed PID=%d replacement PID=%d", failedSessionPID, replacementPID)
	}
}

func TestSettingsReadSerializesWithDeletionStateTransition(t *testing.T) {
	ctx, pool, store := newIntegrationStore(t, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	identity := domain.Identity{
		Issuer: "https://issuer.example", Subject: "settings-reader", Fingerprint: []byte("settings-reader-fingerprint-0000"),
	}
	identity.FingerprintCandidates = [][]byte{identity.Fingerprint}
	user, err := store.ProvisionUser(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceSettings(ctx, user.ID, 1, []byte(`{"theme":"dark"}`), 0, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	deletionRequestedAt := time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC)
	if _, err := tx.Exec(ctx, `UPDATE devhud_users SET deletion_state = 2, deletion_requested_at = $2,
        recoverable_until = $3, updated_at = $2 WHERE user_id = $1`, user.ID, deletionRequestedAt, deletionRequestedAt.Add(domain.RecoveryWindow)); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}

	readResult := make(chan error, 1)
	go func() {
		_, readErr := store.GetSettings(ctx, user.ID)
		readResult <- readErr
	}()
	select {
	case readErr := <-readResult:
		_ = tx.Rollback(ctx)
		t.Fatalf("settings read bypassed the deletion-state row lock: %v", readErr)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case readErr := <-readResult:
		var permission *domain.PermissionError
		if !errors.As(readErr, &permission) || permission.Failure != domain.PermissionFailureDeletionPending {
			t.Fatalf("settings read error = %v, want deletion-pending permission failure", readErr)
		}
	case <-time.After(time.Second):
		t.Fatal("settings read remained blocked after deletion committed")
	}
}

func TestGetSettingsNormalizesCompletedPurge(t *testing.T) {
	ctx, _, store := newIntegrationStore(t, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	identity := domain.Identity{
		Issuer: "https://issuer.example", Subject: "purged-settings-reader", Fingerprint: []byte("purged-settings-reader-fingerprt"),
	}
	identity.FingerprintCandidates = [][]byte{identity.Fingerprint}
	user, err := store.ProvisionUser(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	deletedAt := time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC)
	if _, err := store.DeleteAccount(ctx, user.ID, deletedAt); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimPurgeBatch(ctx, deletedAt.Add(domain.RecoveryWindow), 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("purge claim = %+v, err=%v", claimed, err)
	}
	if err := store.CompleteAccountPurge(ctx, claimed[0], deletedAt.Add(domain.RecoveryWindow)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSettings(ctx, user.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("settings after completed purge error = %v, want ErrNotFound", err)
	}
	if _, err := store.ReplaceSettings(ctx, user.ID, 1, []byte(`{}`), 0, deletedAt.Add(domain.RecoveryWindow)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("settings replacement after completed purge error = %v, want ErrNotFound", err)
	}
}

func TestPurgeClaimsPrioritizePendingAccountsAndAuditOnce(t *testing.T) {
	ctx, pool, store := newIntegrationStore(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	retryIdentity := domain.Identity{
		Issuer: "https://issuer.example", Subject: "retry", Fingerprint: []byte("retry-fingerprint-00000000000000"),
	}
	retryIdentity.FingerprintCandidates = [][]byte{retryIdentity.Fingerprint}
	retryUser, err := store.ProvisionUser(ctx, retryIdentity)
	if err != nil {
		t.Fatal(err)
	}
	retryDeletedAt := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if _, err := store.DeleteAccount(ctx, retryUser.ID, retryDeletedAt); err != nil {
		t.Fatal(err)
	}
	if accounts, err := store.ClaimPurgeBatch(ctx, retryDeletedAt.Add(domain.RecoveryWindow), 1); err != nil || len(accounts) != 1 || accounts[0].ID != retryUser.ID {
		t.Fatalf("initial retry claim = %+v, err=%v", accounts, err)
	}

	pendingIdentity := domain.Identity{
		Issuer: "https://issuer.example", Subject: "pending", Fingerprint: []byte("pending-fingerprint-000000000000"),
	}
	pendingIdentity.FingerprintCandidates = [][]byte{pendingIdentity.Fingerprint}
	pendingUser, err := store.ProvisionUser(ctx, pendingIdentity)
	if err != nil {
		t.Fatal(err)
	}
	pendingDeletedAt := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)
	if _, err := store.DeleteAccount(ctx, pendingUser.ID, pendingDeletedAt); err != nil {
		t.Fatal(err)
	}
	boundary := pendingDeletedAt.Add(domain.RecoveryWindow)
	accounts, err := store.ClaimPurgeBatch(ctx, boundary, 1)
	if err != nil || len(accounts) != 1 || accounts[0].ID != pendingUser.ID {
		t.Fatalf("pending account was starved by retry claim: accounts=%+v err=%v", accounts, err)
	}

	assertPurgeClaimAuditCount(t, ctx, pool, 2)
	if _, err := store.ClaimPurgeBatch(ctx, boundary.Add(time.Hour), 10); err != nil {
		t.Fatal(err)
	}
	assertPurgeClaimAuditCount(t, ctx, pool, 2)
}

func newIntegrationStore(t *testing.T, now time.Time) (context.Context, *pgxpool.Pool, *Store) {
	t.Helper()
	databaseURL := os.Getenv("DEVHUD_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DEVHUD_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := NewPool(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	dropFoundation(t, ctx, pool)
	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		dropFoundation(t, ctx, pool)
		pool.Close()
	})
	return ctx, pool, New(pool, idgen.UUIDv7{}, &mutableClock{now: now})
}

func assertPurgeClaimAuditCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM devhud_audit_events WHERE action = $1", domain.AuditActionAccountPurgeClaimed).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("purge-claim audit count = %d, want %d", count, want)
	}
}

func accountFailureIs(err error, failure domain.AccountFailure) bool {
	var state *domain.AccountStateError
	return errors.As(err, &state) && state.Failure == failure
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
	_, err := pool.Exec(ctx, `DROP TABLE IF EXISTS devhud_audit_events, devhud_uploads,
		devhud_upload_reservations, devhud_upload_groups, devhud_submissions,
		devhud_request_logs, devhud_settings, devhud_purged_identities,
		devhud_users, devhud_schema_migrations CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DROP SEQUENCE IF EXISTS devhud_upload_generation_seq CASCADE`); err != nil {
		t.Fatal(err)
	}
}
