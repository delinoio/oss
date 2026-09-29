package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func diagnosticFixture(session domain.ID) domain.RequestDiagnostic {
	attempted := false
	id := domain.NewID()
	return domain.RequestDiagnostic{ID: id, CorrelationID: id, SessionID: session, ExecutionID: domain.NewID(), AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(), Source: domain.DiagnosticProxyHTTP, Operation: domain.DiagnosticResponse, State: domain.DiagnosticInProgress, Purpose: domain.ConversationUsage, Harness: domain.Codex, ObservedAt: time.Now().UTC(), HTTPAttempted: &attempted}
}

func TestRequestDiagnosticMigrationPreservesV24AndBackup(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	session := domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.session", nil, func(tx *Tx) (any, error) { return tx.Put(domain.SessionKind, session, 0, session, "", struct{}{}) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DROP TABLE request_diagnostics; PRAGMA user_version=24"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	backups, err := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
	if err != nil || len(backups) != 1 {
		t.Fatal("missing synchronized original backup")
	}
	backup, err := sql.Open("sqlite", databaseURI(backups[0], true))
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var version, table int
	if err := backup.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 24 {
		t.Fatal("original backup changed", version, err)
	}
	if err := backup.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='request_diagnostics'").Scan(&table); err != nil || table != 0 {
		t.Fatal("backup contains new layout")
	}
	if _, err := s.Get(ctx, domain.SessionKind, session); err != nil {
		t.Fatal("migration removed historical session", err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		rows, _, err := tx.ListRequestDiagnostics(session, "", "", 50)
		if len(rows) != 0 {
			t.Fatal("migration manufactured historical requests")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRequestDiagnosticRevisionReplayAndSessionDeletion(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	session := domain.NewID()
	value := diagnosticFixture(session)
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.session", nil, func(tx *Tx) (any, error) { return tx.Put(domain.SessionKind, session, 0, session, "", struct{}{}) })
	if err != nil {
		t.Fatal(err)
	}
	request := domain.NewID()
	write := func() (Result, error) {
		return s.Mutate(ctx, request, "fixture.diagnostic", value, func(tx *Tx) (any, error) { return struct{ ID domain.ID }{value.ID}, tx.PutRequestDiagnostic(value, 0) })
	}
	if _, err := write(); err != nil {
		t.Fatal(err)
	}
	if result, err := write(); err != nil || !result.Replayed {
		t.Fatal("original receipt was not retained", err)
	}
	events, err := s.Events(ctx, 0, session, 100)
	if err != nil || len(events) != 2 || events[1].Kind != domain.SessionKind || events[1].Revision != 1 || events[1].Action != Updated {
		t.Fatal("diagnostic event was lost, duplicated or changed session state", events, err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		rows, _, err := tx.ListRequestDiagnostics(session, "", "", 50)
		if len(rows) != 1 || rows[0].Revision != 1 || rows[0].PublicationRequestID != request {
			t.Fatal("receipt duplicated an observation")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	changed := value
	changed.AccountID = domain.NewID()
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.changed-diagnostic", nil, func(tx *Tx) (any, error) { return nil, tx.PutRequestDiagnostic(changed, 1) })
	if domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("changed event-time identity accepted", err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.delete-session", nil, func(tx *Tx) (any, error) { return nil, tx.Delete(domain.SessionKind, session, 1) })
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		_, err := tx.RequestDiagnostic(value.ID)
		if domain.SafeError(err).Code != domain.NotFound {
			t.Fatal("session deletion retained diagnostic", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if result, err := write(); err != nil || !result.Replayed {
		t.Fatal("old receipt tried to recreate deleted observations", err)
	}
}

func TestRequestDiagnosticBoundsAndClosedProjection(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	session := domain.NewID()
	value := diagnosticFixture(session)
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.full-diagnostics", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.SessionKind, session, 0, session, "", struct{}{}); err != nil {
			return nil, err
		}
		if err := tx.PutRequestDiagnostic(value, 0); err != nil {
			return nil, err
		}
		// Populate only the bounded index to exercise admission without spending
		// 10,000 transactions on identical fixture construction.
		_, err := tx.tx.ExecContext(tx.ctx, `WITH RECURSIVE numbers(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM numbers WHERE n<9999) INSERT INTO request_diagnostics SELECT 'fixture-'||n,?, ?,1,'{}' FROM numbers`, session, value.ExecutionID)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.overflow", nil, func(tx *Tx) (any, error) { return nil, tx.PutRequestDiagnostic(diagnosticFixture(session), 0) })
	if domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("retention limit did not fail before new admission", err)
	}
	unsafe := value
	secret := "/private/path user@example.com SECRET_SENTINEL"
	unsafe.EffectiveEffort = &secret
	if unsafe.Validate() == nil {
		t.Fatal("unbounded content setting accepted")
	}
	unsafe = value
	unsafe.NativeRequestID = secret
	if unsafe.Validate() == nil {
		t.Fatal("content accepted as native ID")
	}
}
