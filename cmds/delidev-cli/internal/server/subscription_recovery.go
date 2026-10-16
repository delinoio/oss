// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/google/uuid"
)

// Automatic recovery of a fenced managed ChatGPT EXECUTE lease.
//
// The original Worker synchronizes a protected capture of the latest validated
// bundle before it deletes transient authentication, and later settles the lease
// under a recovery request identity that is distinct from the original Take and
// Finish requests. The capture grants no readiness, Take, quota access or
// redistribution. Settlement commits its credential decision, retires the
// original protected references completely and only then restores readiness or
// settles the account disconnected, so no execution or login is admitted while an
// original reference remains.

// subscriptionRecoveryPhase is the structured-log phase of every recovery event.
const subscriptionRecoveryPhase = "subscription-recovery"

// maxRecoveryMembers bounds the member settlement list of one recovery request.
// Member admission enforces the same bound, so every valid owner fits.
const maxRecoveryMembers = domain.MaxSubscriptionMembers

// subscriptionRecoveryCaptureAbsentCause marks a read of a lease that has no
// capture, so a Worker can distinguish legacy evidence from a transport failure.
const subscriptionRecoveryCaptureAbsentCause = "subscription_recovery_capture_absent"

func subscriptionRecoveryCaptureAbsent() *domain.Error {
	problem := domain.Fail(domain.NotFound, "The lease has no recovery capture.", "Use the retained original evidence; no credential is fabricated.")
	problem.Cause = subscriptionRecoveryCaptureAbsentCause
	return problem
}

// subscriptionRecoveryBundleRejected is the distinct positive result of a capture
// whose bundle itself is refused. The code stays recovery-required; the cause is
// what lets a Worker tell it from every binding or authorization refusal.
func subscriptionRecoveryBundleRejected() *domain.Error {
	problem := subscriptionDenied()
	problem.Cause = domain.SubscriptionRecoveryBundleRejectedCause
	return problem
}

// recoveryPhaseID derives the identifier of the intermediate settlement commit
// from the recovery request identity. The request identity itself is reserved
// for the final receipt, so a replay of the request can never observe a partly
// retired account as complete.
func recoveryPhaseID(request domain.ID) domain.ID {
	parsed, err := uuid.Parse(string(request))
	if err != nil {
		return ""
	}
	parsed[15] ^= 0x55
	return domain.ID(parsed.String())
}

// recoveryBlockID derives the identifier of one observation-only block write.
func recoveryBlockID(request domain.ID, reason domain.SubscriptionRecoveryBlockedReason) domain.ID {
	parsed, err := uuid.Parse(string(request))
	if err != nil {
		return ""
	}
	parsed[15] ^= 0xaa
	return domain.ID(parsed.String())
}

type recoveryCaptureInput struct {
	Account, Lease, Machine, OriginalInstance, Generation domain.ID
	Revision                                              uint64
	Commitment                                            string
}

// recoveryInput is the idempotency identity of one recovery request. It binds
// the original lease evidence and the report, but not the current instance (a
// replacement instance of the original Worker resends the same request) or the
// member list (settled once, when the credential decision is committed).
type recoveryInput struct {
	Account, Lease, Machine, OriginalInstance, Generation domain.ID
	Revision                                              uint64
	Report                                                pb.SubscriptionRecoveryReport
	Capture                                               domain.ID `json:",omitempty"`
}

type recoveryReceipt struct {
	ID      domain.ID                          `json:"id"`
	Outcome domain.SubscriptionRecoveryOutcome `json:"outcome"`
}

func recoveryBlockedReason(value pb.SubscriptionRecoveryBlockedReason) (domain.SubscriptionRecoveryBlockedReason, bool) {
	switch value {
	case pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_CREDENTIAL_PROOF_UNAVAILABLE:
		return domain.RecoveryCredentialProofUnavailable, true
	case pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_CLEANUP_UNCONFIRMED:
		return domain.RecoveryCleanupUnconfirmed, true
	case pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_OWNERSHIP_EVIDENCE_INVALID:
		return domain.RecoveryOwnershipEvidenceInvalid, true
	case pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_MEMBERS_UNSETTLED:
		return domain.RecoveryMembersUnsettled, true
	case pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_CREDENTIAL_USERS_ACTIVE:
		return domain.RecoveryCredentialUsersActive, true
	case pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_ACTIVE_OWNER:
		return domain.RecoveryActiveOwner, true
	}
	return "", false
}

func recoveryBlockedReasonProto(reason domain.SubscriptionRecoveryBlockedReason) pb.SubscriptionRecoveryBlockedReason {
	switch reason {
	case domain.RecoveryCredentialProofUnavailable:
		return pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_CREDENTIAL_PROOF_UNAVAILABLE
	case domain.RecoveryCleanupUnconfirmed:
		return pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_CLEANUP_UNCONFIRMED
	case domain.RecoveryOwnershipEvidenceInvalid:
		return pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_OWNERSHIP_EVIDENCE_INVALID
	case domain.RecoveryMembersUnsettled:
		return pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_MEMBERS_UNSETTLED
	case domain.RecoveryCredentialUsersActive:
		return pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_CREDENTIAL_USERS_ACTIVE
	case domain.RecoveryActiveOwner:
		return pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_ACTIVE_OWNER
	}
	return pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_UNSPECIFIED
}

func recoveryOutcomeProto(outcome domain.SubscriptionRecoveryOutcome) pb.SubscriptionRecoveryOutcome {
	switch outcome {
	case domain.RecoveryRestored:
		return pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_RESTORED
	case domain.RecoveryDisconnected:
		return pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_DISCONNECTED
	}
	return pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_UNSPECIFIED
}

// recoveryMachineSupported reports whether the machine negotiated the recovery
// capability together with managed ChatGPT subscriptions.
func recoveryMachineSupported(machine domain.Machine) bool {
	return slices.Contains(machine.WorkerCapabilities, domain.ManagedCodexRecoveryV1) && slices.Contains(machine.WorkerCapabilities, domain.ManagedCodexSubscriptionsV1)
}

// subscriptionIdentityCommitment is the keyed commitment of the immutable
// provider identity of one validated bundle.
func (s *Service) subscriptionIdentityCommitment(identity subscription.Identity) string {
	identityJSON, _ := json.Marshal(struct{ Account, User string }{identity.Account, identity.User})
	defer clear(identityJSON)
	return s.accountCommitment(s.Identity.ServerID, identityJSON)
}

// recoveryLeaseView resolves the exact managed ChatGPT EXECUTE lease a recovery
// operation addresses. The lease stays bound to its original server, account,
// lease and generation, machine, Worker device and original instance. A fenced
// lease (recovery required) may be addressed by any current instance of that
// original Worker, so a replacement instance can reconcile only the retained
// original journals; a live lease only by its own current instance.
func (s *Service) recoveryLeaseView(ctx context.Context, tx *store.Tx, account, lease, machine, instance, original domain.ID, fenced bool) (store.Record, domain.Account, error) {
	if err := tx.Authorize(); err != nil {
		return store.Record{}, domain.Account{}, err
	}
	r, a, err := subscriptionAccount(tx, account, 0)
	if err != nil {
		return r, a, err
	}
	state := a.Subscription
	if state == nil || state.Lease == nil {
		return r, a, subscriptionDenied()
	}
	l := state.Lease
	actor, _ := domain.PrincipalFrom(ctx)
	if actor.Type != domain.WorkerDevice || actor.DeviceID != l.DeviceID || actor.MachineID != machine {
		return r, a, subscriptionDenied()
	}
	if l.ID != lease || l.MachineID != machine || l.InstanceID != original || l.Action != domain.SubscriptionExecute || l.Generation != state.Generation || state.RecoveryRequired != fenced {
		return r, a, subscriptionDenied()
	}
	if !fenced && (instance != original || l.Epoch != s.subscriptionServerEpoch()) {
		return r, a, subscriptionDenied()
	}
	if err := currentInstance(tx, machine, instance); err != nil {
		return r, a, err
	}
	_, m, err := activeMachine(tx, machine)
	if err != nil {
		return r, a, err
	}
	if !recoveryMachineSupported(m) {
		return r, a, subscriptionDenied()
	}
	return r, a, nil
}

func (s *Service) SyncSubscriptionRecoveryCapture(ctx context.Context, req *connect.Request[pb.SyncSubscriptionRecoveryCaptureRequest]) (*connect.Response[pb.SyncSubscriptionRecoveryCaptureResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	defer clear(req.Msg.Original)
	defer clear(req.Msg.Latest)
	m := req.Msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	for _, id := range []string{req.Msg.LeaseId, req.Msg.MachineId, req.Msg.InstanceId, req.Msg.GenerationId} {
		if err := domain.ID(id).Validate(); err != nil {
			return nil, rpc.Error(err, c)
		}
	}
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	if len(req.Msg.Latest) == 0 || len(req.Msg.Original) > subscription.MaxBundle || len(req.Msg.Latest) > subscription.MaxBundle {
		return nil, rpc.Error(subscription.Invalid(), c)
	}
	account, lease, machine, instance, generation := domain.ID(m.Id), domain.ID(req.Msg.LeaseId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), domain.ID(req.Msg.GenerationId)
	capture := domain.ID(m.RequestId)
	// The original instance is the lease's own; a replacement instance names it
	// through the stored lease and is verified below.
	commitment := s.accountCommitment(capture, append(append([]byte{}, req.Msg.Original...), req.Msg.Latest...))
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	var original domain.Account
	var originalInstance domain.ID
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		_, a, err := subscriptionAccount(tx, account, 0)
		if err != nil {
			return err
		}
		if a.Subscription == nil || a.Subscription.Lease == nil {
			return subscriptionDenied()
		}
		originalInstance = a.Subscription.Lease.InstanceID
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	input := recoveryCaptureInput{account, lease, machine, originalInstance, generation, m.ExpectedRevision, commitment}
	result, replayed, err := s.Store.Replay(ctx, capture, "subscription.recovery-capture", input)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if !replayed {
		var fenced bool
		err = s.Store.Read(ctx, func(tx *store.Tx) error {
			_, a, err := subscriptionAccount(tx, account, 0)
			if err != nil {
				return err
			}
			fenced = a.Subscription != nil && a.Subscription.RecoveryRequired
			_, original, err = s.recoveryLeaseView(ctx, tx, account, lease, machine, instance, originalInstance, fenced)
			return err
		})
		if err != nil {
			s.logger.WarnContext(ctx, "subscription_recovery_capture_refused", "phase", subscriptionRecoveryPhase, "account_id", account, "lease_id", lease, "code", domain.SafeError(err).Code)
			return nil, rpc.Error(err, c)
		}
		state := original.Subscription
		if state.Generation != generation || state.Lease.Revision != m.ExpectedRevision || state.RecoveryCapture != nil && state.RecoveryCapture.ID == capture {
			return nil, rpc.Error(subscriptionDenied(), c)
		}
		vault, err := s.secrets()
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		// The captured original must be the exact bundle this generation delivered,
		// and the latest bundle must keep the account's immutable identity.
		stored, err := vault.Get(ctx, credentials.Ref{Owner: account, ID: state.Generation, Purpose: credentials.AccountLogin})
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		// Retained legacy authentication has no unused original: the latest bundle
		// must then equal the delivered generation or be a valid refresh of it. A
		// live lease always carries its original.
		if len(req.Msg.Original) == 0 && !fenced {
			clear(stored)
			return nil, rpc.Error(subscription.Invalid(), c)
		}
		matches := len(req.Msg.Original) == 0 || bytes.Equal(stored, req.Msg.Original)
		if matches && !bytes.Equal(stored, req.Msg.Latest) {
			matches = subscription.Refreshed(stored, req.Msg.Latest) == nil
		}
		clear(stored)
		if !matches {
			s.logger.WarnContext(ctx, "subscription_recovery_capture_bundle_rejected", "phase", subscriptionRecoveryPhase, "account_id", account, "lease_id", lease, "reason", "not-delivered-generation-or-refresh")
			return nil, rpc.Error(subscriptionRecoveryBundleRejected(), c)
		}
		_, identity, err := subscription.Parse(req.Msg.Latest)
		if err != nil {
			s.logger.WarnContext(ctx, "subscription_recovery_capture_bundle_rejected", "phase", subscriptionRecoveryPhase, "account_id", account, "lease_id", lease, "reason", "unparsable")
			return nil, rpc.Error(subscriptionRecoveryBundleRejected(), c)
		}
		identityCommitment := s.subscriptionIdentityCommitment(identity)
		if identityCommitment != state.IdentityCommitment {
			s.logger.WarnContext(ctx, "subscription_recovery_capture_bundle_rejected", "phase", subscriptionRecoveryPhase, "account_id", account, "lease_id", lease, "reason", "identity-changed")
			return nil, rpc.Error(subscriptionRecoveryBundleRejected(), c)
		}
		entry := domain.SubscriptionRecoveryCapture{ID: capture, Lease: lease, Generation: generation, IdentityCommitment: identityCommitment, SyncedAt: time.Now().UTC().Truncate(time.Millisecond), OriginalCaptured: len(req.Msg.Original) != 0}
		// Stage the protected references before the metadata that names them. A
		// crash leaves only unreferenced vault entries that no lane can grant.
		if entry.OriginalCaptured {
			if _, err := vault.Put(ctx, credentials.Ref{Owner: account, ID: entry.OriginalID(), Purpose: credentials.AccountLogin}, req.Msg.Original); err != nil {
				return nil, rpc.Error(err, c)
			}
		}
		if _, err := vault.Put(ctx, credentials.Ref{Owner: account, ID: capture, Purpose: credentials.AccountLogin}, req.Msg.Latest); err != nil {
			return nil, rpc.Error(err, c)
		}
		var previous *domain.SubscriptionRecoveryCapture
		result, err = s.Store.Mutate(ctx, capture, "subscription.recovery-capture", input, func(tx *store.Tx) (any, error) {
			r, a, err := s.recoveryLeaseView(ctx, tx, account, lease, machine, instance, originalInstance, fenced)
			if err != nil {
				return nil, err
			}
			st := a.Subscription
			if st.Generation != generation || st.Lease.Revision != m.ExpectedRevision {
				return nil, subscriptionDenied()
			}
			previous = st.RecoveryCapture
			st.RecoveryCapture = &entry
			if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
				return nil, err
			}
			return accountReceipt{ID: r.ID}, nil
		})
		if err != nil {
			s.retireRecoveryReferences(ctx, vault, account, &entry)
			return nil, rpc.Error(err, c)
		}
		if previous != nil && previous.ID != capture {
			s.retireRecoveryReferences(ctx, vault, account, previous)
		}
		s.logger.InfoContext(ctx, "subscription_recovery_capture_synced", "phase", subscriptionRecoveryPhase, "account_id", account, "lease_id", lease, "capture_id", capture, "replaced", previous != nil)
	}
	return connect.NewResponse(&pb.SyncSubscriptionRecoveryCaptureResponse{CaptureId: string(capture), Replayed: result.Replayed}), nil
}

// retireRecoveryReferences removes the protected references of one capture. A
// failed deletion is only logged: the references are unreferenced vault entries
// that never grant authority and are retired by the next settlement.
func (s *Service) retireRecoveryReferences(ctx context.Context, vault accountSecrets, account domain.ID, capture *domain.SubscriptionRecoveryCapture) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	ids := []domain.ID{capture.ID}
	if capture.OriginalCaptured {
		ids = append(ids, capture.OriginalID())
	}
	for _, id := range ids {
		if err := vault.Delete(cleanup, credentials.Ref{Owner: account, ID: id, Purpose: credentials.AccountLogin}); err != nil {
			s.logger.WarnContext(ctx, "subscription_recovery_capture_retirement_deferred", "phase", subscriptionRecoveryPhase, "account_id", account, "capture_id", capture.ID, "code", domain.SafeError(err).Code)
		}
	}
}

func (s *Service) ReadSubscriptionRecoveryCapture(ctx context.Context, req *connect.Request[pb.ReadSubscriptionRecoveryCaptureRequest]) (*connect.Response[pb.ReadSubscriptionRecoveryCaptureResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	for _, id := range []string{req.Msg.AccountId, req.Msg.LeaseId, req.Msg.MachineId, req.Msg.InstanceId, req.Msg.OriginalInstanceId, req.Msg.GenerationId} {
		if err := domain.ID(id).Validate(); err != nil {
			return nil, rpc.Error(err, c)
		}
	}
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	account, lease := domain.ID(req.Msg.AccountId), domain.ID(req.Msg.LeaseId)
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	var state *domain.SubscriptionState
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := s.recoveryLeaseView(ctx, tx, account, lease, domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), domain.ID(req.Msg.OriginalInstanceId), true)
		state = a.Subscription
		return err
	})
	if err != nil {
		s.logger.WarnContext(ctx, "subscription_recovery_capture_read_refused", "phase", subscriptionRecoveryPhase, "account_id", account, "lease_id", lease, "code", domain.SafeError(err).Code)
		return nil, rpc.Error(err, c)
	}
	if state.Generation != domain.ID(req.Msg.GenerationId) {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	entry := state.RecoveryCapture
	if entry == nil {
		return nil, rpc.Error(subscriptionRecoveryCaptureAbsent(), c)
	}
	if entry.Lease != lease || entry.Generation != state.Generation || entry.IdentityCommitment != state.IdentityCommitment {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	vault, err := s.secrets()
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	latest, original, err := readRecoveryCapture(ctx, vault, account, entry)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "subscription_recovery_capture_read", "phase", subscriptionRecoveryPhase, "account_id", account, "lease_id", lease, "capture_id", entry.ID)
	return connect.NewResponse(&pb.ReadSubscriptionRecoveryCaptureResponse{CaptureId: string(entry.ID), Original: original, Latest: latest}), nil
}

func readRecoveryCapture(ctx context.Context, vault accountSecrets, account domain.ID, entry *domain.SubscriptionRecoveryCapture) (latest, original []byte, err error) {
	latest, err = vault.Get(ctx, credentials.Ref{Owner: account, ID: entry.ID, Purpose: credentials.AccountLogin})
	if err != nil {
		return nil, nil, err
	}
	if !entry.OriginalCaptured {
		return latest, nil, nil
	}
	original, err = vault.Get(ctx, credentials.Ref{Owner: account, ID: entry.OriginalID(), Purpose: credentials.AccountLogin})
	if err != nil {
		clear(latest)
		return nil, nil, err
	}
	return latest, original, nil
}

// recoveryDecision is the credential decision of one recovery request.
type recoveryDecision struct {
	outcome    domain.SubscriptionRecoveryOutcome
	generation domain.ID
}

// recordRecoveryBlock stores the closed reason a recovery attempt left the lease
// protected. The write is observation only and is skipped when the reason is
// unchanged, so a retrying Worker adds no receipts.
func (s *Service) recordRecoveryBlock(ctx context.Context, account, lease domain.ID, request domain.ID, reason domain.SubscriptionRecoveryBlockedReason) error {
	unchanged := false
	if err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := accountFromTx(tx, account, 0)
		if err != nil {
			return err
		}
		st := a.Subscription
		unchanged = st != nil && st.Lease != nil && st.Lease.ID == lease && st.RecoveryBlock != nil && st.RecoveryBlock.Reason == reason
		return nil
	}); err != nil || unchanged {
		return err
	}
	_, err := s.Store.Mutate(ctx, recoveryBlockID(request, reason), "subscription.recovery-block", struct {
		Account, Lease domain.ID
		Reason         domain.SubscriptionRecoveryBlockedReason
	}{account, lease, reason}, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, account, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.Lease == nil || st.Lease.ID != lease || !st.RecoveryRequired {
			return nil, subscriptionDenied()
		}
		st.RecoveryBlock = &domain.SubscriptionRecoveryBlock{Reason: reason, Request: request, ObservedAt: time.Now().UTC().Truncate(time.Millisecond)}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return accountReceipt{ID: r.ID}, nil
	})
	return err
}

func (s *Service) RecoverSubscription(ctx context.Context, req *connect.Request[pb.RecoverSubscriptionRequest]) (*connect.Response[pb.RecoverSubscriptionResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	for _, id := range []string{req.Msg.LeaseId, req.Msg.MachineId, req.Msg.InstanceId, req.Msg.OriginalInstanceId, req.Msg.GenerationId} {
		if err := domain.ID(id).Validate(); err != nil {
			return nil, rpc.Error(err, c)
		}
	}
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	invalid := domain.Fail(domain.InvalidArgument, "The recovery report is not valid.", "Send a closed report with its matching evidence.")
	var blocked domain.SubscriptionRecoveryBlockedReason
	capture := domain.ID(req.Msg.CaptureId)
	switch req.Msg.Report {
	case pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITH_CAPTURE:
		if capture.Validate() != nil || req.Msg.BlockedReason != pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_UNSPECIFIED {
			return nil, rpc.Error(invalid, c)
		}
	case pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITHOUT_CREDENTIAL:
		if capture != "" || req.Msg.BlockedReason != pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_UNSPECIFIED {
			return nil, rpc.Error(invalid, c)
		}
	case pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_BLOCKED:
		reason, ok := recoveryBlockedReason(req.Msg.BlockedReason)
		if !ok || capture != "" {
			return nil, rpc.Error(invalid, c)
		}
		blocked = reason
	default:
		return nil, rpc.Error(invalid, c)
	}
	if len(req.Msg.SettledMemberLeaseIds) > maxRecoveryMembers {
		return nil, rpc.Error(invalid, c)
	}
	members := make([]domain.ID, 0, len(req.Msg.SettledMemberLeaseIds))
	for _, id := range req.Msg.SettledMemberLeaseIds {
		if domain.ID(id).Validate() != nil {
			return nil, rpc.Error(invalid, c)
		}
		members = append(members, domain.ID(id))
	}
	slices.Sort(members)
	if len(slices.Compact(slices.Clone(members))) != len(members) {
		return nil, rpc.Error(invalid, c)
	}
	account, request := domain.ID(m.Id), domain.ID(m.RequestId)
	lease, machine, instance := domain.ID(req.Msg.LeaseId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId)
	originalInstance, generation := domain.ID(req.Msg.OriginalInstanceId), domain.ID(req.Msg.GenerationId)
	input := recoveryInput{Account: account, Lease: lease, Machine: machine, OriginalInstance: originalInstance, Generation: generation, Revision: m.ExpectedRevision, Report: req.Msg.Report, Capture: capture}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	respond := func(replayed bool, outcome pb.SubscriptionRecoveryOutcome, reason pb.SubscriptionRecoveryBlockedReason) (*connect.Response[pb.RecoverSubscriptionResponse], error) {
		r, err := s.accountRecord(ctx, account)
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		return connect.NewResponse(&pb.RecoverSubscriptionResponse{Account: rpc.Resource(r), Replayed: replayed, Outcome: outcome, BlockedReason: reason}), nil
	}
	// A report that settles nothing is observation only and has no receipt.
	if blocked != "" {
		if err := s.checkRecoveryEvidence(ctx, c, account, lease, machine, instance, originalInstance, generation, m.ExpectedRevision); err != nil {
			return nil, err
		}
		if err := s.recordRecoveryBlock(ctx, account, lease, request, blocked); err != nil {
			return nil, rpc.Error(err, c)
		}
		s.logger.WarnContext(ctx, "subscription_recovery_blocked", "phase", subscriptionRecoveryPhase, "account_id", account, "lease_id", lease, "request_id", request, "reason", blocked, "reporter", "worker")
		return respond(false, pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED, req.Msg.BlockedReason)
	}
	result, replayed, err := s.Store.Replay(ctx, request, "subscription.recover", input)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if replayed {
		var receipt recoveryReceipt
		if err := json.Unmarshal(result.Data, &receipt); err != nil || !receipt.Outcome.Valid() {
			return nil, rpc.Error(subscriptionDenied(), c)
		}
		s.logger.InfoContext(ctx, "subscription_recovery_replayed", "phase", subscriptionRecoveryPhase, "account_id", account, "lease_id", lease, "request_id", request, "outcome", receipt.Outcome)
		return respond(true, recoveryOutcomeProto(receipt.Outcome), pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_UNSPECIFIED)
	}
	var original domain.Account
	var resume *domain.SubscriptionRecoverySettlement
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		_, a, err := subscriptionAccount(tx, account, 0)
		if err != nil {
			return err
		}
		original = a
		st := a.Subscription
		if st != nil && st.RecoverySettlement != nil {
			// The credential decision was already committed. Only the original
			// request, from the original Worker device, may complete its retirement.
			settlement := st.RecoverySettlement
			actor, _ := domain.PrincipalFrom(ctx)
			if settlement.Request != request || settlement.Lease != lease || settlement.Machine != machine || actor.Type != domain.WorkerDevice || actor.DeviceID != settlement.Device {
				return subscriptionDenied()
			}
			if err := currentInstance(tx, machine, instance); err != nil {
				return err
			}
			_, machineValue, err := activeMachine(tx, machine)
			if err != nil {
				return err
			}
			if !recoveryMachineSupported(machineValue) {
				return subscriptionDenied()
			}
			resume = settlement
			return nil
		}
		_, _, err = s.recoveryLeaseView(ctx, tx, account, lease, machine, instance, originalInstance, true)
		return err
	})
	if err != nil {
		s.logger.WarnContext(ctx, "subscription_recovery_refused", "phase", subscriptionRecoveryPhase, "account_id", account, "lease_id", lease, "request_id", request, "code", domain.SafeError(err).Code)
		return nil, rpc.Error(err, c)
	}
	vault, err := s.secrets()
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if resume == nil {
		state := original.Subscription
		if state.Generation != generation || state.Lease.Revision != m.ExpectedRevision {
			return nil, rpc.Error(subscriptionDenied(), c)
		}
		// Shared-owner members and dependent credential users settle before the
		// owner retires; neither is retired or adopted by this request.
		if reason := recoveryUnsettled(state, members); reason != "" {
			if err := s.recordRecoveryBlock(ctx, account, lease, request, reason); err != nil {
				return nil, rpc.Error(err, c)
			}
			s.logger.WarnContext(ctx, "subscription_recovery_blocked", "phase", subscriptionRecoveryPhase, "account_id", account, "lease_id", lease, "request_id", request, "reason", reason, "reporter", "server", "members", len(state.Members), "settled_members", len(members))
			return respond(false, pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED, recoveryBlockedReasonProto(reason))
		}
		decision, err := s.recoveryDecision(ctx, vault, account, state, input)
		if err != nil {
			s.logger.WarnContext(ctx, "subscription_recovery_refused", "phase", subscriptionRecoveryPhase, "account_id", account, "lease_id", lease, "request_id", request, "code", domain.SafeError(err).Code)
			return nil, rpc.Error(err, c)
		}
		if decision.outcome == domain.RecoveryRestored {
			if err := s.publishRecoveredGeneration(ctx, vault, account, state, request); err != nil {
				return nil, rpc.Error(err, c)
			}
		}
		if err := s.commitRecoverySettlement(ctx, c, account, request, input, instance, originalInstance, decision); err != nil {
			return nil, err
		}
		s.logger.InfoContext(ctx, "subscription_recovery_decision_committed", "phase", subscriptionRecoveryPhase, "account_id", account, "lease_id", lease, "request_id", request, "outcome", decision.outcome, "members", len(members))
		resume = &domain.SubscriptionRecoverySettlement{Request: request, Lease: lease, Machine: machine, Outcome: decision.outcome, Generation: decision.generation}
	}
	outcome, err := s.finishRecoverySettlement(ctx, vault, account, request, input, resume.Outcome)
	if err != nil {
		s.logger.WarnContext(ctx, "subscription_recovery_retirement_pending", "phase", subscriptionRecoveryPhase, "account_id", account, "lease_id", lease, "request_id", request, "outcome", resume.Outcome, "code", domain.SafeError(err).Code)
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "subscription_recovery_settled", "phase", subscriptionRecoveryPhase, "account_id", account, "lease_id", lease, "request_id", request, "outcome", outcome)
	return respond(false, recoveryOutcomeProto(outcome), pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_UNSPECIFIED)
}

// checkRecoveryEvidence validates a request that settles nothing against the
// exact fenced lease or an already committed settlement.
func (s *Service) checkRecoveryEvidence(ctx context.Context, correlation string, account, lease, machine, instance, originalInstance, generation domain.ID, revision uint64) error {
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := s.recoveryLeaseView(ctx, tx, account, lease, machine, instance, originalInstance, true)
		if err != nil {
			return err
		}
		if a.Subscription.Generation != generation || a.Subscription.Lease.Revision != revision {
			return subscriptionDenied()
		}
		return nil
	})
	if err != nil {
		return rpc.Error(err, correlation)
	}
	return nil
}

// recoveryUnsettled returns the closed reason that keeps the owner from
// retiring, or the empty string when every member and dependent user settled.
func recoveryUnsettled(state *domain.SubscriptionState, settled []domain.ID) domain.SubscriptionRecoveryBlockedReason {
	expected := make([]domain.ID, 0, len(state.Members))
	for _, member := range state.Members {
		expected = append(expected, member.LeaseID)
	}
	slices.Sort(expected)
	if !slices.Equal(expected, settled) {
		return domain.RecoveryMembersUnsettled
	}
	if len(protectedGenerations(state)) != 0 || state.ServerQuotaActive() || state.ServerCreditActive() {
		return domain.RecoveryCredentialUsersActive
	}
	return ""
}

// recoveryDecision validates the report against the stored capture and decides
// whether the latest credentials are positively usable. A capture that cannot
// be positively validated is treated as unusable, never as proof of readiness.
func (s *Service) recoveryDecision(ctx context.Context, vault accountSecrets, account domain.ID, state *domain.SubscriptionState, input recoveryInput) (recoveryDecision, error) {
	capture := state.RecoveryCapture
	if input.Report == pb.SubscriptionRecoveryReport_SUBSCRIPTION_RECOVERY_REPORT_CLEANUP_WITHOUT_CREDENTIAL {
		// A stored capture holds validated credentials; the Worker must account for
		// them and cannot declare the credentials absent.
		if capture != nil {
			return recoveryDecision{}, subscriptionDenied()
		}
		return recoveryDecision{outcome: domain.RecoveryDisconnected}, nil
	}
	if capture == nil || capture.ID != input.Capture || capture.Lease != state.Lease.ID || capture.Generation != state.Generation {
		return recoveryDecision{}, subscriptionDenied()
	}
	latest, original, err := readRecoveryCapture(ctx, vault, account, capture)
	if err != nil {
		return recoveryDecision{}, err
	}
	defer clear(latest)
	defer clear(original)
	outcome, err := s.recoveredOutcome(ctx, vault, account, state, capture, latest, original)
	if err != nil {
		return recoveryDecision{}, err
	}
	return recoveryDecision{outcome: outcome}, nil
}

// recoveredOutcome re-validates the captured bundle against the account's
// immutable identity and stored generation. Only a bundle that opens, keeps the
// identity and is the original or a valid refresh of it restores readiness. A
// bundle that is positively invalid, or a stored generation that is positively
// absent or unrecoverable, disconnects the account. Any other vault failure is
// returned unchanged: an unavailable vault must keep the recovery fence so a later
// attempt can decide, and must never be read as proof that the credential is bad.
func (s *Service) recoveredOutcome(ctx context.Context, vault accountSecrets, account domain.ID, state *domain.SubscriptionState, capture *domain.SubscriptionRecoveryCapture, latest, original []byte) (domain.SubscriptionRecoveryOutcome, error) {
	_, identity, err := subscription.Parse(latest)
	if err != nil || s.subscriptionIdentityCommitment(identity) != state.IdentityCommitment || capture.IdentityCommitment != state.IdentityCommitment {
		return domain.RecoveryDisconnected, nil
	}
	stored, err := vault.Get(ctx, credentials.Ref{Owner: account, ID: state.Generation, Purpose: credentials.AccountLogin})
	if err != nil {
		if positivelyUnusableCredential(err) {
			s.logger.WarnContext(ctx, "subscription_recovery_generation_unusable", "phase", subscriptionRecoveryPhase, "account_id", account, "generation_id", state.Generation, "code", domain.SafeError(err).Code)
			return domain.RecoveryDisconnected, nil
		}
		s.logger.WarnContext(ctx, "subscription_recovery_generation_read_failed", "phase", subscriptionRecoveryPhase, "account_id", account, "generation_id", state.Generation, "code", domain.SafeError(err).Code)
		return "", err
	}
	defer clear(stored)
	if capture.OriginalCaptured && !bytes.Equal(stored, original) {
		return domain.RecoveryDisconnected, nil
	}
	if !bytes.Equal(stored, latest) && subscription.Refreshed(stored, latest) != nil {
		return domain.RecoveryDisconnected, nil
	}
	return domain.RecoveryRestored, nil
}

// positivelyUnusableCredential reports whether a vault read proves the stored
// credential itself is gone or cannot be recovered. Every other failure is an
// infrastructure error that proves nothing about the credential.
func positivelyUnusableCredential(err error) bool {
	switch domain.SafeError(err).Code {
	case domain.NotFound, domain.RecoveryRequired:
		return true
	}
	return false
}

// publishRecoveredGeneration stages the one immutable generation a restored
// recovery publishes. The reference is named by the recovery request, so a
// replay stages the same generation and never a second one.
func (s *Service) publishRecoveredGeneration(ctx context.Context, vault accountSecrets, account domain.ID, state *domain.SubscriptionState, request domain.ID) error {
	latest, err := vault.Get(ctx, credentials.Ref{Owner: account, ID: state.RecoveryCapture.ID, Purpose: credentials.AccountLogin})
	if err != nil {
		return err
	}
	defer clear(latest)
	_, err = vault.Put(ctx, credentials.Ref{Owner: account, ID: request, Purpose: credentials.AccountLogin}, latest)
	return err
}

// commitRecoverySettlement commits the credential decision. The account keeps
// its recovery fence; only the final settlement restores readiness.
func (s *Service) commitRecoverySettlement(ctx context.Context, correlation string, account, request domain.ID, input recoveryInput, instance, originalInstance domain.ID, decision recoveryDecision) error {
	_, err := s.Store.Mutate(ctx, recoveryPhaseID(request), "subscription.recover-decision", input, func(tx *store.Tx) (any, error) {
		r, a, err := s.recoveryLeaseView(ctx, tx, account, input.Lease, input.Machine, instance, originalInstance, true)
		if err != nil {
			return nil, err
		}
		state := a.Subscription
		lease := *state.Lease
		if state.Generation != input.Generation || lease.Revision != input.Revision {
			return nil, subscriptionDenied()
		}
		actor, _ := domain.PrincipalFrom(ctx)
		settlement := &domain.SubscriptionRecoverySettlement{Request: request, Lease: lease.ID, Machine: lease.MachineID, Device: actor.DeviceID, Outcome: decision.outcome, StartedAt: time.Now().UTC().Truncate(time.Millisecond)}
		state.Lease = nil
		state.Members = nil
		state.RecoveryCapture = nil
		state.RecoveryBlock = nil
		state.AutomaticCreditConsent = nil
		state.PaidCredits = nil
		if decision.outcome == domain.RecoveryRestored {
			settlement.Generation = request
			state.Generation = request
			state.OwnerMachineID = lease.MachineID
		}
		if state.Observation != nil && state.Observation.Generation == input.Generation {
			switch state.Observation.Phase {
			case domain.SubscriptionObservationQueued:
				state.Observation.Phase = domain.SubscriptionObservationFailed
				state.Observation.ErrorCode = domain.Conflict
			case domain.SubscriptionObservationSending:
				if state.Observation.Action == domain.SubscriptionQuota {
					state.Observation.Phase = domain.SubscriptionObservationFailed
					state.Observation.ErrorCode = domain.Unavailable
					state.QuotaState = domain.ObservationFailed
				} else {
					state.Observation.Phase = domain.SubscriptionObservationUncertain
				}
			}
		}
		if decision.outcome == domain.RecoveryDisconnected {
			a.Connection = nil
			a.Health = domain.AccountDisconnected
			a.Validation = nil
			a.Catalog = nil
			a.Quota = nil
			a.ConfirmedExhausted = false
		}
		state.RecoverySettlement = settlement
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return accountReceipt{ID: r.ID}, nil
	})
	if err != nil {
		return rpc.Error(err, correlation)
	}
	return nil
}

// finishRecoverySettlement retires the original protected references completely
// and then commits the final receipt that restores readiness or settles the
// account disconnected. A failed or interrupted deletion leaves the fence and
// the checkpoint, so the original request resumes the same retirement.
func (s *Service) finishRecoverySettlement(ctx context.Context, vault accountSecrets, account, request domain.ID, input recoveryInput, outcome domain.SubscriptionRecoveryOutcome) (domain.SubscriptionRecoveryOutcome, error) {
	var removed int
	var err error
	if outcome == domain.RecoveryRestored {
		removed, err = retireAccountLoginReferencesExcept(ctx, vault, account, request)
	} else {
		removed, err = removeAccountLoginReferences(ctx, vault, account)
	}
	if err != nil {
		return outcome, err
	}
	_, err = s.Store.Mutate(ctx, request, "subscription.recover", input, func(tx *store.Tx) (any, error) {
		if err := tx.Authorize(); err != nil {
			return nil, err
		}
		r, a, err := subscriptionAccount(tx, account, 0)
		if err != nil {
			return nil, err
		}
		state := a.Subscription
		settlement := state.RecoverySettlement
		if settlement == nil || settlement.Request != request || settlement.Outcome != outcome {
			return nil, subscriptionDenied()
		}
		state.RecoverySettlement = nil
		state.RecoveryRequired = false
		state.RecoveryBlock = nil
		if outcome == domain.RecoveryRestored {
			if a.Health != domain.AccountRevoked {
				a.Health = domain.AccountReady
			}
		} else {
			state.Generation = ""
			state.IdentityCommitment = ""
			state.PaidCredits = nil
			state.ResetCredits = nil
			state.QuotaObservedAt = nil
			state.QuotaState = domain.ObservationUnknown
			state.SpendControlReached = nil
			state.SpendControlObservedAt = nil
			if state.Observation != nil && (state.Observation.Phase == domain.SubscriptionObservationSending || state.Observation.Phase == domain.SubscriptionObservationUncertain) {
				state.Observation.Phase = domain.SubscriptionObservationRetiredUncertain
			}
			a.Health = domain.AccountDisconnected
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return recoveryReceipt{ID: r.ID, Outcome: outcome}, nil
	})
	if err != nil {
		return outcome, err
	}
	s.logger.InfoContext(ctx, "subscription_recovery_references_retired", "phase", subscriptionRecoveryPhase, "account_id", account, "request_id", request, "outcome", outcome, "references_removed", removed)
	return outcome, nil
}

// retireAccountLoginReferencesExcept removes every unremoved AccountLogin
// reference of owner except keep, completely and without a count limit. A
// reference of another owner or purpose denies the whole retirement before any
// deletion.
func retireAccountLoginReferencesExcept(ctx context.Context, vault accountSecrets, owner, keep domain.ID) (int, error) {
	refs, err := vault.UnremovedReferences(ctx, owner)
	if err != nil {
		return 0, err
	}
	for _, ref := range refs {
		if ref.Owner != owner || ref.Purpose != credentials.AccountLogin || ref.ID.Validate() != nil {
			return 0, subscriptionDenied()
		}
	}
	removed := 0
	for _, ref := range refs {
		if ref.ID == keep {
			continue
		}
		if err := ctx.Err(); err != nil {
			return removed, domain.SafeError(err)
		}
		if err := vault.Delete(ctx, ref); err != nil {
			return removed, err
		}
		removed++
	}
	remaining, err := vault.UnremovedReferences(ctx, owner)
	if err != nil {
		return removed, err
	}
	for _, ref := range remaining {
		if ref.ID != keep {
			return removed, subscriptionDenied()
		}
	}
	return removed, nil
}
