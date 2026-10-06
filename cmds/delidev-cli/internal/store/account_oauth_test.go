// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func privateOAuthAttempt() domain.AccountOAuthAttempt {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return domain.AccountOAuthAttempt{Version: 1, ID: domain.NewID(), Revision: 1, ServerID: domain.NewID(), ProviderID: domain.NewID(), ProviderRevision: 1, Actor: domain.Principal{Type: domain.OwnerDevice}, Generation: domain.NewID(), StartRequestID: domain.NewID(), AccountID: domain.NewID(), CreateRequestID: domain.NewID(), ConnectRequestID: domain.NewID(), State: domain.OAuthAwaiting, StartedAt: now, ExpiresAt: now.Add(10 * time.Minute), UpdatedAt: now}
}
func TestOAuthPrivateTableMigrationAfterRealPredecessors(t *testing.T) {
	for _, version := range []string{"025", "026", "027"} {
		t.Run(version, func(t *testing.T) {
			s, root := openTest(t)
			retained := create(t, s, domain.NewID(), "retained project")
			if _, err := historicalSchema(s.db, version); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			migrated, err := Open(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			defer migrated.Close()
			var version int
			if err := migrated.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
				t.Fatal(version, err)
			}
			row, err := migrated.Get(context.Background(), retained.Kind, retained.ID)
			if err != nil || row.Revision != retained.Revision {
				t.Fatal("predecessor records changed", err)
			}
			var marker string
			if err := migrated.db.QueryRow("SELECT value FROM metadata WHERE key='account_oauth_layout'").Scan(&marker); err != nil || marker != "pkce-once-v1" {
				t.Fatal(marker, err)
			}
			images, _ := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
			if len(images) != 1 || ValidateBackup(context.Background(), images[0]) != nil {
				t.Fatal("original migration safety image missing")
			}
		})
	}
}
func TestOAuthMigrationFailureRollsBackRealPredecessors(t *testing.T) {
	s, root := openTest(t)
	if _, err := historicalSchema(s.db, "025"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("CREATE TABLE account_oauth_attempts(foreign_state TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := Open(context.Background(), root)
	if err == nil {
		t.Fatal("foreign OAuth table accepted")
	}
	db, err := sql.Open("sqlite", databaseURI(filepath.Join(root, "state.sqlite"), true))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 25 {
		t.Fatal("failed migration advanced original schema", version, err)
	}
	var tables int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN ('request_diagnostics','retired_configurations')").Scan(&tables); err != nil || tables != 0 {
		t.Fatal("failed migration left predecessor writes", tables, err)
	}
}
func TestOAuthAttemptPrivateCASAndLifetimeInterruption(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	a := privateOAuthAttempt()
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.private-oauth", nil, func(tx *Tx) (any, error) { return nil, tx.PutAccountOAuth(a, 0) })
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Get(ctx, domain.AccountKind, a.AccountID)
	assertCode(t, err, domain.NotFound)
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.interrupt-oauth", nil, func(tx *Tx) (any, error) {
		return nil, tx.InterruptAccountOAuth(domain.NewID(), a.UpdatedAt.Add(time.Second))
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Read(ctx, func(tx *Tx) error {
		got, err := tx.AccountOAuth(a.ID)
		if err == nil && (got.State != domain.OAuthInterrupted || got.Revision != 2 || got.StagingClaimed) {
			t.Fatal("lifetime restored original dispatch")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.stale-oauth", nil, func(tx *Tx) (any, error) { a.Revision = 2; return nil, tx.PutAccountOAuth(a, 1) })
	assertCode(t, err, domain.Conflict)
}

func TestOAuthExpiredAwaitingDoesNotBlockRestoreButClaimsDo(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	var attempts []domain.AccountOAuthAttempt
	for _, state := range []domain.AccountOAuthState{domain.OAuthAwaiting, domain.OAuthAwaiting, domain.OAuthExchanging} {
		a := privateOAuthAttempt()
		a.State = state
		if len(attempts) != 1 {
			a.StartedAt = now.Add(-11 * time.Minute)
			a.ExpiresAt = a.StartedAt.Add(10 * time.Minute)
			a.UpdatedAt = a.StartedAt
		}
		if state == domain.OAuthExchanging {
			a.CompletionRequestID = domain.NewID()
			a.CompletionRevision = 1
			a.CodeCommitment = strings.Repeat("a", 64)
		}
		attempts = append(attempts, a)
	}
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.oauth-expiry", nil, func(tx *Tx) (any, error) {
		for _, a := range attempts {
			if err := tx.PutAccountOAuth(a, 0); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		n, err := tx.AccountOAuthPending()
		if err == nil && n != 2 {
			t.Fatalf("expiry blocked restore or released exchange: %d", n)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Eligibility is read-only: expiry never manufactures exchange cleanup proof.
	if err := s.Read(ctx, func(tx *Tx) error {
		a, err := tx.AccountOAuth(attempts[0].ID)
		if err == nil && (a.State != domain.OAuthAwaiting || a.Revision != 1) {
			t.Fatal("read eligibility changed stored attempt")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
