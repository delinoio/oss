// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"slices"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func (s *Service) isClaudeSubscription(ctx context.Context, id string) (bool, error) {
	if domain.ID(id).Validate() != nil {
		return false, subscriptionDenied()
	}
	var matched bool
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := accountFromTx(tx, domain.ID(id), 0)
		matched = err == nil && a.Type == domain.SubscriptionAccount && a.SubscriptionService == domain.SubscriptionClaude
		return err
	})
	return matched, err
}
func claudeSubscriptionAccount(tx *store.Tx, id domain.ID, revision uint64) (store.Record, domain.Account, error) {
	r, a, err := accountFromTx(tx, id, revision)
	// Device-owned lifecycle never adopts a server-token account.
	if err == nil && (a.Type != domain.SubscriptionAccount || a.SubscriptionService != domain.SubscriptionClaude || a.ProviderID != "" || a.Subscription != nil && a.Subscription.ManagedClaude()) {
		err = subscriptionDenied()
	}
	return r, a, err
}
func subscriptionWorkerLane(tx *store.Tx, machine domain.ID) error {
	_, m, err := activeMachine(tx, machine)
	if err != nil {
		return err
	}
	if !slices.Contains(m.WorkerCapabilities, domain.ManagedCodexSubscriptionsV1) && !slices.Contains(m.WorkerCapabilities, domain.NativeClaudeSubscriptionsV1) {
		return subscriptionDenied()
	}
	return nil
}

// claudeNativeCapability proves the Runner Device negotiated the native Claude
// subscription capability. Every device-owned Claude lease depends on it,
// including a direct-startup execution that skips inspected inventory.
func claudeNativeCapability(m domain.Machine) error {
	if !slices.Contains(m.WorkerCapabilities, domain.NativeClaudeSubscriptionsV1) {
		return domain.Fail(domain.Unsupported, "The selected Runner Device does not support Claude subscription authentication.", "Update this Runner Device before starting login or execution.")
	}
	return nil
}

// claudeDirectStartupAdmission is the admission requirement of a claimed
// version-4 direct-startup Claude assignment. The assignment owns its immutable
// executable selection, which the original Worker process validates before any
// input, so the machine-global inspected inventory is not consulted. Only the
// negotiated native-subscription and startup capabilities are required.
func claudeDirectStartupAdmission(tx *store.Tx, machine domain.ID) error {
	_, m, err := activeMachine(tx, machine)
	if err != nil {
		return err
	}
	if err := claudeNativeCapability(m); err != nil {
		return err
	}
	if !slices.Contains(m.WorkerCapabilities, domain.ExecutionStartupV1) {
		return domain.Fail(domain.Unsupported, "The selected Runner Device does not support direct execution startup.", "Update this Runner Device before starting execution.")
	}
	return nil
}

// claudeDirectStartupAssignment reports whether the claimed job is a version-4
// direct-startup Claude execution. Any lookup or decode problem reports false so
// the historical inspected-installation gate keeps its existing refusal; the
// complete assignment fences still run afterwards on the same job.
func claudeDirectStartupAssignment(tx *store.Tx, operation domain.ID, revision uint64) bool {
	jr, err := tx.Get(domain.JobKind, operation)
	if err != nil || jr.Revision != revision {
		return false
	}
	job, err := store.Decode[domain.Job](jr)
	var execution domain.ExecutionJobInput
	if err != nil || job.Type != domain.ExecuteSessionJob || domain.Decode(job.Input, &execution) != nil || execution.Validate() != nil {
		return false
	}
	return execution.Version == 4 && execution.Startup != nil && execution.Configuration.Harness == domain.ClaudeCode
}

func claudeSubscriptionInstallation(tx *store.Tx, machine domain.ID) (domain.Installation, error) {
	_, m, err := activeMachine(tx, machine)
	if err != nil {
		return domain.Installation{}, err
	}
	if err := claudeNativeCapability(m); err != nil {
		return domain.Installation{}, err
	}
	var found *domain.Installation
	for i := range m.Installations {
		if m.Installations[i].Harness == domain.ClaudeCode {
			if found != nil {
				return domain.Installation{}, subscriptionDenied()
			}
			found = &m.Installations[i]
		}
	}
	if found == nil || !domain.HarnessVersionAllowed(domain.ClaudeCode, found.Version) || found.State != domain.InstallationDetected || !found.ProtocolVerified || found.Protocol == nil || found.Protocol.Protocol != domain.ProtocolFor(domain.ClaudeCode) || found.Protocol.State != domain.ProtocolVerified || found.Protocol.Problem != nil || found.Problem != nil || found.ResolvedPath == "" || found.ObservedAt == nil || found.ObservedAt.IsZero() || found.ObservedAt.After(time.Now().UTC().Add(time.Second)) {
		return domain.Installation{}, domain.Fail(domain.Unsupported, "Claude subscription authentication requires a verified, supported Claude Code installation.", "Discover the original installed CLI on the selected Runner Device and update it if the discovered version is below the supported baseline.")
	}
	return *found, nil
}
func claudeActor(ctx context.Context) (domain.Principal, error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice {
		return actor, subscriptionDenied()
	}
	return actor, nil
}
func (s *Service) requestClaudeSubscription(ctx context.Context, req *connect.Request[pb.RequestSubscriptionRequest]) (*connect.Response[pb.RequestSubscriptionResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	actor, err := claudeActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	action := subscriptionAction(req.Msg.Action)
	machine := domain.ID(req.Msg.MachineId)
	if machine.Validate() != nil || req.Msg.DeviceCode || action != domain.SubscriptionLogin && action != domain.SubscriptionRefresh && action != domain.SubscriptionLogout {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Select the original Runner Device to log out a device-owned Claude account.", "Claude sign-in is server-owned. Claude Console/API login and device authentication are not supported."), c)
	}
	input := claudeLifecycleInput{domain.ID(m.Id), machine, m.ExpectedRevision, action, actor}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	requestID := domain.ID(m.RequestId)
	result, err := s.Store.Mutate(ctx, requestID, claudeLifecycleOperation, input, func(tx *store.Tx) (any, error) {
		// Fresh machine-bound sign-in is retired. The rejection runs inside the
		// Mutate closure, after the receipt check, so a request accepted before
		// the retirement still replays through its original receipt.
		if action == domain.SubscriptionLogin || action == domain.SubscriptionRefresh {
			s.logger.WarnContext(ctx, "claude_runner_signin_rejected", "operation_id", m.RequestId, "account_id", m.Id, "machine_id", machine, "action", action, "code", domain.Unsupported)
			return nil, claudeRunnerSignInRetired()
		}
		return s.admitClaudeLifecycle(tx, input, requestID)
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	r, err := s.accountRecord(ctx, input.Account)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "claude_subscription_operation_accepted", "operation_id", m.RequestId, "machine_id", machine, "action", action, "replayed", result.Replayed)
	return connect.NewResponse(&pb.RequestSubscriptionResponse{Account: rpc.Resource(r), OperationId: m.RequestId, Replayed: result.Replayed}), nil
}

// claudeLifecycleOperation is the receipt operation name of a Claude lifecycle
// request. Admission and replay share it so a retained receipt stays valid.
const claudeLifecycleOperation = "claude.subscription.request"

// claudeLifecycleInput is the receipt-bound input of a Claude lifecycle request.
type claudeLifecycleInput struct {
	Account, Machine domain.ID
	Revision         uint64
	Action           domain.SubscriptionAction
	Actor            domain.Principal
}

// claudeRunnerSignInRetired is returned for fresh machine-bound Claude login and
// reauthentication. Claude sign-in is server-owned; only logout of an existing
// device-owned account still admits a Runner-bound request.
func claudeRunnerSignInRetired() error {
	return domain.Fail(domain.Unsupported, "Claude sign-in on a Runner Device is no longer supported.", "Connect a new Claude account from this server instead. Existing device-owned accounts can still run work and log out.")
}

// admitClaudeLifecycle applies one machine-bound Claude lifecycle admission
// inside an open transaction. Production reaches it for logout only; LOGIN and
// REFRESH admission is retained so tests can seed a state that an earlier
// release accepted.
func (s *Service) admitClaudeLifecycle(tx *store.Tx, input claudeLifecycleInput, requestID domain.ID) (any, error) {
	r, a, err := claudeSubscriptionAccount(tx, input.Account, input.Revision)
	if err != nil {
		return nil, err
	}
	if err := subscriptionActorValid(tx, input.Actor); err != nil {
		return nil, err
	}
	if _, err := claudeSubscriptionInstallation(tx, input.Machine); err != nil {
		return nil, err
	}
	if a.Subscription == nil {
		a.Subscription = &domain.SubscriptionState{}
	}
	st := a.Subscription
	// An abandoned queued operation must never block a new request forever.
	expireStaleQueuedClaudeOperation(st, time.Now().UTC())
	if st.OwnerMachineID != "" && st.OwnerMachineID != input.Machine {
		return nil, domain.Fail(domain.Unsupported, "This Claude account belongs to another Runner Device.", "Select its original Runner Device; authentication cannot move between devices.")
	}
	// A live shared Execute owner does not block a Refresh: the queued
	// operation closes admission (the join branch of Take requires no Pending)
	// and its lifecycle Take stays contended until the last runnable member
	// retires the owner. Every other lease holder keeps the request exclusive.
	refreshBehindSharedOwner := input.Action == domain.SubscriptionRefresh && st.SharedOwner() && st.Lease.Action == domain.SubscriptionExecute
	if st.Pending != nil || st.RecoveryRequired || a.Removal != nil || st.Lease != nil && input.Action != domain.SubscriptionLogout && !refreshBehindSharedOwner {
		return nil, subscriptionDenied()
	}
	if input.Action == domain.SubscriptionLogin {
		if a.Connection != nil || st.Generation != "" || st.NativeProfileID != "" {
			return nil, subscriptionDenied()
		}
		st.NativeProfileID = domain.NewID()
		st.OwnerMachineID = input.Machine
	} else if st.NativeProfileID == "" || st.Generation == "" || a.Connection == nil {
		return nil, subscriptionDenied()
	}
	now := time.Now().UTC()
	id := requestID
	st.NativeOperation = &domain.NativeSubscriptionOperation{ID: id, Actor: input.Actor, MachineID: input.Machine, Epoch: s.subscriptionServerEpoch(), Action: input.Action, State: domain.SubscriptionPreparing, StartedAt: now, ExpiresAt: now.Add(claudeOperationWindow)}
	st.Pending = &domain.SubscriptionOperation{ID: id, Action: input.Action, MachineID: input.Machine, Actor: input.Actor, Phase: domain.SubscriptionQueued, QueuedBehindLease: st.Lease != nil}
	if input.Action == domain.SubscriptionLogout {
		a.Health = domain.AccountRevoked
		if err := cancelAccountExecutions(tx, r.ID); err != nil {
			return nil, err
		}
	}
	_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	return accountReceipt{ID: r.ID}, err
}

// claudeOperationWindow is how long a claimed lifecycle operation may run.
const claudeOperationWindow = 15 * time.Minute

// claudeQueuedWindowDeferred reports whether the pending lifecycle operation is
// still queued behind a lease (or is a logout, which must always run) and so has
// not started its window. Such an operation never expires while it waits.
func claudeQueuedWindowDeferred(st *domain.SubscriptionState) bool {
	return st != nil && st.Pending != nil && st.Pending.Phase == domain.SubscriptionQueued && (st.Pending.QueuedBehindLease || st.Pending.Action == domain.SubscriptionLogout)
}

// expireStaleQueuedClaudeOperation retires a queued login or reauthentication
// whose window elapsed before any Worker claimed it. A Take rejects an expired
// operation, so leaving it pending would block every later admission until a
// user canceled it. The retirement mirrors a queued cancellation. It reports
// whether it changed the state; the caller persists it with its own write.
func expireStaleQueuedClaudeOperation(st *domain.SubscriptionState, now time.Time) bool {
	op, o := st.Pending, st.NativeOperation
	if op == nil || o == nil || o.ID != op.ID || op.Phase != domain.SubscriptionQueued || claudeQueuedWindowDeferred(st) || o.ExpiresAt.After(now) {
		return false
	}
	if op.Action != domain.SubscriptionLogin && op.Action != domain.SubscriptionRefresh {
		return false
	}
	o.State = domain.SubscriptionExpired
	st.Pending = nil
	if st.Generation == "" {
		st.NativeProfileID = ""
		st.OwnerMachineID = ""
		st.RecoveryRequired = false
	}
	return true
}
func (s *Service) cancelClaudeSubscription(ctx context.Context, req *connect.Request[pb.CancelSubscriptionRequest]) (*connect.Response[pb.CancelSubscriptionResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	actor, err := claudeActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	var operation domain.ID
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "claude.subscription.cancel", disconnectAccountInput{domain.ID(m.Id), m.ExpectedRevision}, func(tx *store.Tx) (any, error) {
		r, a, err := claudeSubscriptionAccount(tx, domain.ID(m.Id), m.ExpectedRevision)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.Pending == nil || st.NativeOperation == nil || st.NativeOperation.Actor != actor || (st.Pending.Action != domain.SubscriptionLogin && st.Pending.Action != domain.SubscriptionRefresh) {
			return nil, subscriptionDenied()
		}
		operation = st.Pending.ID
		if st.Pending.Phase == domain.SubscriptionQueued {
			st.NativeOperation.State = domain.SubscriptionCanceled
			st.Pending = nil
			if st.Generation == "" {
				st.NativeProfileID = ""
				st.OwnerMachineID = ""
				st.RecoveryRequired = false
			}
		} else {
			st.Pending.Canceled = true
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, err
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.clearClaudeLoginInput(operation)
	r, err := s.accountRecord(ctx, domain.ID(m.Id))
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	return connect.NewResponse(&pb.CancelSubscriptionResponse{Account: rpc.Resource(r), Replayed: result.Replayed}), nil
}
func (s *Service) clearClaudeLoginInput(operation domain.ID) {
	clear(s.claudeLoginCodes[operation])
	delete(s.claudeLoginCodes, operation)
	delete(s.subscriptionProgress, operation)
}
func (s *Service) getClaudeSubscriptionProgress(ctx context.Context, req *connect.Request[pb.GetSubscriptionProgressRequest]) (*connect.Response[pb.GetSubscriptionProgressResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := claudeActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	response := &pb.GetSubscriptionProgressResponse{}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := claudeSubscriptionAccount(tx, domain.ID(req.Msg.AccountId), 0)
		if err != nil {
			return err
		}
		st := a.Subscription
		if st == nil || st.NativeOperation == nil || string(st.NativeOperation.ID) != req.Msg.OperationId || st.NativeOperation.Actor != actor {
			return subscriptionDenied()
		}
		if err := subscriptionActorValid(tx, actor); err != nil {
			return err
		}
		o := st.NativeOperation
		state := o.State
		if st.RecoveryRequired || o.Epoch != s.subscriptionServerEpoch() && (state == domain.SubscriptionPreparing || state == domain.SubscriptionWaiting) {
			state = domain.SubscriptionRecovery
		}
		if !o.ExpiresAt.After(time.Now().UTC()) && (state == domain.SubscriptionPreparing || state == domain.SubscriptionWaiting) && !claudeQueuedWindowDeferred(st) {
			state = domain.SubscriptionExpired
		}
		response.State = loginState(state)
		response.LoginMethod = pb.SubscriptionLoginMethod(o.LoginMethod)
		response.NativeDiagnostic = wireNativeSubscriptionDiagnostic(o.Diagnostic)
		if st.Pending != nil {
			response.Canceled = st.Pending.Canceled
		}
		if state == domain.SubscriptionWaiting && !response.Canceled {
			p := s.subscriptionProgress[o.ID]
			response.Url = p.URL
		}
		if state == domain.SubscriptionSucceeded {
			response.Generation = string(st.Generation)
			response.SuggestedName = "Claude"
		}
		if state == domain.SubscriptionRecovery || state == domain.SubscriptionExpired {
			s.clearClaudeLoginInput(o.ID)
		}
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	return connect.NewResponse(response), nil
}
func wireNativeSubscriptionDiagnostic(d *domain.NativeSubscriptionDiagnostic) *pb.NativeSubscriptionDiagnostic {
	if d == nil {
		return nil
	}
	return &pb.NativeSubscriptionDiagnostic{DetectedVersion: d.DetectedVersion, RequiredVersion: d.RequiredVersion, Phase: pb.NativeSubscriptionDiagnosticPhase(d.Phase), Code: string(d.Code), CorrelationId: string(d.CorrelationID)}
}

// claudeLifecycleLeaseHeld proves that the stored lease is the claimed
// lifecycle lease of the pending operation. A Refresh may now queue behind a
// live shared Execute owner; that owner's identifier must never read, advance or
// consume the queued operation's native progress or login code.
func claudeLifecycleLeaseHeld(st *domain.SubscriptionState) bool {
	return st.Lease != nil && st.Pending != nil && !st.Lease.Shared && st.Lease.OperationID == st.Pending.ID && st.Lease.Action == st.Pending.Action
}

// claudeLeaseDirectStartup reports whether the stored owner lease and every
// retained member belong to v4 direct-startup assignments. One historical or
// lifecycle holder keeps the whole set behind the inspected-installation gate.
func claudeLeaseDirectStartup(st *domain.SubscriptionState) bool {
	if st == nil || st.Lease == nil || !st.Lease.DirectStartup || st.Lease.Action != domain.SubscriptionExecute {
		return false
	}
	for _, member := range st.Members {
		if !member.DirectStartup {
			return false
		}
	}
	return true
}
func (s *Service) claudeSubscriptionLease(ctx context.Context, tx *store.Tx, id, lease, machine, instance domain.ID) (store.Record, domain.Account, error) {
	r, a, _, _, err := s.claudeSubscriptionLeaseView(ctx, tx, id, lease, machine, instance, false)
	if err == nil && a.Subscription.Lease.ID != lease {
		return r, a, subscriptionDenied()
	}
	return r, a, err
}

// claudeSubscriptionLeaseView is the device-owned Claude counterpart of
// subscriptionLeaseView: the owner bindings are checked against the stored
// owner lease, and a member scope resolves exactly one execution member.
func (s *Service) claudeSubscriptionLeaseView(ctx context.Context, tx *store.Tx, id, lease, machine, instance domain.ID, memberScope bool) (store.Record, domain.Account, domain.SubscriptionLease, *domain.SubscriptionMember, error) {
	r, a, err := claudeSubscriptionAccount(tx, id, 0)
	if err != nil {
		return r, a, domain.SubscriptionLease{}, nil, err
	}
	st := a.Subscription
	actor, _ := domain.PrincipalFrom(ctx)
	if st == nil || st.Lease == nil || st.NativeProfileID == "" || st.OwnerMachineID != machine {
		return r, a, domain.SubscriptionLease{}, nil, subscriptionDenied()
	}
	l := st.Lease
	if actor.Type != domain.WorkerDevice || actor.DeviceID != l.DeviceID || actor.MachineID != machine || l.MachineID != machine || l.InstanceID != instance || l.Epoch != s.subscriptionServerEpoch() {
		return r, a, domain.SubscriptionLease{}, nil, subscriptionDenied()
	}
	var view domain.SubscriptionLease
	var member *domain.SubscriptionMember
	var ok bool
	if memberScope {
		view, member, ok = st.MemberLease(lease)
	} else {
		view, ok = st.OwnerLeaseFor(lease)
		if m, found := st.Member(lease); found && l.Shared {
			member = &m
		}
	}
	if !ok {
		return r, a, domain.SubscriptionLease{}, nil, subscriptionDenied()
	}
	if err := currentInstance(tx, machine, instance); err != nil {
		return r, a, view, member, err
	}
	// A lease granted for a v4 direct-startup assignment never required the
	// inspected inventory, so its readback must not reintroduce it.
	if view.DirectStartup {
		_, m, err := activeMachine(tx, machine)
		if err == nil {
			err = claudeNativeCapability(m)
		}
		if err != nil {
			return r, a, view, member, err
		}
		return r, a, view, member, nil
	}
	if _, err := claudeSubscriptionInstallation(tx, machine); err != nil {
		return r, a, view, member, err
	}
	return r, a, view, member, nil
}
func (s *Service) takeClaudeSubscription(ctx context.Context, req *connect.Request[pb.TakeSubscriptionRequest]) (response *connect.Response[pb.TakeSubscriptionResponse], returned error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	action := subscriptionAction(req.Msg.Action)
	input := struct {
		Account, Lease, Machine, Instance, Operation domain.ID
		Revision                                     uint64
		Action                                       domain.SubscriptionAction
		Actor                                        domain.Principal
	}{domain.ID(m.Id), domain.ID(m.RequestId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), domain.ID(req.Msg.OperationId), m.ExpectedRevision, action, actor}
	if input.Operation.Validate() != nil || action != domain.SubscriptionLogin && action != domain.SubscriptionRefresh && action != domain.SubscriptionLogout && action != domain.SubscriptionExecute {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	owned := false
	defer func() {
		if returned != nil && owned {
			if err := s.markSubscriptionRecovery(input.Account); err != nil {
				s.logger.Warn("claude_subscription_recovery_unconfirmed", "operation_id", input.Operation, "code", domain.SafeError(err).Code)
			}
		}
	}()
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	// sharedOwner founds a device-owned Claude account owner and joinedOwner
	// admits one more execution member. Both require the negotiated
	// ClaudeDeviceOwnedMembersV1 capability; every other holder stays exclusive.
	sharedOwner, joinedOwner, directStartup := false, false, false
	result, err := s.Store.Mutate(ctx, input.Lease, "claude.subscription.take", input, func(tx *store.Tx) (any, error) {
		sharedOwner, joinedOwner, directStartup = false, false, false
		r, a, err := claudeSubscriptionAccount(tx, input.Account, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.RecoveryRequired {
			return nil, subscriptionDenied()
		}
		if action == domain.SubscriptionExecute {
			// A queued operation whose window elapsed can never be claimed. It must
			// not keep blocking admission until a user cancels it.
			expireStaleQueuedClaudeOperation(st, time.Now().UTC())
		}

		accountBusy := func() error {
			if st.Lease != nil {
				return domain.Fail(domain.ResourceExhausted, "This Claude account is exclusively owned by another operation.", "Wait for that original operation and cleanup on this Runner Device.")
			}
			return nil
		}
		// Execution keeps contention first. A fresh lifecycle Take classifies its
		// queued original after the Worker fences and before contention below.
		sharedCapable := false
		if action == domain.SubscriptionExecute {
			if _, machine, machineErr := activeMachine(tx, input.Machine); machineErr == nil {
				sharedCapable = slices.Contains(machine.WorkerCapabilities, domain.ClaudeDeviceOwnedMembersV1)
			}
			if !(sharedCapable && st.SharedOwner()) {
				if err := accountBusy(); err != nil {
					return nil, err
				}
			}
		}
		if err := currentInstance(tx, input.Machine, input.Instance); err != nil {
			return nil, err
		}
		// The claimed v4 assignment, not machine-global inventory, decides whether
		// inspected installation evidence is required for an Execute lease. Login,
		// Refresh, Logout and historical execution keep the inventory requirement.
		directAssignment := action == domain.SubscriptionExecute && claudeDirectStartupAssignment(tx, input.Operation, input.Revision)
		if directAssignment {
			if err := claudeDirectStartupAdmission(tx, input.Machine); err != nil {
				return nil, err
			}
		} else if _, err := claudeSubscriptionInstallation(tx, input.Machine); err != nil {
			return nil, err
		}
		profileOwned := st.NativeProfileID != "" && st.OwnerMachineID == input.Machine
		if action == domain.SubscriptionExecute {
			if !profileOwned {
				return nil, subscriptionDenied()
			}
		} else {
			if err := tx.WorkerUpdateAdmission(input.Machine); err != nil {
				return nil, err
			}
			op := st.Pending
			if input.Revision > r.Revision {
				return nil, subscriptionDenied()
			}
			// A queued replacement owns its own profile, selected machine, action and
			// phase. Retire only this stale claim after the original Worker fences;
			// never inspect or adopt the replacement as this claim's authority.
			if op != nil && op.ID != input.Operation {
				return nil, subscriptionTakeNotAdmitted()
			}
			// Queued initial login cancellation retires its unused profile owner.
			// Only the retained original canceled operation proves no native grant.
			o := st.NativeOperation
			var original bool
			if op == nil {
				original = profileOwned || o != nil && o.ID == input.Operation && o.Action == action && o.MachineID == input.Machine && o.State == domain.SubscriptionCanceled
			} else {
				original = op.Phase == domain.SubscriptionQueued && op.Action == action && op.MachineID == input.Machine && profileOwned
			}
			if original && (op == nil || op.Canceled) {
				return nil, subscriptionTakeNotAdmitted()
			}
			// A matching claim that fails its original fields keeps the existing
			// contention-first outcome and never carries the no-grant proof.
			if err := accountBusy(); err != nil {
				return nil, err
			}
			if !original {
				return nil, subscriptionDenied()
			}
		}
		if action == domain.SubscriptionExecute {
			if st.Pending != nil || a.Connection == nil || st.Generation == "" || !a.Enabled || a.Health != domain.AccountReady || a.Removal != nil {
				return nil, subscriptionDenied()
			}
			jr, err := tx.Get(domain.JobKind, input.Operation)
			if err != nil {
				return nil, err
			}
			job, err := store.Decode[domain.Job](jr)
			var execution domain.ExecutionJobInput
			if err != nil || jr.Revision != input.Revision || job.Type != domain.ExecuteSessionJob || job.State != domain.JobClaimed || job.MachineID != input.Machine || job.InstanceID != input.Instance || job.AssignedDeviceID != actor.DeviceID || domain.Decode(job.Input, &execution) != nil || execution.Validate() != nil || execution.AccountID != r.ID || execution.ConnectionID != a.Connection.ID || !execution.Configuration.Subscription || execution.Configuration.SubscriptionService != domain.SubscriptionClaude || execution.Configuration.Harness != domain.ClaudeCode {
				return nil, subscriptionDenied()
			}
			if canceled, err := tx.JobCancellationRequested(jr.ID); err != nil || canceled {
				return nil, subscriptionDenied()
			}
			if directAssignment != (execution.Version == 4 && execution.Startup != nil) {
				return nil, subscriptionDenied()
			}
			directStartup = directAssignment
			if sharedCapable {
				if st.Lease == nil {
					sharedOwner = true
				} else if err := s.sharedMemberAdmissible(st, input.Machine, input.Instance, actor.DeviceID, input.Operation); err != nil {
					return nil, err
				} else {
					joinedOwner = true
				}
			}
		} else {
			op := st.Pending
			o := st.NativeOperation
			if o != nil && op != nil && o.ID == op.ID && op.ID == input.Operation && claudeQueuedWindowDeferred(st) && o.State == domain.SubscriptionPreparing {
				// The operation waited behind a live lease. Its window starts at this
				// claim, so the wait cannot have expired it.
				claimed := time.Now().UTC()
				o.StartedAt, o.ExpiresAt = claimed, claimed.Add(claudeOperationWindow)
			}
			if o == nil || o.Epoch != s.subscriptionServerEpoch() || !o.ExpiresAt.After(time.Now().UTC()) {
				return nil, subscriptionDenied()
			}
			if err := subscriptionActorValid(tx, op.Actor); err != nil {
				return nil, err
			}
			op.Phase = domain.SubscriptionClaimed
		}
		if err := tx.WorkerUpdateAdmission(input.Machine); err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		if joinedOwner {
			st.AdmitMember(domain.SubscriptionMember{LeaseID: input.Lease, OperationID: input.Operation, Revision: r.Revision + 1, State: domain.SubscriptionMemberRunning, AdmittedAt: now, DirectStartup: directStartup})
		} else {
			st.Lease = &domain.SubscriptionLease{ID: input.Lease, OperationID: input.Operation, Revision: r.Revision + 1, Action: action, MachineID: input.Machine, InstanceID: input.Instance, DeviceID: actor.DeviceID, Epoch: s.subscriptionServerEpoch(), Generation: st.Generation, StartedAt: now, Shared: sharedOwner, DirectStartup: directStartup}
			if sharedOwner {
				st.Members = []domain.SubscriptionMember{{LeaseID: input.Lease, OperationID: input.Operation, Revision: r.Revision + 1, State: domain.SubscriptionMemberRunning, AdmittedAt: now, DirectStartup: directStartup}}
			}
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, err
	})
	if err != nil {
		unlock()
		return nil, rpc.Error(err, c)
	}
	if result.Replayed {
		unlock()
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	owned = true
	var a domain.Account
	var view domain.SubscriptionLease
	var installation domain.Installation
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, view, _, err = s.claudeSubscriptionLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance, joinedOwner)
		// The lease view already proved the capability for a direct-startup lease;
		// only historical execution and lifecycle leases return inspected evidence.
		if err == nil && !view.DirectStartup {
			installation, err = claudeSubscriptionInstallation(tx, input.Machine)
		}
		return err
	})
	if err != nil && joinedOwner {
		// A joined member owns no native process yet, so a failed admission read
		// releases only that member and never fences the owner or its siblings.
		if releaseErr := s.releaseUndeliveredMember(ctx, s.claudeSubscriptionLeaseView, input.Account, input.Lease, input.Machine, input.Instance, "admission-read-failed"); releaseErr == nil {
			owned = false
			unlock()
			return nil, rpc.Error(subscriptionMemberNotAdmitted(domain.Unavailable), c)
		}
	}
	unlock()
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	// A direct-startup lease carries no inspected installation: the Worker resolves
	// the executable from the claimed assignment's own startup selection.
	var raw []byte
	if !view.DirectStartup {
		raw, _ = json.Marshal(installation)
	}
	st := a.Subscription
	owner := domain.ID("")
	if joinedOwner {
		owner = st.Lease.ID
	}
	s.logger.InfoContext(ctx, "claude_subscription_lease_granted", "operation_id", input.Operation, "lease_id", input.Lease, "action", action, "shared_owner", sharedOwner, "joined_owner", joinedOwner, "owner_lease_id", owner, "direct_startup", view.DirectStartup)
	return connect.NewResponse(&pb.TakeSubscriptionResponse{LeaseId: string(input.Lease), GenerationId: string(st.Generation), LeaseRevision: view.Revision, InstallationJson: raw, NativeProfileId: string(st.NativeProfileID), OwnerLeaseId: string(owner)}), nil
}
func (s *Service) publishClaudeSubscriptionProgress(ctx context.Context, req *connect.Request[pb.PublishSubscriptionProgressRequest]) (*connect.Response[pb.PublishSubscriptionProgressResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	msg := req.Msg
	if err := workerActor(ctx, msg.MachineId, msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	if msg.UserCode != "" || msg.SuggestedName != "" || msg.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_PREPARING && msg.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING && msg.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_FAILED && msg.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_UNSUPPORTED && msg.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_EXPIRED {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	if msg.Url != "" && claude.ValidateLoginURL(msg.Url) != nil {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	method := domain.SubscriptionLoginMethod(msg.LoginMethod)
	if msg.State == pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING && (msg.Url == "" || method != domain.SubscriptionBrowserCode && method != domain.SubscriptionBrowserCallback) {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	canceled := false
	var operation domain.ID
	changed := false
	observe := func(tx *store.Tx, write bool) (any, error) {
		r, a, err := s.claudeSubscriptionLease(ctx, tx, domain.ID(msg.AccountId), domain.ID(msg.LeaseId), domain.ID(msg.MachineId), domain.ID(msg.InstanceId))
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		op := st.Pending
		o := st.NativeOperation
		if st.RecoveryRequired || op == nil || o == nil || op.Action == domain.SubscriptionLogout || !o.ExpiresAt.After(time.Now().UTC()) || !claudeLifecycleLeaseHeld(st) {
			return nil, subscriptionDenied()
		}
		operation = op.ID
		canceled = op.Canceled
		if previous := s.subscriptionProgress[operation].URL; previous != "" && msg.Url != "" && previous != msg.Url {
			return nil, subscriptionDenied()
		}
		if method != 0 && o.LoginMethod != 0 && o.LoginMethod != method {
			return nil, subscriptionDenied()
		}
		var diagnostic *domain.NativeSubscriptionDiagnostic
		if d := msg.NativeDiagnostic; d != nil {
			diagnostic = &domain.NativeSubscriptionDiagnostic{DetectedVersion: d.DetectedVersion, RequiredVersion: d.RequiredVersion, Phase: domain.NativeSubscriptionPhase(d.Phase), Code: domain.Code(d.Code), CorrelationID: domain.ID(d.CorrelationId)}
			if diagnostic.Validate() != nil || diagnostic.CorrelationID != op.ID {
				return nil, subscriptionDenied()
			}
		}
		next := subscriptionStateFromWire(msg.State)
		if o.State == next && (method == 0 || o.LoginMethod == method) && (diagnostic == nil || o.Diagnostic != nil) {
			return accountReceipt{ID: r.ID}, nil
		}
		if o.State != domain.SubscriptionPreparing && o.State != domain.SubscriptionWaiting {
			return nil, subscriptionDenied()
		}
		changed = true
		if !write {
			return accountReceipt{ID: r.ID}, nil
		}
		o.State = next
		if method != 0 {
			o.LoginMethod = method
		}
		if o.Diagnostic == nil {
			o.Diagnostic = diagnostic
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, err
	}
	// Idle progress polling validates its original lease without creating durable receipts.
	err = s.Store.Read(ctx, func(tx *store.Tx) error { _, err := observe(tx, false); return err })
	if err == nil && changed {
		_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.subscription.progress", struct {
			Account, Lease domain.ID
			State          pb.SubscriptionLoginState
		}{domain.ID(msg.AccountId), domain.ID(msg.LeaseId), msg.State}, func(tx *store.Tx) (any, error) { return observe(tx, true) })
	}
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if msg.Url != "" && !canceled {
		if s.subscriptionProgress == nil {
			s.subscriptionProgress = map[domain.ID]subscriptionProgress{}
		}
		s.subscriptionProgress[operation] = subscriptionProgress{URL: msg.Url}
	}
	if canceled {
		s.clearClaudeLoginInput(operation)
	}
	return connect.NewResponse(&pb.PublishSubscriptionProgressResponse{Canceled: canceled}), nil
}
func subscriptionStateFromWire(state pb.SubscriptionLoginState) domain.SubscriptionLoginState {
	switch state {
	case pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_PREPARING:
		return domain.SubscriptionPreparing
	case pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING:
		return domain.SubscriptionWaiting
	case pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_FAILED:
		return domain.SubscriptionFailed
	case pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_UNSUPPORTED:
		return domain.SubscriptionUnsupported
	case pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_EXPIRED:
		return domain.SubscriptionExpired
	}
	return ""
}
func (s *Service) SubmitSubscriptionLoginCode(ctx context.Context, req *connect.Request[pb.SubmitSubscriptionLoginCodeRequest]) (*connect.Response[pb.SubmitSubscriptionLoginCodeResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	defer clear(req.Msg.Code)
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	if _, managed, err := s.claudeAccountMode(ctx, m.Id); err != nil {
		return nil, rpc.Error(err, c)
	} else if managed {
		return s.submitManagedClaudeCode(ctx, req)
	}
	actor, err := claudeActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if domain.ID(req.Msg.OperationId).Validate() != nil || !claude.LoginCodeValid(req.Msg.Code) || !utf8.Valid(req.Msg.Code) {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	operation := domain.ID(req.Msg.OperationId)
	input := struct {
		Account, Operation domain.ID
		Revision           uint64
		Actor              domain.Principal
		Commitment         string
	}{domain.ID(m.Id), operation, m.ExpectedRevision, actor, s.accountCommitment(domain.ID(m.RequestId), req.Msg.Code)}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "claude.subscription.login-code", input, func(tx *store.Tx) (any, error) {
		r, a, err := claudeSubscriptionAccount(tx, input.Account, input.Revision)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.RecoveryRequired || st.Lease == nil || st.Lease.Epoch != s.subscriptionServerEpoch() || st.Pending == nil || st.Pending.Canceled || st.NativeOperation == nil || !claudeLifecycleLeaseHeld(st) {
			return nil, subscriptionDenied()
		}
		o := st.NativeOperation
		if o.ID != operation || o.Actor != actor || o.State != domain.SubscriptionWaiting || o.LoginMethod != domain.SubscriptionBrowserCode || o.CodeSubmissionID != "" || !o.ExpiresAt.After(time.Now().UTC()) || subscriptionActorValid(tx, actor) != nil || currentInstance(tx, st.Lease.MachineID, st.Lease.InstanceID) != nil {
			return nil, subscriptionDenied()
		}
		o.CodeSubmissionID = domain.ID(m.RequestId)
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, err
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if !result.Replayed {
		if s.claudeLoginCodes == nil {
			s.claudeLoginCodes = map[domain.ID][]byte{}
		}
		s.claudeLoginCodes[operation] = slices.Clone(req.Msg.Code)
	}
	s.logger.InfoContext(ctx, "claude_subscription_login_input_claimed", "operation_id", operation, "replayed", result.Replayed)
	return connect.NewResponse(&pb.SubmitSubscriptionLoginCodeResponse{Accepted: true}), nil
}
func (s *Service) TakeSubscriptionLoginCode(ctx context.Context, req *connect.Request[pb.TakeSubscriptionLoginCodeRequest]) (*connect.Response[pb.TakeSubscriptionLoginCodeResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	msg := req.Msg
	if err := workerActor(ctx, msg.MachineId, msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	operation := domain.ID(msg.OperationId)
	var submission domain.ID
	var ready bool
	observe := func(tx *store.Tx, consume bool) (any, error) {
		r, a, err := s.claudeSubscriptionLease(ctx, tx, domain.ID(msg.AccountId), domain.ID(msg.LeaseId), domain.ID(msg.MachineId), domain.ID(msg.InstanceId))
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		o := st.NativeOperation
		if st.RecoveryRequired || st.Pending == nil || st.Pending.Canceled || o == nil || o.ID != operation || o.State != domain.SubscriptionWaiting || o.LoginMethod != domain.SubscriptionBrowserCode || !o.ExpiresAt.After(time.Now().UTC()) || subscriptionActorValid(tx, o.Actor) != nil || !claudeLifecycleLeaseHeld(st) {
			return nil, subscriptionDenied()
		}
		if o.CodeConsumed {
			return nil, subscriptionDenied()
		}
		if o.CodeSubmissionID == "" {
			return accountReceipt{ID: r.ID}, nil
		}
		if len(s.claudeLoginCodes[operation]) == 0 {
			return nil, subscriptionDenied()
		}
		submission = o.CodeSubmissionID
		ready = true
		if !consume {
			return accountReceipt{ID: r.ID}, nil
		}
		o.CodeConsumed = true
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, err
	}
	err = s.Store.Read(ctx, func(tx *store.Tx) error { _, err := observe(tx, false); return err })
	if err == nil && ready {
		// Record consumption before releasing code bytes; an uncertain response never permits replay.
		_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.subscription.take-code", struct{ Account, Lease, Operation domain.ID }{domain.ID(msg.AccountId), domain.ID(msg.LeaseId), operation}, func(tx *store.Tx) (any, error) { return observe(tx, true) })
	}
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	response := &pb.TakeSubscriptionLoginCodeResponse{}
	if ready {
		response.Code = slices.Clone(s.claudeLoginCodes[operation])
		response.SubmissionId = string(submission)
		clear(s.claudeLoginCodes[operation])
		delete(s.claudeLoginCodes, operation)
	}
	return connect.NewResponse(response), nil
}
func (s *Service) finishClaudeSubscription(ctx context.Context, req *connect.Request[pb.FinishSubscriptionRequest]) (*connect.Response[pb.FinishSubscriptionResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	msg := req.Msg
	defer clear(msg.Bundle)
	if err := workerActor(ctx, msg.MachineId, msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	if len(msg.Bundle) != 0 || msg.RefreshConfirmed {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	scope, err := subscriptionFinishScope(msg.FinishScope)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	profile, identity := domain.ID(""), ""
	if msg.NativeIdentity != nil {
		profile = domain.ID(msg.NativeIdentity.ProfileId)
		identity = msg.NativeIdentity.IdentityCommitment
		if profile.Validate() != nil || !domain.NativeIdentityCommitmentValid(identity) {
			return nil, rpc.Error(subscriptionDenied(), c)
		}
	}
	input := claudeFinishInput{domain.ID(m.Id), domain.ID(msg.LeaseId), domain.ID(msg.MachineId), domain.ID(msg.InstanceId), domain.ID(msg.GenerationId), profile, identity, m.ExpectedRevision, msg.CleanupConfirmed, msg.Succeeded, scope}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	var operation domain.ID
	var result store.Result
	if input.Scope == domain.SubscriptionFinishMember {
		// A member Finish releases or retains exactly one execution member. It
		// carries no identity, confirms no authentication and never reaches the
		// owner settlement below.
		if profile != "" || msg.RefreshConfirmed {
			return nil, rpc.Error(subscriptionDenied(), c)
		}
		result, err = s.Store.Mutate(ctx, domain.ID(m.RequestId), "claude.subscription.finish", input, func(tx *store.Tx) (any, error) {
			return s.settleSubscriptionMember(ctx, tx, s.claudeSubscriptionLeaseView, input.Account, input.Lease, input.Machine, input.Instance, input.Generation, input.Revision, input.Cleanup, input.Success)
		})
	} else {
		result, err = s.Store.Mutate(ctx, domain.ID(m.RequestId), "claude.subscription.finish", input, func(tx *store.Tx) (any, error) {
			return s.settleClaudeOwner(ctx, tx, input, domain.ID(m.RequestId), &operation)
		})
	}
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.clearClaudeLoginInput(operation)
	r, err := s.accountRecord(ctx, input.Account)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "claude_subscription_operation_finished", "lease_id", input.Lease, "cleanup_confirmed", input.Cleanup, "replayed", result.Replayed, "finish_scope", input.Scope)
	return connect.NewResponse(&pb.FinishSubscriptionResponse{Account: rpc.Resource(r), Replayed: result.Replayed}), nil
}

// settleClaudeOwner is the legacy singleton and shared-owner settlement of the
// device-owned Claude lane. A shared owner arrives here only through the owner
// scope after its last runnable member.
func (s *Service) settleClaudeOwner(ctx context.Context, tx *store.Tx, input claudeFinishInput, requestID domain.ID, operationOut *domain.ID) (any, error) {
	profile, identity := input.Profile, input.Identity
	r, a, err := claudeSubscriptionAccount(tx, input.Account, 0)
	recoveryCleanup := false
	// A recovery finish that is both cleanup-confirmed and succeeded carries no
	// identity and no native cleanup: the original Worker proved it never opened
	// the account profile. It keeps the account, unlike the cleanup-only signal
	// that confirms a logout. Only a lost singleton EXECUTE may claim it.
	recoveryUntouched := err == nil && a.Subscription != nil && a.Subscription.RecoveryRequired && input.Cleanup && input.Success && input.Profile == "" &&
		a.Subscription.Lease != nil && a.Subscription.Lease.Action == domain.SubscriptionExecute && !a.Subscription.SharedOwner() && input.Scope != domain.SubscriptionFinishOwner
	if err == nil && a.Subscription != nil && a.Subscription.RecoveryRequired && input.Cleanup && (!input.Success && input.Profile == "" || recoveryUntouched) {
		st := a.Subscription
		l := st.Lease
		actor, _ := domain.PrincipalFrom(ctx)
		if l == nil || l.ID != input.Lease || l.MachineID != input.Machine || l.InstanceID != input.Instance || actor.DeviceID != l.DeviceID || st.OwnerMachineID != input.Machine || st.NativeProfileID == "" {
			return nil, subscriptionDenied()
		}
		// Recovery cleanup of a lost direct-startup execution never depended on
		// inspected inventory. Every other lease keeps the inventory requirement.
		if claudeLeaseDirectStartup(st) {
			_, machine, machineErr := activeMachine(tx, input.Machine)
			if machineErr == nil {
				machineErr = claudeNativeCapability(machine)
			}
			if machineErr != nil {
				return nil, machineErr
			}
		} else if _, err := claudeSubscriptionInstallation(tx, input.Machine); err != nil {
			return nil, err
		}
		// Only original-device cleanup can cross a restart fence. This never
		// accepts authentication, execution, code retransmission or a new login.
		// A lost shared owner is cleaned only as one whole owner/member set.
		if st.SharedOwner() != (input.Scope == domain.SubscriptionFinishOwner) {
			return nil, subscriptionDenied()
		}
		recoveryCleanup = true
	} else if err == nil {
		var member *domain.SubscriptionMember
		r, a, _, member, err = s.claudeSubscriptionLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance, false)
		if err == nil {
			if err = ownerFinishAllowed(a.Subscription, input.Scope, member); err != nil {
				s.logger.WarnContext(ctx, "claude_subscription_owner_finish_refused", "account_id", input.Account, "lease_id", input.Lease, "finish_scope", input.Scope, "runnable_members", a.Subscription.RunnableMembers(), "code", domain.SafeError(err).Code)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	st := a.Subscription
	l := st.Lease
	if st.RecoveryRequired && !recoveryCleanup || l.Generation != input.Generation || st.Generation != input.Generation || l.Revision != input.Revision {
		return nil, subscriptionDenied()
	}
	operation := l.OperationID
	*operationOut = operation
	if !input.Cleanup {
		st.RecoveryRequired = true
		a.Health = domain.AccountFailed
		if st.NativeOperation != nil && st.NativeOperation.ID == operation {
			st.NativeOperation.State = domain.SubscriptionRecovery
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, err
	}
	canceled := st.Pending != nil && st.Pending.ID == operation && st.Pending.Canceled
	authenticated := profile != "" && profile == st.NativeProfileID && (st.IdentityCommitment == "" || st.IdentityCommitment == identity)
	if profile != "" && !authenticated {
		return nil, subscriptionDenied()
	}
	if input.Success && l.Action != domain.SubscriptionLogout && !authenticated && !(recoveryCleanup && recoveryUntouched) || l.Action == domain.SubscriptionLogout && profile != "" {
		return nil, subscriptionDenied()
	}
	if recoveryCleanup && recoveryUntouched {
		// Keep generation, identity, native profile and connection exactly as they
		// were before the interrupted execution.
		if st.Pending == nil || st.Pending.ID == operation {
			if a.Health != domain.AccountRevoked {
				a.Health = domain.AccountReady
			}
		}
	} else if authenticated {
		if err := uniqueSubscriptionIdentity(tx, input.Account, identity); err != nil {
			return nil, err
		}
		if l.Action == domain.SubscriptionLogin || l.Action == domain.SubscriptionRefresh {
			if canceled || st.NativeOperation == nil || !st.NativeOperation.ExpiresAt.After(time.Now().UTC()) || subscriptionActorValid(tx, st.NativeOperation.Actor) != nil {
				return nil, subscriptionDenied()
			}
			st.Generation = domain.NewID()
			st.IdentityCommitment = identity
			if a.Connection == nil {
				a.Connection = &domain.AccountConnection{ID: requestID, Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()}
			}
		}
		if st.Pending == nil || st.Pending.ID == operation {
			if a.Health != domain.AccountRevoked {
				a.Health = domain.AccountReady
			}
		}
	} else if l.Action != domain.SubscriptionExecute || recoveryCleanup {
		// Missing identity is positive joined logout/status evidence from this
		// original Runner, never a server filesystem or credential-file read.
		st.Generation = ""
		st.IdentityCommitment = ""
		st.NativeProfileID = ""
		st.OwnerMachineID = ""
		a.Connection = nil
		a.Health = domain.AccountDisconnected
	} else {
		st.RecoveryRequired = true
		a.Health = domain.AccountFailed
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, err
	}
	st.Lease = nil
	// Owner retirement settles every retained member with the owner.
	st.Members = nil
	st.RecoveryRequired = false
	if recoveryCleanup && st.Pending != nil && st.Pending.ID != operation {
		if st.NativeOperation != nil {
			if st.Pending.Action == domain.SubscriptionLogout {
				st.NativeOperation.State = domain.SubscriptionSucceeded
			} else {
				st.NativeOperation.State = domain.SubscriptionFailed
			}
		}
		st.Pending = nil
	}
	if st.Pending != nil && st.Pending.ID == operation {
		st.Pending = nil
		if o := st.NativeOperation; o != nil {
			if canceled {
				o.State = domain.SubscriptionCanceled
			} else if input.Success && !recoveryUntouched {
				o.State = domain.SubscriptionSucceeded
			} else if !o.ExpiresAt.After(time.Now().UTC()) {
				o.State = domain.SubscriptionExpired
			} else if o.State == domain.SubscriptionPreparing || o.State == domain.SubscriptionWaiting || o.State == domain.SubscriptionRecovery {
				o.State = domain.SubscriptionFailed
			}
		}
	}
	_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	return accountReceipt{ID: r.ID}, err
}
