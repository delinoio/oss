// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func (f *subscriptionFixture) enableRecovery() {
	f.t.Helper()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.recovery-capability", nil, func(tx *store.Tx) (any, error) {
		mr, machine, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.ManagedCodexRecoveryV1)
		return tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
	})
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *subscriptionFixture) syncCapture(lease *pb.TakeSubscriptionResponse, bundle []byte) (*pb.SyncSubscriptionRecoveryCaptureResponse, error) {
	response, err := f.client.SyncSubscriptionRecoveryCapture(context.Background(), subscriptionRequest(f.workerToken, &pb.SyncSubscriptionRecoveryCaptureRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: lease.LeaseRevision}, LeaseId: lease.LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), GenerationId: lease.GenerationId, Bundle: bytes.Clone(bundle)}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (f *subscriptionFixture) readCapture(lease *pb.TakeSubscriptionResponse, capture string) ([]byte, error) {
	response, err := f.client.ReadSubscriptionRecoveryCapture(context.Background(), subscriptionRequest(f.workerToken, &pb.ReadSubscriptionRecoveryCaptureRequest{AccountId: string(f.input.AccountID), LeaseId: lease.LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), CaptureId: capture}))
	if err != nil {
		return nil, err
	}
	return response.Msg.Bundle, nil
}

func (f *subscriptionFixture) recoverRequest(lease *pb.TakeSubscriptionResponse, capture string, outcome pb.SubscriptionRecoveryOutcome, reason pb.SubscriptionRecoveryBlockedReason, request string) *pb.RecoverSubscriptionRequest {
	return &pb.RecoverSubscriptionRequest{Mutation: &pb.Mutation{RequestId: request, Id: string(f.input.AccountID), ExpectedRevision: lease.LeaseRevision}, LeaseId: lease.LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), GenerationId: lease.GenerationId, CaptureId: capture, Outcome: outcome, BlockedReason: reason}
}

func (f *subscriptionFixture) recoverCall(request *pb.RecoverSubscriptionRequest) (*pb.RecoverSubscriptionResponse, error) {
	response, err := f.client.RecoverSubscription(context.Background(), subscriptionRequest(f.workerToken, request))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

// fencedExecute logs in, takes an Execute lease and reports a failed cleanup so
// the account carries the recovery fence. A non-nil latest is synchronized as the
// protected capture first, exactly as a negotiated Worker does before deleting
// its transient authentication.
func (f *subscriptionFixture) fencedExecute(latest []byte) (*pb.TakeSubscriptionResponse, string, []byte) {
	f.t.Helper()
	f.enableRecovery()
	original := f.login()
	lease := takeSubscriptionExecutionFixture(f.t, f)
	clear(lease.Bundle)
	capture := ""
	if latest != nil {
		synced, err := f.syncCapture(lease, latest)
		if err != nil {
			f.t.Fatal(err)
		}
		capture = synced.CaptureId
	}
	if _, err := f.finish(lease, nil, false, false, false); err != nil {
		f.t.Fatal(err)
	}
	_, account := f.record()
	if !account.Subscription.RecoveryRequired || account.Subscription.Lease == nil {
		f.t.Fatal("failed cleanup did not fence the Execute lease")
	}
	return lease, capture, original
}

func (f *subscriptionFixture) liveReferences() map[domain.ID]bool {
	f.secrets.mu.Lock()
	defer f.secrets.mu.Unlock()
	live := map[domain.ID]bool{}
	for ref := range f.secrets.values {
		if !f.secrets.removed[ref] {
			live[ref.ID] = true
		}
	}
	return live
}

func TestSubscriptionRecoveryRestoresReadinessWithOneNewGeneration(t *testing.T) {
	f := newSubscriptionFixture(t)
	latest := subscriptionTestBundle("fixture-account", "second", time.Now().UTC())
	defer clear(latest)
	lease, capture, original := f.fencedExecute(latest)
	defer clear(original)
	before, beforeAccount := f.record()
	if beforeAccount.Subscription.RecoveryCapture == nil || !beforeAccount.Subscription.RecoveryCapture.Usable {
		t.Fatal("a refreshed successor was not retained as a usable capture")
	}
	read, err := f.readCapture(lease, capture)
	if err != nil || !bytes.Equal(read, latest) {
		t.Fatal("the original Worker could not read its retained capture", err)
	}
	clear(read)
	request := f.recoverRequest(lease, capture, pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_SETTLED, pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_UNSPECIFIED, string(domain.NewID()))
	response, err := f.recoverCall(request)
	if err != nil || response.Replayed {
		t.Fatal("recovery was not accepted", err)
	}
	_, after := f.record()
	state := after.Subscription
	if state.RecoveryRequired || state.Lease != nil || state.RecoveryCapture != nil || state.RecoveryReason != "" || after.Health != domain.AccountReady || after.Connection == nil {
		t.Fatal("recovery did not restore readiness", state)
	}
	if state.Generation != domain.ID(request.Mutation.RequestId) || state.Generation == domain.ID(lease.GenerationId) {
		t.Fatal("recovery did not publish exactly one new immutable generation")
	}
	live := f.liveReferences()
	if len(live) != 1 || !live[state.Generation] {
		t.Fatal("recovery left a protected reference beyond the one current generation", live)
	}
	stored, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: f.input.AccountID, ID: state.Generation, Purpose: credentials.AccountLogin})
	if err != nil || !bytes.Equal(stored, latest) {
		t.Fatal("the new generation is not the validated latest bundle", err)
	}
	// Original receipts stay verbatim: the failed Finish replays unchanged and the
	// original Take is still refused.
	if _, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: lease.LeaseId, Id: string(f.input.AccountID), ExpectedRevision: before.Revision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: string(f.job), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE})); err == nil {
		t.Fatal("recovery re-ran or re-delivered the original Take")
	}
	// A lost acknowledgement repeats the same recovery request.
	replay, err := f.recoverCall(request)
	if err != nil || !replay.Replayed {
		t.Fatal("repeated recovery delivery was not idempotent", err)
	}
	if _, again := f.record(); again.Subscription.Generation != state.Generation {
		t.Fatal("a repeated recovery published a second generation")
	}
}

func TestSubscriptionRecoveryRetiresReferencesWhenLatestCredentialIsUnusable(t *testing.T) {
	f := newSubscriptionFixture(t)
	foreign := subscriptionTestBundle("another-provider-account", "second", time.Now().UTC())
	defer clear(foreign)
	lease, capture, original := f.fencedExecute(foreign)
	defer clear(original)
	if _, account := f.record(); account.Subscription.RecoveryCapture == nil || account.Subscription.RecoveryCapture.Usable {
		t.Fatal("a different-identity capture was marked usable")
	}
	response, err := f.recoverCall(f.recoverRequest(lease, capture, pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_SETTLED, pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_UNSPECIFIED, string(domain.NewID())))
	if err != nil || response.Replayed {
		t.Fatal("cleanup without usable credentials was not settled", err)
	}
	_, account := f.record()
	state := account.Subscription
	if account.Connection != nil || account.Health != domain.AccountDisconnected || state.Generation != "" || state.IdentityCommitment != "" || state.Lease != nil || state.RecoveryRequired || state.RecoveryCapture != nil {
		t.Fatal("an unusable credential was not settled as disconnected", account.Health)
	}
	if live := f.liveReferences(); len(live) != 0 {
		t.Fatal("original protected references survived retirement", live)
	}
	// The existing explicit login flow now applies; recovery never logs in.
	op := f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if op.OperationId == "" {
		t.Fatal("the existing login flow was not admitted")
	}
}

func TestSubscriptionRecoveryBlockedReportKeepsFenceAndPublishesClosedReason(t *testing.T) {
	f := newSubscriptionFixture(t)
	lease, _, original := f.fencedExecute(nil)
	defer clear(original)
	request := f.recoverRequest(lease, "", pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED, pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_CREDENTIAL_PROOF_UNAVAILABLE, string(domain.NewID()))
	if _, err := f.recoverCall(request); err != nil {
		t.Fatal(err)
	}
	_, account := f.record()
	state := account.Subscription
	if !state.RecoveryRequired || state.Lease == nil || state.Lease.ID != domain.ID(lease.LeaseId) || state.RecoveryReason != domain.RecoveryCredentialProofUnavailable || state.Generation != domain.ID(lease.GenerationId) {
		t.Fatal("a blocked report cleared protection or lost its closed reason", state)
	}
	// Settling without any capture never clears the fence.
	settle := f.recoverRequest(lease, string(domain.NewID()), pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_SETTLED, pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_UNSPECIFIED, string(domain.NewID()))
	if _, err := f.recoverCall(settle); err == nil {
		t.Fatal("insufficient proof cleared the recovery fence")
	}
	if _, after := f.record(); !after.Subscription.RecoveryRequired || after.Subscription.Lease == nil {
		t.Fatal("insufficient proof changed the fence")
	}
}

func TestSubscriptionRecoveryRejectsForeignOrStaleAuthority(t *testing.T) {
	latest := subscriptionTestBundle("fixture-account", "second", time.Now().UTC())
	defer clear(latest)
	for name, mutate := range map[string]func(f *subscriptionFixture, request *pb.RecoverSubscriptionRequest){
		"owner-credential": func(f *subscriptionFixture, request *pb.RecoverSubscriptionRequest) {},
		"replaced-generation": func(f *subscriptionFixture, request *pb.RecoverSubscriptionRequest) {
			request.GenerationId = string(domain.NewID())
		},
		"stale-revision": func(f *subscriptionFixture, request *pb.RecoverSubscriptionRequest) {
			request.Mutation.ExpectedRevision++
		},
		"wrong-lease": func(f *subscriptionFixture, request *pb.RecoverSubscriptionRequest) {
			request.LeaseId = string(domain.NewID())
		},
		"foreign-capture": func(f *subscriptionFixture, request *pb.RecoverSubscriptionRequest) {
			request.CaptureId = string(domain.NewID())
		},
		"other-machine": func(f *subscriptionFixture, request *pb.RecoverSubscriptionRequest) {
			request.MachineId = string(domain.NewID())
		},
		"replaced-instance": func(f *subscriptionFixture, request *pb.RecoverSubscriptionRequest) {
			request.InstanceId = string(domain.NewID())
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSubscriptionFixture(t)
			lease, capture, original := f.fencedExecute(latest)
			defer clear(original)
			request := f.recoverRequest(lease, capture, pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_SETTLED, pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_UNSPECIFIED, string(domain.NewID()))
			mutate(f, request)
			token := f.workerToken
			if name == "owner-credential" {
				token = f.service.Identity.Token
			}
			beforeRecord, before := f.record()
			beforeJSON, _ := json.Marshal(before)
			_, err := f.client.RecoverSubscription(context.Background(), subscriptionRequest(token, request))
			if err == nil {
				t.Fatal("foreign or stale evidence settled the recovery fence")
			}
			afterRecord, after := f.record()
			afterJSON, _ := json.Marshal(after)
			if afterRecord.Revision != beforeRecord.Revision || !bytes.Equal(beforeJSON, afterJSON) {
				t.Fatal("rejected recovery changed the original evidence")
			}
		})
	}
}

func TestSubscriptionRecoveryRequiresNegotiatedCapabilityAndFence(t *testing.T) {
	f := newSubscriptionFixture(t)
	original := f.login()
	defer clear(original)
	lease := takeSubscriptionExecutionFixture(t, f)
	clear(lease.Bundle)
	latest := subscriptionTestBundle("fixture-account", "second", time.Now().UTC())
	defer clear(latest)
	// An older peer never reaches the lane.
	if _, err := f.syncCapture(lease, latest); err == nil || domain.SafeError(rpc.ClientError(err)).Code != domain.Unsupported {
		t.Fatal("a Worker without the capability synchronized a capture")
	}
	f.enableRecovery()
	synced, err := f.syncCapture(lease, latest)
	if err != nil {
		t.Fatal(err)
	}
	// Reads and recovery require the fence: a live lease is not reconcilable.
	if _, err := f.readCapture(lease, synced.CaptureId); err == nil {
		t.Fatal("a live lease exposed its capture")
	}
	live := f.recoverRequest(lease, synced.CaptureId, pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_SETTLED, pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_UNSPECIFIED, string(domain.NewID()))
	if _, err := f.recoverCall(live); err == nil {
		t.Fatal("concurrent active original ownership was reconciled")
	}
	// A successful ordinary Finish releases the lease and retires the capture.
	if _, err := f.finish(lease, latest, true, false, true); err != nil {
		t.Fatal(err)
	}
	_, account := f.record()
	if account.Subscription.RecoveryCapture != nil {
		t.Fatal("a released lease retained its capture metadata")
	}
	if refs := f.liveReferences(); len(refs) != 1 || !refs[account.Subscription.Generation] {
		t.Fatal("a released lease retained a protected capture", refs)
	}
}

func TestSubscriptionRecoveryReplacementInstanceOfOriginalWorkerSettles(t *testing.T) {
	f := newSubscriptionFixture(t)
	latest := subscriptionTestBundle("fixture-account", "second", time.Now().UTC())
	defer clear(latest)
	lease, capture, original := f.fencedExecute(latest)
	defer clear(original)
	replacement := domain.NewID()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.replacement-instance", nil, func(tx *store.Tx) (any, error) {
		return nil, tx.SetWorkerInstance(f.input.MachineID, replacement, time.Now().UTC())
	})
	if err != nil {
		t.Fatal(err)
	}
	// The retired original instance is no longer authoritative.
	if _, err := f.readCapture(lease, capture); err == nil {
		t.Fatal("a replaced instance read the capture")
	}
	request := f.recoverRequest(lease, capture, pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_SETTLED, pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_UNSPECIFIED, string(domain.NewID()))
	request.InstanceId = string(replacement)
	if _, err := f.recoverCall(request); err != nil {
		t.Fatal("the replacement instance of the original Worker could not settle", err)
	}
	if _, account := f.record(); account.Subscription.Lease != nil || account.Health != domain.AccountReady {
		t.Fatal("replacement-instance recovery did not restore readiness")
	}
	// A lost acknowledgement is repeated by yet another instance of the same
	// original Worker; the durable recovery identity still replays.
	next := domain.NewID()
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.next-instance", nil, func(tx *store.Tx) (any, error) {
		return nil, tx.SetWorkerInstance(f.input.MachineID, next, time.Now().UTC())
	})
	if err != nil {
		t.Fatal(err)
	}
	request.InstanceId = string(next)
	if replay, err := f.recoverCall(request); err != nil || !replay.Replayed {
		t.Fatal("the durable recovery request did not replay across instances", err)
	}
}

func TestSubscriptionRecoveryRetireWaitsForIndependentCredentialUsers(t *testing.T) {
	f := newSubscriptionFixture(t)
	foreign := subscriptionTestBundle("another-provider-account", "second", time.Now().UTC())
	defer clear(foreign)
	lease, capture, original := f.fencedExecute(foreign)
	defer clear(original)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.independent-user", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.Subscription.ServerQuota = &domain.ServerQuotaOperation{ID: domain.NewID(), Generation: domain.NewID(), AccessOnly: true, ReferenceRetirementPending: true}
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		t.Skip("the fixture cannot represent an independent credential user:", err)
	}
	request := f.recoverRequest(lease, capture, pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_SETTLED, pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_UNSPECIFIED, string(domain.NewID()))
	if _, err := f.recoverCall(request); domain.SafeError(rpc.ClientError(err)).Code != domain.ResourceExhausted {
		t.Fatal("owner retirement did not wait for independent credential users", err)
	}
	if _, account := f.record(); account.Subscription.Lease == nil || !account.Subscription.RecoveryRequired {
		t.Fatal("owner retirement released the fence under an independent user")
	}
}

func TestSubscriptionRecoveryWatchSelectsOnlyNegotiatedOriginalWorker(t *testing.T) {
	f := newSubscriptionFixture(t)
	latest := subscriptionTestBundle("fixture-account", "second", time.Now().UTC())
	defer clear(latest)
	lease, _, original := f.fencedExecute(latest)
	defer clear(original)
	watch := func() int {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stream, err := f.client.WatchSubscription(ctx, subscriptionRequest(f.workerToken, &pb.WatchSubscriptionRequest{MachineId: string(f.input.MachineID), InstanceId: string(f.instance)}))
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		accounts := 0
		for stream.Receive() {
			if stream.Msg().Account == nil {
				break
			}
			accounts++
		}
		return accounts
	}
	if watch() != 1 {
		t.Fatal("the negotiated original Worker was not offered its fenced Execute lease")
	}
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.drop-recovery-capability", nil, func(tx *store.Tx) (any, error) {
		mr, machine, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		kept := machine.WorkerCapabilities[:0]
		for _, capability := range machine.WorkerCapabilities {
			if capability != domain.ManagedCodexRecoveryV1 {
				kept = append(kept, capability)
			}
		}
		machine.WorkerCapabilities = kept
		return tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
	})
	if err != nil {
		t.Fatal(err)
	}
	if watch() != 0 {
		t.Fatal("an older peer was offered the recovery lane", lease.LeaseId)
	}
}

func TestSubscriptionRecoveryCrashReplayKeepsCaptureAndRepairsVaultCleanup(t *testing.T) {
	f := newSubscriptionFixture(t)
	f.enableRecovery()
	original := f.login()
	defer clear(original)
	lease := takeSubscriptionExecutionFixture(t, f)
	clear(lease.Bundle)
	latest := subscriptionTestBundle("fixture-account", "second", time.Now().UTC())
	defer clear(latest)
	// A repeated capture request after a lost acknowledgement replays the same
	// capture instead of recapturing.
	captureRequest := &pb.SyncSubscriptionRecoveryCaptureRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: lease.LeaseRevision}, LeaseId: lease.LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), GenerationId: lease.GenerationId, Bundle: bytes.Clone(latest)}
	first, err := f.client.SyncSubscriptionRecoveryCapture(context.Background(), subscriptionRequest(f.workerToken, captureRequest))
	if err != nil || first.Msg.Replayed {
		t.Fatal("capture was not stored", err)
	}
	captureRequest.Bundle = bytes.Clone(latest)
	second, err := f.client.SyncSubscriptionRecoveryCapture(context.Background(), subscriptionRequest(f.workerToken, captureRequest))
	if err != nil || !second.Msg.Replayed || second.Msg.CaptureId != first.Msg.CaptureId {
		t.Fatal("repeated capture delivery recaptured", err)
	}
	// A different bundle under the same capture identity is refused.
	other := subscriptionTestBundle("fixture-account", "third", time.Now().UTC())
	defer clear(other)
	captureRequest.Bundle = bytes.Clone(other)
	if _, err := f.client.SyncSubscriptionRecoveryCapture(context.Background(), subscriptionRequest(f.workerToken, captureRequest)); err == nil {
		t.Fatal("a capture identity accepted different content")
	}
	if _, err := f.finish(lease, nil, false, false, false); err != nil {
		t.Fatal(err)
	}
	request := f.recoverRequest(lease, first.Msg.CaptureId, pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_SETTLED, pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_UNSPECIFIED, string(domain.NewID()))
	// The state commit lands but the old-reference retirement crashes before the
	// response: the same recovery request must finish the retirement.
	f.secrets.mu.Lock()
	f.secrets.deleteError = domain.Fail(domain.Unavailable, "Injected vault failure.", "")
	f.secrets.mu.Unlock()
	if _, err := f.recoverCall(request); err == nil {
		t.Fatal("an unconfirmed vault retirement was reported as complete")
	}
	_, mid := f.record()
	if mid.Subscription.Lease != nil || mid.Subscription.Generation != domain.ID(request.Mutation.RequestId) {
		t.Fatal("the committed settlement was lost across the crash")
	}
	f.secrets.mu.Lock()
	f.secrets.deleteError = nil
	f.secrets.mu.Unlock()
	replay, err := f.recoverCall(request)
	if err != nil || !replay.Replayed {
		t.Fatal("the repeated recovery did not replay", err)
	}
	if live := f.liveReferences(); len(live) != 1 || !live[domain.ID(request.Mutation.RequestId)] {
		t.Fatal("the replayed recovery did not retire the old references", live)
	}
}

func TestSubscriptionRecoveryCapturesSurvivingFileOnceUnderTheFence(t *testing.T) {
	f := newSubscriptionFixture(t)
	lease, _, original := f.fencedExecute(nil)
	defer clear(original)
	surviving := subscriptionTestBundle("fixture-account", "second", time.Now().UTC())
	defer clear(surviving)
	captured, err := f.syncCapture(lease, surviving)
	if err != nil {
		t.Fatal("the original Worker could not capture its surviving file under the fence", err)
	}
	if _, err := f.syncCapture(lease, surviving); err == nil {
		t.Fatal("a retained capture was replaced after the lease was lost")
	}
	response, err := f.recoverCall(f.recoverRequest(lease, captured.CaptureId, pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_SETTLED, pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_UNSPECIFIED, string(domain.NewID())))
	if err != nil || response.Replayed {
		t.Fatal("a legacy lease with a surviving file did not recover", err)
	}
	if _, account := f.record(); account.Health != domain.AccountReady || account.Subscription.Lease != nil {
		t.Fatal("legacy recovery did not restore readiness")
	}
}

// A crash after the protected references were deleted but before the settlement
// committed must leave a resumable state: the same recovery request finishes the
// retirement even though the capture and the old generation are gone.
func TestSubscriptionRecoveryRetirementResumesAfterEvidenceWasDeleted(t *testing.T) {
	f := newSubscriptionFixture(t)
	foreign := subscriptionTestBundle("another-provider-account", "second", time.Now().UTC())
	defer clear(foreign)
	lease, capture, original := f.fencedExecute(foreign)
	defer clear(original)
	request := f.recoverRequest(lease, capture, pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_SETTLED, pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_UNSPECIFIED, string(domain.NewID()))
	// The store failure lands right after the last deletion.
	f.secrets.mu.Lock()
	f.secrets.enumerationFailsAfterDeletes = f.secrets.deletes + len(f.secrets.values)
	f.secrets.mu.Unlock()
	if _, err := f.recoverCall(request); err == nil {
		t.Fatal("an interrupted retirement was reported as complete")
	}
	if live := f.liveReferences(); len(live) != 0 {
		t.Fatal("the interrupted retirement did not delete the evidence", live)
	}
	_, mid := f.record()
	if mid.Subscription.Lease == nil || !mid.Subscription.RecoveryRequired || !mid.Subscription.RecoveryRetiring || mid.Subscription.RecoveryCapture == nil {
		t.Fatal("the retirement checkpoint was not committed before the evidence was deleted")
	}
	f.secrets.mu.Lock()
	f.secrets.enumerationFailsAfterDeletes = 0
	f.secrets.mu.Unlock()
	response, err := f.recoverCall(request)
	if err != nil || response.Replayed {
		t.Fatal("the identical recovery request did not resume the retirement", err)
	}
	_, account := f.record()
	state := account.Subscription
	if account.Connection != nil || account.Health != domain.AccountDisconnected || state.Lease != nil || state.RecoveryRequired || state.RecoveryRetiring || state.RecoveryCapture != nil || state.Generation != "" {
		t.Fatal("the resumed retirement did not settle the account as disconnected", account.Health)
	}
	if again, err := f.recoverCall(request); err != nil || !again.Replayed {
		t.Fatal("the settled retirement did not replay", err)
	}
}

// The restore path never writes the checkpoint, and a vault read failure never
// degrades a restore into a retirement.
func TestSubscriptionRecoveryRestoreDoesNotCheckpointRetirement(t *testing.T) {
	f := newSubscriptionFixture(t)
	latest := subscriptionTestBundle("fixture-account", "second", time.Now().UTC())
	defer clear(latest)
	lease, capture, original := f.fencedExecute(latest)
	defer clear(original)
	request := f.recoverRequest(lease, capture, pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_SETTLED, pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_UNSPECIFIED, string(domain.NewID()))
	f.secrets.mu.Lock()
	f.secrets.referenceError = nil
	f.secrets.mu.Unlock()
	if _, err := f.recoverCall(request); err != nil {
		t.Fatal(err)
	}
	_, account := f.record()
	if account.Subscription.RecoveryRetiring || account.Connection == nil || account.Health == domain.AccountDisconnected {
		t.Fatal("a restore was recorded as a retirement")
	}
}
