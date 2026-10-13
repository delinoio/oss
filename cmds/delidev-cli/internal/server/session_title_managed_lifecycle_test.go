// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// dropTitleCapability removes the managed title capability from the original
// Worker, modeling a same-process reattach that no longer proves the adapter.
func (f *managedTitleFixture) dropTitleCapability() error {
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.drop-title-capability", nil, func(tx *store.Tx) (any, error) {
		mr, err := tx.Get(domain.MachineKind, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		machine, err := store.Decode[domain.Machine](mr)
		if err != nil {
			return nil, err
		}
		machine.WorkerCapabilities = slices.DeleteFunc(slices.Clone(machine.WorkerCapabilities), func(c domain.WorkerCapability) bool { return c == domain.ManagedCodexTitlesV1 })
		return tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
	})
	return err
}

// hookOnce runs a fixture mutation the first time the vault is read, which is
// after TakeSubscription committed the lease and before it returns any bundle.
func (f *managedTitleFixture) hookOnce(change func() error) (*error, func()) {
	var once sync.Once
	var hookErr error
	f.service.accountSecrets = &getHookSecrets{accountSecrets: f.secrets, before: func() { once.Do(func() { hookErr = change() }) }}
	return &hookErr, func() { f.service.accountSecrets = f.secrets }
}

// A Worker that reattaches without the managed title capability after Take
// still owns the exact title lease. Custody validates the immutable lineage, so
// the confirmed cleanup and refreshed bundle publish and the title applies; only
// admission and Take consult the current capability set.
func TestManagedTitleCustodySurvivesCapabilityChangeAfterTake(t *testing.T) {
	for _, when := range []string{"before-delivery", "after-delivery"} {
		t.Run(when, func(t *testing.T) {
			f := newManagedTitleFixture(t, managedTitleOptions{})
			record, err := f.claim()
			if err != nil {
				t.Fatal(err)
			}
			var hookErr *error
			if when == "before-delivery" {
				var restore func()
				hookErr, restore = f.hookOnce(f.dropTitleCapability)
				defer restore()
			}
			lease, err := f.takeTitle(record.Revision)
			if err != nil {
				t.Fatal("a capability change between lease and delivery denied the exact title custody", err)
			}
			if hookErr != nil && *hookErr != nil {
				t.Fatal(*hookErr)
			}
			if when == "after-delivery" {
				if err := f.dropTitleCapability(); err != nil {
					t.Fatal(err)
				}
			}
			finish, err := f.finishLease(lease, f.bundle, true, true)
			if err != nil {
				t.Fatal("a capability change fenced the protected Finish of an exact title lease", err)
			}
			_, account := f.record()
			if account.Subscription.Lease != nil || account.Subscription.RecoveryRequired || account.Subscription.Generation != finish {
				t.Fatalf("the title lease was not settled on the staged generation: %+v", account.Subscription)
			}
			if err := f.report(record, f.result(finish, "Plan the migration", true), nil); err != nil {
				t.Fatal(err)
			}
			if session := f.session(t); session.Name != "Plan the migration" || session.TitleState != domain.TitleSucceeded {
				t.Fatalf("the proven title was discarded after a capability change: %+v", session)
			}
		})
	}
}

// The capability gate still applies before any custody exists: a claimed title
// whose Worker lost the adapter is refused at Take without a lease.
func TestManagedTitleTakeStillRequiresCurrentCapability(t *testing.T) {
	f := newManagedTitleFixture(t, managedTitleOptions{})
	record, err := f.claim()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.dropTitleCapability(); err != nil {
		t.Fatal(err)
	}
	if response, err := f.takeTitle(record.Revision); err == nil || response != nil {
		t.Fatal("a Worker without the managed title capability received protected credentials")
	}
	if _, account := f.record(); account.Subscription.Lease != nil || account.Subscription.RecoveryRequired {
		t.Fatalf("a refused Take left custody state: %+v", account.Subscription)
	}
}

// Every definite no-delivery denial after the lease commits releases the exact
// title lease. Account logout, disablement or removal accepted in that window
// must not leave a recovery fence, because the logout itself needs the lease
// released to run.
func TestManagedTitleAccountLossBeforeDeliveryReleasesLease(t *testing.T) {
	for _, name := range []string{"logout", "disabled", "removal"} {
		t.Run(name, func(t *testing.T) {
			f := newManagedTitleFixture(t, managedTitleOptions{})
			record, err := f.claim()
			if err != nil {
				t.Fatal(err)
			}
			var logoutOperation string
			change := func() error {
				switch name {
				case "logout":
					r, _ := f.record()
					response, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT}))
					if err == nil {
						logoutOperation = response.Msg.OperationId
					}
					return err
				default:
					_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.account-loss", nil, func(tx *store.Tx) (any, error) {
						r, a, err := accountFromTx(tx, f.input.AccountID, 0)
						if err != nil {
							return nil, err
						}
						if name == "disabled" {
							a.Enabled = false
						} else {
							a.Removal = &domain.AccountRemoval{RequestID: domain.NewID()}
						}
						return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
					})
					return err
				}
			}
			hookErr, restore := f.hookOnce(change)
			defer restore()
			response, err := f.takeTitle(record.Revision)
			if *hookErr != nil {
				t.Fatal(*hookErr)
			}
			if err == nil || response != nil {
				t.Fatal("a title received protected credentials after its account was lost")
			}
			_, account := f.record()
			if account.Subscription.Lease != nil || account.Subscription.RecoveryRequired || account.Health == domain.AccountFailed {
				t.Fatalf("an undelivered title lease fenced the account: health=%s %+v", account.Health, account.Subscription)
			}
			if name == "logout" {
				if account.Health != domain.AccountRevoked || account.Subscription.Pending == nil {
					t.Fatalf("the accepted logout was lost: %+v", account)
				}
				// The released account lets the accepted logout take its own lease.
				f.service.accountSecrets = f.secrets
				if _, err := f.take(&pb.RequestSubscriptionResponse{OperationId: logoutOperation}, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT); err != nil {
					t.Fatal("the accepted logout cannot run after an undelivered title lease", err)
				}
			}
			// The one-time inference claim stays consumed, so the title can never send.
			f.service.accountSecrets = f.secrets
			if _, err := f.takeTitle(record.Revision); err == nil {
				t.Fatal("a released undelivered title received credentials on retry")
			}
		})
	}
}
