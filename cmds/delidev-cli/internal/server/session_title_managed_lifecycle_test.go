// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
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

// A busy managed title is a candidate-specific deferral, not a full lane. The
// auxiliary scan must advance past it so later titles for free accounts run.
func TestManagedTitleBusyCandidateDoesNotBlockLaterTitles(t *testing.T) {
	f := newManagedTitleFixture(t, managedTitleOptions{})
	f.mutate(t, "busy-account", func(tx *store.Tx) error {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return err
		}
		a.Subscription.Lease = &domain.SubscriptionLease{ID: domain.NewID(), OperationID: domain.NewID(), Revision: 1, Action: domain.SubscriptionExecute, MachineID: f.input.MachineID, InstanceID: f.instance, DeviceID: f.authorityFixture.device, Epoch: domain.NewID(), Generation: a.Subscription.Generation, StartedAt: time.Now().UTC()}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return err
	})
	// A later queued title on the same Worker whose assignment admission retires
	// proves the scan reached it past the busy first candidate.
	later := domain.NewID()
	raw, _ := json.Marshal(map[string]any{"version": 99})
	f.mutate(t, "later-title", func(tx *store.Tx) error {
		_, err := tx.PutJob(later, 0, f.original.SessionID, "", domain.Job{Type: domain.GenerateSessionTitleJob, State: domain.JobQueued, MachineID: f.input.MachineID, ParentID: f.job, Input: raw, AcceptedAt: time.Now().UTC()})
		return err
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stream, err := f.authorityFixture.client.WatchAuxiliaryWork(ctx, ownerRequest(security.Identity{Token: f.workerToken}, &pb.WatchAuxiliaryWorkRequest{MachineId: string(f.input.MachineID), InstanceId: string(f.instance)}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		record, err := f.service.Store.Get(context.Background(), domain.JobKind, later)
		if err != nil {
			t.Fatal(err)
		}
		job, err := store.Decode[domain.Job](record)
		if err != nil {
			t.Fatal(err)
		}
		if job.State == domain.JobFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a busy managed title blocked the later queued title: %+v", job)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, first := f.titleRecord(t); first.State != domain.JobQueued {
		t.Fatalf("the busy title was not kept durably queued: %+v", first)
	}
	if session := f.session(t); session.TitleState != domain.TitleQueued {
		t.Fatalf("the busy title session was retired: %+v", session)
	}
}
