// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func claudeUsageRestoreAccount(usage *domain.ClaudeLimitState) domain.Account {
	return domain.Account{Alias: "fixture", SubscriptionService: domain.SubscriptionClaude, Type: domain.SubscriptionAccount, Health: domain.AccountReady, Quota: []domain.QuotaWindow{}, Connection: &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()}, Subscription: &domain.SubscriptionState{ClaudeCredential: domain.ClaudeCredentialServerToken, Generation: domain.NewID(), IdentityCommitment: strings.Repeat("a", 64), ClaudeUsage: usage}}
}

func linkedClaudeUsage() *domain.ClaudeLimitState {
	linked := time.Now().UTC()
	return &domain.ClaudeLimitState{Generation: domain.NewID(), LinkedAt: &linked, CredentialID: domain.NewID(), Identity: &domain.ClaudeLimitIdentity{Email: "alex@example.com", Plan: "max", Commitment: strings.Repeat("b", 64)}}
}

func TestBackupRestoreNeverResurrectsClaudeUsageAuthority(t *testing.T) {
	s, root, ctx, in, _ := restoreFixture(t)
	id := domain.NewID()
	account := claudeUsageRestoreAccount(linkedClaudeUsage())
	if err := account.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Mutate(ctx, domain.NewID(), "fixture.claude-usage-restore", nil, func(tx *Tx) (any, error) { return tx.Put(domain.AccountKind, id, 0, "", "", account) }); err != nil {
		t.Fatal(err)
	}
	backup, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := s.InspectBackup(ctx, backup, in.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	in.Backup, in.SHA256 = observed.Backup, observed.SHA256
	if in.ExpectedRevision, err = s.RestoreRevision(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.RestoreBackup(ctx, domain.NewID(), in); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	r, err := reopened.Get(ctx, domain.AccountKind, id)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Decode[domain.Account](r)
	if err != nil || restored.Validate() != nil || restored.Connection != nil || restored.Health != domain.AccountDisconnected {
		t.Fatal("restored account is invalid or connected", err)
	}
	if restored.Subscription != nil && restored.Subscription.ClaudeUsage != nil {
		t.Fatalf("restore resurrected the usage connection: %+v", restored.Subscription.ClaudeUsage)
	}
	if len(restored.Quota) != 0 {
		t.Fatal("restore retained usage windows")
	}
}

func TestBackupRestoreRequiresSettledClaudeUsageOwnership(t *testing.T) {
	started := time.Now().UTC()
	scenarios := map[string]func(*domain.ClaudeLimitState){
		"fetching read": func(s *domain.ClaudeLimitState) {
			s.Read = domain.ClaudeLimitRead{Phase: domain.ClaudeLimitReadFetching, ID: domain.NewID(), Epoch: domain.NewID(), StartedAt: &started, ConnectionID: domain.NewID(), Generation: s.Generation, Actor: domain.Principal{Type: domain.OwnerDevice}}
		},
		"refreshing read": func(s *domain.ClaudeLimitState) {
			s.Read = domain.ClaudeLimitRead{Phase: domain.ClaudeLimitReadRefreshing, ID: domain.NewID(), Epoch: domain.NewID(), StartedAt: &started, ConnectionID: domain.NewID(), Generation: s.Generation, Actor: domain.Principal{Type: domain.OwnerDevice}}
		},
		"cleanup pending": func(s *domain.ClaudeLimitState) { s.CleanupPending = true },
		"live login": func(s *domain.ClaudeLimitState) {
			s.Operation = &domain.ClaudeLimitOperation{ID: domain.NewID(), Epoch: domain.NewID(), Generation: domain.NewID(), Actor: domain.Principal{Type: domain.OwnerDevice}, State: domain.ClaudeLimitLoginWaiting, NativeStarted: true, StartedAt: started, ExpiresAt: started.Add(time.Minute)}
		},
	}
	for name, change := range scenarios {
		t.Run(name, func(t *testing.T) {
			s, _, ctx, in, _ := restoreFixture(t)
			usage := linkedClaudeUsage()
			change(usage)
			account := claudeUsageRestoreAccount(usage)
			if err := account.Validate(); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Mutate(ctx, domain.NewID(), "fixture.claude-usage-unsettled", nil, func(tx *Tx) (any, error) { return tx.Put(domain.AccountKind, domain.NewID(), 0, "", "", account) }); err != nil {
				t.Fatal(err)
			}
			var err error
			if in.ExpectedRevision, err = s.RestoreRevision(ctx); err != nil {
				t.Fatal(err)
			}
			if _, _, err = s.RestoreBackup(ctx, domain.NewID(), in); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatalf("restore bypassed unsettled usage ownership: %v", err)
			}
		})
	}
}
