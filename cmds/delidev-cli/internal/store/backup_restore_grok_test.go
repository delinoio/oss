// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// grokRestoreFixture is a connected Grok account holding one protected
// generation and its keyed identity commitment. It carries no credential
// material, which lives only in the external vault.
func grokRestoreFixture(state *domain.SubscriptionState) domain.Account {
	return domain.Account{Alias: "grok-fixture", SubscriptionService: domain.SubscriptionGrok, Type: domain.SubscriptionAccount, Health: domain.AccountReady, Connection: &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()}, Subscription: state}
}

// restoreGrokAccount stores account, backs it up, replaces the current account
// with settled (so restore preflight can pass), restores the image and returns
// the reopened account exactly as persisted.
func restoreGrokAccount(t *testing.T, original, settled domain.Account) (domain.Account, domain.Account) {
	t.Helper()
	s, root, ctx, in, _ := restoreFixture(t)
	id := domain.NewID()
	if err := original.Validate(); err != nil {
		t.Fatal("invalid Grok fixture", err)
	}
	if _, err := s.Mutate(ctx, domain.NewID(), "fixture.grok-image", nil, func(tx *Tx) (any, error) { return tx.Put(domain.AccountKind, id, 0, "", "", original) }); err != nil {
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
	// The current database settles after the selected image was taken, so only
	// the image carries the unsettled ownership evidence.
	if _, err := s.Mutate(ctx, domain.NewID(), "fixture.grok-settled", nil, func(tx *Tx) (any, error) {
		r, err := tx.Get(domain.AccountKind, id)
		if err != nil {
			return nil, err
		}
		return tx.Put(domain.AccountKind, id, r.Revision, "", "", settled)
	}); err != nil {
		t.Fatal(err)
	}
	if in.ExpectedRevision, err = s.RestoreRevision(ctx); err != nil {
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
	restored, err := Decode[domain.Account](r)
	if err != nil {
		t.Fatal(err)
	}
	return restored, original
}

func TestBackupRestoreQuarantinesGrokOwnershipEvidence(t *testing.T) {
	commitment := strings.Repeat("c", 64)
	for _, phase := range []string{"connected", "recovery", "pending-logout", "unsettled-cleanup"} {
		t.Run(phase, func(t *testing.T) {
			state := &domain.SubscriptionState{Generation: domain.NewID(), IdentityCommitment: commitment}
			switch phase {
			case "recovery":
				state.RecoveryRequired = true
			case "pending-logout", "unsettled-cleanup":
				machine := domain.NewID()
				state.Pending = &domain.SubscriptionOperation{ID: domain.NewID(), MachineID: machine, Actor: domain.Principal{Type: domain.OwnerDevice}, Action: domain.SubscriptionLogout, Phase: domain.SubscriptionQueued}
				if phase == "unsettled-cleanup" {
					state.Pending.Phase = domain.SubscriptionClaimed
					state.Lease = &domain.SubscriptionLease{ID: domain.NewID(), OperationID: state.Pending.ID, Revision: 3, Action: domain.SubscriptionLogout, MachineID: machine, DeviceID: domain.NewID(), InstanceID: domain.NewID(), Epoch: domain.NewID(), Generation: state.Generation, StartedAt: time.Now().UTC()}
					state.RecoveryRequired = true
				}
			}
			// The settled replacement keeps the same connected identity but owns
			// no lease, pending operation or recovery fence.
			settled := grokRestoreFixture(&domain.SubscriptionState{Generation: domain.NewID(), IdentityCommitment: commitment})
			original := grokRestoreFixture(state)
			if phase == "unsettled-cleanup" || phase == "recovery" {
				// A fenced account keeps its connection only until the failure that
				// fenced it; the fixture stays valid either way.
				original.Health = domain.AccountFailed
			}
			restored, original := restoreGrokAccount(t, original, settled)
			if err := restored.Validate(); err != nil {
				t.Fatal("restored Grok account is not a valid quarantined shape", err)
			}
			if restored.SubscriptionService != domain.SubscriptionGrok || restored.Connection != nil || restored.Health != domain.AccountDisconnected {
				t.Fatal("restore left usable Grok authentication", restored.Health)
			}
			st := restored.Subscription
			if st == nil || !st.RecoveryRequired || st.Generation != original.Subscription.Generation || st.IdentityCommitment != commitment {
				t.Fatal("restore lost the quarantined Grok generation and commitment evidence")
			}
			// Cleanup ownership is evidence only: every original owner field stays
			// byte-for-byte equal, so no other Worker can claim the cleanup.
			if !reflect.DeepEqual(st.Pending, original.Subscription.Pending) || !reflect.DeepEqual(st.Lease, original.Subscription.Lease) {
				t.Fatal("restore rewrote the original Grok cleanup ownership")
			}
			if st.Lease != nil {
				if got, want := st.Lease, original.Subscription.Lease; got.MachineID != want.MachineID || got.DeviceID != want.DeviceID || got.InstanceID != want.InstanceID || got.Epoch != want.Epoch || got.ID != want.ID || got.Revision != want.Revision {
					t.Fatal("restore changed the Grok lease owner identity")
				}
			}
			// A disconnected generation can never regain authority outside quarantine.
			st.RecoveryRequired = false
			if restored.Validate() == nil {
				t.Fatal("disconnected Grok generation regained authority")
			}
		})
	}
}

func TestBackupRestoreClearsEmptyGrokSubscriptionState(t *testing.T) {
	original := domain.Account{Alias: "grok-empty", SubscriptionService: domain.SubscriptionGrok, Type: domain.SubscriptionAccount, Health: domain.AccountDisconnected, Subscription: &domain.SubscriptionState{}}
	restored, _ := restoreGrokAccount(t, original, original)
	if restored.Validate() != nil || restored.Subscription != nil || restored.Connection != nil || restored.Health != domain.AccountDisconnected {
		t.Fatal("restore manufactured ownership for an empty Grok subscription")
	}
}
