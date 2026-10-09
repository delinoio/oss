// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func privateOAuthAttempt() domain.AccountOAuthAttempt {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return domain.AccountOAuthAttempt{Version: 1, ID: domain.NewID(), Revision: 1, ServerID: domain.NewID(), ProviderID: domain.NewID(), ProviderRevision: 1, Actor: domain.Principal{Type: domain.OwnerDevice}, Generation: domain.NewID(), StartRequestID: domain.NewID(), AccountID: domain.NewID(), CreateRequestID: domain.NewID(), ConnectRequestID: domain.NewID(), State: domain.OAuthAwaiting, StartedAt: now, ExpiresAt: now.Add(10 * time.Minute), UpdatedAt: now}
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
