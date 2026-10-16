package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type subscriptionProgress struct {
	URL, UserCode, Name string
	Generation          domain.ID
	Until               time.Time
}

func (s *Service) subscriptionServerEpoch() domain.ID {
	s.subscriptionOnce.Do(func() { s.subscriptionEpoch = domain.NewID() })
	return s.subscriptionEpoch
}

func subscriptionAction(value pb.SubscriptionAction) domain.SubscriptionAction {
	switch value {
	case pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN:
		return domain.SubscriptionLogin
	case pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH:
		return domain.SubscriptionRefresh
	case pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT:
		return domain.SubscriptionLogout
	case pb.SubscriptionAction_SUBSCRIPTION_ACTION_QUOTA:
		return domain.SubscriptionQuota
	case pb.SubscriptionAction_SUBSCRIPTION_ACTION_RESET_CREDIT:
		return domain.SubscriptionResetCredit
	case pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE:
		return domain.SubscriptionExecute
	default:
		return ""
	}
}

func subscriptionDenied() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The managed account cannot grant authentication authority.", "Reconcile its original lease, credential generation and confirmed native cleanup; never redistribute an older bundle.")
}

// Only an uncommitted fresh lifecycle claim can prove that no lease was granted.
func subscriptionTakeNotAdmitted() *domain.Error {
	problem := domain.Fail(domain.Canceled, "The original queued subscription operation is no longer available.", "Inspect the current account operation before requesting another lifecycle action.")
	problem.Cause = "subscription_take_not_admitted"
	return problem
}

// subscriptionTitleNotDelivered is the definite result of a managed title Take
// whose exact lease was committed and then released without any credential
// crossing the boundary. Unlike subscriptionDenied it carries no recovery
// authority, so the Worker can report and terminate the title normally instead
// of treating the outcome as uncertain. The code only classifies the report
// (Canceled for a denied or revoked title, Unavailable for a vault failure).
func subscriptionTitleNotDelivered(code domain.Code) *domain.Error {
	problem := domain.Fail(code, "The managed automatic title received no credentials.", "No credential was delivered and no inference was sent; keep the current title and the ready account.")
	problem.Cause = subscriptionTitleNotDeliveredCause
	return problem
}

// subscriptionTitleNotDeliveredCause is the typed proof the Worker accepts for a
// released, undelivered managed title lease.
const subscriptionTitleNotDeliveredCause = "subscription_title_not_delivered"

func subscriptionActorValid(tx *store.Tx, actor domain.Principal) error {
	if actor.Type == domain.OwnerDevice {
		return nil
	}
	r, err := tx.Get(domain.DeviceKind, actor.DeviceID)
	if err != nil {
		return subscriptionDenied()
	}
	d, err := store.Decode[domain.Device](r)
	if err != nil || d.Revoked || d.Type != actor.Type || d.MachineID != actor.MachineID {
		return subscriptionDenied()
	}
	return nil
}

// cancelServerOperation settles a queued operation's server record as canceled
// just before the cancellation removes the pending operation. A canceled record
// never keeps a failure diagnostic, such as the provisional Grok record of an
// earlier refused Take.
//
// A Grok operation is Worker-owned and has no server record until it settles, so
// canceling one that was never claimed would otherwise leave progress with
// neither a pending operation nor a record, and the original actor's poll would
// be denied instead of reporting `canceled`. For a Grok account a canceled
// record is therefore created from the pending operation when none exists, and
// an earlier refused Take's record is replaced. A settled record of the same
// operation is final and is kept.
func cancelServerOperation(account *domain.Account, epoch domain.ID, now time.Time) {
	state := account.Subscription
	if state == nil || state.Pending == nil {
		return
	}
	if account.SubscriptionService == domain.SubscriptionGrok {
		p := state.Pending
		// A server-owned Grok login already has an active record of its own; a
		// queued one settles that record as canceled.
		if o := state.ServerOperation; o != nil && o.ID == p.ID && (o.State == domain.SubscriptionPreparing || o.State == domain.SubscriptionWaiting) {
			o.State, o.GrokDiagnostic = domain.SubscriptionCanceled, nil
			return
		}
		settleGrokOperation(state, grokSettlement{Operation: p.ID, Action: p.Action, Actor: p.Actor, Epoch: epoch, State: domain.SubscriptionCanceled}, now)
		return
	}
	if o := state.ServerOperation; o != nil && o.ID == state.Pending.ID {
		o.State, o.GrokDiagnostic = domain.SubscriptionCanceled, nil
	}
}

// cancelPendingLogin applies a login cancellation to the account's pending
// operation. A queued login holds no lease, so it is removed and its server
// record is settled as canceled; a claimed login keeps its pending operation
// with the cancellation flag until its Worker reports Finish.
func cancelPendingLogin(a *domain.Account, epoch domain.ID, now time.Time) error {
	if a.Subscription == nil || a.Subscription.Pending == nil {
		return domain.Fail(domain.Conflict, "No subscription operation is pending.", "Read current account status; cancellation never starts another operation.")
	}
	if a.Subscription.Pending.Action != domain.SubscriptionLogin {
		return domain.Fail(domain.Unsupported, "Only an active login can be canceled.", "Let refresh or logout finish under exclusive ownership.")
	}
	if a.Subscription.Pending.Phase == domain.SubscriptionQueued {
		cancelServerOperation(a, epoch, now)
		a.Subscription.Pending = nil
	} else {
		a.Subscription.Pending.Canceled = true
	}
	return nil
}

// Queued lifecycle requests have not granted native authority. Settle them in
// the same transaction as client revocation, without releasing independent or
// claimed leases and without undoing logout's accepted execution revocation.
// A queued server-owned Grok logout is the exception: it owes the provider a
// token revocation that no initiator is needed to perform, so it stays queued
// and its runner revokes the token after the original execution exits.
func cancelQueuedSubscriptionInitiator(tx *store.Tx, device, epoch domain.ID) error {
	accounts, err := all(tx, domain.AccountKind)
	if err != nil {
		return err
	}
	for _, record := range accounts {
		account, err := store.Decode[domain.Account](record)
		if err != nil {
			return err
		}
		state := account.Subscription
		if state == nil || state.Pending == nil || state.Pending.Actor.DeviceID != device || state.Pending.Phase != domain.SubscriptionQueued {
			continue
		}
		if grokAccountLoggingOut(account) {
			continue
		}
		cancelServerOperation(&account, epoch, time.Now().UTC())
		if o := state.NativeOperation; o != nil && o.ID == state.Pending.ID {
			o.State = domain.SubscriptionCanceled
			if state.Pending.Action == domain.SubscriptionLogin && state.Lease == nil && state.Generation == "" {
				state.NativeProfileID = ""
				state.OwnerMachineID = ""
			}
		}
		state.Pending = nil
		if _, err := tx.Put(domain.AccountKind, record.ID, record.Revision, "", "", account); err != nil {
			return err
		}
	}
	return nil
}

func subscriptionInstallation(tx *store.Tx, machineID domain.ID) (domain.Installation, error) {
	_, machine, err := activeMachine(tx, machineID)
	if err != nil {
		return domain.Installation{}, err
	}
	if !slices.Contains(machine.WorkerCapabilities, domain.ManagedCodexSubscriptionsV1) {
		return domain.Installation{}, domain.Fail(domain.Unsupported, "The selected Runner Device has no managed Codex capability.", "Connect a current Runner Device with a verified installed Codex profile.")
	}
	var selected *domain.Installation
	for i := range machine.Installations {
		if machine.Installations[i].Harness == domain.Codex {
			if selected != nil {
				return domain.Installation{}, subscriptionDenied()
			}
			selected = &machine.Installations[i]
		}
	}
	if selected != nil && domain.CodexVersionAllowed(selected.Version) && selected.State == domain.InstallationDetected && selected.ProtocolVerified && selected.ResolvedPath != "" && selected.Problem == nil && selected.Protocol != nil && selected.Protocol.Protocol == domain.ProtocolFor(domain.Codex) && selected.Protocol.State == domain.ProtocolVerified && selected.Protocol.Problem == nil && selected.ObservedAt != nil && !selected.ObservedAt.IsZero() && !selected.ObservedAt.After(time.Now().UTC().Add(time.Second)) {
		return *selected, nil
	}
	return domain.Installation{}, domain.Fail(domain.Unsupported, "The selected Runner Device has no verified Codex installation.", "Check the selected Runner Device's installed Codex and protocol verification.")
}

func subscriptionAccount(tx *store.Tx, id domain.ID, revision uint64) (store.Record, domain.Account, error) {
	r, a, err := accountFromTx(tx, id, revision)
	if err != nil {
		return r, a, err
	}
	if a.Type != domain.SubscriptionAccount {
		return r, a, domain.Fail(domain.Unsupported, "This operation requires a Codex subscription account.", "Preserve API accounts through their existing connection operations.")
	}
	if a.Type != domain.SubscriptionAccount || a.SubscriptionService != domain.SubscriptionChatGPT || a.ProviderID != "" {
		return r, a, domain.Fail(domain.Unsupported, "This service has no managed subscription profile.", "Select a ChatGPT service account for the verified Codex lifecycle.")
	}
	return r, a, nil
}

func (s *Service) RequestSubscription(ctx context.Context, req *connect.Request[pb.RequestSubscriptionRequest]) (*connect.Response[pb.RequestSubscriptionResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	if matched, err := s.isClaudeSubscription(ctx, m.Id); err != nil {
		return nil, rpc.Error(err, c)
	} else if matched {
		if managed, err := s.routesToManagedClaude(ctx, req.Msg); err != nil {
			return nil, rpc.Error(err, c)
		} else if managed {
			return s.requestManagedClaude(ctx, req)
		}
		return s.requestClaudeSubscription(ctx, req)
	}
	if matched, err := s.isGrokSubscription(ctx, m.Id); err != nil {
		return nil, rpc.Error(err, c)
	} else if matched {
		return s.requestGrokSubscription(ctx, req)
	}
	if req.Msg.MachineId == "" {
		return s.requestServerSubscription(ctx, req)
	}
	action := subscriptionAction(req.Msg.Action)
	if action == "" || (action != domain.SubscriptionLogin && action != domain.SubscriptionRefresh && action != domain.SubscriptionLogout) || (req.Msg.DeviceCode && action != domain.SubscriptionLogin) || domain.ID(req.Msg.MachineId).Validate() != nil {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Choose a supported subscription operation and Runner Device.", "Use login, refresh or logout with an explicit machine."), c)
	}
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type == domain.WorkerDevice {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	input := struct {
		ID, Machine domain.ID
		Revision    uint64
		Action      domain.SubscriptionAction
		DeviceCode  bool
		Actor       domain.Principal
	}{domain.ID(m.Id), domain.ID(req.Msg.MachineId), m.ExpectedRevision, action, req.Msg.DeviceCode, actor}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "subscription.request", input, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, input.ID, input.Revision)
		if err != nil {
			return nil, err
		}
		if _, err = subscriptionInstallation(tx, input.Machine); err != nil {
			return nil, err
		}
		if a.Subscription == nil {
			a.Subscription = &domain.SubscriptionState{}
		}
		state := a.Subscription
		if state.ServerObservationActive() || state.Pending != nil || state.RecoveryRequired || a.Removal != nil || state.Observation != nil && state.Observation.Active() && action != domain.SubscriptionLogout {
			return nil, subscriptionDenied()
		}
		if action == domain.SubscriptionLogin && (a.Connection != nil || state.Generation != "" || state.Lease != nil) {
			return nil, domain.Fail(domain.Conflict, "The account already owns a login or lease.", "Complete its original logout and cleanup before a new login.")
		}
		if action != domain.SubscriptionLogin && (a.Connection == nil || state.Generation == "") {
			return nil, subscriptionDenied()
		}
		if action == domain.SubscriptionRefresh {
			// A refresh rotates the credential generation. A claimed managed title
			// still owns the frozen generation, so the refresh waits for it.
			if reserved, err := managedTitleReservation(tx, r.ID, ""); err != nil {
				return nil, err
			} else if reserved {
				return nil, managedTitleAccountReserved()
			}
		}
		if state.ServerOperation != nil && state.ServerOperation.CleanupPhase == domain.SubscriptionCredentialCleanupConfirmed {
			// A fresh explicit Worker login supersedes the settled server attempt.
			// Its cleanup checkpoint cannot describe the new pending/native owner.
			state.ServerOperation = nil
		}
		state.Pending = &domain.SubscriptionOperation{ID: domain.ID(m.RequestId), Action: action, MachineID: input.Machine, Actor: actor, DeviceCode: input.DeviceCode, Phase: domain.SubscriptionQueued}
		if action == domain.SubscriptionLogout {
			state.AutomaticCreditConsent = nil
			// Deny new executions at acceptance. Existing ownership remains leased
			// until its original Worker reports native/file cleanup.
			a.Health = domain.AccountRevoked
			if state.Observation != nil && state.Observation.Phase == domain.SubscriptionObservationQueued {
				state.Observation.Phase = domain.SubscriptionObservationFailed
				state.Observation.ErrorCode = domain.Canceled
			}
			if err := cancelAccountExecutions(tx, r.ID); err != nil {
				return nil, err
			}
			if err := retireAccountQueuedTitles(tx, r.ID); err != nil {
				return nil, err
			}
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return accountReceipt{ID: r.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	r, err := s.accountRecord(ctx, input.ID)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "subscription_operation_accepted", "account_id", input.ID, "operation_id", m.RequestId, "action", action, "replayed", result.Replayed)
	return connect.NewResponse(&pb.RequestSubscriptionResponse{Account: rpc.Resource(r), OperationId: m.RequestId, Replayed: result.Replayed}), nil
}

func (s *Service) CancelSubscription(ctx context.Context, req *connect.Request[pb.CancelSubscriptionRequest]) (*connect.Response[pb.CancelSubscriptionResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	if matched, err := s.isClaudeSubscription(ctx, m.Id); err != nil {
		return nil, rpc.Error(err, c)
	} else if matched {
		if _, managed, err := s.claudeAccountMode(ctx, m.Id); err != nil {
			return nil, rpc.Error(err, c)
		} else if managed {
			return s.cancelManagedClaude(ctx, req)
		}
		return s.cancelClaudeSubscription(ctx, req)
	}
	if matched, err := s.isGrokSubscription(ctx, m.Id); err != nil {
		return nil, rpc.Error(err, c)
	} else if matched {
		return s.cancelGrokSubscription(ctx, req)
	}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "subscription.cancel", disconnectAccountInput{domain.ID(m.Id), m.ExpectedRevision}, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, domain.ID(m.Id), m.ExpectedRevision)
		if err != nil {
			return nil, err
		}
		if err := cancelPendingLogin(&a, s.subscriptionServerEpoch(), time.Now().UTC()); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return accountReceipt{ID: r.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	r, err := s.accountRecord(ctx, domain.ID(m.Id))
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	return connect.NewResponse(&pb.CancelSubscriptionResponse{Account: rpc.Resource(r), Replayed: result.Replayed}), nil
}

func (s *Service) GetSubscriptionProgress(ctx context.Context, req *connect.Request[pb.GetSubscriptionProgressRequest]) (*connect.Response[pb.GetSubscriptionProgressResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	if matched, err := s.isClaudeSubscription(ctx, req.Msg.AccountId); err != nil {
		return nil, rpc.Error(err, c)
	} else if matched {
		if _, managed, err := s.claudeAccountMode(ctx, req.Msg.AccountId); err != nil {
			return nil, rpc.Error(err, c)
		} else if managed {
			return s.managedClaudeProgress(ctx, req)
		}
		return s.getClaudeSubscriptionProgress(ctx, req)
	}
	if matched, err := s.isGrokSubscription(ctx, req.Msg.AccountId); err != nil {
		return nil, rpc.Error(err, c)
	} else if matched {
		return s.grokSubscriptionProgress(ctx, req)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	if response, matched, err := s.serverSubscriptionProgress(ctx, req.Msg); matched || err != nil {
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		return connect.NewResponse(response), nil
	}
	var op *domain.SubscriptionOperation
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := subscriptionAccount(tx, domain.ID(req.Msg.AccountId), 0)
		if err != nil {
			return err
		}
		if a.Subscription == nil || a.Subscription.Pending == nil || string(a.Subscription.Pending.ID) != req.Msg.OperationId {
			return domain.Fail(domain.NotFound, "The original login presentation is unavailable.", "Read account status without starting another login.")
		}
		op = a.Subscription.Pending
		if a.Subscription.RecoveryRequired || (a.Subscription.Lease != nil && a.Subscription.Lease.Epoch != s.subscriptionServerEpoch()) {
			return subscriptionDenied()
		}
		return subscriptionActorValid(tx, op.Actor)
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	if actor != op.Actor {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	p := s.subscriptionProgress[op.ID]
	return connect.NewResponse(&pb.GetSubscriptionProgressResponse{Url: p.URL, UserCode: p.UserCode, Canceled: op.Canceled}), nil
}

func (s *Service) WatchSubscription(ctx context.Context, req *connect.Request[pb.WatchSubscriptionRequest], stream *connect.ServerStream[pb.WatchSubscriptionResponse]) error {
	c := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return rpc.Error(err, c)
	}
	defer func() {
		if err := s.retainLostSubscriptionLeases(domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), false); err != nil {
			s.logger.Warn("subscription_owner_loss_unconfirmed", "code", domain.SafeError(err).Code)
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	// recoveryCursor rotates the fenced managed ChatGPT accounts this stream
	// offers, so more fenced accounts than the lane bound cannot starve each other.
	// otherCursor does the same for the ordinary rows that share the bound.
	recoveryCursor, otherCursor := 0, 0
	for {
		var records []store.Record
		var recovery []store.Record
		err := s.Store.Read(ctx, func(tx *store.Tx) error {
			if err := tx.Authorize(); err != nil {
				return err
			}
			if err := currentInstance(tx, domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId)); err != nil {
				return err
			}
			_, machine, err := activeMachine(tx, domain.ID(req.Msg.MachineId))
			if err != nil {
				return err
			}
			if !slices.Contains(machine.WorkerCapabilities, domain.ManagedCodexSubscriptionsV1) && !slices.Contains(machine.WorkerCapabilities, domain.ManagedClaudeExecutionV1) && !slices.Contains(machine.WorkerCapabilities, domain.ManagedGrokSubscriptionsV1) {
				return subscriptionDenied()
			}
			// A current adapter may wait for explicitly accepted lifecycle work
			// without an installed harness. Acceptance/Take still own authority.
			if !slices.Contains(machine.WorkerCapabilities, domain.ExecutionStartupV1) {
				if _, err := subscriptionInstallation(tx, domain.ID(req.Msg.MachineId)); err != nil {
					return err
				}
			}
			accounts, err := all(tx, domain.AccountKind)
			if err != nil {
				return err
			}
			for _, r := range accounts {
				a, err := store.Decode[domain.Account](r)
				if err != nil {
					return err
				}
				if a.ManagedClaudeAccount() {
					// A server-token account is watched only for original-Worker execution
					// cleanup; its login and logout run on the server.
					if !slices.Contains(machine.WorkerCapabilities, domain.ManagedClaudeExecutionV1) || a.Subscription.Lease == nil || !a.Subscription.RecoveryRequired || a.Subscription.Lease.MachineID != domain.ID(req.Msg.MachineId) {
						continue
					}
					records = append(records, r)
					continue
				}
				// Grok lifecycle work is server-owned and is never pushed to a Worker. A
				// Worker is offered a Grok account only to clean up the private home of
				// its own interrupted execution lease, and only after it negotiated the
				// managed Grok capability.
				if a.SubscriptionService == domain.SubscriptionGrok {
					if state := a.Subscription; slices.Contains(machine.WorkerCapabilities, domain.ManagedGrokSubscriptionsV1) && state != nil && state.RecoveryRequired && state.Lease != nil && state.Lease.Action == domain.SubscriptionExecute && state.Lease.MachineID == domain.ID(req.Msg.MachineId) {
						records = append(records, r)
					}
					continue
				}
				if a.SubscriptionService == domain.SubscriptionClaude && !slices.Contains(machine.WorkerCapabilities, domain.NativeClaudeSubscriptionsV1) || a.SubscriptionService == domain.SubscriptionChatGPT && !slices.Contains(machine.WorkerCapabilities, domain.ManagedCodexSubscriptionsV1) {
					continue
				}
				state := a.Subscription
				// A fenced managed ChatGPT EXECUTE lease is offered only to its original
				// Worker, and only after both sides negotiated the recovery capability.
				// Older peers keep the existing fence.
				if a.SubscriptionService == domain.SubscriptionChatGPT && chatGPTRecoveryOffer(a, machine, domain.ID(req.Msg.MachineId)) {
					recovery = append(recovery, r)
					continue
				}
				if state != nil && a.SubscriptionService == domain.SubscriptionClaude && state.RecoveryRequired && state.Lease != nil && state.Lease.MachineID == domain.ID(req.Msg.MachineId) {
					records = append(records, r)
					continue
				}
				if state != nil && state.Observation != nil && state.Observation.Phase == domain.SubscriptionObservationQueued && state.Observation.MachineID == domain.ID(req.Msg.MachineId) && !state.RecoveryRequired && (state.Lease == nil || state.Lease.Action == domain.SubscriptionExecute && state.Lease.InstanceID == domain.ID(req.Msg.InstanceId)) {
					records = append(records, r)
					continue
				}
				if state != nil && state.Pending != nil && state.Pending.MachineID == domain.ID(req.Msg.MachineId) && state.Pending.Phase == domain.SubscriptionQueued && state.Lease == nil && !state.RecoveryRequired {
					records = append(records, r)
				}
			}
			records, otherCursor, recoveryCursor = mergeRecoveryRows(records, recovery, otherCursor, recoveryCursor)
			return nil
		})
		if err != nil {
			return rpc.Error(err, c)
		}
		for _, r := range records {
			if err := stream.Send(&pb.WatchSubscriptionResponse{Account: rpc.Resource(r)}); err != nil {
				return err
			}
		}
		if err := stream.Send(&pb.WatchSubscriptionResponse{}); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// subscriptionWatchLaneBound is the number of accounts one Worker lane serves at
// a time.
const subscriptionWatchLaneBound = 4

// chatGPTRecoveryOffer reports whether a managed ChatGPT account holds a fenced
// EXECUTE lease of this machine, or a committed recovery settlement awaiting its
// protected-reference retirement, and the machine negotiated recovery.
func chatGPTRecoveryOffer(a domain.Account, machine domain.Machine, id domain.ID) bool {
	state := a.Subscription
	if a.Type != domain.SubscriptionAccount || a.ProviderID != "" || state == nil || !recoveryMachineSupported(machine) {
		return false
	}
	if state.RecoverySettlement != nil {
		return state.RecoverySettlement.Machine == id
	}
	return state.RecoveryRequired && state.Lease != nil && state.Lease.Action == domain.SubscriptionExecute && state.Lease.MachineID == id
}

// mergeRecoveryRows combines the ordinary rows with rotating windows of the
// recovery rows under the single lane bound. Without recovery rows every
// ordinary row is offered, as before. Otherwise recovery rows keep at least one
// slot, and both windows advance by the rows they offered, so every fenced
// account and every ordinary account is offered in turn instead of the stable
// head of either list.
func mergeRecoveryRows(others, recovery []store.Record, otherCursor, recoveryCursor int) (rows []store.Record, nextOther, nextRecovery int) {
	if len(recovery) == 0 {
		return others, 0, 0
	}
	reserved := min(len(recovery), max(1, subscriptionWatchLaneBound-len(others)))
	slots := subscriptionWatchLaneBound - reserved
	if len(others) <= slots {
		rows = append(rows, others...)
	} else if slots > 0 {
		start := otherCursor % len(others)
		for i := 0; i < slots; i++ {
			rows = append(rows, others[(start+i)%len(others)])
		}
		nextOther = (start + slots) % len(others)
	}
	start := recoveryCursor % len(recovery)
	for i := 0; i < reserved; i++ {
		rows = append(rows, recovery[(start+i)%len(recovery)])
	}
	return rows, nextOther, (start + reserved) % len(recovery)
}

// subscriptionLease resolves the stored owner-scoped lease: a historical
// singleton lease or the shared account-owner lease by its own identifier. A
// member identifier is not an owner identifier, so owner-scoped operations
// (progress, observations, lifecycle) never accept one.
func (s *Service) subscriptionLease(ctx context.Context, tx *store.Tx, id, lease, machine, instance domain.ID) (store.Record, domain.Account, error) {
	r, a, _, _, err := s.subscriptionLeaseView(ctx, tx, id, lease, machine, instance, false)
	if err == nil && a.Subscription.Lease.ID != lease {
		return r, a, subscriptionDenied()
	}
	return r, a, err
}

// subscriptionLeaseView resolves a Worker-presented lease identifier against the
// exact account owner. With memberScope the identifier must name one execution
// member of a shared owner and the returned view carries that member's job and
// revision. Without it the owner's identifier (shared with its founding member)
// or, for a shared owner, another member's identifier resolves. Every owner
// binding (Runner, device, instance, epoch, generation) is checked against the
// stored owner lease in both scopes.
func (s *Service) subscriptionLeaseView(ctx context.Context, tx *store.Tx, id, lease, machine, instance domain.ID, memberScope bool) (store.Record, domain.Account, domain.SubscriptionLease, *domain.SubscriptionMember, error) {
	r, a, err := subscriptionAccount(tx, id, 0)
	if err != nil {
		return r, a, domain.SubscriptionLease{}, nil, err
	}
	if a.Subscription == nil || a.Subscription.Lease == nil {
		return r, a, domain.SubscriptionLease{}, nil, subscriptionDenied()
	}
	l := a.Subscription.Lease
	actor, _ := domain.PrincipalFrom(ctx)
	if actor.Type != domain.WorkerDevice || actor.DeviceID != l.DeviceID {
		return r, a, domain.SubscriptionLease{}, nil, subscriptionDenied()
	}
	if l.MachineID != machine || l.InstanceID != instance || l.Epoch != s.subscriptionServerEpoch() {
		return r, a, domain.SubscriptionLease{}, nil, subscriptionDenied()
	}
	var view domain.SubscriptionLease
	var member *domain.SubscriptionMember
	if memberScope {
		var ok bool
		if view, member, ok = a.Subscription.MemberLease(lease); !ok {
			return r, a, domain.SubscriptionLease{}, nil, subscriptionDenied()
		}
	} else {
		var ok bool
		if view, ok = a.Subscription.OwnerLeaseFor(lease); !ok {
			return r, a, domain.SubscriptionLease{}, nil, subscriptionDenied()
		}
		if m, found := a.Subscription.Member(lease); found && l.Shared {
			member = &m
		}
	}
	if err := currentInstance(tx, machine, instance); err != nil {
		return r, a, view, member, err
	}
	if _, _, err := activeMachine(tx, machine); err != nil {
		return r, a, view, member, err
	}
	return r, a, view, member, nil
}

func (s *Service) TakeSubscription(ctx context.Context, req *connect.Request[pb.TakeSubscriptionRequest]) (response *connect.Response[pb.TakeSubscriptionResponse], returned error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	owned := false
	defer func() {
		if returned != nil && owned {
			if err := s.markSubscriptionRecovery(domain.ID(m.Id)); err != nil {
				s.logger.Warn("subscription_recovery_publication_unconfirmed", "account_id", m.Id, "code", domain.SafeError(err).Code)
			}
		}
	}()
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	if matched, err := s.isClaudeSubscription(ctx, m.Id); err != nil {
		return nil, rpc.Error(err, c)
	} else if matched {
		if _, managed, err := s.claudeAccountMode(ctx, m.Id); err != nil {
			return nil, rpc.Error(err, c)
		} else if managed {
			return s.takeManagedClaude(ctx, req)
		}
		return s.takeClaudeSubscription(ctx, req)
	}
	// Grok accounts never enter the Codex lifecycle below; they use the sealed
	// Grok bundle lifecycle, and the Codex account guard still refuses them.
	if matched, err := s.isGrokSubscription(ctx, m.Id); err != nil {
		return nil, rpc.Error(err, c)
	} else if matched {
		return s.takeGrokSubscription(ctx, req)
	}
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
	if input.Operation.Validate() != nil || action == "" {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	directExecution := false
	var forkRevision, titleRevision uint64
	// sharedOwner founds a versioned account-owner lease; joinedOwner admits one
	// more execution member to the live owner. Both require the negotiated
	// SharedSubscriptionOwnerV1 capability and only an execute-session job.
	sharedOwner, joinedOwner := false, false
	result, err := s.Store.Mutate(ctx, input.Lease, "subscription.take", input, func(tx *store.Tx) (any, error) {
		sharedOwner, joinedOwner = false, false
		// Take retains the original observation while another lease or metadata
		// update advances the account. Lifecycle authority is the exact still-queued
		// operation below; execution authority is its independently checked job.
		r, a, err := subscriptionAccount(tx, input.Account, 0)
		if err != nil {
			return nil, err
		}
		if action != domain.SubscriptionExecute && input.Revision > r.Revision {
			return nil, domain.Fail(domain.Conflict, "The account revision has not been observed.", "Retain the original queued operation and its observed revision.")
		}
		// A denial before the managed title's inference claim commits rolls this
		// transaction back: no lease, claim or credential exists. It is a definite
		// no-delivery result (Stop, Archive, rename, logout, disablement, creator
		// revocation, a recovery fence or a lost capability won first), so the
		// Worker can report and settle the title instead of treating it as an
		// uncertain execution. Once an earlier Take committed the claim, delivery
		// is unknown and the conservative recovery denial remains. Every other job
		// type keeps the recovery denial.
		claimedHere := false
		preLeaseDenied := func(job domain.Job, reason string) error {
			if job.Type != domain.GenerateSessionTitleJob {
				return subscriptionDenied()
			}
			if claimed, claimErr := tx.TitleInferenceClaimed(input.Operation); claimErr != nil || claimed && !claimedHere {
				return subscriptionDenied()
			}
			s.logger.InfoContext(ctx, "subscription_title_take_denied_before_lease", "account_id", input.Account, "operation_id", input.Operation, "reason", reason)
			return subscriptionTitleNotDelivered(domain.Canceled)
		}
		// accountGateDenied classifies a denial raised before the job is decoded.
		// It reads the operation only to prove it is this Worker's own managed
		// title; any other caller or job type keeps the generic denial.
		accountGateDenied := func(reason string) error {
			if action != domain.SubscriptionExecute {
				return subscriptionDenied()
			}
			jobRecord, err := tx.Get(domain.JobKind, input.Operation)
			if err != nil {
				return subscriptionDenied()
			}
			job, err := store.Decode[domain.Job](jobRecord)
			if err != nil || job.MachineID != input.Machine || job.InstanceID != input.Instance || job.AssignedDeviceID != actor.DeviceID {
				return subscriptionDenied()
			}
			return preLeaseDenied(job, reason)
		}
		state := a.Subscription
		if state == nil {
			return nil, subscriptionDenied()
		}
		if state.RecoveryRequired {
			return nil, accountGateDenied("recovery-fence")
		}
		// Execution, quota and reset-credit Take keep contention first. A fresh
		// lifecycle Take instead proves its queued original is still admissible
		// after the Worker fences and before contention, so a definitely unadmitted
		// claim is retired locally rather than retried behind an unrelated lease.
		lifecycle := action != domain.SubscriptionExecute && action != domain.SubscriptionQuota && action != domain.SubscriptionResetCredit
		accountBusy := func() error {
			if state.ExclusiveServerObservationActive() || state.Lease != nil || state.ServerOperation != nil && state.ServerOperation.NativeStarted {
				return domain.Fail(domain.ResourceExhausted, "The selected account is exclusively leased.", "Wait for its original cleanup; never switch accounts automatically.")
			}
			return nil
		}
		// A negotiated Worker may join the live shared owner of this account. The
		// job type decides below; every other holder keeps contention first.
		sharedCapable := false
		if action == domain.SubscriptionExecute {
			if _, machine, machineErr := activeMachine(tx, input.Machine); machineErr == nil {
				sharedCapable = slices.Contains(machine.WorkerCapabilities, domain.SharedSubscriptionOwnerV1)
			}
		}
		if !lifecycle {
			if sharedCapable && state.SharedOwner() {
				if state.ExclusiveServerObservationActive() || state.ServerOperation != nil && state.ServerOperation.NativeStarted {
					return nil, domain.Fail(domain.ResourceExhausted, "The selected account is exclusively leased.", "Wait for its original cleanup; never switch accounts automatically.")
				}
			} else if err := accountBusy(); err != nil {
				return nil, err
			}
		}
		if err := currentInstance(tx, input.Machine, input.Instance); err != nil {
			return nil, err
		}
		if action != domain.SubscriptionExecute {
			if _, err := subscriptionInstallation(tx, input.Machine); err != nil {
				return nil, err
			}
		}
		if action == domain.SubscriptionExecute {
			if state.Observation != nil && state.Observation.Active() {
				return nil, domain.Fail(domain.ResourceExhausted, "The selected account has a retained native observation.", "Wait for its original operation or explicitly reconcile the original reset-credit key.")
			}
			if state.Pending != nil && state.Pending.Action == domain.SubscriptionRefresh {
				return nil, domain.Fail(domain.ResourceExhausted, "The selected account is refreshing under exclusive ownership.", "Wait on this account's original operation without failover.")
			}
			// An accepted logout, a login, disablement or removal that won before the
			// title's Take leaves the claimed title untouched, so this gate must
			// return the same typed result as the other pre-lease denials.
			if state.Pending != nil || !a.Enabled || a.Health != domain.AccountReady || a.Removal != nil || a.Connection == nil || state.Generation == "" {
				return nil, accountGateDenied("account-state")
			}
			jr, err := tx.Get(domain.JobKind, input.Operation)
			if err != nil {
				return nil, err
			}
			job, err := store.Decode[domain.Job](jr)
			var execution domain.ExecutionJobInput
			if err != nil || jr.Revision != input.Revision || job.State != domain.JobClaimed || job.MachineID != input.Machine || job.InstanceID != input.Instance || job.AssignedDeviceID != actor.DeviceID {
				if err != nil {
					return nil, subscriptionDenied()
				}
				return nil, preLeaseDenied(job, "claim-lost")
			}
			if job.Type != domain.GenerateSessionTitleJob {
				// A claimed managed title has no lease until its own Take, yet it owns
				// the account's frozen generation. Any other owner that took the account
				// in that window would advance the generation on Finish and strand the
				// running title, so eligible work waits for the title's original custody.
				if reserved, err := managedTitleReservation(tx, r.ID, jr.ID); err != nil {
					return nil, err
				} else if reserved {
					s.logger.DebugContext(ctx, "subscription_take_deferred_for_title", "account_id", r.ID, "operation_id", jr.ID, "job_type", job.Type)
					return nil, managedTitleAccountReserved()
				}
			}
			switch job.Type {
			case domain.ExecuteSessionJob:
				if domain.Decode(job.Input, &execution) != nil || execution.Validate() != nil {
					return nil, subscriptionDenied()
				}
				if sharedCapable && !execution.SharedCodexOwnerEligible() {
					sharedCapable = false
					if err := accountBusy(); err != nil {
						return nil, err
					}
				}
				if sharedCapable {
					if state.Lease == nil {
						sharedOwner = true
					} else if err := s.sharedMemberAdmissible(state, input.Machine, input.Instance, actor.DeviceID, input.Operation); err != nil {
						return nil, err
					} else {
						joinedOwner = true
					}
				}

			case domain.ForkSessionJob:
				var fork domain.ForkJobInput
				if domain.Decode(job.Input, &fork) != nil || fork.Validate() != nil || (fork.Purpose != domain.SidechatFork && fork.Purpose != domain.IndependentFork) || fork.SubscriptionGeneration != state.Generation || (fork.Retry == nil && fork.SourceSessionID != jr.SessionID || fork.Retry != nil && fork.ChildSessionID != jr.SessionID) {
					return nil, subscriptionDenied()
				}
				if err := validateForkAuthority(tx, fork, forkAuthorityTake); err != nil {
					return nil, err
				}
				execution = fork.SourceAssignment
				forkRevision = jr.Revision
			case domain.NativeCodeReviewJob:
				var review domain.NativeCodeReviewInput
				if domain.DecodeNativeCodeReviewInput(job.Input, &review) != nil || review.Validate() != nil || review.SubscriptionGeneration != state.Generation {
					return nil, subscriptionDenied()
				}
				if _, err := nativeReviewAuthority(tx, jr, job); err != nil {
					return nil, err
				}
				execution = review.Source
			case domain.CompactSessionJob:
				var compact domain.SessionCompactionInput
				if domain.DecodeCompactionInput(job.Input, &compact) != nil || compact.Validate() != nil || (compact.Version != 2 && compact.Version != 4) {
					return nil, subscriptionDenied()
				}
				sr, session, err := sessionRecord(tx, jr.SessionID)
				_, machine, machineErr := activeMachine(tx, input.Machine)
				if err != nil || machineErr != nil || !compactionLeaseCapabilities(machine.WorkerCapabilities, compact) || session.CompactionJobID != jr.ID || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || session.ActiveExecutionID != "" || !session.OwnsExecution(compact.Assignment) {
					return nil, subscriptionDenied()
				}
				if err := checkedExecutionSource(tx, sr, session, machine, compact.Assignment); err != nil {
					return nil, err
				}
				if err := tx.RequireSessionBudget(sr.ID, session.EstimatedCostBudget); err != nil {
					return nil, err
				}
				execution = compact.Assignment
			case domain.GenerateSessionTitleJob:
				// A managed title reuses the EXECUTE lease but is admitted only by its
				// exact immutable assignment. The one-time inference claim commits in
				// this transaction, so a lost response can never deliver credentials or
				// send inference again. Pre-lease denials use preLeaseDenied above.
				authority, err := s.validateManagedTitle(tx, jr, job, managedTitleTake)
				if err != nil {
					switch domain.SafeError(err).Code {
					case domain.BudgetReached:
						return nil, err
					case domain.PermissionDenied, domain.Unsupported:
						return nil, preLeaseDenied(job, "authority-lost")
					}
					return nil, subscriptionDenied()
				}
				if authority.Input.Managed.Generation != state.Generation {
					return nil, preLeaseDenied(job, "generation-changed")
				}
				if claimed, err := tx.ClaimTitleInference(jr.ID); err != nil || !claimed {
					return nil, subscriptionDenied()
				}
				// The claim exists only inside this transaction. Any denial below
				// rolls it back with the rest, so it is still a no-delivery result.
				claimedHere = true
				execution = authority.Original
				titleRevision = jr.Revision
			default:
				return nil, subscriptionDenied()
			}
			// Fork, compaction, review and title jobs keep exclusive custody of the
			// account, so a live shared owner (or any lease) refuses them.
			if !sharedOwner && !joinedOwner {
				if err := accountBusy(); err != nil {
					return nil, err
				}
			}
			if execution.Version == 4 {
				_, machine, err := activeMachine(tx, input.Machine)
				if err != nil || !slices.Contains(machine.WorkerCapabilities, domain.ManagedCodexSubscriptionsV1) || !slices.Contains(machine.WorkerCapabilities, domain.ExecutionStartupV1) {
					return nil, preLeaseDenied(job, "capability-lost")
				}
				directExecution = true
			} else if _, err := subscriptionInstallation(tx, input.Machine); err != nil {
				return nil, err
			}

			if execution.AccountID != r.ID || execution.ConnectionID != a.Connection.ID || !execution.Configuration.Subscription || execution.Configuration.SubscriptionService != domain.SubscriptionChatGPT || execution.Configuration.Harness != domain.Codex {
				return nil, preLeaseDenied(job, "assignment-mismatch")
			}
			if canceled, err := tx.JobCancellationRequested(jr.ID); err != nil || canceled {
				if err != nil {
					return nil, subscriptionDenied()
				}
				return nil, preLeaseDenied(job, "cancellation-requested")
			}
		} else if action == domain.SubscriptionQuota || action == domain.SubscriptionResetCredit {
			op := state.Observation
			if op == nil || op.AutomaticEpisodeID != "" || op.ID != input.Operation || op.Action != action || op.MachineID != input.Machine || op.Phase != domain.SubscriptionObservationQueued || op.Generation != state.Generation || !quotaAccountReady(a) || state.Pending != nil || observationMachine(tx, input.Machine) != nil || subscriptionActorValid(tx, op.Actor) != nil {
				return nil, subscriptionDenied()
			}
		} else {
			op := state.Pending
			if err := tx.WorkerUpdateAdmission(input.Machine); err != nil {
				return nil, err
			}
			// A missing or replaced original is classified before any field of the
			// current Pending operation, which belongs to someone else's claim.
			if op == nil || op.ID != input.Operation {
				return nil, subscriptionTakeNotAdmitted()
			}
			original := op.Action == action && op.MachineID == input.Machine && op.Phase == domain.SubscriptionQueued
			if original && op.Canceled {
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
			// A member never rotates or receives authentication; it only binds one
			// exact job to the live owner.
			state.AdmitMember(domain.SubscriptionMember{LeaseID: input.Lease, OperationID: input.Operation, Revision: r.Revision + 1, State: domain.SubscriptionMemberRunning, AdmittedAt: now})
		} else {
			state.Lease = &domain.SubscriptionLease{ForkRevision: forkRevision, TitleRevision: titleRevision, ID: input.Lease, OperationID: input.Operation, Revision: r.Revision + 1, Action: action, MachineID: input.Machine, InstanceID: input.Instance, DeviceID: actor.DeviceID, Epoch: s.subscriptionServerEpoch(), Generation: state.Generation, StartedAt: now, Shared: sharedOwner}
			if sharedOwner {
				state.Members = []domain.SubscriptionMember{{LeaseID: input.Lease, OperationID: input.Operation, Revision: r.Revision + 1, State: domain.SubscriptionMemberRunning, AdmittedAt: now}}
			}
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return accountReceipt{ID: r.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	// A receipt never redistributes a secret. An uncertain first delivery retains
	// the lease and requires recovery rather than launching another native owner.
	if result.Replayed {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	owned = true
	var generation domain.ID
	var leaseRevision uint64
	var installation domain.Installation
	var ownerLease domain.ID
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, view, _, err := s.subscriptionLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance, joinedOwner)
		if err == nil {
			generation = a.Subscription.Generation
			leaseRevision = view.Revision
			if joinedOwner {
				ownerLease = a.Subscription.Lease.ID
			}
			// The v4 job already owns its immutable startup selection. Protected
			// bundle delivery cannot reintroduce discovery or version admission.
			if !directExecution {
				installation, err = subscriptionInstallation(tx, input.Machine)
			}
		}
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	// Every error below returns before any bundle crosses the boundary. For a
	// managed title that is a definite no-delivery outcome, so its exact lease is
	// released instead of fencing the account for recovery, and the caller returns
	// the typed no-delivery result instead of the original error (whose code, for
	// example RecoveryRequired, the Worker must treat as uncertain). When the
	// release is unconfirmed, or for every other lease, the original error is
	// returned and the conservative recovery fence stays.
	definiteNoDelivery := func(reason string, original error, code domain.Code) error {
		if joinedOwner {
			// A joined member holds no credential, so a failed admission releases
			// only that member and never fences the owner or its siblings.
			if releaseErr := s.releaseUndeliveredMember(ctx, s.subscriptionLeaseView, input.Account, input.Lease, input.Machine, input.Instance, reason); releaseErr != nil {
				s.logger.WarnContext(ctx, "subscription_member_release_unconfirmed", "account_id", input.Account, "lease_id", input.Lease, "reason", reason, "code", domain.SafeError(releaseErr).Code)
				return original
			}
			owned = false
			return subscriptionMemberNotAdmitted(code)
		}
		if titleRevision == 0 {
			return original
		}
		if releaseErr := s.releaseUndeliveredTitleLease(ctx, input.Account, input.Lease, input.Machine, input.Instance, leaseRevision); releaseErr != nil {
			// The lease stays owned, so the deferred recovery fence still applies.
			s.logger.WarnContext(ctx, "subscription_title_lease_release_unconfirmed", "account_id", input.Account, "lease_id", input.Lease, "reason", reason, "code", domain.SafeError(releaseErr).Code)
			return original
		}
		owned = false
		s.logger.InfoContext(ctx, "subscription_title_lease_released", "account_id", input.Account, "lease_id", input.Lease, "reason", reason, "result_code", code)
		return subscriptionTitleNotDelivered(code)
	}
	var bundle []byte
	// A joined member shares the live owner's native runtime. DeliDev never
	// distributes a second copy of the authentication, so no bundle is read.
	if generation != "" && !joinedOwner {
		vault, err := s.secrets()
		if err != nil {
			return nil, rpc.Error(definiteNoDelivery("vault-unavailable", err, domain.Unavailable), c)
		}
		bundle, err = vault.Get(ctx, credentials.Ref{Owner: input.Account, ID: generation, Purpose: credentials.AccountLogin})
		if err != nil {
			return nil, rpc.Error(definiteNoDelivery("vault-read-failed", err, domain.Unavailable), c)
		}
		if _, _, err := subscription.Parse(bundle); err != nil {
			clear(bundle)
			return nil, rpc.Error(definiteNoDelivery("bundle-invalid", err, domain.Unavailable), c)
		}
	}
	// Authorization after native I/O prevents revocation from winning during Get.
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		_, a, _, _, err := s.subscriptionLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance, joinedOwner)
		if err != nil || a.Subscription.RecoveryRequired {
			return subscriptionDenied()
		}
		if action == domain.SubscriptionExecute {
			if !a.Enabled || a.Health != domain.AccountReady || a.Removal != nil {
				return subscriptionDenied()
			}
			if canceled, err := tx.JobCancellationRequested(input.Operation); err != nil || canceled {
				return subscriptionDenied()
			}
			if lease := a.Subscription.Lease; lease.TitleRevision != 0 {
				if _, _, err := s.managedTitleLease(tx, *lease, managedTitleCustody); err != nil {
					return subscriptionDenied()
				}
			}
		} else if action == domain.SubscriptionQuota || action == domain.SubscriptionResetCredit {
			op := a.Subscription.Observation
			if op == nil || op.Phase != domain.SubscriptionObservationQueued || op.Generation != generation || subscriptionActorValid(tx, op.Actor) != nil || !quotaAccountReady(a) {
				return subscriptionDenied()
			}
		} else {
			op := a.Subscription.Pending
			if op == nil || op.Canceled || subscriptionActorValid(tx, op.Actor) != nil {
				return subscriptionDenied()
			}
		}
		return nil
	})
	if err != nil {
		clear(bundle)
		return nil, rpc.Error(definiteNoDelivery("denied-before-delivery", err, domain.Canceled), c)
	}
	s.logger.InfoContext(ctx, "subscription_lease_granted", "account_id", input.Account, "lease_id", input.Lease, "action", action, "shared_owner", sharedOwner, "joined_owner", joinedOwner, "owner_lease_id", ownerLease, "operation_id", input.Operation)
	installationJSON, _ := json.Marshal(installation)
	return connect.NewResponse(&pb.TakeSubscriptionResponse{LeaseId: string(input.Lease), GenerationId: string(generation), Bundle: bundle, LeaseRevision: leaseRevision, InstallationJson: installationJSON, OwnerLeaseId: string(ownerLease)}), nil
}

var subscriptionUserCode = regexp.MustCompile(`^[A-Z0-9-]{4,32}$`)

func (s *Service) PublishSubscriptionProgress(ctx context.Context, req *connect.Request[pb.PublishSubscriptionProgressRequest]) (*connect.Response[pb.PublishSubscriptionProgressResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	if matched, err := s.isClaudeSubscription(ctx, req.Msg.AccountId); err != nil {
		return nil, rpc.Error(err, c)
	} else if matched {
		return s.publishClaudeSubscriptionProgress(ctx, req)
	}
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	u, err := url.Parse(req.Msg.Url)
	if err != nil || len(req.Msg.Url) > 8192 || u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Host != "auth.openai.com" && u.Host != "chatgpt.com") || (req.Msg.UserCode != "" && !subscriptionUserCode.MatchString(req.Msg.UserCode)) {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	var op *domain.SubscriptionOperation
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		_, a, err := s.subscriptionLease(ctx, tx, domain.ID(req.Msg.AccountId), domain.ID(req.Msg.LeaseId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId))
		if err != nil {
			return err
		}
		op = a.Subscription.Pending
		if a.Subscription.RecoveryRequired || op == nil || op.Action != domain.SubscriptionLogin {
			return subscriptionDenied()
		}
		return subscriptionActorValid(tx, op.Actor)
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if s.subscriptionProgress == nil {
		s.subscriptionProgress = map[domain.ID]subscriptionProgress{}
	}
	if !op.Canceled {
		s.subscriptionProgress[op.ID] = subscriptionProgress{URL: req.Msg.Url, UserCode: req.Msg.UserCode}
	}
	return connect.NewResponse(&pb.PublishSubscriptionProgressResponse{Canceled: op.Canceled}), nil
}

func (s *Service) FinishSubscription(ctx context.Context, req *connect.Request[pb.FinishSubscriptionRequest]) (response *connect.Response[pb.FinishSubscriptionResponse], returned error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	owned := false
	defer func() {
		if returned != nil && owned {
			if err := s.markSubscriptionRecovery(domain.ID(m.Id)); err != nil {
				s.logger.Warn("subscription_recovery_publication_unconfirmed", "account_id", m.Id, "code", domain.SafeError(err).Code)
			}
		}
	}()
	defer clear(req.Msg.Bundle)
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	scope, err := subscriptionFinishScope(req.Msg.FinishScope)
	if err != nil {
		s.logger.Warn("subscription_finish_scope_rejected", "account_id", m.Id, "finish_scope", int32(req.Msg.FinishScope))
		return nil, rpc.Error(err, c)
	}
	if matched, err := s.isClaudeSubscription(ctx, m.Id); err != nil {
		return nil, rpc.Error(err, c)
	} else if matched {
		if _, managed, err := s.claudeAccountMode(ctx, m.Id); err != nil {
			return nil, rpc.Error(err, c)
		} else if managed {
			return s.finishManagedClaudeExecution(ctx, req)
		}
		return s.finishClaudeSubscription(ctx, req)
	}
	if matched, err := s.isGrokSubscription(ctx, m.Id); err != nil {
		return nil, rpc.Error(err, c)
	} else if matched {
		return s.finishGrokSubscription(ctx, req)
	}
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	if len(req.Msg.Bundle) > subscription.MaxBundle {
		return nil, rpc.Error(subscription.Invalid(), c)
	}
	input := subscriptionFinishInput{domain.ID(m.Id), domain.ID(req.Msg.LeaseId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), domain.ID(req.Msg.GenerationId), s.accountCommitment(domain.ID(m.RequestId), req.Msg.Bundle), m.ExpectedRevision, req.Msg.CleanupConfirmed, req.Msg.RefreshConfirmed, req.Msg.Succeeded, scope}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	result, replayed, err := s.Store.Replay(ctx, domain.ID(m.RequestId), "subscription.finish", input)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	var original domain.Account
	if !replayed && input.Scope == domain.SubscriptionFinishMember {
		// A member Finish releases one execution member and never reaches the
		// credential path below.
		if len(req.Msg.Bundle) != 0 || req.Msg.RefreshConfirmed {
			return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "A member Finish carries no authentication.", "Only the owner Finish writes back the credential generation."), c)
		}
		result, err = s.Store.Mutate(ctx, domain.ID(m.RequestId), "subscription.finish", input, func(tx *store.Tx) (any, error) {
			return s.settleSubscriptionMember(ctx, tx, s.subscriptionLeaseView, input.Account, input.Lease, input.Machine, input.Instance, input.Generation, input.Revision, input.Cleanup, input.Success)
		})
		if err != nil {
			return nil, rpc.Error(err, c)
		}
	} else if !replayed {
		var member *domain.SubscriptionMember
		err = s.Store.Read(ctx, func(tx *store.Tx) error {
			if err := tx.Authorize(); err != nil {
				return err
			}
			var view domain.SubscriptionLease
			_, original, view, member, err = s.subscriptionLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance, false)
			if err == nil && view.Action == domain.SubscriptionExecute {
				_, err = managedSidechatLease(tx, view)
				if err == nil && view.TitleRevision != 0 {
					_, _, err = s.managedTitleLease(tx, view, managedTitleCustody)
				}
			}
			return err
		})
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		state := original.Subscription
		lease := state.Lease
		// A shared owner is retired only by an explicit owner-scope Finish after
		// its last runnable member, and a singleton lease only by the legacy scope.
		// Neither crosses into the other representation.
		if err := ownerFinishAllowed(state, input.Scope, member); err != nil {
			s.logger.WarnContext(ctx, "subscription_owner_finish_refused", "account_id", input.Account, "lease_id", input.Lease, "finish_scope", input.Scope, "runnable_members", state.RunnableMembers(), "code", domain.SafeError(err).Code)
			return nil, rpc.Error(err, c)
		}
		// Receipt replay above is read-only. A new finish cannot resolve an
		// ownership loss or touch its vault generations through this channel.
		if state.RecoveryRequired || state.Generation != input.Generation || lease.Generation != input.Generation || lease.Revision != input.Revision {
			return nil, rpc.Error(subscriptionDenied(), c)
		}
		owned = true
		usable := input.Success && input.Cleanup && (lease.Action != domain.SubscriptionRefresh || input.Refresh)
		if state.Pending != nil && state.Pending.Canceled {
			usable = false
		}
		var identityCommitment string
		vault, err := s.secrets()
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		// A failed execution may release only confirmed pre-native ownership.
		// Cleanup alone is insufficient: the Worker must return the exact unused
		// original bundle, independently compared with its immutable generation.
		preNativeFailure := lease.Action == domain.SubscriptionExecute && !input.Success && input.Cleanup && !input.Refresh && len(req.Msg.Bundle) != 0
		if preNativeFailure {
			old, err := vault.Get(ctx, credentials.Ref{Owner: input.Account, ID: state.Generation, Purpose: credentials.AccountLogin})
			if err != nil {
				return nil, rpc.Error(err, c)
			}
			unchanged := bytes.Equal(old, req.Msg.Bundle)
			clear(old)
			if !unchanged {
				return nil, rpc.Error(subscriptionDenied(), c)
			}
		}
		if usable && lease.Action != domain.SubscriptionLogout {
			_, identity, err := subscription.Parse(req.Msg.Bundle)
			if err != nil {
				return nil, rpc.Error(err, c)
			}
			identityJSON, _ := json.Marshal(struct{ Account, User string }{identity.Account, identity.User})
			identityCommitment = s.accountCommitment(s.Identity.ServerID, identityJSON)
			clear(identityJSON)
			if state.IdentityCommitment != "" && identityCommitment != state.IdentityCommitment {
				return nil, rpc.Error(subscriptionDenied(), c)
			}
			if lease.Action == domain.SubscriptionLogin {
				err := s.Store.Read(ctx, func(tx *store.Tx) error {
					return uniqueSubscriptionIdentity(tx, input.Account, identityCommitment)
				})
				if err != nil {
					return nil, rpc.Error(err, c)
				}
			}
			if state.Generation != "" {
				old, err := vault.Get(ctx, credentials.Ref{Owner: input.Account, ID: state.Generation, Purpose: credentials.AccountLogin})
				if err != nil {
					return nil, rpc.Error(err, c)
				}
				if lease.Action == domain.SubscriptionRefresh || subscription.Digest(old) != subscription.Digest(req.Msg.Bundle) {
					err = subscription.Refreshed(old, req.Msg.Bundle)
				}
				clear(old)
				if err != nil {
					return nil, rpc.Error(err, c)
				}
			}
			// Only the exact authenticated Worker lease can stage this freshly
			// produced native bundle. No owner/client import endpoint exists.
			if _, err := vault.Put(ctx, credentials.Ref{Owner: input.Account, ID: domain.ID(m.RequestId), Purpose: credentials.AccountLogin}, req.Msg.Bundle); err != nil {
				return nil, rpc.Error(err, c)
			}
		}
		if input.Cleanup {
			// An uncertain failed operation retains its blocked original material
			// for recovery. Explicit logout still removes every owned reference.
			keep := state.Generation
			if usable && lease.Action != domain.SubscriptionLogout {
				keep = domain.ID(m.RequestId)
			} else if lease.Action == domain.SubscriptionLogout {
				keep = ""
			}
			if err := cleanupSubscriptionReferences(ctx, vault, input.Account, keep, protectedWorkerReferences(original.Subscription)...); err != nil {
				return nil, rpc.Error(err, c)
			}
		}
		var releasedCapture *domain.SubscriptionRecoveryCapture
		result, err = s.Store.Mutate(ctx, domain.ID(m.RequestId), "subscription.finish", input, func(tx *store.Tx) (any, error) {
			releasedCapture = nil
			if err := tx.Authorize(); err != nil {
				return nil, err
			}
			r, a, _, currentMember, err := s.subscriptionLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance, false)
			if err != nil {
				return nil, err
			}
			state := a.Subscription
			if err := ownerFinishAllowed(state, input.Scope, currentMember); err != nil {
				// A sibling was admitted between the check and the write.
				return nil, err
			}
			if lease.Action == domain.SubscriptionExecute {
				if _, err := managedSidechatLease(tx, *lease); err != nil {
					return nil, err
				}
				if lease.TitleRevision != 0 {
					if _, _, err := s.managedTitleLease(tx, *lease, managedTitleCustody); err != nil {
						return nil, err
					}
				}
			}
			if state.RecoveryRequired || state.Generation != input.Generation {
				return nil, subscriptionDenied()
			}
			if state.Pending != nil {
				if err := subscriptionActorValid(tx, state.Pending.Actor); err != nil {
					return nil, err
				}
				if state.Pending.Canceled {
					usable = false
				}
			}
			if usable && lease.Action == domain.SubscriptionLogin {
				if err := uniqueSubscriptionIdentity(tx, r.ID, identityCommitment); err != nil {
					return nil, err
				}
			}
			if !input.Cleanup || (!usable && lease.Action != domain.SubscriptionLogin && !preNativeFailure) {
				state.RecoveryRequired = true
				a.Health = domain.AccountFailed
				// Keep the lease and old generation denied. Neither an error nor
				// a process timeout establishes safe refresh-token redistribution.
			} else {
				state.Lease = nil
				// Owner retirement settles every retained member's cleanup ownership
				// together with the owner's confirmed containment. The protected
				// recovery capture belongs to the released lease and is retired with it.
				state.Members = nil
				releasedCapture, state.RecoveryCapture, state.RecoveryBlock = state.RecoveryCapture, nil, nil
				if state.Pending != nil && state.Pending.ID == lease.OperationID {
					state.Pending = nil
				}
				if usable && lease.Action != domain.SubscriptionLogout {
					// The original lease's first successful Finish is the only
					// generation that can continue an already-frozen server-owned
					// automatic claim. It is recorded only while the exact consent
					// snapshot is still current, so a revoked or replaced consent
					// and any later rotation cannot adopt the claim.
					if episode := state.AutomaticCreditEpisode; lease.Action == domain.SubscriptionExecute && episode != nil && episode.ServerProof != nil && episode.ServerFinishGeneration == "" && episode.ServerProof.LeaseID == lease.ID && state.AutomaticCreditConsent != nil && *state.AutomaticCreditConsent == episode.ServerProof.Consent && state.Generation == episode.ServerProof.Consent.Generation {
						episode.ServerFinishGeneration = domain.ID(m.RequestId)
					}
					state.AutomaticCreditConsent = nil
					state.Generation = domain.ID(m.RequestId)
					state.PaidCredits = nil
					state.IdentityCommitment = identityCommitment
					state.OwnerMachineID = lease.MachineID
					if state.Observation != nil && state.Observation.Generation == input.Generation {
						if state.Observation.Phase == domain.SubscriptionObservationQueued {
							state.Observation.Phase = domain.SubscriptionObservationFailed
							state.Observation.ErrorCode = domain.Conflict
						} else if state.Observation.Phase == domain.SubscriptionObservationSending {
							if state.Observation.Action == domain.SubscriptionQuota {
								// Quota reads consume nothing. Joined owner cleanup settles
								// a lost publication as failure, preserving last good values
								// without blocking future reads or native execution.
								state.Observation.Phase = domain.SubscriptionObservationFailed
								state.Observation.ErrorCode = domain.Unavailable
								state.QuotaState = domain.ObservationFailed
							} else {
								state.Observation.Phase = domain.SubscriptionObservationUncertain
							}
						}
					}
					if a.Connection == nil {
						a.Connection = &domain.AccountConnection{ID: domain.ID(m.RequestId), Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()}
						a.Health = domain.AccountReady
					}
					// A queued logout may be canceled by initiator revocation while
					// this execution finishes. Write-back cannot undo its revocation.
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
					state.PaidCredits = nil
					state.ResetCredits = nil
					state.QuotaObservedAt = nil
					state.QuotaState = domain.ObservationUnknown
					state.SpendControlReached = nil
					state.SpendControlObservedAt = nil
					if state.Observation != nil && (state.Observation.Phase == domain.SubscriptionObservationSending || state.Observation.Phase == domain.SubscriptionObservationUncertain) {
						state.Observation.Phase = domain.SubscriptionObservationRetiredUncertain
					}
				}
			}
			if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
				return nil, err
			}

			receipt := accountReceipt{ID: r.ID}
			if releasedCapture != nil {
				receipt.RetiredCapture = recoveryCaptureReferences(releasedCapture)
			}
			if usable && input.Cleanup && lease.Action == domain.SubscriptionExecute && lease.ForkRevision != 0 {
				jr, err := tx.Get(domain.JobKind, lease.OperationID)
				if err != nil {
					return nil, err
				}
				job, err := store.Decode[domain.Job](jr)
				if err != nil {
					return nil, err
				}
				if job.Type == domain.ForkSessionJob {
					var fork domain.ForkJobInput
					if domain.Decode(job.Input, &fork) != nil || fork.Validate() != nil || (fork.Purpose != domain.SidechatFork && fork.Purpose != domain.IndependentFork) || fork.SubscriptionGeneration != input.Generation || job.State != domain.JobClaimed || job.MachineID != lease.MachineID || job.InstanceID != lease.InstanceID || job.AssignedDeviceID != lease.DeviceID {
						return nil, subscriptionDenied()
					}
					receipt.ManagedSidechat = &domain.SubscriptionForkFinish{JobRevision: jr.Revision, Job: jr.ID, Account: r.ID, Machine: lease.MachineID, Instance: lease.InstanceID, Device: lease.DeviceID, Generation: input.Generation, Finish: domain.ID(m.RequestId)}
				}
			}
			if usable && input.Cleanup && lease.Action == domain.SubscriptionExecute && lease.TitleRevision != 0 {
				// Only a successful, independently cleaned managed title leaves the
				// server-written receipt that publication requires.
				// Account custody was validated above, before this transaction advanced
				// the account generation; here only the exact job revision is bound.
				jr, err := tx.Get(domain.JobKind, lease.OperationID)
				if err != nil {
					return nil, err
				}
				job, err := store.Decode[domain.Job](jr)
				if err != nil || job.Type != domain.GenerateSessionTitleJob || job.State != domain.JobClaimed || jr.Revision != lease.TitleRevision || job.MachineID != lease.MachineID || job.InstanceID != lease.InstanceID || job.AssignedDeviceID != lease.DeviceID {
					return nil, subscriptionDenied()
				}
				receipt.ManagedTitle = &domain.SubscriptionTitleFinish{JobRevision: jr.Revision, Job: jr.ID, Account: r.ID, Machine: lease.MachineID, Instance: lease.InstanceID, Device: lease.DeviceID, Generation: input.Generation, Finish: domain.ID(m.RequestId)}
			}
			return receipt, nil
		})
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		if original.Subscription.Pending != nil {
			delete(s.subscriptionProgress, original.Subscription.Pending.ID)
		}
		// Cancellation can win during vault staging. Take remains blocked by the
		// account gate until the unpublished new generation is tombstoned too.
		if !usable && input.Cleanup {
			keep := original.Subscription.Generation
			if lease.Action == domain.SubscriptionLogout {
				keep = ""
			}
			if err := cleanupSubscriptionReferences(ctx, vault, input.Account, keep, protectedWorkerReferences(original.Subscription)...); err != nil {
				// The owned-error defer records recovery after releasing accountGate.
				return nil, rpc.Error(err, c)
			}
		}
	}
	// Ownership mutation and the last vault cleanup are settled. A canceled
	// response-resource read cannot turn that committed receipt into recovery.
	owned = false
	if err := s.confirmFinishCaptureRetirement(ctx, input.Account, input.Lease, result); err != nil {
		return nil, rpc.Error(err, c)
	}
	r, err := s.accountRecord(ctx, input.Account)
	if err != nil {
		s.logger.WarnContext(ctx, "subscription_completion_presentation_unavailable", "account_id", input.Account, "lease_id", input.Lease, "replayed", replayed, "code", domain.SafeError(err).Code)
		return nil, rpc.Error(err, c)
	}
	// Unreferenced staged/old generations stay vault-owned and are never granted.
	// The explicit logout cleanup removes every owned reference before success.
	s.logger.InfoContext(ctx, "subscription_operation_finished", "account_id", input.Account, "lease_id", input.Lease, "cleanup_confirmed", input.Cleanup, "replayed", replayed, "finish_scope", input.Scope)
	return connect.NewResponse(&pb.FinishSubscriptionResponse{Account: rpc.Resource(r), Replayed: result.Replayed}), nil
}

// confirmFinishCaptureRetirement deletes the protected recovery references that
// a committed Finish released with its lease. The lease is already settled, so a
// failed deletion is returned as a retryable error without fencing the account;
// the Worker resends the same Finish and the exact replay deletes again from the
// receipt. The success response is therefore withheld until no captured original
// or latest credential bundle remains in the vault.
func (s *Service) confirmFinishCaptureRetirement(ctx context.Context, account, lease domain.ID, result store.Result) error {
	var receipt accountReceipt
	if len(result.Data) == 0 || domain.Decode(result.Data, &receipt) != nil || len(receipt.RetiredCapture) == 0 {
		return nil
	}
	vault, err := s.secrets()
	if err != nil {
		return err
	}
	if err := s.confirmRecoveryReferencesRetired(ctx, vault, account, receipt.RetiredCapture); err != nil {
		s.logger.WarnContext(ctx, "subscription_recovery_capture_retirement_pending", "phase", subscriptionRecoveryPhase, "account_id", account, "lease_id", lease, "references", len(receipt.RetiredCapture), "code", domain.SafeError(err).Code)
		return domain.Fail(domain.Unavailable, "The released recovery capture could not be removed yet.", "Resend the same Finish request; the receipt repeats the removal.")
	}
	s.logger.InfoContext(ctx, "subscription_recovery_capture_retired", "phase", subscriptionRecoveryPhase, "account_id", account, "lease_id", lease, "references", len(receipt.RetiredCapture))
	return nil
}

func uniqueSubscriptionIdentity(tx *store.Tx, owner domain.ID, identity string) error {
	accounts, err := all(tx, domain.AccountKind)
	if err != nil {
		return err
	}
	for _, other := range accounts {
		a, err := store.Decode[domain.Account](other)
		if err != nil {
			return err
		}
		if other.ID != owner && a.Subscription != nil && a.Subscription.IdentityCommitment == identity {
			return domain.Fail(domain.Conflict, "This provider account already has a managed owner.", "Use that account's exclusive connection instead of duplicating its credentials.")
		}
	}
	return nil
}

// workerReferenceRetirementBudget bounds the obsolete references one Worker
// Finish retires. Server-owned operations retain every superseded generation, so
// the number of unremoved references is not bounded by the Worker lifecycle. The
// budget is therefore per-call work, not a refusal limit: a Finish never fails
// because earlier operations retained generations, and the remaining obsolete
// generations stay vault-owned (they grant nothing) until a later Finish retires
// another budget or explicit logout or account deletion removes all of them.
const workerReferenceRetirementBudget = 256

// cleanupSubscriptionReferences is the Worker Finish retirement of obsolete
// AccountLogin generations: every reference except keep and protected is
// deleted, up to workerReferenceRetirementBudget per call. A reference of
// another owner or purpose denies the call before the first deletion. Server
// operations and explicit removal never call it; they use the server's own
// retention and removeAccountLoginReferences.
func cleanupSubscriptionReferences(ctx context.Context, vault accountSecrets, owner, keep domain.ID, protected ...domain.ID) error {
	refs, err := vault.UnremovedReferences(ctx, owner)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if ref.Owner != owner || ref.Purpose != credentials.AccountLogin {
			return subscriptionDenied()
		}
	}
	retired := 0
	for _, ref := range refs {
		if ref.ID == keep || slices.Contains(protected, ref.ID) {
			continue
		}
		if retired == workerReferenceRetirementBudget {
			break
		}
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		if err := vault.Delete(ctx, ref); err != nil {
			return err
		}
		retired++
	}
	return nil
}

// releaseUndeliveredTitleLease releases the exact EXECUTE lease of a managed
// title whose TakeSubscription is returning an error after the lease committed
// but before any credential was returned: cancellation, account logout, disable
// or removal, an invalid vault read or a lost custody lineage. No bundle crossed
// the boundary and no generation changed, so the account stays ready (or keeps
// its accepted logout runnable) instead of entering recovery. The one-time
// inference claim stays consumed, so the title can never send. The release is a
// bounded write detached from request cancellation, so a client disconnect
// cannot turn a definite no-delivery outcome into a recovery fence. The caller
// holds the accounts gate.
func (s *Service) releaseUndeliveredTitleLease(ctx context.Context, account, lease, machine, instance domain.ID, revision uint64) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := s.Store.Mutate(ctx, domain.NewID(), "subscription.title-release", struct{ Account, Lease domain.ID }{account, lease}, func(tx *store.Tx) (any, error) {
		r, a, err := s.subscriptionLease(ctx, tx, account, lease, machine, instance)
		if err != nil {
			return nil, err
		}
		state := a.Subscription
		held := state.Lease
		if state.RecoveryRequired || held.Action != domain.SubscriptionExecute || held.TitleRevision == 0 || held.Revision != revision {
			return nil, subscriptionDenied()
		}
		state.Lease = nil
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, err
	})
	return err
}

func (s *Service) markSubscriptionRecovery(id domain.ID) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	var pending domain.ID
	_, err = s.Store.Mutate(ctx, domain.NewID(), "subscription.recovery-required", id, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, id, 0)
		if err != nil {
			return nil, err
		}
		if a.Subscription == nil {
			return nil, subscriptionDenied()
		}
		if a.Subscription.Pending != nil {
			pending = a.Subscription.Pending.ID
		}
		a.Subscription.RecoveryRequired = true
		a.Health = domain.AccountFailed
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, err
	})
	if err == nil {
		s.clearClaudeLoginInput(pending)
	}
	return err
}

// Missing native/write-back acknowledgment never releases a lease, including
// across server restarts and Worker replacement. This denial publishes no key.
func (s *Service) retainLostSubscriptionLeases(machine, instance domain.ID, executionOnly bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Serialize recovery publication and cache invalidation with progress reads.
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	var presentations []domain.ID
	_, err = s.Store.Mutate(ctx, domain.NewID(), "subscription.lost-owner", struct {
		Machine, Instance domain.ID
		ExecutionOnly     bool
		Epoch             domain.ID
	}{machine, instance, executionOnly, s.subscriptionServerEpoch()}, func(tx *store.Tx) (any, error) {
		accounts, err := all(tx, domain.AccountKind)
		if err != nil {
			return nil, err
		}
		count := 0
		for _, r := range accounts {
			a, err := store.Decode[domain.Account](r)
			if err != nil {
				return nil, err
			}
			state := a.Subscription
			if state == nil || state.Lease == nil {
				continue
			}
			lease := state.Lease
			if machine == "" {
				if lease.Epoch == s.subscriptionServerEpoch() {
					continue
				}
			} else if lease.MachineID != machine || lease.InstanceID != instance || (lease.Action == domain.SubscriptionExecute) != executionOnly {
				continue
			}
			if state.Pending != nil {
				presentations = append(presentations, state.Pending.ID)
			}
			if state.RecoveryRequired {
				continue
			}
			state.RecoveryRequired = true
			a.Health = domain.AccountFailed
			if err := cancelAccountExecutions(tx, r.ID); err != nil {
				return nil, err
			}
			if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
				return nil, err
			}
			if lease.Shared {
				// Loss of the shared owner fences every member at once. No member is
				// adopted by another process and no input is replayed.
				s.logger.WarnContext(ctx, "subscription_shared_owner_lost", "account_id", r.ID, "owner_lease_id", lease.ID, "members", len(state.Members), "runnable_members", state.RunnableMembers())
			}
			count++
		}
		return struct{ Count int }{count}, nil
	})
	if err == nil {
		for _, operation := range presentations {
			s.clearClaudeLoginInput(operation)
		}
	}
	return err
}

// managedSidechatLease checks original Fork ownership before vault staging and
// again inside protected Finish. Ordinary execution and compaction keep their
// existing Finish behavior; a Sidechat cannot borrow those jobs' authority.
func managedSidechatLease(tx *store.Tx, lease domain.SubscriptionLease) (*domain.ForkJobInput, error) {
	if lease.ForkRevision == 0 {
		return nil, nil
	}
	jr, err := tx.Get(domain.JobKind, lease.OperationID)
	if err != nil {
		return nil, err
	}
	job, err := store.Decode[domain.Job](jr)
	if err != nil {
		return nil, err
	}
	if job.Type != domain.ForkSessionJob {
		return nil, nil
	}
	var fork domain.ForkJobInput
	if domain.Decode(job.Input, &fork) != nil || fork.Validate() != nil || (fork.Purpose != domain.SidechatFork && fork.Purpose != domain.IndependentFork) || fork.SubscriptionGeneration != lease.Generation || lease.ForkRevision == 0 || jr.Revision != lease.ForkRevision || job.State != domain.JobClaimed || job.MachineID != lease.MachineID || job.InstanceID != lease.InstanceID || job.AssignedDeviceID != lease.DeviceID {
		return nil, subscriptionDenied()
	}
	if err := validateForkAuthority(tx, fork, forkAuthorityLease); err != nil {
		return nil, err
	}
	return &fork, nil
}

// protectedWorkerReferences extends protectedGenerations with the protected
// recovery capture of a held lease. The Worker Finish retirement never deletes it
// before the lease it belongs to settles.
func protectedWorkerReferences(st *domain.SubscriptionState) []domain.ID {
	protected := protectedGenerations(st)
	if st != nil && st.RecoveryCapture != nil {
		protected = append(protected, st.RecoveryCapture.ID)
		if st.RecoveryCapture.OriginalCaptured {
			protected = append(protected, st.RecoveryCapture.OriginalID())
		}
	}
	return protected
}

// Access-only quota and credit readers own their captured references
// independently of the writer's current generation. Rotation may retire them
// only after joined observer cleanup and checked obsolete-reference retirement.
func protectedGenerations(st *domain.SubscriptionState) []domain.ID {
	var protected []domain.ID
	if st != nil && st.ServerQuota != nil && (st.ServerQuota.Active() || st.ServerQuota.ReferenceRetirementPending) {
		protected = append(protected, st.ServerQuota.Generation)
	}
	if st != nil && st.ServerCredit != nil && st.ServerCredit.AccessOnly && st.ServerCredit.Active() {
		protected = append(protected, st.ServerCredit.Generation)
	}
	return protected
}

// compactionLeaseCapabilities reports whether a Worker advertises the exact
// capability set for the validated compaction context-input kind. Revert
// (version 4) is an independent operation gated by CodexSessionRevertV1, plus
// CodexSessionRevertTargetProofV1 when the target selected skills or freezes a
// retained checkpoint reference; every other kind keeps the original native/Codex compaction pair. Neither
// set authorizes the other operation.
func compactionLeaseCapabilities(capabilities []domain.WorkerCapability, compact domain.SessionCompactionInput) bool {
	if compact.Revert != nil {
		return compact.Version == 4 && machineCapabilityContains(capabilities, domain.CodexSessionRevertV1) && (!compact.Revert.RequiresProofCapability() || machineCapabilityContains(capabilities, domain.CodexSessionRevertTargetProofV1))
	}
	return machineCapabilityContains(capabilities, domain.NativeSessionCompactionV1) && machineCapabilityContains(capabilities, domain.CodexSessionCompactionV1)
}

// subscriptionFinishScope maps the wire scope to the domain scope. Only
// UNSPECIFIED keeps the historical singleton meaning. Protobuf preserves unknown
// numeric enum values, so any other value (a malformed request or a future
// peer's scope) fails closed instead of inheriting the credential-writing
// legacy Finish.
func subscriptionFinishScope(value pb.SubscriptionFinishScope) (domain.SubscriptionFinishScope, error) {
	switch value {
	case pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_UNSPECIFIED:
		return domain.SubscriptionFinishLegacy, nil
	case pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER:
		return domain.SubscriptionFinishMember, nil
	case pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER:
		return domain.SubscriptionFinishOwner, nil
	default:
		return domain.SubscriptionFinishLegacy, domain.Fail(domain.InvalidArgument, "The subscription finish scope is not supported.", "Send an unspecified, member or owner finish scope that this server understands.")
	}
}
