// SPDX-License-Identifier: Apache-2.0
package store

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestBackupRestoreRequiresSettledSubscriptionOwnership(t *testing.T) {
	for _, phase := range []string{"queued", "claimed", "recovery", "shared-owner", "settled"} {
		t.Run(phase, func(t *testing.T) {
			s, _, ctx, in, _ := restoreFixture(t)
			id, machine := domain.NewID(), domain.NewID()
			state := &domain.SubscriptionState{Generation: domain.NewID(), IdentityCommitment: strings.Repeat("a", 64)}
			if phase == "queued" || phase == "claimed" {
				state.Pending = &domain.SubscriptionOperation{ID: domain.NewID(), MachineID: machine, Actor: domain.Principal{Type: domain.OwnerDevice}, Action: domain.SubscriptionRefresh, Phase: domain.SubscriptionQueued}
			}
			if phase == "claimed" {
				state.Pending.Phase = domain.SubscriptionClaimed
				state.Lease = &domain.SubscriptionLease{ID: domain.NewID(), OperationID: state.Pending.ID, Revision: 1, Action: state.Pending.Action, MachineID: machine, DeviceID: domain.NewID(), InstanceID: domain.NewID(), Epoch: domain.NewID(), Generation: state.Generation, StartedAt: time.Now().UTC()}
			}
			if phase == "shared-owner" {
				// A live shared owner with members is never settled, even when no
				// lifecycle operation is pending.
				operation, lease := domain.NewID(), domain.NewID()
				state.Lease = &domain.SubscriptionLease{ID: lease, OperationID: operation, Revision: 1, Action: domain.SubscriptionExecute, MachineID: machine, DeviceID: domain.NewID(), InstanceID: domain.NewID(), Epoch: domain.NewID(), Generation: state.Generation, StartedAt: time.Now().UTC(), Shared: true}
				state.Members = []domain.SubscriptionMember{{LeaseID: lease, OperationID: operation, Revision: 1, State: domain.SubscriptionMemberRunning, AdmittedAt: time.Now().UTC()}, {LeaseID: domain.NewID(), OperationID: domain.NewID(), Revision: 2, State: domain.SubscriptionMemberRetained, AdmittedAt: time.Now().UTC()}}
			}
			state.RecoveryRequired = phase == "recovery"
			account := domain.Account{Alias: "fixture", SubscriptionService: domain.SubscriptionChatGPT, Type: domain.SubscriptionAccount, Health: domain.AccountReady, Connection: &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()}, Subscription: state}
			if err := account.Validate(); err != nil {
				t.Fatal("invalid subscription fixture", err)
			}
			_, err := s.Mutate(ctx, domain.NewID(), "fixture.subscription-restore", nil, func(tx *Tx) (any, error) { return tx.Put(domain.AccountKind, id, 0, "", "", account) })
			if err != nil {
				t.Fatal(err)
			}
			before, err := s.Get(ctx, domain.AccountKind, id)
			if err != nil {
				t.Fatal(err)
			}
			in.ExpectedRevision, err = s.RestoreRevision(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = s.RestoreBackup(ctx, domain.NewID(), in)
			if phase == "settled" {
				if err != nil {
					t.Fatal("settled subscription blocked database restore", err)
				}
			} else {
				if domain.SafeError(err).Code != domain.RecoveryRequired || s.RestoreFrozen() {
					t.Fatal("restore bypassed unsettled subscription ownership", err)
				}
				after, err := s.Get(ctx, domain.AccountKind, id)
				if err != nil || before.Revision != after.Revision || !bytes.Equal(before.Data, after.Data) {
					t.Fatal("refused restore changed subscription ownership", err)
				}
			}
		})
	}
}

func TestBackupRestoreQuarantinesHistoricalSubscriptionGeneration(t *testing.T) {
	s, root, ctx, in, _ := restoreFixture(t)
	id := domain.NewID()
	original := domain.Account{Alias: "fixture", SubscriptionService: domain.SubscriptionChatGPT, Type: domain.SubscriptionAccount, Health: domain.AccountReady, Connection: &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()}, Subscription: &domain.SubscriptionState{Generation: domain.NewID(), IdentityCommitment: strings.Repeat("a", 64)}}
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.subscription-image", nil, func(tx *Tx) (any, error) { return tx.Put(domain.AccountKind, id, 0, "", "", original) })
	if err != nil {
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
	// Rotate the current database after the selected image. Restoring it must
	// neither adopt this generation nor grant the older external vault bundle.
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.current-generation", nil, func(tx *Tx) (any, error) {
		r, err := tx.Get(domain.AccountKind, id)
		if err != nil {
			return nil, err
		}
		a, err := Decode[domain.Account](r)
		if err != nil {
			return nil, err
		}
		a.Subscription.Generation = domain.NewID()
		return tx.Put(domain.AccountKind, id, r.Revision, "", "", a)
	})
	if err != nil {
		t.Fatal(err)
	}
	in.ExpectedRevision, err = s.RestoreRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.RestoreBackup(ctx, domain.NewID(), in); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
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
	a, err := Decode[domain.Account](r)
	if err != nil || a.Validate() != nil || a.Connection != nil || a.Health != domain.AccountDisconnected || a.Subscription == nil || !a.Subscription.RecoveryRequired || a.Subscription.Generation != original.Subscription.Generation || a.Subscription.IdentityCommitment != original.Subscription.IdentityCommitment {
		t.Fatal("restored subscription lost valid quarantined evidence", err)
	}
	// Missing connection can never establish a usable generation outside quarantine.
	a.Subscription.RecoveryRequired = false
	if a.Validate() == nil {
		t.Fatal("disconnected historical generation regained authority")
	}
}

func TestBackupRestoreClearsEmptySubscriptionState(t *testing.T) {
	for _, allocated := range []bool{false, true} {
		name := "absent"
		if allocated {
			name = "settled-empty"
		}
		t.Run(name, func(t *testing.T) {
			s, root, ctx, in, _ := restoreFixture(t)
			id := domain.NewID()
			account := domain.Account{Alias: "fixture", SubscriptionService: domain.SubscriptionChatGPT, Type: domain.SubscriptionAccount, Health: domain.AccountDisconnected}
			if allocated {
				account.Subscription = &domain.SubscriptionState{}
			}
			_, err := s.Mutate(ctx, domain.NewID(), "fixture.empty-subscription", nil, func(tx *Tx) (any, error) {
				return tx.Put(domain.AccountKind, id, 0, "", "", account)
			})
			if err != nil {
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
			in.ExpectedRevision, err = s.RestoreRevision(ctx)
			if err != nil {
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
			if err != nil || restored.Validate() != nil || restored.Subscription != nil || restored.Connection != nil || restored.Health != domain.AccountDisconnected {
				t.Fatal("restore manufactured ownership for an empty subscription", err)
			}
		})
	}
}

func claudeUsageRestoreAccount(usage *domain.ClaudeServerUsageState) domain.Account {
	return domain.Account{Alias: "fixture", SubscriptionService: domain.SubscriptionClaude, Type: domain.SubscriptionAccount, Health: domain.AccountReady, Connection: &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()}, Subscription: &domain.SubscriptionState{ClaudeCredential: domain.ClaudeCredentialServerToken, Generation: domain.NewID(), IdentityCommitment: strings.Repeat("a", 64), ClaudeUsage: usage}}
}

func linkedClaudeUsageForRestore() *domain.ClaudeServerUsageState {
	linked, observed := time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(-time.Minute)
	return &domain.ClaudeServerUsageState{
		Connection: domain.ClaudeUsageLinked, Generation: domain.NewID(), CredentialID: domain.NewID(), Published: true, LinkedAt: &linked,
		Identity:      &domain.ClaudeUsageIdentity{Email: "person@example.com", Commitment: strings.Repeat("b", 64)},
		LastSuccessAt: &observed,
		Windows:       []domain.ClaudeUsageWindowEvidence{{ID: domain.ClaudeUsageFiveHour, Remaining: 0.5, ObservedAt: observed}},
	}
}

func TestBackupRestoreRetiresLinkedClaudeUsageAuthority(t *testing.T) {
	for _, name := range []string{"linked", "empty-after-retire"} {
		t.Run(name, func(t *testing.T) {
			s, root, ctx, in, _ := restoreFixture(t)
			id := domain.NewID()
			usage := linkedClaudeUsageForRestore()
			credential := usage.CredentialID
			if name == "empty-after-retire" {
				usage = &domain.ClaudeServerUsageState{Connection: domain.ClaudeUsageReconnectRequired, Generation: domain.NewID()}
			}
			account := claudeUsageRestoreAccount(usage)
			if err := account.Validate(); err != nil {
				t.Fatal("invalid usage fixture", err)
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
				t.Fatal("restore of a linked usage account failed", err)
			}
			if err = s.Close(); err != nil {
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
			if err != nil || restored.Validate() != nil || restored.Connection != nil || restored.Subscription == nil || len(restored.Quota) != 0 {
				t.Fatal("restored usage account is invalid or kept quota", err)
			}
			restoredUsage := restored.Subscription.ClaudeUsage
			if name == "empty-after-retire" {
				if restoredUsage != nil {
					t.Fatal("a section that owes nothing was restored")
				}
				return
			}
			if restoredUsage == nil || restoredUsage.Reading() || restoredUsage.Connection != domain.ClaudeUsageReconnectRequired || restoredUsage.Published || restoredUsage.CredentialID != "" || len(restoredUsage.RetiredCredentials) != 1 || restoredUsage.RetiredCredentials[0] != credential {
				t.Fatalf("restore kept usage authority or lost cleanup evidence: %+v", restoredUsage)
			}
		})
	}
}

func TestBackupRestoreRequiresSettledClaudeUsageOperations(t *testing.T) {
	for _, phase := range []string{"login", "reading", "renewing", "rotation-uncertain", "settled"} {
		t.Run(phase, func(t *testing.T) {
			s, _, ctx, in, _ := restoreFixture(t)
			id := domain.NewID()
			usage := linkedClaudeUsageForRestore()
			now := time.Now().UTC()
			switch phase {
			case "login":
				usage = &domain.ClaudeServerUsageState{Connection: domain.ClaudeUsageConnecting, Generation: domain.NewID(), Operation: &domain.ClaudeUsageOperation{ID: domain.NewID(), Epoch: domain.NewID(), Actor: domain.Principal{Type: domain.OwnerDevice}, State: domain.SubscriptionWaiting, StartedAt: now, ExpiresAt: now.Add(10 * time.Minute)}}
			case "reading":
				usage.Refresh = domain.ClaudeUsageReading
			case "renewing":
				usage.Refresh = domain.ClaudeUsageRenewing
			case "rotation-uncertain":
				usage.Refresh = domain.ClaudeUsageRotationUncertain
			}
			account := claudeUsageRestoreAccount(usage)
			if err := account.Validate(); err != nil {
				t.Fatal("invalid usage fixture", err)
			}
			if _, err := s.Mutate(ctx, domain.NewID(), "fixture.claude-usage-eligibility", nil, func(tx *Tx) (any, error) { return tx.Put(domain.AccountKind, id, 0, "", "", account) }); err != nil {
				t.Fatal(err)
			}
			before, err := s.Get(ctx, domain.AccountKind, id)
			if err != nil {
				t.Fatal(err)
			}
			if in.ExpectedRevision, err = s.RestoreRevision(ctx); err != nil {
				t.Fatal(err)
			}
			_, _, err = s.RestoreBackup(ctx, domain.NewID(), in)
			if phase == "settled" {
				if err != nil {
					t.Fatal("settled usage state blocked database restore", err)
				}
				return
			}
			if domain.SafeError(err).Code != domain.RecoveryRequired || s.RestoreFrozen() {
				t.Fatal("restore bypassed an unsettled usage operation", err)
			}
			after, err := s.Get(ctx, domain.AccountKind, id)
			if err != nil || before.Revision != after.Revision || !bytes.Equal(before.Data, after.Data) {
				t.Fatal("refused restore changed usage ownership", err)
			}
		})
	}
}
