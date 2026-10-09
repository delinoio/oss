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

func takeSubscriptionExecutionFixture(t *testing.T, f *subscriptionFixture) *pb.TakeSubscriptionResponse {
	t.Helper()
	_, account := f.record()
	f.input.ConnectionID = account.Connection.ID
	f.input.Configuration.Subscription = true
	f.input.ConfigurationDigest, _ = f.input.Configuration.Digest()
	var revision uint64
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.managed-execution", nil, func(tx *store.Tx) (any, error) {
		sr, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		session.InitialExecution.ConnectionID = f.input.ConnectionID
		session.InitialExecution.Configuration = f.input.Configuration
		session.InitialExecution.ConfigurationDigest = f.input.ConfigurationDigest
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.SessionID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		jr, err := tx.Get(domain.JobKind, f.job)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](jr)
		if err != nil {
			return nil, err
		}
		job.Input, _ = json.Marshal(f.input)
		updated, err := tx.PutJob(jr.ID, jr.Revision, jr.SessionID, jr.ProjectID, job)
		revision = updated.Revision
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: revision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: string(f.job), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE}))
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg
}

func TestSubscriptionDirectExecutionDeliversOriginalBundleWithoutInspection(t *testing.T) {
	f := newSubscriptionFixture(t)
	original := f.login()
	defer clear(original)
	f.input.Version, f.input.Startup = 4, &domain.ExecutionStartupSelection{Harness: domain.Codex}
	f.input.Installation = domain.Installation{}
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.subscription-direct-worker", nil, func(tx *store.Tx) (any, error) {
		mr, machine, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		machine.Installations = nil
		machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.ExecutionStartupV1)
		return tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
	})
	if err != nil {
		t.Fatal(err)
	}
	lease := takeSubscriptionExecutionFixture(t, f)
	defer clear(lease.Bundle)
	if !bytes.Equal(lease.Bundle, original) {
		t.Fatal("direct execution replaced the protected account generation")
	}
	var installation domain.Installation
	if domain.Decode(lease.InstallationJson, &installation) != nil || installation.State != "" || installation.Version != "" || installation.ResolvedPath != "" {
		t.Fatal("direct bundle delivery fabricated installation readiness")
	}
	_, account := f.record()
	if account.Subscription.RecoveryRequired || account.Subscription.Lease == nil || account.Subscription.Lease.OperationID != f.job {
		t.Fatal("direct bundle delivery lost its original execution lease")
	}
}

func TestSubscriptionLostOwnershipRejectsLateFinishWithoutVaultChanges(t *testing.T) {
	for _, action := range []pb.SubscriptionAction{pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT, pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE} {
		t.Run(action.String(), func(t *testing.T) {
			f := newSubscriptionFixture(t)
			if action != pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN {
				clear(f.login())
			}
			var lease *pb.TakeSubscriptionResponse
			if action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE {
				lease = takeSubscriptionExecutionFixture(t, f)
			} else {
				var err error
				lease, err = f.take(f.start(action), action)
				if err != nil {
					t.Fatal(err)
				}
			}
			clear(lease.Bundle)
			if err := f.service.retainLostSubscriptionLeases(f.input.MachineID, f.instance, action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE); err != nil {
				t.Fatal(err)
			}
			r, before := f.record()
			beforeJSON, _ := json.Marshal(before)
			f.secrets.mu.Lock()
			puts, deletes := f.secrets.puts, f.secrets.deletes
			f.secrets.mu.Unlock()
			bundle := subscriptionTestBundle("fixture-account", "late-rotation", time.Now().UTC())
			defer clear(bundle)
			if action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT {
				clear(bundle)
				bundle = nil
			}
			if _, err := f.finish(lease, bundle, true, true, true); domain.SafeError(rpc.ClientError(err)).Code != domain.RecoveryRequired {
				t.Fatal("lost native ownership accepted a late finish", err)
			}
			afterRecord, after := f.record()
			afterJSON, _ := json.Marshal(after)
			if afterRecord.Revision != r.Revision || !bytes.Equal(beforeJSON, afterJSON) || after.Subscription.Lease == nil || after.Subscription.Lease.ID != domain.ID(lease.LeaseId) {
				t.Fatal("late finish changed the original recovery evidence")
			}
			f.secrets.mu.Lock()
			changed := f.secrets.puts != puts || f.secrets.deletes != deletes
			f.secrets.mu.Unlock()
			if changed {
				t.Fatal("late finish staged or deleted a vault generation")
			}
		})
	}
}

func TestSubscriptionAcceptedFinishReplayPreservesLaterRecovery(t *testing.T) {
	f := newSubscriptionFixture(t)
	lease, err := f.take(f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN), pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	bundle := subscriptionTestBundle("fixture-account", "first", time.Now().UTC().Add(-time.Minute))
	defer clear(bundle)
	request := &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: lease.LeaseRevision}, LeaseId: lease.LeaseId, GenerationId: lease.GenerationId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), Bundle: bundle, Succeeded: true, CleanupConfirmed: true}
	if _, err := f.client.FinishSubscription(context.Background(), subscriptionRequest(f.workerToken, request)); err != nil {
		t.Fatal(err)
	}
	active, err := f.take(f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH), pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	if err != nil {
		t.Fatal(err)
	}
	clear(active.Bundle)
	if err := f.service.retainLostSubscriptionLeases(f.input.MachineID, f.instance, false); err != nil {
		t.Fatal(err)
	}
	response, err := f.client.FinishSubscription(context.Background(), subscriptionRequest(f.workerToken, request))
	if err != nil || !response.Msg.Replayed {
		t.Fatal("accepted receipt replay was confused with a new native finish", err)
	}
	_, account := f.record()
	if !account.Subscription.RecoveryRequired || account.Subscription.Lease == nil || account.Subscription.Lease.ID != domain.ID(active.LeaseId) || account.Subscription.Pending == nil {
		t.Fatal("accepted receipt replay erased later recovery ownership")
	}
}
