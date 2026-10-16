// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type codexRecoveryFixture struct {
	*subscriptionFixture
	original []byte
	lease    *pb.TakeSubscriptionResponse
}

// newCodexRecoveryFixture holds a live managed ChatGPT EXECUTE lease on a Worker that
// negotiated the recovery capability.
func newCodexRecoveryFixture(t *testing.T) *codexRecoveryFixture {
	t.Helper()
	f := newSubscriptionFixture(t)
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
	original := f.login()
	lease := takeSubscriptionExecutionFixture(t, f)
	return &codexRecoveryFixture{subscriptionFixture: f, original: original, lease: lease}
}

func (f *codexRecoveryFixture) latest(rotation string) []byte {
	return subscriptionTestBundle("fixture-account", rotation, time.Now().UTC())
}

func (f *codexRecoveryFixture) sync(capture domain.ID, original, latest []byte) (*pb.SyncSubscriptionRecoveryCaptureResponse, error) {
	response, err := f.client.SyncSubscriptionRecoveryCapture(context.Background(), subscriptionRequest(f.workerToken, &pb.SyncSubscriptionRecoveryCaptureRequest{
		Mutation: &pb.Mutation{RequestId: string(capture), Id: string(f.input.AccountID), ExpectedRevision: f.lease.LeaseRevision},
		LeaseId:  f.lease.LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), GenerationId: f.lease.GenerationId,
		Original: bytes.Clone(original), Latest: bytes.Clone(latest),
	}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (f *codexRecoveryFixture) read() (*pb.ReadSubscriptionRecoveryCaptureResponse, error) {
	response, err := f.client.ReadSubscriptionRecoveryCapture(context.Background(), subscriptionRequest(f.workerToken, &pb.ReadSubscriptionRecoveryCaptureRequest{
		AccountId: string(f.input.AccountID), LeaseId: f.lease.LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance),
		OriginalInstanceId: string(f.instance), GenerationId: f.lease.GenerationId,
	}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

// fence reports an unconfirmed-cleanup Finish, which is how a failed ChatGPT
// authentication cleanup leaves the lease retained and the account fenced.
func (f *codexRecoveryFixture) fence() {
	f.t.Helper()
	if _, err := f.finish(f.lease, nil, false, false, false); err != nil {
		f.t.Fatal(err)
	}
	if _, a := f.record(); !a.Subscription.RecoveryRequired || a.Subscription.Lease == nil {
		f.t.Fatal("unconfirmed cleanup did not retain the fenced lease")
	}
}

func (f *codexRecoveryFixture) recoverRequest(request domain.ID, report pb.SubscriptionRecoveryReport, capture domain.ID, members ...domain.ID) *pb.RecoverSubscriptionRequest {
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, string(m))
	}
	return &pb.RecoverSubscriptionRequest{
		Mutation: &pb.Mutation{RequestId: string(request), Id: string(f.input.AccountID), ExpectedRevision: f.lease.LeaseRevision},
		LeaseId:  f.lease.LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OriginalInstanceId: string(f.instance),
		GenerationId: f.lease.GenerationId, Report: report, CaptureId: string(capture), SettledMemberLeaseIds: ids,
	}
}

func (f *codexRecoveryFixture) recover(request *pb.RecoverSubscriptionRequest) (*pb.RecoverSubscriptionResponse, error) {
	response, err := f.client.RecoverSubscription(context.Background(), subscriptionRequest(f.workerToken, request))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (f *codexRecoveryFixture) references() map[domain.ID]bool {
	f.secrets.mu.Lock()
	defer f.secrets.mu.Unlock()
	out := map[domain.ID]bool{}
	for ref := range f.secrets.values {
		if ref.Owner == f.input.AccountID {
			out[ref.ID] = true
		}
	}
	return out
}

func recoveryCode(err error) domain.Code { return domain.SafeError(rpc.ClientError(err)).Code }

func TestRecoveryCaptureRestoresReadinessWithOneGeneration(t *testing.T) {
	f := newCodexRecoveryFixture(t)
	defer clear(f.original)
	latest := f.latest("rotated")
	defer clear(latest)
	capture := domain.NewID()
	if _, err := f.sync(capture, f.original, latest); err != nil {
		t.Fatal(err)
	}
	_, account := f.record()
	if account.Subscription.RecoveryCapture == nil || account.Subscription.RecoveryCapture.ID != capture || account.Subscription.RecoveryRequired {
		t.Fatal("capture was not recorded against the live lease")
	}
	f.fence()
	read, err := f.read()
	if err != nil || !bytes.Equal(read.Latest, latest) || !bytes.Equal(read.Original, f.original) {
		t.Fatal("original Worker could not read its own capture", err)
	}
	request := domain.NewID()
	response, err := f.recover(f.recoverRequest(request, pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITH_CAPTURE, capture))
	if err != nil || response.Outcome != pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_RESTORED || response.Replayed {
		t.Fatal("verified latest credentials did not restore the account", err)
	}
	_, account = f.record()
	state := account.Subscription
	if state.RecoveryRequired || state.Lease != nil || state.RecoveryCapture != nil || state.RecoverySettlement != nil || state.Generation != request || account.Health != domain.AccountReady {
		t.Fatal("restored account kept recovery state or did not publish the recovery generation")
	}
	refs := f.references()
	if len(refs) != 1 || !refs[request] {
		t.Fatal("recovery did not retire the original generation and the capture", refs)
	}
	// The published generation is the validated latest bundle, delivered by an
	// ordinary Take of a later execution.
	next := takeSubscriptionExecutionFixture(t, f.subscriptionFixture)
	defer clear(next.Bundle)
	if !bytes.Equal(next.Bundle, latest) || next.GenerationId != string(request) {
		t.Fatal("next execution did not receive the single recovered generation")
	}
}

func TestRecoveryWithoutUsableCredentialsDisconnectsAndAllowsLogin(t *testing.T) {
	f := newCodexRecoveryFixture(t)
	defer clear(f.original)
	f.fence()
	request := domain.NewID()
	response, err := f.recover(f.recoverRequest(request, pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITHOUT_CREDENTIAL, ""))
	if err != nil || response.Outcome != pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_DISCONNECTED {
		t.Fatal("proven cleanup without credentials did not disconnect", err)
	}
	_, account := f.record()
	state := account.Subscription
	if account.Connection != nil || account.Health != domain.AccountDisconnected || state.Generation != "" || state.IdentityCommitment != "" || state.RecoveryRequired || state.Lease != nil || state.RecoverySettlement != nil {
		t.Fatal("disconnected recovery kept connection or credential state")
	}
	if len(f.references()) != 0 {
		t.Fatal("disconnected recovery left a protected original reference")
	}
	// The existing explicit login is allowed again; recovery never starts it.
	if f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN) == nil {
		t.Fatal("existing login was not allowed after recovery")
	}
}

func TestRecoveryUnusableCaptureDisconnectsAfterProvenCleanup(t *testing.T) {
	f := newCodexRecoveryFixture(t)
	defer clear(f.original)
	latest := f.latest("rotated")
	defer clear(latest)
	capture := domain.NewID()
	if _, err := f.sync(capture, f.original, latest); err != nil {
		t.Fatal(err)
	}
	f.fence()
	// A capture whose protected bytes no longer validate is not usable evidence.
	f.secrets.mu.Lock()
	f.secrets.values[credentials.Ref{Owner: f.input.AccountID, ID: capture, Purpose: credentials.AccountLogin}] = []byte("not a bundle")
	f.secrets.mu.Unlock()
	response, err := f.recover(f.recoverRequest(domain.NewID(), pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITH_CAPTURE, capture))
	if err != nil || response.Outcome != pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_DISCONNECTED {
		t.Fatal("an invalid capture restored readiness", err)
	}
	if len(f.references()) != 0 {
		t.Fatal("an invalid capture left protected references")
	}
}

func TestRecoveryCaptureIsReplayableAndNeverGrantsAuthority(t *testing.T) {
	f := newCodexRecoveryFixture(t)
	defer clear(f.original)
	latest := f.latest("rotated")
	defer clear(latest)
	capture := domain.NewID()
	first, err := f.sync(capture, f.original, latest)
	if err != nil || first.Replayed {
		t.Fatal(err)
	}
	f.secrets.mu.Lock()
	puts := f.secrets.puts
	f.secrets.mu.Unlock()
	again, err := f.sync(capture, f.original, latest)
	if err != nil || !again.Replayed {
		t.Fatal("a lost capture response was not replayed", err)
	}
	f.secrets.mu.Lock()
	unchanged := f.secrets.puts == puts
	f.secrets.mu.Unlock()
	if !unchanged {
		t.Fatal("capture replay staged the credential again")
	}
	_, account := f.record()
	if account.Subscription.Generation != domain.ID(f.lease.GenerationId) || account.Subscription.Lease == nil {
		t.Fatal("a capture changed the generation or the lease")
	}
	// A different identity, an unrelated original or a stale lease revision is refused.
	other := subscriptionTestBundle("another-account", "rotated", time.Now().UTC())
	defer clear(other)
	if _, err := f.sync(domain.NewID(), f.original, other); err == nil || !recoveryBundleRejected(err) {
		t.Fatal("a capture for another provider identity was not positively rejected", err)
	}
	unrelated := f.latest("unrelated")
	defer clear(unrelated)
	if _, err := f.sync(domain.NewID(), unrelated, latest); err == nil || !recoveryBundleRejected(err) {
		t.Fatal("a capture whose original is not the delivered generation was not positively rejected", err)
	}
}

func TestRecoveryCaptureReplacementRetiresThePreviousCapture(t *testing.T) {
	f := newCodexRecoveryFixture(t)
	defer clear(f.original)
	one, two := f.latest("one"), f.latest("two")
	defer clear(one)
	defer clear(two)
	first, second := domain.NewID(), domain.NewID()
	if _, err := f.sync(first, f.original, one); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sync(second, f.original, two); err != nil {
		t.Fatal(err)
	}
	refs := f.references()
	if refs[first] || refs[domain.RecoveryOriginalReference(first)] || !refs[second] || !refs[domain.RecoveryOriginalReference(second)] || !refs[domain.ID(f.lease.GenerationId)] {
		t.Fatal("capture replacement did not retire only the previous capture", refs)
	}
}

func TestRecoveryOrdinaryFinishRetiresOrKeepsTheCaptureWithItsLease(t *testing.T) {
	// A fenced Finish with confirmed cleanup but no native success keeps the lease
	// and therefore the capture.
	f := newCodexRecoveryFixture(t)
	defer clear(f.original)
	latest := f.latest("rotated")
	defer clear(latest)
	capture := domain.NewID()
	if _, err := f.sync(capture, f.original, latest); err != nil {
		t.Fatal(err)
	}
	if _, err := f.finish(f.lease, nil, false, false, true); err != nil {
		t.Fatal(err)
	}
	_, account := f.record()
	if !account.Subscription.RecoveryRequired || account.Subscription.RecoveryCapture == nil || !f.references()[capture] {
		t.Fatal("a fenced Finish retired the capture of its retained lease")
	}
	// A successful Finish releases the lease and retires the capture with it.
	g := newCodexRecoveryFixture(t)
	defer clear(g.original)
	rotated := g.latest("rotated")
	defer clear(rotated)
	captured := domain.NewID()
	if _, err := g.sync(captured, g.original, rotated); err != nil {
		t.Fatal(err)
	}
	if _, err := g.finish(g.lease, rotated, true, true, true); err != nil {
		t.Fatal(err)
	}
	_, account = g.record()
	refs := g.references()
	if account.Subscription.Lease != nil || account.Subscription.RecoveryCapture != nil || refs[captured] || refs[domain.RecoveryOriginalReference(captured)] {
		t.Fatal("a released lease kept its recovery capture", refs)
	}
}

func TestRecoveryBlockedReportsKeepTheFenceAndExposeTheReason(t *testing.T) {
	f := newCodexRecoveryFixture(t)
	defer clear(f.original)
	f.fence()
	before, _ := f.record()
	report := f.recoverRequest(domain.NewID(), pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_BLOCKED, "")
	report.BlockedReason = pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_CREDENTIAL_PROOF_UNAVAILABLE
	response, err := f.recover(report)
	if err != nil || response.Outcome != pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED || response.BlockedReason != report.BlockedReason {
		t.Fatal("legacy evidence without proof did not stay blocked", err)
	}
	record, account := f.record()
	if account.Subscription.RecoveryBlock == nil || account.Subscription.RecoveryBlock.Reason != domain.RecoveryCredentialProofUnavailable || !account.Subscription.RecoveryRequired || account.Subscription.Lease == nil || account.Health != domain.AccountFailed {
		t.Fatal("blocked recovery did not expose its reason and keep the fence")
	}
	if len(f.references()) != 1 {
		t.Fatal("blocked recovery changed protected references")
	}
	// The same reason is observed again without another account revision.
	repeat := f.recoverRequest(domain.NewID(), pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_BLOCKED, "")
	repeat.BlockedReason = report.BlockedReason
	if _, err := f.recover(repeat); err != nil {
		t.Fatal(err)
	}
	if again, _ := f.record(); again.Revision != record.Revision || record.Revision <= before.Revision {
		t.Fatal("an unchanged blocked reason rewrote the account")
	}
}

func TestRecoveryRejectsForeignAndMismatchedEvidence(t *testing.T) {
	f := newCodexRecoveryFixture(t)
	defer clear(f.original)
	latest := f.latest("rotated")
	defer clear(latest)
	capture := domain.NewID()
	if _, err := f.sync(capture, f.original, latest); err != nil {
		t.Fatal(err)
	}
	// A live lease cannot be recovered.
	if _, err := f.recover(f.recoverRequest(domain.NewID(), pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITH_CAPTURE, capture)); recoveryCode(err) != domain.RecoveryRequired {
		t.Fatal("a live lease was recovered", err)
	}
	f.fence()
	cases := map[string]func(*pb.RecoverSubscriptionRequest){
		"unknown capture":     func(r *pb.RecoverSubscriptionRequest) { r.CaptureId = string(domain.NewID()) },
		"replaced generation": func(r *pb.RecoverSubscriptionRequest) { r.GenerationId = string(domain.NewID()) },
		"other lease":         func(r *pb.RecoverSubscriptionRequest) { r.LeaseId = string(domain.NewID()) },
		"other instance":      func(r *pb.RecoverSubscriptionRequest) { r.OriginalInstanceId = string(domain.NewID()) },
		"stale revision":      func(r *pb.RecoverSubscriptionRequest) { r.Mutation.ExpectedRevision++ },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			request := f.recoverRequest(domain.NewID(), pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITH_CAPTURE, capture)
			mutate(request)
			before, _ := f.record()
			if _, err := f.recover(request); err == nil {
				t.Fatal("mismatched evidence was accepted")
			}
			if after, account := f.record(); after.Revision != before.Revision || !account.Subscription.RecoveryRequired || account.Subscription.Lease == nil {
				t.Fatal("rejected evidence changed the fenced account")
			}
		})
	}
	// Credentials cannot be declared absent while a validated capture exists.
	if _, err := f.recover(f.recoverRequest(domain.NewID(), pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITHOUT_CREDENTIAL, "")); recoveryCode(err) != domain.RecoveryRequired {
		t.Fatal("a captured credential was declared absent", err)
	}
	// A report that names the capture without cleanup proof carries no capture.
	bad := f.recoverRequest(domain.NewID(), pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_BLOCKED, capture)
	bad.BlockedReason = pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_CLEANUP_UNCONFIRMED
	if _, err := f.recover(bad); connect.CodeOf(err) == 0 {
		t.Fatal("a blocked report carried capture evidence", err)
	}
}

func TestRecoveryOldPeersKeepTheExistingFence(t *testing.T) {
	f := newSubscriptionFixture(t)
	original := f.login()
	defer clear(original)
	lease := takeSubscriptionExecutionFixture(t, f)
	defer clear(lease.Bundle)
	rf := &codexRecoveryFixture{subscriptionFixture: f, original: original, lease: lease}
	latest := rf.latest("rotated")
	defer clear(latest)
	if _, err := rf.sync(domain.NewID(), original, latest); recoveryCode(err) != domain.RecoveryRequired || recoveryBundleRejected(err) {
		t.Fatal("a Worker without the capability synchronized a capture, or its refusal claimed a bundle rejection", err)
	}
	rf.fence()
	if _, err := rf.recover(rf.recoverRequest(domain.NewID(), pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITHOUT_CREDENTIAL, "")); recoveryCode(err) != domain.RecoveryRequired {
		t.Fatal("a Worker without the capability recovered a lease", err)
	}
	if _, err := rf.read(); err == nil {
		t.Fatal("a Worker without the capability read a capture")
	}
	if _, a := rf.record(); !a.Subscription.RecoveryRequired || a.Subscription.Lease == nil {
		t.Fatal("an older peer lost its recovery fence")
	}
}

func TestRecoveryReplayAndCrashPointsNeverDuplicateMutation(t *testing.T) {
	f := newCodexRecoveryFixture(t)
	defer clear(f.original)
	latest := f.latest("rotated")
	defer clear(latest)
	capture := domain.NewID()
	if _, err := f.sync(capture, f.original, latest); err != nil {
		t.Fatal(err)
	}
	f.fence()
	request := domain.NewID()
	report := f.recoverRequest(request, pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITH_CAPTURE, capture)
	// Crash after the credential decision: vault retirement fails, the checkpoint
	// keeps the fence and no execution or login can be admitted.
	f.secrets.mu.Lock()
	f.secrets.deleteError = errors.New("simulated vault outage")
	f.secrets.mu.Unlock()
	if _, err := f.recover(report); err == nil {
		t.Fatal("incomplete vault retirement was reported as settled")
	}
	_, account := f.record()
	state := account.Subscription
	if !state.RecoveryRequired || state.RecoverySettlement == nil || state.RecoverySettlement.Request != request || state.Lease != nil || state.Generation != request {
		t.Fatal("incomplete retirement lost its durable checkpoint")
	}
	r, _ := f.record()
	if _, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH})); err == nil {
		t.Fatal("a lifecycle operation was admitted while original references remained")
	}
	f.secrets.mu.Lock()
	f.secrets.deleteError = nil
	puts := f.secrets.puts
	f.secrets.mu.Unlock()
	// A foreign request cannot complete another request's checkpoint.
	foreign := f.recoverRequest(domain.NewID(), pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITH_CAPTURE, capture)
	if _, err := f.recover(foreign); err == nil {
		t.Fatal("a foreign request completed the retirement checkpoint")
	}
	response, err := f.recover(report)
	if err != nil || response.Outcome != pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_RESTORED || response.Replayed {
		t.Fatal("the original request did not resume the checkpoint", err)
	}
	f.secrets.mu.Lock()
	staged := f.secrets.puts != puts
	f.secrets.mu.Unlock()
	if staged {
		t.Fatal("resuming the checkpoint staged another generation")
	}
	// A lost response replays the exact receipt without any mutation.
	record, _ := f.record()
	f.secrets.mu.Lock()
	puts, deletes := f.secrets.puts, f.secrets.deletes
	f.secrets.mu.Unlock()
	replay, err := f.recover(report)
	if err != nil || !replay.Replayed || replay.Outcome != response.Outcome {
		t.Fatal("exact receipt replay changed", err)
	}
	after, _ := f.record()
	f.secrets.mu.Lock()
	changed := f.secrets.puts != puts || f.secrets.deletes != deletes
	f.secrets.mu.Unlock()
	if after.Revision != record.Revision || changed {
		t.Fatal("receipt replay mutated the account or the vault")
	}
	// The same identity with different evidence is a conflict, not a second recovery.
	different := f.recoverRequest(request, pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITHOUT_CREDENTIAL, "")
	if _, err := f.recover(different); err == nil {
		t.Fatal("a reused recovery request identity with different evidence was accepted")
	}
}

func TestRecoveryRequiresSettledMembersAndCredentialUsers(t *testing.T) {
	f := newCodexRecoveryFixture(t)
	defer clear(f.original)
	f.fence()
	member := domain.NewID()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.recovery-member", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.Subscription.Lease.Shared = true
		a.Subscription.Members = []domain.SubscriptionMember{{LeaseID: domain.ID(f.lease.LeaseId), OperationID: a.Subscription.Lease.OperationID, Revision: 1, State: domain.SubscriptionMemberRetained, AdmittedAt: time.Now().UTC()}, {LeaseID: member, OperationID: domain.NewID(), Revision: 2, State: domain.SubscriptionMemberRunning, AdmittedAt: time.Now().UTC()}}
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		t.Fatal(err)
	}
	withoutMembers := f.recoverRequest(domain.NewID(), pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITHOUT_CREDENTIAL, "")
	response, err := f.recover(withoutMembers)
	if err != nil || response.Outcome != pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED || response.BlockedReason != pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_MEMBERS_UNSETTLED {
		t.Fatal("owner retired before its members settled", err)
	}
	if _, account := f.record(); account.Subscription.Lease == nil || account.Subscription.RecoveryBlock == nil || account.Subscription.RecoveryBlock.Reason != domain.RecoveryMembersUnsettled || len(f.references()) != 1 {
		t.Fatal("unsettled members changed the fenced owner")
	}
	settled := f.recoverRequest(domain.NewID(), pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITHOUT_CREDENTIAL, "", domain.ID(f.lease.LeaseId), member)
	response, err = f.recover(settled)
	if err != nil || response.Outcome != pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_DISCONNECTED {
		t.Fatal("settled members did not allow owner retirement", err)
	}
}

func TestRecoveryRotationOffersEveryFencedAccount(t *testing.T) {
	rows := make([]store.Record, 6)
	for i := range rows {
		rows[i] = store.Record{ID: domain.NewID()}
	}
	seen := map[domain.ID]int{}
	cursor := 0
	for tick := 0; tick < 3; tick++ {
		var offered []store.Record
		offered, _, cursor = mergeRecoveryRows(nil, rows, 0, cursor)
		if len(offered) > subscriptionWatchLaneBound {
			t.Fatal("rotation exceeded the lane bound", len(offered))
		}
		for _, r := range offered {
			seen[r.ID]++
		}
	}
	for _, r := range rows {
		if seen[r.ID] == 0 {
			t.Fatal("a fenced account starved behind the lane bound")
		}
	}
	// Ordinary rows keep room: recovery never takes the whole bound from them.
	others := []store.Record{{ID: domain.NewID()}, {ID: domain.NewID()}, {ID: domain.NewID()}, {ID: domain.NewID()}}
	offered, _, _ := mergeRecoveryRows(others, rows, 0, 0)
	if len(offered) != subscriptionWatchLaneBound {
		t.Fatal("merged rows exceeded the lane bound", len(offered))
	}
	if offered[len(offered)-1].ID != rows[0].ID || offered[0].ID != others[0].ID {
		t.Fatal("recovery rows lost their reserved slot", len(offered))
	}
}

func TestRecoveryRotationOffersEveryOrdinaryAccount(t *testing.T) {
	recovery := []store.Record{{ID: domain.NewID()}}
	others := make([]store.Record, 7)
	for i := range others {
		others[i] = store.Record{ID: domain.NewID()}
	}
	seen := map[domain.ID]int{}
	otherCursor, recoveryCursor := 0, 0
	for tick := 0; tick < 4; tick++ {
		var offered []store.Record
		offered, otherCursor, recoveryCursor = mergeRecoveryRows(others, recovery, otherCursor, recoveryCursor)
		if len(offered) > subscriptionWatchLaneBound {
			t.Fatal("rotation exceeded the lane bound", len(offered))
		}
		for _, r := range offered {
			seen[r.ID]++
		}
	}
	for _, r := range others {
		if seen[r.ID] == 0 {
			t.Fatal("an ordinary account starved behind a persistent recovery row")
		}
	}
	if seen[recovery[0].ID] != 4 {
		t.Fatal("the recovery row lost its reserved slot", seen[recovery[0].ID])
	}
	// Without fenced accounts nothing is truncated.
	all, _, _ := mergeRecoveryRows(others, nil, 0, 0)
	if len(all) != len(others) {
		t.Fatal("ordinary rows were truncated without recovery rows", len(all))
	}
}

func TestRecoveryOfferRequiresOriginalWorkerAndCapability(t *testing.T) {
	machine := domain.ID("machine")
	supported := domain.Machine{WorkerCapabilities: []domain.WorkerCapability{domain.ManagedCodexSubscriptionsV1, domain.ManagedCodexRecoveryV1}}
	legacy := domain.Machine{WorkerCapabilities: []domain.WorkerCapability{domain.ManagedCodexSubscriptionsV1}}
	fenced := domain.Account{Type: domain.SubscriptionAccount, SubscriptionService: domain.SubscriptionChatGPT, Subscription: &domain.SubscriptionState{RecoveryRequired: true, Lease: &domain.SubscriptionLease{Action: domain.SubscriptionExecute, MachineID: machine}}}
	if !chatGPTRecoveryOffer(fenced, supported, machine) {
		t.Fatal("a fenced Execute lease was not offered to its original Worker")
	}
	if chatGPTRecoveryOffer(fenced, supported, "other") || chatGPTRecoveryOffer(fenced, legacy, machine) {
		t.Fatal("recovery was offered to another Worker or an older peer")
	}
	login := fenced
	login.Subscription = &domain.SubscriptionState{RecoveryRequired: true, Lease: &domain.SubscriptionLease{Action: domain.SubscriptionLogin, MachineID: machine}}
	if chatGPTRecoveryOffer(login, supported, machine) {
		t.Fatal("a fenced login lease entered the execution recovery lane")
	}
	settling := domain.Account{Type: domain.SubscriptionAccount, SubscriptionService: domain.SubscriptionChatGPT, Subscription: &domain.SubscriptionState{RecoveryRequired: true, RecoverySettlement: &domain.SubscriptionRecoverySettlement{Machine: machine}}}
	if !chatGPTRecoveryOffer(settling, supported, machine) || chatGPTRecoveryOffer(settling, supported, "other") {
		t.Fatal("a committed settlement was not offered only to its original Worker")
	}
}

func (f *codexRecoveryFixture) watchOffersAccount() bool {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := f.client.WatchSubscription(ctx, subscriptionRequest(f.workerToken, &pb.WatchSubscriptionRequest{MachineId: string(f.input.MachineID), InstanceId: string(f.instance)}))
	if err != nil {
		f.t.Fatal(err)
	}
	defer stream.Close()
	for stream.Receive() {
		return stream.Msg().Account != nil
	}
	return false
}

func TestRecoveryWatchOffersFencedExecuteLeaseToItsOriginalWorker(t *testing.T) {
	f := newCodexRecoveryFixture(t)
	defer clear(f.original)
	if f.watchOffersAccount() {
		t.Fatal("a healthy execution lease was offered for recovery")
	}
	f.fence()
	if !f.watchOffersAccount() {
		t.Fatal("the original Worker was not offered its fenced ChatGPT Execute lease")
	}
	// Without the negotiated capability the older peer keeps the existing fence.
	if _, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.recovery-capability-lost", nil, func(tx *store.Tx) (any, error) {
		mr, machine, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		machine.WorkerCapabilities = []domain.WorkerCapability{domain.ManagedCodexSubscriptionsV1}
		return tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
	}); err != nil {
		t.Fatal(err)
	}
	if f.watchOffersAccount() {
		t.Fatal("a Worker without the recovery capability was offered a fenced lease")
	}
}

func TestRecoveryEvidenceBindsTheOriginalWorkerDevice(t *testing.T) {
	f := newCodexRecoveryFixture(t)
	defer clear(f.original)
	f.fence()
	view := func(principal domain.Principal, machine, instance, original domain.ID) error {
		ctx := domain.WithPrincipal(context.Background(), principal)
		return f.service.Store.Read(ctx, func(tx *store.Tx) error {
			_, _, err := f.service.recoveryLeaseView(ctx, tx, f.input.AccountID, domain.ID(f.lease.LeaseId), machine, instance, original, true)
			return err
		})
	}
	var lease domain.SubscriptionLease
	_, account := f.record()
	lease = *account.Subscription.Lease
	owner := domain.Principal{Type: domain.WorkerDevice, DeviceID: lease.DeviceID, MachineID: lease.MachineID}
	if err := view(owner, lease.MachineID, lease.InstanceID, lease.InstanceID); err != nil {
		t.Fatal("the original Worker device was refused", err)
	}
	for name, principal := range map[string]domain.Principal{
		"other device":  {Type: domain.WorkerDevice, DeviceID: domain.NewID(), MachineID: lease.MachineID},
		"other machine": {Type: domain.WorkerDevice, DeviceID: lease.DeviceID, MachineID: domain.NewID()},
		"client device": {Type: domain.ClientDevice, DeviceID: lease.DeviceID},
	} {
		if err := view(principal, lease.MachineID, lease.InstanceID, lease.InstanceID); err == nil {
			t.Fatal("foreign evidence was accepted:", name)
		}
	}
	// A replacement instance may only claim the lease through the original instance.
	if err := view(owner, lease.MachineID, lease.InstanceID, domain.NewID()); err == nil {
		t.Fatal("evidence naming a foreign original instance was accepted")
	}
}

// Legacy retained authentication has no unused original: only a fenced lease may
// capture the latest bundle alone, and the account is restored from it.
func TestRecoveryLegacyAuthenticationCapturesOnlyTheLatestBundle(t *testing.T) {
	f := newCodexRecoveryFixture(t)
	defer clear(f.original)
	latest := f.latest("rotated")
	defer clear(latest)
	if _, err := f.sync(domain.NewID(), nil, latest); err == nil || recoveryBundleRejected(err) {
		t.Fatal("a live lease captured without its unused original, or the refusal claimed the bundle was rejected", err)
	}
	f.fence()
	capture := domain.NewID()
	if _, err := f.sync(capture, nil, latest); err != nil {
		t.Fatal(err)
	}
	read, err := f.read()
	if err != nil || !bytes.Equal(read.Latest, latest) || len(read.Original) != 0 {
		t.Fatal("legacy capture did not read back only the latest bundle", err)
	}
	unrelated := subscriptionTestBundle("another-account", "rotated", time.Now().UTC())
	defer clear(unrelated)
	if _, err := f.sync(domain.NewID(), nil, unrelated); err == nil || !recoveryBundleRejected(err) {
		t.Fatal("a legacy capture changed the provider identity without a positive rejection", err)
	}
	notRefresh := subscriptionTestBundle("fixture-account", "stale", time.Now().UTC().Add(-48*time.Hour))
	defer clear(notRefresh)
	if _, err := f.sync(domain.NewID(), nil, notRefresh); err == nil || !recoveryBundleRejected(err) {
		t.Fatal("a legacy bundle that is not a refresh of the delivered generation was not positively rejected", err)
	}
	if _, err := f.sync(domain.NewID(), nil, []byte("not a bundle")); err == nil || !recoveryBundleRejected(err) {
		t.Fatal("an unparsable legacy bundle was not positively rejected", err)
	}
	request := domain.NewID()
	response, err := f.recover(f.recoverRequest(request, pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITH_CAPTURE, capture))
	if err != nil || response.Outcome != pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_RESTORED {
		t.Fatal("legacy capture did not restore the account", err)
	}
	if refs := f.references(); len(refs) != 1 || !refs[request] {
		t.Fatal("legacy recovery left protected references", refs)
	}
}

func TestRecoveryReadWithoutCaptureReportsTheClosedAbsence(t *testing.T) {
	f := newCodexRecoveryFixture(t)
	defer clear(f.original)
	f.fence()
	_, err := f.read()
	if err == nil || domain.SafeError(rpc.ClientError(err)).Cause != subscriptionRecoveryCaptureAbsentCause {
		t.Fatal("a lease without a capture did not report the closed absence", err)
	}
}

// recoveryBundleRejected reports whether a refusal is the distinct positive
// rejection of the presented bundle itself.
func recoveryBundleRejected(err error) bool {
	return domain.SafeError(rpc.ClientError(err)).Cause == domain.SubscriptionRecoveryBundleRejectedCause
}
