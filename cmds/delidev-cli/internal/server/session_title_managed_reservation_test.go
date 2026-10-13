// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// claimedPrimary stores one claimed conversation job for the title's account,
// owned by the same Worker process, so its Take competes with the title.
func (f *managedTitleFixture) claimedPrimary(t *testing.T) store.Record {
	t.Helper()
	id := domain.NewID()
	var record store.Record
	f.mutate(t, "claimed-primary", func(tx *store.Tx) error {
		other := f.original
		other.ExecutionID = domain.NewID()
		raw, err := json.Marshal(other)
		if err != nil {
			return err
		}
		record, err = tx.PutJob(id, 0, other.SessionID, "", domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobClaimed, MachineID: other.MachineID, InstanceID: f.instance, AssignedDeviceID: f.authorityFixture.device, Input: raw, AcceptedAt: time.Now().UTC()})
		return err
	})
	return record
}

func (f *managedTitleFixture) takePrimary(record store.Record) (*pb.TakeSubscriptionResponse, error) {
	response, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: record.Revision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: string(record.ID), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (f *managedTitleFixture) reserved(t *testing.T) bool {
	t.Helper()
	var reserved bool
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		var err error
		reserved, err = managedTitleReservation(tx, f.input.AccountID, "")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return reserved
}

// Window: auxiliary admission marks the title running before its own Take.
// A primary Take in that gap would lease the account and advance the frozen
// generation on Finish, stranding the running title. The primary Take waits
// (definite busy, no credentials, no lease) and the title keeps its custody.
func TestManagedTitleReservesAccountBetweenClaimAndTake(t *testing.T) {
	f := newManagedTitleFixture(t, managedTitleOptions{})
	record, err := f.claim()
	if err != nil {
		t.Fatal(err)
	}
	if !f.reserved(t) {
		t.Fatal("a claimed managed title did not reserve its account")
	}
	_, before := f.record()
	primary := f.claimedPrimary(t)
	if response, err := f.takePrimary(primary); domain.SafeError(rpc.ClientError(err)).Code != domain.ResourceExhausted || response != nil {
		t.Fatal("a primary Take acquired an account reserved by a claimed managed title", err)
	}
	_, account := f.record()
	if account.Subscription.Lease != nil || account.Subscription.Generation != before.Subscription.Generation || account.Subscription.RecoveryRequired {
		t.Fatalf("the refused primary Take changed account custody: %+v", account.Subscription)
	}
	// The title still takes the original generation, finishes and publishes.
	lease, err := f.takeTitle(record.Revision)
	if err != nil {
		t.Fatal("the reserved title lost its custody window", err)
	}
	if !bytes.Equal(lease.Bundle, f.bundle) {
		t.Fatal("the title did not receive the original protected generation")
	}
	clear(lease.Bundle)
	if f.reserved(t) {
		t.Fatal("a committed inference claim still reserved the account beside its own lease")
	}
	// The lease itself keeps primary work out until the title's Finish.
	if _, err := f.takePrimary(primary); domain.SafeError(rpc.ClientError(err)).Code != domain.ResourceExhausted {
		t.Fatal("a primary Take acquired a leased account", err)
	}
	finish, err := f.finishLease(lease, f.bundle, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.report(record, f.result(finish, "Reserved title", false), nil); err != nil {
		t.Fatal(err)
	}
	if session := f.session(t); session.Name != "Reserved title" || session.TitleState != domain.TitleSucceeded {
		t.Fatalf("the reserved title did not publish: %+v", session)
	}
	// Once the title settles, the waiting primary work proceeds on the new generation.
	if _, err := f.takePrimary(primary); err != nil {
		t.Fatal("the waiting primary Take stayed blocked after the title settled", err)
	}
}

// Window: the reservation must never outlive the title's ability to Take, or a
// canceled, replaced or dead Worker would stall the account's primary work.
func TestManagedTitleReservationLapsesWhenTitleCannotTake(t *testing.T) {
	cases := map[string]func(f *managedTitleFixture, tx *store.Tx) error{
		"canceled": func(f *managedTitleFixture, tx *store.Tx) error {
			_, err := cancelSessionTitleJob(tx, f.titleJob)
			return err
		},
		"worker-replaced": func(f *managedTitleFixture, tx *store.Tx) error {
			return tx.SetWorkerInstance(f.input.MachineID, domain.NewID(), time.Now().UTC())
		},
		"worker-stale": func(f *managedTitleFixture, tx *store.Tx) error {
			return tx.SetWorkerInstance(f.input.MachineID, f.instance, time.Now().UTC().Add(-2*domain.WorkerConnectionTimeout))
		},
		"settled-uncertain": func(f *managedTitleFixture, tx *store.Tx) error {
			r, err := tx.Get(domain.JobKind, f.titleJob)
			if err != nil {
				return err
			}
			job, err := store.Decode[domain.Job](r)
			if err != nil {
				return err
			}
			job.State = domain.JobUncertain
			_, err = tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job)
			return err
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := newManagedTitleFixture(t, managedTitleOptions{})
			if _, err := f.claim(); err != nil {
				t.Fatal(err)
			}
			if !f.reserved(t) {
				t.Fatal("a claimed managed title did not reserve its account")
			}
			f.mutate(t, name, func(tx *store.Tx) error { return change(f, tx) })
			if f.reserved(t) {
				t.Fatalf("%s title still reserved the account", name)
			}
		})
	}
	t.Run("canceled-primary-proceeds", func(t *testing.T) {
		f := newManagedTitleFixture(t, managedTitleOptions{})
		if _, err := f.claim(); err != nil {
			t.Fatal(err)
		}
		primary := f.claimedPrimary(t)
		f.mutate(t, "cancel", func(tx *store.Tx) error {
			_, err := cancelSessionTitleJob(tx, f.titleJob)
			return err
		})
		if _, err := f.takePrimary(primary); err != nil {
			t.Fatal("a canceled title kept primary work out of the account", err)
		}
	})
}

// A title that is only queued reserves nothing: admission, not queueing, starts
// the custody window, and queued titles compete through serialized admission.
func TestManagedTitleQueuedAndOtherAccountsAreNotReserved(t *testing.T) {
	f := newManagedTitleFixture(t, managedTitleOptions{})
	if f.reserved(t) {
		t.Fatal("a queued title reserved the account")
	}
	if _, err := f.claim(); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		other, err := managedTitleReservation(tx, domain.NewID(), "")
		if err != nil || other {
			t.Fatal("a title reserved a foreign account", other, err)
		}
		self, err := managedTitleReservation(tx, f.input.AccountID, f.titleJob)
		if err != nil || self {
			t.Fatal("a title reserved the account against itself", self, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Window: a refresh or Worker observation accepted between claim and Take would
// rotate the frozen generation, so both wait for the running title as well.
func TestManagedTitleReservationBlocksRotatingAccountOperations(t *testing.T) {
	f := newManagedTitleFixture(t, managedTitleOptions{})
	if _, err := f.claim(); err != nil {
		t.Fatal(err)
	}
	for _, machine := range []string{string(f.input.MachineID), ""} {
		r, _ := f.record()
		_, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: machine, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH}))
		if domain.SafeError(rpc.ClientError(err)).Code != domain.ResourceExhausted {
			t.Fatalf("a refresh (machine %q) was accepted while a title reserved the account: %v", machine, err)
		}
	}
	f.mutate(t, "worker-observation", func(tx *store.Tx) error {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return err
		}
		op := domain.SubscriptionObservationOperation{ID: domain.NewID(), Action: domain.SubscriptionQuota, MachineID: f.input.MachineID, Actor: domain.Principal{Type: domain.OwnerDevice}, ConnectionID: a.Connection.ID, Generation: a.Subscription.Generation, Phase: domain.SubscriptionObservationQueued, RequestedAt: time.Now().UTC()}
		if err := acceptSubscriptionObservation(tx, r, a, op, f.service.subscriptionServerEpoch()); domain.SafeError(err).Code != domain.ResourceExhausted {
			t.Fatal("a Worker observation was accepted while a title reserved the account", err)
		}
		return nil
	})
	_, account := f.record()
	if account.Subscription.Pending != nil || account.Subscription.ServerOperation != nil || account.Subscription.Observation != nil {
		t.Fatalf("a refused operation left account state behind: %+v", account.Subscription)
	}
	// Once the reservation lapses, the same refresh is accepted.
	f.mutate(t, "cancel", func(tx *store.Tx) error {
		_, err := cancelSessionTitleJob(tx, f.titleJob)
		return err
	})
	f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
}
