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
	for _, phase := range []string{"queued", "claimed", "recovery", "settled"} {
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
			state.RecoveryRequired = phase == "recovery"
			account := domain.Account{Alias: "fixture", ProviderID: domain.NewID(), Type: domain.SubscriptionAccount, Health: domain.AccountReady, Connection: &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()}, Subscription: state}
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
	original := domain.Account{Alias: "fixture", ProviderID: domain.NewID(), Type: domain.SubscriptionAccount, Health: domain.AccountReady, Connection: &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()}, Subscription: &domain.SubscriptionState{Generation: domain.NewID(), IdentityCommitment: strings.Repeat("a", 64)}}
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
			account := domain.Account{Alias: "fixture", ProviderID: domain.NewID(), Type: domain.SubscriptionAccount, Health: domain.AccountDisconnected}
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
