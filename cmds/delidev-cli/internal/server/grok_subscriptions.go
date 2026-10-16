// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// grokIdentityCommitment derives the identity commitment persisted on a Grok
// account's SubscriptionState. The bundle's unkeyed identity key is guessable
// from a user and principal ID, so the stored value is a server-keyed,
// domain-separated HMAC over it, matching the Codex commitment strategy. The
// result is 64 hex characters, accepted by the existing commitment validation
// and by uniqueSubscriptionIdentity. It returns "" for a bundle with no identity.
func (s *Service) grokIdentityCommitment(bundle subscription.GrokBundle) string {
	key := bundle.IdentityKey()
	if key == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(s.Identity.Token))
	mac.Write([]byte("delidev/subscription/grok-identity/v1\x00" + string(s.Identity.ServerID) + "\x00" + key))
	return hex.EncodeToString(mac.Sum(nil))
}

// Bounds for mandatory Grok bookkeeping. A single-document write (a recovery
// fence, a staged-generation retirement) is short; the post-commit removal of
// superseded generations may delete several vault entries.
const (
	grokBookkeepingTimeout = 5 * time.Second
	grokCleanupTimeout     = 30 * time.Second
)

// grokBookkeepingContext returns the context for mandatory Grok bookkeeping. It
// keeps the request's values (correlation and logging) but is detached from its
// cancellation and deadline, is bounded by timeout, and carries owner authority
// instead of the Worker's: a client disconnect, a deadline, or the revocation of
// the very Worker whose request is being settled must not stop the write that
// keeps an unfinished lease from being stranded. Such a write runs only inside
// this server, after the request itself has been authorized.
func grokBookkeepingContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(domain.WithPrincipal(context.WithoutCancel(ctx), domain.Principal{Type: domain.OwnerDevice}), timeout)
}

// Grok Take/Finish carries the lifecycle (login, refresh, logout) of a
// service-native Grok account and, for an execute Take, the managed execution
// lease of grok_execution.go. The credential moves only as the sealed
// subscription.GrokBundle envelope under credentials.AccountLogin, bound to this
// server, the account and one credential generation. The Codex lifecycle keeps
// refusing every Grok account through subscriptionAccount.

// grokLifecycleAction reports the lifecycle actions of the pending-operation
// lease; execution is handled before this check.
func grokLifecycleAction(action domain.SubscriptionAction) bool {
	return action == domain.SubscriptionLogin || action == domain.SubscriptionRefresh || action == domain.SubscriptionLogout
}

func grokUnsupported() *domain.Error {
	return domain.Fail(domain.Unsupported, "This Grok operation is not available.", "Use login, refresh or logout; managed Grok execution needs a Worker that negotiated its capability.")
}

func (s *Service) isGrokSubscription(ctx context.Context, id string) (bool, error) {
	if domain.ID(id).Validate() != nil {
		return false, subscriptionDenied()
	}
	var matched bool
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := accountFromTx(tx, domain.ID(id), 0)
		matched = err == nil && a.Type == domain.SubscriptionAccount && a.SubscriptionService == domain.SubscriptionGrok
		return err
	})
	return matched, err
}

func grokSubscriptionAccount(tx *store.Tx, id domain.ID, revision uint64) (store.Record, domain.Account, error) {
	r, a, err := accountFromTx(tx, id, revision)
	if err != nil {
		return r, a, err
	}
	if a.Type != domain.SubscriptionAccount || a.SubscriptionService != domain.SubscriptionGrok || a.ProviderID != "" {
		return r, a, domain.Fail(domain.Unsupported, "This operation requires a Grok subscription account.", "Select a Grok service account for the Grok lifecycle.")
	}
	return r, a, nil
}

// grokSubscriptionInstallation requires exactly one verified Grok Build
// installation on the selected Runner Device whose version is at or above the
// subscription baseline. The legacy discovery baseline is deliberately not used:
// it admits every release from 1.0.41, while a managed credential may be
// delivered only to an installation that meets the subscription profile. The managed-Grok Worker capability
// (Worker 21) is reserved but not yet declared, so admission is bound to the
// verified installation until that capability activates.
func grokSubscriptionInstallation(tx *store.Tx, machine domain.ID) (domain.Installation, error) {
	_, m, err := activeMachine(tx, machine)
	if err != nil {
		return domain.Installation{}, err
	}
	var found *domain.Installation
	for i := range m.Installations {
		if m.Installations[i].Harness == domain.GrokBuild {
			if found != nil {
				return domain.Installation{}, subscriptionDenied()
			}
			found = &m.Installations[i]
		}
	}
	now := time.Now().UTC()
	if found == nil || !grokDiscoveryObserved(found, now) || !domain.GrokSubscriptionVersionAllowed(found.Version) || !found.ProtocolVerified || found.Protocol == nil || found.Protocol.Protocol != domain.ProtocolFor(domain.GrokBuild) || found.Protocol.State != domain.ProtocolVerified || found.Protocol.Problem != nil || found.Problem != nil {
		// The refusal carries the original phase, installed version and supported
		// baseline as a closed diagnostic; it never exposes native text or paths.
		return domain.Installation{}, grokAdmissionFailure(found, now)
	}
	return *found, nil
}

// grokSubscriptionLeaseView resolves the exact singleton Grok lease for the
// authenticated Worker. Every owner binding (Runner, device, instance, epoch)
// is checked against the stored lease. Installation admission belongs to Take
// alone: harness discovery may replace the Runner Device's installation list at
// any time, and the Worker may already have joined the original native process
// and removed its local credential, so a claimed lease is never re-gated on
// mutable installation metadata and the original owner can always report Finish.
func (s *Service) grokSubscriptionLeaseView(ctx context.Context, tx *store.Tx, id, lease, machine, instance domain.ID) (store.Record, domain.Account, domain.SubscriptionLease, error) {
	r, a, err := grokSubscriptionAccount(tx, id, 0)
	if err != nil {
		return r, a, domain.SubscriptionLease{}, err
	}
	st := a.Subscription
	actor, _ := domain.PrincipalFrom(ctx)
	if st == nil || st.Lease == nil || st.Lease.ID != lease || st.Lease.Shared || len(st.Members) != 0 {
		return r, a, domain.SubscriptionLease{}, subscriptionDenied()
	}
	l := st.Lease
	if actor.Type != domain.WorkerDevice || actor.DeviceID != l.DeviceID || l.MachineID != machine || l.InstanceID != instance || l.Epoch != s.subscriptionServerEpoch() {
		return r, a, domain.SubscriptionLease{}, subscriptionDenied()
	}
	if err := currentInstance(tx, machine, instance); err != nil {
		return r, a, *l, err
	}
	return r, a, *l, nil
}

// grokOriginalTake reports whether a Take addresses exactly the account's
// pending operation: the same operation ID, lifecycle action and Runner Device,
// still queued. Cancellation is classified separately by the caller.
func grokOriginalTake(op *domain.SubscriptionOperation, operation, machine domain.ID, action domain.SubscriptionAction) bool {
	return op != nil && op.ID == operation && op.Action == action && op.MachineID == machine && op.Phase == domain.SubscriptionQueued
}

// grokLifecycleShape reports whether the account can start the action: login
// starts from no credential, refresh and logout require the connected account's
// current generation.
func grokLifecycleShape(a *domain.Account, action domain.SubscriptionAction) bool {
	st := a.Subscription
	if action == domain.SubscriptionLogin {
		return a.Connection == nil && st.Generation == ""
	}
	return a.Connection != nil && st.Generation != ""
}

// grokTakeRefusalTarget returns the pending operation that a refused Take may
// annotate with its admission diagnostic: only the account's own queued,
// uncanceled operation, addressed by the Take's exact operation ID, lifecycle
// action and Runner Device, on an account that is not leased or fenced and
// whose lifecycle shape and initiating actor would otherwise admit the claim.
// Every other Take is refused without leaving a trace on any operation.
func grokTakeRefusalTarget(tx *store.Tx, a domain.Account, operation, machine domain.ID, action domain.SubscriptionAction) *domain.SubscriptionOperation {
	st := a.Subscription
	if st == nil || st.RecoveryRequired || st.Lease != nil || !grokOriginalTake(st.Pending, operation, machine, action) || st.Pending.Canceled {
		return nil
	}
	if !grokLifecycleShape(&a, action) || subscriptionActorValid(tx, st.Pending.Actor) != nil {
		return nil
	}
	return st.Pending
}

// grokSettledEvent is the settlement that one Finish transaction wrote.
type grokSettledEvent struct {
	Account            domain.ID
	Operation          domain.ID
	Action             domain.SubscriptionAction
	State              domain.SubscriptionLoginState
	DiagnosticAttached bool
}

func (s *Service) takeGrokSubscription(ctx context.Context, req *connect.Request[pb.TakeSubscriptionRequest]) (response *connect.Response[pb.TakeSubscriptionResponse], returned error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	action := subscriptionAction(req.Msg.Action)
	if action == domain.SubscriptionExecute {
		// Execution is keyed by the claimed job, not by a pending account
		// operation, and has its own admission (grok_execution.go).
		return s.takeGrokExecution(ctx, req)
	}
	input := struct {
		Account, Lease, Machine, Instance, Operation domain.ID
		Revision                                     uint64
		Action                                       domain.SubscriptionAction
		Actor                                        domain.Principal
	}{domain.ID(m.Id), domain.ID(m.RequestId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), domain.ID(req.Msg.OperationId), m.ExpectedRevision, action, actor}
	if input.Operation.Validate() != nil || action == "" {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	if !grokLifecycleAction(action) {
		s.logger.WarnContext(ctx, "grok_subscription_take_unsupported_action", "account_id", input.Account, "action", action)
		return nil, rpc.Error(grokUnsupported(), c)
	}
	owned := false
	// admitted is the installation verified in the claiming transaction. It is
	// the only installation observation this lease ever uses.
	var admitted domain.Installation
	// refusal is the original diagnostic of an installation refusal, persisted for
	// progress after the refused transaction rolls back.
	var refusal *domain.GrokDiagnostic
	defer func() {
		if returned != nil && owned {
			if err := s.markSubscriptionRecovery(input.Account); err != nil {
				s.logger.Warn("grok_subscription_recovery_unconfirmed", "account_id", input.Account, "operation_id", input.Operation, "code", domain.SafeError(err).Code)
			}
		}
	}()
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	result, err := s.Store.Mutate(ctx, input.Lease, "grok.subscription.take", input, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, input.Account, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.RecoveryRequired {
			return nil, subscriptionDenied()
		}
		if input.Revision > r.Revision {
			return nil, domain.Fail(domain.Conflict, "The account revision has not been observed.", "Retain the original queued operation and its observed revision.")
		}
		if err := currentInstance(tx, input.Machine, input.Instance); err != nil {
			return nil, err
		}
		installation, err := grokSubscriptionInstallation(tx, input.Machine)
		if err != nil {
			if d := domain.GrokErrorDiagnostic(err); d != nil {
				// The installation refusal is returned for every Take, but only a
				// Take that addresses the account's own queued operation exactly
				// may annotate it; any other Take would otherwise attach a false
				// terminal failure to an operation it does not own.
				recordable := grokTakeRefusalTarget(tx, a, input.Operation, input.Machine, action) != nil
				if recordable {
					refusal = d
				}
				// This transaction rolls back, so nothing is persisted here: the
				// field only says that recording is allowed. The outcome is logged
				// by recordGrokTakeRefusal once its own transaction has finished.
				s.logger.WarnContext(ctx, "grok_subscription_take_installation_refused", "account_id", input.Account, "operation_id", input.Operation, "action", action, "refusal_recordable", recordable, "phase", d.Phase, "code", d.Code, "installed_version", d.DetectedVersion, "supported_version", d.SupportedVersion)
			}
			return nil, err
		}
		if err := tx.WorkerUpdateAdmission(input.Machine); err != nil {
			return nil, err
		}
		// An accepted server-owned logout holds the account: new work is refused
		// with a typed unavailable problem, never silently queued behind it.
		if grokAccountLoggingOut(a) {
			s.logger.WarnContext(ctx, "grok_subscription_take_refused_logging_out", "phase", grokLogoutPhase, "account_id", input.Account, "operation_id", input.Operation, "action", action)
			return nil, grokLoggingOutProblem()
		}
		op := st.Pending
		// A missing or replaced original is classified before any field of the
		// current Pending operation, which belongs to someone else's claim.
		if op == nil || op.ID != input.Operation {
			return nil, subscriptionTakeNotAdmitted()
		}
		original := grokOriginalTake(op, input.Operation, input.Machine, action)
		if original && op.Canceled {
			return nil, subscriptionTakeNotAdmitted()
		}
		if st.Lease != nil {
			return nil, domain.Fail(domain.ResourceExhausted, "The selected account is exclusively leased.", "Wait for its original cleanup; never switch accounts automatically.")
		}
		if !original {
			return nil, subscriptionDenied()
		}
		if !grokLifecycleShape(&a, action) {
			return nil, subscriptionDenied()
		}
		if err := subscriptionActorValid(tx, op.Actor); err != nil {
			return nil, err
		}
		op.Phase = domain.SubscriptionClaimed
		// A claim supersedes the provisional record of an earlier refused Take.
		if o := st.ServerOperation; o != nil && o.ID == op.ID && o.State == domain.SubscriptionUnsupported {
			st.ServerOperation = nil
		}
		st.Lease = &domain.SubscriptionLease{ID: input.Lease, OperationID: input.Operation, Revision: r.Revision + 1, Action: action, MachineID: input.Machine, InstanceID: input.Instance, DeviceID: actor.DeviceID, Epoch: s.subscriptionServerEpoch(), Generation: st.Generation, StartedAt: time.Now().UTC()}
		admitted = installation
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, err
	})
	if err != nil {
		if refusal != nil {
			s.recordGrokTakeRefusal(ctx, input.Account, input.Operation, input.Machine, action, refusal)
		}
		return nil, rpc.Error(err, c)
	}
	// A receipt never redistributes a secret; an uncertain first delivery keeps
	// the lease and requires recovery instead of a second native owner. The first
	// response may have been lost after the lease committed, in which case the
	// Worker lacks the generation and lease revision it needs to Finish, so the
	// exact unfinished lease is fenced for recovery before the denial.
	if result.Replayed {
		if err := s.fenceReplayedGrokTake(ctx, input.Account, input.Lease, input.Operation, input.Machine, input.Instance, action, actor); err != nil {
			s.logger.WarnContext(ctx, "grok_subscription_replay_fence_unconfirmed", "account_id", input.Account, "lease_id", input.Lease, "operation_id", input.Operation, "code", domain.SafeError(err).Code)
		}
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	owned = true
	var generation domain.ID
	var leaseRevision uint64
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, _, view, err := s.grokSubscriptionLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance)
		if err == nil {
			generation, leaseRevision = view.Generation, view.Revision
		}
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	var bundle []byte
	if generation != "" {
		vault, err := s.secrets()
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		bundle, err = vault.Get(ctx, credentials.Ref{Owner: input.Account, ID: generation, Purpose: credentials.AccountLogin})
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		// Only this server's exact, unrebound envelope may cross the boundary.
		opened, err := subscription.OpenGrokBundle(bundle, s.Identity.ServerID, input.Account, generation)
		if err != nil {
			clear(bundle)
			s.logger.WarnContext(ctx, "grok_subscription_bundle_invalid", "account_id", input.Account, "lease_id", input.Lease, "operation_id", input.Operation)
			return nil, rpc.Error(err, c)
		}
		opened.Clear()
	}
	// Authorization after vault I/O prevents revocation or cancellation from
	// winning during Get.
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		_, a, _, err := s.grokSubscriptionLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance)
		if err != nil || a.Subscription.RecoveryRequired {
			return subscriptionDenied()
		}
		op := a.Subscription.Pending
		if op == nil || op.Canceled || subscriptionActorValid(tx, op.Actor) != nil {
			return subscriptionDenied()
		}
		return nil
	})
	if err != nil {
		clear(bundle)
		return nil, rpc.Error(err, c)
	}
	installationJSON, _ := json.Marshal(admitted)
	s.logger.InfoContext(ctx, "grok_subscription_lease_granted", "account_id", input.Account, "lease_id", input.Lease, "operation_id", input.Operation, "action", action, "bundle_delivered", len(bundle) != 0)
	return connect.NewResponse(&pb.TakeSubscriptionResponse{LeaseId: string(input.Lease), GenerationId: string(generation), Bundle: bundle, LeaseRevision: leaseRevision, InstallationJson: installationJSON}), nil
}

// retireUnpublishedGrokGeneration removes the generation a Finish staged when
// that Finish aborted, so a failed or racing publication (a revoked Worker, a
// concurrent cancellation, a store failure) does not leave a second credential
// beside the current one. The account is read first and the generation is
// removed only when the account document provably does not reference it: a
// commit that succeeded before its caller saw an error keeps its generation.
// An unreadable account, an unexpected state or a failed removal keeps the
// generation, which stays vault-owned and is never granted (Take reads only the
// referenced generation); the next cleanup or the logout cleanup removes it,
// and the abort already fenced the account for recovery. The write is bounded
// and carries no request cancellation or authority (see grokBookkeepingContext),
// so neither a client disconnect nor the revocation that aborted the Finish can
// skip it.
// The caller holds the account gate.
func (s *Service) retireUnpublishedGrokGeneration(ctx context.Context, vault accountSecrets, account, lease, generation domain.ID) {
	ctx, cancel := grokBookkeepingContext(ctx, grokBookkeepingTimeout)
	defer cancel()
	unreferenced := false
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := accountFromTx(tx, account, 0)
		if err != nil {
			return err
		}
		unreferenced = a.Subscription != nil && a.Subscription.Generation != generation
		return nil
	})
	if err != nil || !unreferenced {
		s.logger.WarnContext(ctx, "grok_subscription_staged_generation_kept", "account_id", account, "lease_id", lease, "reason", "publication_unproven")
		return
	}
	if err := vault.Delete(ctx, credentials.Ref{Owner: account, ID: generation, Purpose: credentials.AccountLogin}); err != nil {
		s.logger.WarnContext(ctx, "grok_subscription_staged_generation_kept", "account_id", account, "lease_id", lease, "reason", "removal_failed", "code", domain.SafeError(err).Code)
		return
	}
	s.logger.InfoContext(ctx, "grok_subscription_staged_generation_retired", "account_id", account, "lease_id", lease)
}

// fenceReplayedGrokTake marks the still-held original Grok lease
// recovery-required so the unfinished lease is never silently stranded. The
// write is bounded and detached from the request's cancellation and authority
// (see grokBookkeepingContext). The caller holds the account gate. A lease that
// was already finished, replaced, or held by another operation, device or
// instance is left untouched, and the credential is never redistributed.
func (s *Service) fenceReplayedGrokTake(requestCtx context.Context, id, lease, operation, machine, instance domain.ID, action domain.SubscriptionAction, actor domain.Principal) error {
	// The fence is mandatory bookkeeping for a lease that is already claimed, so
	// it must not depend on the replaying request: if that request is canceled or
	// times out while the fence waits or writes, a request-bound write would
	// return without fencing and strand the lease with no RecoveryRequired.
	ctx, cancel := grokBookkeepingContext(requestCtx, grokBookkeepingTimeout)
	defer cancel()
	fenced := false
	_, err := s.Store.Mutate(ctx, domain.NewID(), "grok.subscription.take-replay-fence", struct{ Account, Lease domain.ID }{id, lease}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.RecoveryRequired {
			return struct{}{}, nil
		}
		l := st.Lease
		if l == nil || l.ID != lease || l.OperationID != operation || l.Action != action || l.MachineID != machine || l.InstanceID != instance || l.DeviceID != actor.DeviceID {
			return struct{}{}, nil
		}
		st.RecoveryRequired = true
		a.Health = domain.AccountFailed
		fenced = true
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if err == nil && fenced {
		s.logger.WarnContext(requestCtx, "grok_subscription_replayed_take_fenced", "account_id", id, "lease_id", lease, "operation_id", operation)
	}
	return err
}

func (s *Service) finishGrokSubscription(ctx context.Context, req *connect.Request[pb.FinishSubscriptionRequest]) (response *connect.Response[pb.FinishSubscriptionResponse], returned error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	owned := false
	defer func() {
		if returned != nil && owned {
			if err := s.markSubscriptionRecovery(domain.ID(m.Id)); err != nil {
				s.logger.Warn("grok_subscription_recovery_unconfirmed", "account_id", m.Id, "code", domain.SafeError(err).Code)
			}
		}
	}()
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	// Grok keeps one exclusive singleton lease; member and owner scopes belong to
	// shared execution owners.
	if req.Msg.FinishScope != pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_UNSPECIFIED {
		return nil, rpc.Error(grokUnsupported(), c)
	}
	// The request owns a copy of the sealed credential; clear it on every path.
	defer clear(req.Msg.Bundle)
	if len(req.Msg.Bundle) > subscription.MaxGrokBundleEnvelope {
		return nil, rpc.Error(subscription.GrokBundleInvalid(), c)
	}
	// An execute lease has its own settlement. A lease that is already released
	// falls through so a replayed Finish reaches the shared receipt replay.
	if execute, err := s.grokExecuteLease(ctx, m.Id, req.Msg.LeaseId); err != nil {
		return nil, rpc.Error(err, c)
	} else if execute {
		return s.finishGrokExecution(ctx, req)
	}
	input := subscriptionFinishInput{domain.ID(m.Id), domain.ID(req.Msg.LeaseId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), domain.ID(req.Msg.GenerationId), s.accountCommitment(domain.ID(m.RequestId), req.Msg.Bundle), m.ExpectedRevision, req.Msg.CleanupConfirmed, req.Msg.RefreshConfirmed, req.Msg.Succeeded, domain.SubscriptionFinishLegacy}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	result, replayed, err := s.Store.Replay(ctx, domain.ID(m.RequestId), "grok.subscription.finish", input)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if !replayed {
		var original domain.Account
		err = s.Store.Read(ctx, func(tx *store.Tx) error {
			if err := tx.Authorize(); err != nil {
				return err
			}
			_, original, _, err = s.grokSubscriptionLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance)
			return err
		})
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		state := original.Subscription
		lease := state.Lease
		// A lease whose owner reported unconfirmed private cleanup is fenced for
		// recovery, and only that same owner (the view above binds the Runner
		// Device, Worker device and instance) may report it again.
		if state.RecoveryRequired && !lease.CleanupUnconfirmed || state.Generation != input.Generation || lease.Generation != input.Generation || lease.Revision != input.Revision {
			s.logger.WarnContext(ctx, "grok_subscription_finish_refused", "phase", grokCleanupPhase, "account_id", input.Account, "lease_id", input.Lease, "recovery_owner", state.RecoveryRequired && lease.CleanupUnconfirmed)
			return nil, rpc.Error(subscriptionDenied(), c)
		}
		// The first reported outcome stays authoritative across cleanup retries: a
		// retry confirms the cleanup and can never turn a reported native failure
		// into a success.
		success, refreshed := input.Success, input.Refresh
		if state.RecoveryRequired {
			success, refreshed = lease.FirstFinish.Frozen(input.Success, input.Refresh)
			s.logger.InfoContext(ctx, "grok_subscription_cleanup_retry", "phase", grokCleanupPhase, "account_id", input.Account, "lease_id", input.Lease, "cleanup_confirmed", input.Cleanup, "outcome_frozen", success != input.Success || refreshed != input.Refresh)
		}
		owned = true
		usable := success && input.Cleanup && (lease.Action != domain.SubscriptionRefresh || refreshed)
		if state.Pending != nil && state.Pending.Canceled {
			usable = false
		}
		vault, err := s.secrets()
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		newGeneration := domain.ID(m.RequestId)
		// staged is set before the vault write begins, because a write that
		// reports an error may still have left the staged generation behind. Any
		// abort after that point retires it unless the account references it.
		staged := false
		defer func() {
			if staged && returned != nil {
				s.retireUnpublishedGrokGeneration(ctx, vault, input.Account, input.Lease, newGeneration)
			}
		}()
		var identityCommitment string
		if usable && lease.Action != domain.SubscriptionLogout {
			// The envelope is bound to the generation this Finish creates, so a
			// bundle taken for another account, server or generation is refused.
			bundle, err := subscription.OpenGrokBundle(req.Msg.Bundle, s.Identity.ServerID, input.Account, newGeneration)
			if err != nil {
				s.logger.WarnContext(ctx, "grok_subscription_finish_bundle_rejected", "account_id", input.Account, "lease_id", input.Lease, "operation_id", lease.OperationID)
				return nil, rpc.Error(err, c)
			}
			defer bundle.Clear()
			identityCommitment = s.grokIdentityCommitment(bundle)
			if state.IdentityCommitment != "" && identityCommitment != state.IdentityCommitment {
				s.logger.WarnContext(ctx, "grok_subscription_identity_changed", "account_id", input.Account, "lease_id", input.Lease)
				return nil, rpc.Error(subscriptionDenied(), c)
			}
			if lease.Action == domain.SubscriptionLogin {
				err := s.Store.Read(ctx, func(tx *store.Tx) error {
					return uniqueSubscriptionIdentity(tx, input.Account, identityCommitment)
				})
				if err != nil {
					s.logger.WarnContext(ctx, "grok_subscription_identity_duplicate", "account_id", input.Account, "lease_id", input.Lease, "code", domain.SafeError(err).Code)
					return nil, rpc.Error(err, c)
				}
			}
			if lease.Action == domain.SubscriptionRefresh {
				oldRaw, err := vault.Get(ctx, credentials.Ref{Owner: input.Account, ID: state.Generation, Purpose: credentials.AccountLogin})
				if err != nil {
					return nil, rpc.Error(err, c)
				}
				old, err := subscription.OpenGrokBundle(oldRaw, s.Identity.ServerID, input.Account, state.Generation)
				clear(oldRaw)
				if err == nil {
					err = subscription.GrokRefreshed(old, bundle)
				}
				old.Clear()
				if err != nil {
					return nil, rpc.Error(err, c)
				}
			}
			// Only the exact authenticated Worker lease can stage this freshly
			// produced native bundle. No owner/client import endpoint exists.
			staged = true
			if _, err := vault.Put(ctx, credentials.Ref{Owner: input.Account, ID: newGeneration, Purpose: credentials.AccountLogin}, req.Msg.Bundle); err != nil {
				return nil, rpc.Error(err, c)
			}
		}
		// The superseded generation stays referenced and readable until the
		// account document durably publishes its replacement (or the logout), so a
		// failed or retried publication never points at a deleted generation.
		// committed is the one generation the account references after the commit.
		var committed domain.ID
		// settledVersion is the installed Grok Build version observed while the
		// outcome was settled; it only labels a later cleanup diagnostic.
		var settledVersion string
		// settled describes the outcome the committed transaction wrote. It is
		// logged only after the commit, because the transaction can still fail
		// after the in-memory record changed.
		var settled *grokSettledEvent
		result, err = s.Store.Mutate(ctx, domain.ID(m.RequestId), "grok.subscription.finish", input, func(tx *store.Tx) (any, error) {
			settled = nil
			if err := tx.Authorize(); err != nil {
				return nil, err
			}
			r, a, _, err := s.grokSubscriptionLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance)
			if err != nil {
				return nil, err
			}
			state := a.Subscription
			if state.RecoveryRequired && !state.Lease.CleanupUnconfirmed || state.Generation != input.Generation {
				return nil, subscriptionDenied()
			}
			success, refreshed := input.Success, input.Refresh
			if state.RecoveryRequired {
				success, refreshed = state.Lease.FirstFinish.Frozen(input.Success, input.Refresh)
			}
			// The original operation is captured before a login failure or a
			// settled outcome clears it, so its actor survives for progress.
			var settling *domain.SubscriptionOperation
			canceled := false
			if state.Pending != nil {
				if err := subscriptionActorValid(tx, state.Pending.Actor); err != nil {
					return nil, err
				}
				if state.Pending.Canceled {
					usable, canceled = false, true
				}
				if state.Pending.ID == lease.OperationID {
					captured := *state.Pending
					settling = &captured
				}
			}
			if usable && lease.Action == domain.SubscriptionLogin {
				if err := uniqueSubscriptionIdentity(tx, r.ID, identityCommitment); err != nil {
					return nil, err
				}
			}
			if !input.Cleanup || !usable && lease.Action != domain.SubscriptionLogin {
				// Keep the lease and old generation denied. Neither an error nor a
				// process timeout establishes safe refresh-token redistribution.
				state.RecoveryRequired = true
				a.Health = domain.AccountFailed
				// Unconfirmed private cleanup is retried by the original owner only.
				if !input.Cleanup {
					state.Lease.CleanupUnconfirmed = true
					// A retry arrives here already narrowed to the first report, so the
					// frozen outcome only ever stays or lowers.
					state.Lease.FirstFinish = &domain.SubscriptionFinishOutcome{Succeeded: success, RefreshConfirmed: refreshed}
				}
			} else {
				// A confirmed cleanup by the original owner releases the lease and
				// lifts the recovery fence its unconfirmed report set.
				if state.RecoveryRequired {
					state.RecoveryRequired = false
					if a.Health == domain.AccountFailed {
						a.Health = domain.AccountDisconnected
						if a.Connection != nil {
							a.Health = domain.AccountReady
						}
					}
				}
				state.Lease = nil
				state.Members = nil
				if state.Pending != nil && state.Pending.ID == lease.OperationID {
					state.Pending = nil
				}
				if usable && lease.Action != domain.SubscriptionLogout {
					state.Generation = newGeneration
					state.IdentityCommitment = identityCommitment
					state.OwnerMachineID = lease.MachineID
					if a.Connection == nil {
						a.Connection = &domain.AccountConnection{ID: newGeneration, Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()}
					}
					if state.Pending == nil && a.Health != domain.AccountRevoked {
						a.Health = domain.AccountReady
					}
				} else if lease.Action == domain.SubscriptionLogout {
					a.Connection = nil
					a.Health = domain.AccountDisconnected
					a.Validation = nil
					a.Catalog = nil
					a.Quota = nil
					a.ConfirmedExhausted = false
					state.Generation = ""
					state.IdentityCommitment = ""
				}
			}
			if settling != nil {
				settledVersion = grokInstalledVersion(tx, lease.MachineID)
				settlement := grokSettlement{Operation: settling.ID, Action: lease.Action, Actor: settling.Actor, Epoch: s.subscriptionServerEpoch()}
				// The first failure stays authoritative: a native failure the Worker
				// reported is never replaced by its unconfirmed cleanup, which only
				// fences the account above. An unconfirmed cleanup is the diagnostic
				// only when the operation itself succeeded or was canceled.
				nativeFailed := !success || lease.Action == domain.SubscriptionRefresh && !refreshed
				switch {
				case canceled && input.Cleanup:
					settlement.State = domain.SubscriptionCanceled
				case canceled:
					settlement.State, settlement.Diagnostic = domain.SubscriptionFailed, grokLocalDiagnostic(settledVersion, domain.GrokCleanup, domain.RecoveryRequired)
				case nativeFailed:
					settlement.State, settlement.Diagnostic = domain.SubscriptionFailed, grokLocalDiagnostic(settledVersion, grokActionPhase(lease.Action), domain.Unavailable)
				case !input.Cleanup:
					settlement.State, settlement.Diagnostic = domain.SubscriptionFailed, grokLocalDiagnostic(settledVersion, domain.GrokCleanup, domain.RecoveryRequired)
				default:
					settlement.State = domain.SubscriptionSucceeded
					if lease.Action != domain.SubscriptionLogout {
						settlement.Generation = newGeneration
					}
				}
				if settleGrokOperation(state, settlement, time.Now().UTC()) {
					settled = &grokSettledEvent{Account: r.ID, Operation: settling.ID, Action: lease.Action, State: settlement.State, DiagnosticAttached: state.ServerOperation.GrokDiagnostic != nil}
				}
			}
			if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
				return nil, err
			}
			committed = state.Generation
			return accountReceipt{ID: r.ID}, nil
		})
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		if settled != nil {
			s.logger.InfoContext(ctx, "grok_subscription_operation_settled", "account_id", settled.Account, "operation_id", settled.Operation, "action", settled.Action, "state", settled.State, "diagnostic_attached", settled.DiagnosticAttached)
		}
		if original.Subscription.Pending != nil {
			delete(s.subscriptionProgress, original.Subscription.Pending.ID)
		}
		// Only after the commit are the unreferenced generations removed: the
		// superseded one after a publication, the staged new one after a
		// cancellation that won during vault staging, and every generation after a
		// completed logout. A logout fenced for recovery still references its
		// generation, so that generation is kept. A failed removal is fenced for
		// recovery by the owned-error guard.
		if input.Cleanup {
			cleanupCtx, cancelCleanup := grokBookkeepingContext(ctx, grokCleanupTimeout)
			err := cleanupSubscriptionReferences(cleanupCtx, vault, input.Account, committed, protectedGenerations(original.Subscription)...)
			cancelCleanup()
			if err != nil {
				s.recordGrokCleanupFailure(ctx, input.Account, lease.OperationID, settledVersion, err)
				return nil, rpc.Error(err, c)
			}
		}
	}
	owned = false
	r, err := s.accountRecord(ctx, input.Account)
	if err != nil {
		s.logger.WarnContext(ctx, "grok_subscription_completion_presentation_unavailable", "account_id", input.Account, "lease_id", input.Lease, "replayed", replayed, "code", domain.SafeError(err).Code)
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "grok_subscription_operation_finished", "account_id", input.Account, "lease_id", input.Lease, "cleanup_confirmed", input.Cleanup, "replayed", replayed)
	return connect.NewResponse(&pb.FinishSubscriptionResponse{Account: rpc.Resource(r), Replayed: result.Replayed}), nil
}
