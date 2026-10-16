// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// chatGPTRecoveryFixture holds a synthetic original ChatGPT Execute lease whose
// authentication cleanup was reported unconfirmed.
type chatGPTRecoveryFixture struct {
	*subscriptionFixture
	lease    *pb.TakeSubscriptionResponse
	original []byte
}

func newChatGPTRecoveryFixture(t *testing.T) *chatGPTRecoveryFixture {
	t.Helper()
	f := newSubscriptionFixture(t)
	original := f.login()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.recovery-capability", nil, func(tx *store.Tx) (any, error) {
		mr, machine, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.ManagedCodexRecoveryV1)
		return tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
	})
	if err != nil {
		t.Fatal(err)
	}
	lease := takeSubscriptionExecutionFixture(t, f)
	return &chatGPTRecoveryFixture{subscriptionFixture: f, lease: lease, original: original}
}

func (f *chatGPTRecoveryFixture) sync(requestID domain.ID, bundle []byte) (*pb.SyncSubscriptionRecoveryCaptureResponse, error) {
	response, err := f.client.SyncSubscriptionRecoveryCapture(context.Background(), subscriptionRequest(f.workerToken, &pb.SyncSubscriptionRecoveryCaptureRequest{Mutation: &pb.Mutation{RequestId: string(requestID), Id: string(f.input.AccountID), ExpectedRevision: f.lease.LeaseRevision}, LeaseId: f.lease.LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), GenerationId: f.lease.GenerationId, Bundle: bytes.Clone(bundle)}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (f *chatGPTRecoveryFixture) read() (*pb.ReadSubscriptionRecoveryCaptureResponse, error) {
	response, err := f.client.ReadSubscriptionRecoveryCapture(context.Background(), subscriptionRequest(f.workerToken, &pb.ReadSubscriptionRecoveryCaptureRequest{AccountId: string(f.input.AccountID), LeaseId: f.lease.LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), GenerationId: f.lease.GenerationId}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (f *chatGPTRecoveryFixture) recover(requestID domain.ID, outcome pb.SubscriptionRecoveryOutcome, capture string, reason pb.SubscriptionRecoveryReason) (*pb.RecoverSubscriptionResponse, error) {
	response, err := f.client.RecoverSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.RecoverSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(requestID), Id: string(f.input.AccountID), ExpectedRevision: f.lease.LeaseRevision}, LeaseId: f.lease.LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), GenerationId: f.lease.GenerationId, Outcome: outcome, CaptureId: capture, Reason: reason}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (f *chatGPTRecoveryFixture) fence() {
	f.t.Helper()
	if _, err := f.finish(f.lease, nil, false, false, false); err != nil {
		f.t.Fatal(err)
	}
	_, a := f.record()
	if !a.Subscription.RecoveryRequired || a.Subscription.Lease == nil {
		f.t.Fatal("unconfirmed cleanup did not fence the lease")
	}
}

func (f *chatGPTRecoveryFixture) replaceInstance() {
	f.t.Helper()
	replacement := domain.NewID()
	err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error { return nil })
	if err != nil {
		f.t.Fatal(err)
	}
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.recovery-instance", nil, func(tx *store.Tx) (any, error) {
		return nil, tx.SetWorkerInstance(f.input.MachineID, replacement, time.Now().UTC())
	})
	if err != nil {
		f.t.Fatal(err)
	}
	f.instance = replacement
}

func (f *chatGPTRecoveryFixture) refs() int {
	_, _, count := f.secrets.counts()
	return count
}

func TestSubscriptionRecoveryPublishesProtectedCaptureOnce(t *testing.T) {
	f := newChatGPTRecoveryFixture(t)
	latest := subscriptionTestBundle("fixture-account", "second", time.Now().UTC())
	defer clear(latest)
	capture := domain.NewID()
	if _, err := f.sync(capture, latest); err != nil {
		t.Fatal(err)
	}
	if replay, err := f.sync(capture, latest); err != nil || !replay.Replayed {
		t.Fatal("capture synchronization is not idempotent", err)
	}
	if _, err := f.read(); domain.SafeError(rpc.ClientError(err)).Code != domain.RecoveryRequired {
		t.Fatal("capture was readable before the lease was fenced", err)
	}
	f.fence()
	// A currently authenticated replacement instance of the same Worker device
	// reconciles the retained original lease.
	f.replaceInstance()
	read, err := f.read()
	if err != nil {
		t.Fatal(err)
	}
	if read.CaptureId != string(capture) || !bytes.Equal(read.LatestBundle, latest) || !bytes.Equal(read.OriginalBundle, f.original) {
		t.Fatal("recovery read did not return the protected evidence")
	}
	recovery := domain.NewID()
	first, err := f.recover(recovery, pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_CREDENTIALS_VERIFIED, string(capture), 0)
	if err != nil {
		t.Fatal(err)
	}
	_, a := f.record()
	if a.Subscription.RecoveryRequired || a.Subscription.Lease != nil || a.Health != domain.AccountReady || a.Subscription.Generation != recovery {
		t.Fatal("verified recovery did not publish one ready generation")
	}
	published, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: f.input.AccountID, ID: recovery, Purpose: credentials.AccountLogin})
	if err != nil || !bytes.Equal(published, latest) {
		t.Fatal("recovered generation does not carry the validated latest credentials", err)
	}
	if f.refs() != 1 {
		t.Fatal("original and capture references were not retired", f.refs())
	}
	revision, _ := f.record()
	second, err := f.recover(recovery, pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_CREDENTIALS_VERIFIED, string(capture), 0)
	if err != nil || !second.Replayed || first.Account.Revision != second.Account.Revision {
		t.Fatal("recovery replay changed the settled result", err)
	}
	if again, _ := f.record(); again.Revision != revision.Revision {
		t.Fatal("recovery replay mutated the account")
	}
	// The next admission receives the recovered latest generation.
	next, err := f.take(f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH), pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	if err != nil || !bytes.Equal(next.Bundle, latest) {
		t.Fatal("next admission did not receive the recovered generation")
	}
}

func TestSubscriptionRecoveryWithoutUsableCredentialsDisconnects(t *testing.T) {
	f := newChatGPTRecoveryFixture(t)
	f.fence()
	recovery := domain.NewID()
	if _, err := f.recover(recovery, pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_NO_USABLE_CREDENTIALS, "", 0); err != nil {
		t.Fatal(err)
	}
	_, a := f.record()
	if a.Connection != nil || a.Health != domain.AccountDisconnected || a.Subscription.Lease != nil || a.Subscription.RecoveryRequired || a.Subscription.Generation != "" {
		t.Fatal("cleanup without credentials did not settle disconnected")
	}
	if f.refs() != 0 {
		t.Fatal("protected references survived", f.refs())
	}
	if _, err := f.take(&pb.RequestSubscriptionResponse{OperationId: string(f.job)}, pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE); err == nil {
		t.Fatal("an old generation was granted after recovery")
	}
	// The existing explicit login applies.
	clear(f.login())
}

func TestSubscriptionRecoveryRejectsInsufficientOrForeignAuthority(t *testing.T) {
	f := newChatGPTRecoveryFixture(t)
	foreign := subscriptionTestBundle("foreign-account", "second", time.Now().UTC())
	defer clear(foreign)
	if _, err := f.sync(domain.NewID(), foreign); err == nil {
		t.Fatal("a foreign identity was captured")
	}
	f.fence()
	if _, err := f.recover(domain.NewID(), pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_CREDENTIALS_VERIFIED, string(domain.NewID()), 0); err == nil {
		t.Fatal("recovery succeeded without a protected capture")
	}
	if _, err := f.recover(domain.NewID(), pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_NO_USABLE_CREDENTIALS, "x", 0); err == nil {
		t.Fatal("invalid outcome evidence was accepted")
	}
	changed := *f.lease
	changed.GenerationId = string(domain.NewID())
	original := f.lease
	f.lease = &changed
	if _, err := f.read(); err == nil {
		t.Fatal("a replaced generation read the capture")
	}
	f.lease = original
	if _, err := f.client.RecoverSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RecoverSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: f.lease.LeaseRevision}, LeaseId: f.lease.LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), GenerationId: f.lease.GenerationId, Outcome: pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_NO_USABLE_CREDENTIALS})); err == nil {
		t.Fatal("a non-Worker credential reconciled the lease")
	}
	_, a := f.record()
	if !a.Subscription.RecoveryRequired || a.Subscription.Lease == nil {
		t.Fatal("rejected recovery cleared the fence")
	}
}

func TestSubscriptionRecoveryRequiresNegotiatedPeer(t *testing.T) {
	f := newChatGPTRecoveryFixture(t)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.recovery-unsupported", nil, func(tx *store.Tx) (any, error) {
		mr, machine, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		machine.WorkerCapabilities = []domain.WorkerCapability{domain.ManagedCodexSubscriptionsV1}
		return tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.fence()
	if _, err := f.read(); err == nil {
		t.Fatal("an unsupported peer read the capture")
	}
	if _, err := f.recover(domain.NewID(), pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_NO_USABLE_CREDENTIALS, "", 0); err == nil {
		t.Fatal("an unsupported peer settled recovery")
	}
}

func TestSubscriptionRecoveryBlockedRecordsClosedReasonOnly(t *testing.T) {
	f := newChatGPTRecoveryFixture(t)
	f.fence()
	if _, err := f.recover(domain.NewID(), pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED, "", pb.SubscriptionRecoveryReason_SUBSCRIPTION_RECOVERY_REASON_UNSPECIFIED); err == nil {
		t.Fatal("a blocked report without a reason was accepted")
	}
	if _, err := f.recover(domain.NewID(), pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED, "", pb.SubscriptionRecoveryReason_SUBSCRIPTION_RECOVERY_REASON_CREDENTIAL_PROOF_UNAVAILABLE); err != nil {
		t.Fatal(err)
	}
	_, a := f.record()
	if a.Subscription.RecoveryReason != domain.RecoveryCredentialProofUnavailable || !a.Subscription.RecoveryRequired || a.Subscription.Lease == nil {
		t.Fatal("blocked recovery changed more than the closed reason")
	}
	if err := a.Subscription.Validate(a); err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionRecoveryWatchDispatchesOnlyToNegotiatedOriginalWorker(t *testing.T) {
	f := newChatGPTRecoveryFixture(t)
	f.fence()
	_, a := f.record()
	if !recoveryEligible(a, f.input.MachineID) || recoveryEligible(a, domain.NewID()) {
		t.Fatal("recovery eligibility is not bound to the original machine")
	}
	a.Subscription.Lease.Action = domain.SubscriptionLogin
	if recoveryEligible(a, f.input.MachineID) {
		t.Fatal("a non-Execute lease became eligible")
	}
}
