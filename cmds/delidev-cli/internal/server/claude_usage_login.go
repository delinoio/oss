// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"slices"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// claudeUsageRun is the live owner of one account's usage work. Cancelling it
// makes the original native child stop; done closes once it is joined.
type claudeUsageRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type claudeUsagePresentation struct {
	URL   string
	Until time.Time
}

// claudeUsageState is the in-memory, never-persisted presentation and input of
// the live usage logins. It is cleared at every lifetime boundary.
type claudeUsageMemory struct {
	mu           sync.Mutex
	runs         map[domain.ID]*claudeUsageRun
	codes        map[domain.ID][]byte
	presentation map[domain.ID]claudeUsagePresentation
	running      map[domain.ID]bool
}

func (m *claudeUsageMemory) lock() func() { m.mu.Lock(); return m.mu.Unlock }

// registerRun records the live run of an account. A second concurrent run for
// the same account is refused; the owner guards this with the account gate.
func (s *Service) registerClaudeUsageRun(account domain.ID, cancel context.CancelFunc) (*claudeUsageRun, bool) {
	defer s.claudeUsage.lock()()
	if s.claudeUsage.runs == nil {
		s.claudeUsage.runs = map[domain.ID]*claudeUsageRun{}
	}
	if _, exists := s.claudeUsage.runs[account]; exists {
		return nil, false
	}
	run := &claudeUsageRun{cancel: cancel, done: make(chan struct{})}
	s.claudeUsage.runs[account] = run
	return run, true
}

func (s *Service) finishClaudeUsageRun(account domain.ID, run *claudeUsageRun) {
	unlock := s.claudeUsage.lock()
	if s.claudeUsage.runs[account] == run {
		delete(s.claudeUsage.runs, account)
	}
	unlock()
	close(run.done)
}

// cancelClaudeUsageRun asks the live run to stop and returns it so a caller that
// holds no account gate can join it.
func (s *Service) cancelClaudeUsageRun(account domain.ID) *claudeUsageRun {
	defer s.claudeUsage.lock()()
	run := s.claudeUsage.runs[account]
	if run != nil {
		run.cancel()
	}
	return run
}

// joinClaudeUsageRun waits for a cancelled run to join its native child. It is
// called without the account gate because the run needs the gate to settle.
func joinClaudeUsageRun(ctx context.Context, run *claudeUsageRun) error {
	if run == nil {
		return nil
	}
	timer := time.NewTimer(60 * time.Second)
	defer timer.Stop()
	select {
	case <-run.done:
		return nil
	case <-ctx.Done():
		return domain.SafeError(ctx.Err())
	case <-timer.C:
		return domain.NewClaudeLimitFailure(domain.ClaudeLimitFailureCleanupUncertain, 0)
	}
}

// stopClaudeUsageWork cancels the live usage run of an account and waits for it
// to join its native child. The caller must not hold the account gate because
// the run needs the gate to settle.
func (s *Service) stopClaudeUsageWork(ctx context.Context, account domain.ID) error {
	return joinClaudeUsageRun(ctx, s.cancelClaudeUsageRun(account))
}

// addClaudeUsageRetired records claude-usage references that are owed a
// deletion. The list stays sorted and free of duplicates and empty identifiers.
func addClaudeUsageRetired(usage *domain.ClaudeLimitState, references ...domain.ID) {
	for _, reference := range references {
		if reference != "" {
			usage.RetiredCredentials = append(usage.RetiredCredentials, reference)
		}
	}
	usage.RetiredCredentials = slices.Compact(slices.Sorted(slices.Values(usage.RetiredCredentials)))
}

// recordClaudeUsageRetirement durably records references whose vault state is
// unproven (an uncertain write or a failed deletion) as removal obligations of
// the maintenance scanner. The caller holds the account gate.
func (s *Service) recordClaudeUsageRetirement(ctx context.Context, id domain.ID, references ...domain.ID) error {
	_, err := s.Store.Mutate(ctx, domain.NewID(), "claude.usage.retire-reference", struct {
		Account    domain.ID
		References []domain.ID
	}{id, references}, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, id, 0)
		if err != nil {
			return nil, err
		}
		if a.Subscription == nil {
			return struct{}{}, nil
		}
		addClaudeUsageRetired(claudeUsageStateOf(&a), references...)
		return struct{}{}, putClaudeUsage(tx, r, a)
	})
	if err != nil {
		s.logger.WarnContext(ctx, "claude_usage_retirement_unrecorded", "account_id", id, "references", len(references), "code", domain.SafeError(err).Code)
		return err
	}
	s.logger.InfoContext(ctx, "claude_usage_retirement_recorded", "account_id", id, "references", len(references))
	return nil
}

func (s *Service) setClaudeUsagePresentation(operation domain.ID, value claudeUsagePresentation) {
	defer s.claudeUsage.lock()()
	if s.claudeUsage.presentation == nil {
		s.claudeUsage.presentation = map[domain.ID]claudeUsagePresentation{}
	}
	s.claudeUsage.presentation[operation] = value
}

func (s *Service) claudeUsagePresentationOf(operation domain.ID) claudeUsagePresentation {
	defer s.claudeUsage.lock()()
	return s.claudeUsage.presentation[operation]
}

// clearClaudeUsageInput clears the single-use approval code and the login
// address of one operation from server memory.
func (s *Service) clearClaudeUsageInput(operation domain.ID) {
	defer s.claudeUsage.lock()()
	clear(s.claudeUsage.codes[operation])
	delete(s.claudeUsage.codes, operation)
	delete(s.claudeUsage.presentation, operation)
}

// clearAllClaudeUsageInput clears every live approval code and address, used
// when the server closes.
func (s *Service) clearAllClaudeUsageInput() {
	defer s.claudeUsage.lock()()
	for operation := range s.claudeUsage.codes {
		clear(s.claudeUsage.codes[operation])
		delete(s.claudeUsage.codes, operation)
	}
	clear(s.claudeUsage.presentation)
}

// claudeUsageAccountTx decodes an account that may own a usage connection: a
// connected server-token Claude subscription account.
func claudeUsageAccountTx(tx *store.Tx, id domain.ID, revision uint64) (store.Record, domain.Account, error) {
	r, a, err := accountFromTx(tx, id, revision)
	if err == nil && (!a.ManagedClaudeAccount() || a.ProviderID != "" || a.Connection == nil || a.Removal != nil) {
		err = domain.Fail(domain.Unsupported, "Usage tracking requires a connected server-owned Claude account.", "Connect the Claude account first; usage tracking never changes its execution sign-in.")
	}
	return r, a, err
}

// claudeUsageStateOf returns the account's usage state, creating an empty one.
func claudeUsageStateOf(a *domain.Account) *domain.ClaudeLimitState {
	if a.Subscription.ClaudeUsage == nil {
		a.Subscription.ClaudeUsage = &domain.ClaudeLimitState{}
	}
	return a.Subscription.ClaudeUsage
}

// putClaudeUsage re-projects the informational windows from the authoritative
// usage state and stores the account.
func putClaudeUsage(tx *store.Tx, r store.Record, a domain.Account) error {
	a.ProjectClaudeLimits()
	_, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	return err
}

func claudeUsageConflict(message, guidance string) error {
	return domain.Fail(domain.Conflict, message, guidance)
}

func claudeUsageValidGeneration(value string) (domain.ID, error) {
	id := domain.ID(value)
	if id != "" {
		if err := id.Validate(); err != nil {
			return "", err
		}
	}
	return id, nil
}

// StartClaudeUsage accepts one usage login. Replays of the original request
// return the original operation and never launch another sign-in.
func (s *Service) StartClaudeUsage(ctx context.Context, req *connect.Request[pb.StartClaudeUsageRequest]) (*connect.Response[pb.StartClaudeUsageResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := subscriptionClient(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	m := req.Msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	observed, err := claudeUsageValidGeneration(req.Msg.ObservedUsageGeneration)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	input := struct {
		ID       domain.ID
		Revision uint64
		Observed domain.ID
		Actor    domain.Principal
	}{domain.ID(m.Id), m.ExpectedRevision, observed, actor}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "claude.usage.start", input, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, input.ID, input.Revision)
		if err != nil {
			return nil, err
		}
		state := a.Subscription
		if state.Pending != nil || state.RecoveryRequired {
			return nil, subscriptionDenied()
		}
		usage := claudeUsageStateOf(&a)
		if usage.Generation != input.Observed {
			return nil, claudeUsageConflict("The Claude usage connection changed.", "Read the current account before connecting usage tracking.")
		}
		if usage.Operation != nil && usage.Operation.Active() || usage.Read.Active() {
			return nil, claudeUsageConflict("Claude usage work is already in progress.", "Wait for it to finish or cancel it before starting another sign-in.")
		}
		if usage.CleanupPending || len(usage.RetiredCredentials) != 0 {
			return nil, claudeUsageConflict("Claude usage cleanup is still pending.", "Wait for the previous usage connection to finish its cleanup.")
		}
		now := time.Now().UTC()
		usage.Operation = &domain.ClaudeLimitOperation{ID: domain.ID(m.RequestId), Epoch: s.subscriptionServerEpoch(), Generation: domain.ID(m.RequestId), Actor: actor, State: domain.ClaudeLimitLoginPreparing, StartedAt: now, ExpiresAt: now.Add(domain.ClaudeLimitOperationWindow)}
		if err := putClaudeUsage(tx, r, a); err != nil {
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
	s.logger.InfoContext(ctx, "claude_usage_login_accepted", "account_id", input.ID, "operation_id", m.RequestId, "replayed", result.Replayed)
	return connect.NewResponse(&pb.StartClaudeUsageResponse{Account: rpc.Resource(r), OperationId: m.RequestId, UsageGeneration: m.RequestId, Replayed: result.Replayed}), nil
}

// GetClaudeUsageProgress is a read: it never launches, retries or advances
// native work and never extends the operation window.
func (s *Service) GetClaudeUsageProgress(ctx context.Context, req *connect.Request[pb.GetClaudeUsageProgressRequest]) (*connect.Response[pb.GetClaudeUsageProgressResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := subscriptionClient(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	response := &pb.GetClaudeUsageProgressResponse{}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := claudeUsageAccountTx(tx, domain.ID(req.Msg.AccountId), 0)
		if err != nil {
			return err
		}
		usage := a.Subscription.ClaudeUsage
		if usage == nil || usage.Operation == nil {
			return subscriptionDenied()
		}
		o := usage.Operation
		if string(o.ID) != req.Msg.OperationId || string(o.Generation) != req.Msg.UsageGeneration || o.Actor != actor {
			return subscriptionDenied()
		}
		if err := subscriptionActorValid(tx, o.Actor); err != nil {
			s.clearClaudeUsageInput(o.ID)
			return err
		}
		state := o.State
		if (state == domain.ClaudeLimitLoginPreparing || state == domain.ClaudeLimitLoginWaiting) && (o.Epoch != s.subscriptionServerEpoch() || !o.ExpiresAt.After(time.Now().UTC())) {
			state = domain.ClaudeLimitLoginExpired
			if o.Epoch != s.subscriptionServerEpoch() {
				state = domain.ClaudeLimitLoginRecovery
			}
		}
		response.State = wireClaudeUsageState(state)
		response.Failure = wireClaudeUsageFailure(o.Failure)
		response.Canceled = o.Canceled || state == domain.ClaudeLimitLoginCanceled
		if state == domain.ClaudeLimitLoginWaiting && !response.Canceled {
			response.Url = s.claudeUsagePresentationOf(o.ID).URL
			response.LoginMethod = pb.SubscriptionLoginMethod_SUBSCRIPTION_LOGIN_METHOD_BROWSER_CODE
			response.CodeRequested = o.CodeSubmissionID == ""
		}
		if state == domain.ClaudeLimitLoginConfirming && o.Identity != nil {
			response.Identity = &pb.ClaudeUsageIdentity{Email: o.Identity.Email, Organization: o.Identity.Organization, Plan: o.Identity.Plan}
		}
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	return connect.NewResponse(response), nil
}

// SubmitClaudeUsageCode records one approval-code claim. The bytes stay in
// server memory until the original child consumes them once.
func (s *Service) SubmitClaudeUsageCode(ctx context.Context, req *connect.Request[pb.SubmitClaudeUsageCodeRequest]) (*connect.Response[pb.SubmitClaudeUsageCodeResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	defer clear(req.Msg.Code)
	actor, err := subscriptionClient(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	m := req.Msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	operation, generation := domain.ID(req.Msg.OperationId), domain.ID(req.Msg.UsageGeneration)
	if operation.Validate() != nil || generation.Validate() != nil || !claude.LoginCodeValid(req.Msg.Code) {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	input := struct {
		Account, Operation, Generation domain.ID
		Revision                       uint64
		Actor                          domain.Principal
		Commitment                     string
	}{domain.ID(m.Id), operation, generation, m.ExpectedRevision, actor, s.accountCommitment(domain.ID(m.RequestId), req.Msg.Code)}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "claude.usage.code", input, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, input.Account, input.Revision)
		if err != nil {
			return nil, err
		}
		usage := a.Subscription.ClaudeUsage
		if usage == nil || usage.Operation == nil {
			return nil, subscriptionDenied()
		}
		o := usage.Operation
		if o.ID != operation || o.Generation != generation || o.Actor != actor || o.Canceled || !o.NativeStarted || o.Epoch != s.subscriptionServerEpoch() || o.State != domain.ClaudeLimitLoginWaiting || o.CodeSubmissionID != "" || !o.ExpiresAt.After(time.Now().UTC()) || subscriptionActorValid(tx, actor) != nil {
			return nil, subscriptionDenied()
		}
		o.CodeSubmissionID = domain.ID(m.RequestId)
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, err
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	// An exact replay of a claim whose first acknowledgment was lost restores the
	// bytes while the committed claim is still unconsumed, so the original child
	// still receives the code it was promised.
	if retain := !result.Replayed || s.claudeUsageClaimPending(ctx, input.Account, operation, domain.ID(m.RequestId)); retain {
		unlockMemory := s.claudeUsage.lock()
		if s.claudeUsage.codes == nil {
			s.claudeUsage.codes = map[domain.ID][]byte{}
		}
		if len(s.claudeUsage.codes[operation]) == 0 {
			s.claudeUsage.codes[operation] = slices.Clone(req.Msg.Code)
		}
		unlockMemory()
	}
	s.logger.InfoContext(ctx, "claude_usage_code_claimed", "operation_id", operation, "replayed", result.Replayed)
	return connect.NewResponse(&pb.SubmitClaudeUsageCodeResponse{Accepted: true}), nil
}

// claudeUsageClaimPending reports whether the stored operation still holds the
// given approval-code claim unconsumed. The caller holds the account gate.
func (s *Service) claudeUsageClaimPending(ctx context.Context, account, operation, claim domain.ID) bool {
	pending := false
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := claudeUsageAccountTx(tx, account, 0)
		if err != nil {
			return err
		}
		usage := a.Subscription.ClaudeUsage
		pending = usage != nil && usage.Operation != nil && usage.Operation.ID == operation && usage.Operation.State == domain.ClaudeLimitLoginWaiting && usage.Operation.CodeSubmissionID == claim && !usage.Operation.CodeConsumed
		return nil
	})
	return err == nil && pending
}

// ConfirmClaudeUsageLink publishes the association the user confirmed. It never
// infers that the validated identity equals the execution account.
func (s *Service) ConfirmClaudeUsageLink(ctx context.Context, req *connect.Request[pb.ConfirmClaudeUsageLinkRequest]) (*connect.Response[pb.ConfirmClaudeUsageLinkResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := subscriptionClient(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	m := req.Msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	operation, generation := domain.ID(req.Msg.OperationId), domain.ID(req.Msg.UsageGeneration)
	if operation.Validate() != nil || generation.Validate() != nil {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	input := struct {
		Account, Operation, Generation domain.ID
		Revision                       uint64
		Actor                          domain.Principal
	}{domain.ID(m.Id), operation, generation, m.ExpectedRevision, actor}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "claude.usage.confirm", input, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, input.Account, input.Revision)
		if err != nil {
			return nil, err
		}
		state := a.Subscription
		usage := state.ClaudeUsage
		if usage == nil || usage.Operation == nil || state.Pending != nil || state.RecoveryRequired {
			return nil, subscriptionDenied()
		}
		o := usage.Operation
		if o.ID != operation || o.Generation != generation || o.Actor != actor || o.State != domain.ClaudeLimitLoginConfirming || o.Canceled || o.Identity == nil || o.CredentialID == "" || usage.CleanupPending || subscriptionActorValid(tx, actor) != nil {
			return nil, subscriptionDenied()
		}
		now := time.Now().UTC()
		if usage.CredentialID != "" {
			usage.RetiredCredentials = append(usage.RetiredCredentials, usage.CredentialID)
		}
		// Reconnect: the generation always changes, so the previous usage evidence
		// is cleared. The previous credential is retired after this commit.
		usage.Generation = o.Generation
		usage.Identity = o.Identity
		usage.CredentialID = o.CredentialID
		usage.LinkedAt = &now
		usage.ClearEvidence()
		usage.Read = domain.ClaudeLimitRead{}
		usage.ReconnectRequired = false
		o.State = domain.ClaudeLimitLoginLinked
		o.CredentialID = ""
		o.Identity = nil
		if err := putClaudeUsage(tx, r, a); err != nil {
			return nil, err
		}
		return accountReceipt{ID: r.ID}, nil
	})
	unlock()
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.clearClaudeUsageInput(operation)
	s.logger.InfoContext(ctx, "claude_usage_linked", "account_id", input.Account, "operation_id", operation, "replayed", result.Replayed)
	// A previous credential retired by a reconnect is deleted after the commit;
	// a failure is retried by the usage maintenance loop.
	if err := s.completeClaudeUsageCleanup(ctx, input.Account, false); err != nil {
		s.logger.WarnContext(ctx, "claude_usage_cleanup_pending", "account_id", input.Account, "code", domain.SafeError(err).Code)
	}
	r, err := s.accountRecord(ctx, input.Account)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	return connect.NewResponse(&pb.ConfirmClaudeUsageLinkResponse{Account: rpc.Resource(r), Replayed: result.Replayed}), nil
}

// CancelClaudeUsage cancels an unconfirmed usage login. A live login is
// asked to stop and the runner joins it and discards its credentials; an
// operation waiting for confirmation discards its staged credential directly.
func (s *Service) CancelClaudeUsage(ctx context.Context, req *connect.Request[pb.CancelClaudeUsageRequest]) (*connect.Response[pb.CancelClaudeUsageResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := subscriptionClient(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	m := req.Msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	operation, generation := domain.ID(req.Msg.OperationId), domain.ID(req.Msg.UsageGeneration)
	if operation.Validate() != nil || generation.Validate() != nil {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	input := struct {
		Account, Operation, Generation domain.ID
		Revision                       uint64
		Actor                          domain.Principal
	}{domain.ID(m.Id), operation, generation, m.ExpectedRevision, actor}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	var live bool
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "claude.usage.cancel", input, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, input.Account, input.Revision)
		if err != nil {
			return nil, err
		}
		usage := a.Subscription.ClaudeUsage
		if usage == nil || usage.Operation == nil {
			return nil, subscriptionDenied()
		}
		o := usage.Operation
		if o.ID != operation || o.Generation != generation || o.Actor != actor {
			return nil, subscriptionDenied()
		}
		switch o.State {
		case domain.ClaudeLimitLoginPreparing, domain.ClaudeLimitLoginWaiting:
			o.Canceled = true
			if !o.NativeStarted {
				o.State = domain.ClaudeLimitLoginCanceled
			}
			live = o.NativeStarted
		case domain.ClaudeLimitLoginConfirming:
			if o.CredentialID != "" {
				usage.RetiredCredentials = append(usage.RetiredCredentials, o.CredentialID)
			}
			o.State, o.Canceled, o.CredentialID, o.Identity = domain.ClaudeLimitLoginCanceled, true, "", nil
		default:
			return nil, claudeUsageConflict("This usage login can no longer be canceled.", "Read the current usage connection state.")
		}
		if err := putClaudeUsage(tx, r, a); err != nil {
			return nil, err
		}
		return accountReceipt{ID: r.ID}, nil
	})
	unlock()
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if live {
		// The runner joins the original child and discards unconfirmed credentials.
		s.cancelClaudeUsageRun(input.Account)
	} else if err := s.completeClaudeUsageCleanup(ctx, input.Account, false); err != nil {
		s.logger.WarnContext(ctx, "claude_usage_cleanup_pending", "account_id", input.Account, "code", domain.SafeError(err).Code)
	}
	s.clearClaudeUsageInput(operation)
	s.logger.InfoContext(ctx, "claude_usage_cancel_accepted", "account_id", input.Account, "operation_id", operation, "replayed", result.Replayed)
	r, err := s.accountRecord(ctx, input.Account)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	return connect.NewResponse(&pb.CancelClaudeUsageResponse{Account: rpc.Resource(r), Replayed: result.Replayed}), nil
}

// DisconnectClaudeUsage revokes usage reads immediately, stops and joins any
// live usage work and removes only the usage credentials and profile. The
// execution setup-token and the account's execution readiness are untouched.
// Live work is canceled only after a current, non-replayed disconnect was
// accepted, so a stale or replayed request can never interrupt another client's
// work.
func (s *Service) DisconnectClaudeUsage(ctx context.Context, req *connect.Request[pb.DisconnectClaudeUsageRequest]) (*connect.Response[pb.DisconnectClaudeUsageResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := subscriptionClient(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	m := req.Msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	generation, err := claudeUsageValidGeneration(req.Msg.UsageGeneration)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	// The initiating actor is part of the immutable input, so another paired
	// client reusing the request identifier is a different request.
	input := struct {
		Account    domain.ID
		Revision   uint64
		Generation domain.ID
		Actor      domain.Principal
	}{domain.ID(m.Id), m.ExpectedRevision, generation, actor}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "claude.usage.disconnect", input, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, input.Account, input.Revision)
		if err != nil {
			return nil, err
		}
		usage := a.Subscription.ClaudeUsage
		if usage == nil || usage.Generation != generation {
			return nil, claudeUsageConflict("The Claude usage connection changed.", "Read the current account before disconnecting usage tracking.")
		}
		revokeClaudeUsage(usage)
		if err := putClaudeUsage(tx, r, a); err != nil {
			return nil, err
		}
		return accountReceipt{ID: r.ID}, nil
	})
	revoked := err == nil && s.claudeUsageRevoked(ctx, input.Account)
	unlock()
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "claude_usage_disconnect_accepted", "account_id", input.Account, "replayed", result.Replayed)
	// The revocation is durable, so the live run can no longer publish anything.
	// Stop and join it outside the gate before the profile is removed. A replay
	// only joins while the stored state is still the revoked one it produced; a
	// newer connection's run is never touched.
	if !result.Replayed || revoked {
		if err := s.stopClaudeUsageWork(ctx, input.Account); err != nil {
			s.logger.WarnContext(ctx, "claude_usage_disconnect_join_unconfirmed", "account_id", input.Account, "code", domain.SafeError(err).Code)
			return nil, rpc.Error(err, c)
		}
	}
	// Cleanup completes before it is reported: a replay retries an incomplete one
	// and never reports success while storage or references remain.
	if err := s.completeClaudeUsageCleanup(ctx, input.Account, true); err != nil {
		s.logger.WarnContext(ctx, "claude_usage_cleanup_pending", "account_id", input.Account, "code", domain.SafeError(err).Code)
		return nil, rpc.Error(domain.Fail(domain.RecoveryRequired, "Claude usage reads were revoked but cleanup is not complete.", "Retry the same disconnect request; cleanup is retried automatically."), c)
	}
	r, err := s.accountRecord(ctx, input.Account)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	return connect.NewResponse(&pb.DisconnectClaudeUsageResponse{Account: rpc.Resource(r), Replayed: result.Replayed}), nil
}

// claudeUsageRevoked reports whether the stored usage state is still the revoked
// one a disconnect leaves behind: no generation, no operation and a pending
// removal. A new connection or login makes it false. The caller holds the gate.
func (s *Service) claudeUsageRevoked(ctx context.Context, account domain.ID) bool {
	revoked := false
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := accountFromTx(tx, account, 0)
		if err != nil {
			return err
		}
		var usage *domain.ClaudeLimitState
		if a.Subscription != nil {
			usage = a.Subscription.ClaudeUsage
		}
		revoked = usage != nil && usage.Generation == "" && usage.Operation == nil && !usage.Read.Active() && (usage.CleanupPending || len(usage.RetiredCredentials) != 0)
		return nil
	})
	return err == nil && revoked
}

// revokeClaudeUsage ends the connection in the stored state: reads stop at
// once, every credential reference becomes a removal obligation and the usage
// evidence is cleared. The execution state is not touched.
func revokeClaudeUsage(usage *domain.ClaudeLimitState) {
	addClaudeUsageRetired(usage, usage.CredentialID)
	if o := usage.Operation; o != nil {
		addClaudeUsageRetired(usage, o.CredentialID)
	}
	usage.Generation, usage.Identity, usage.LinkedAt, usage.CredentialID = "", nil, nil, ""
	usage.ClearEvidence()
	usage.Read = domain.ClaudeLimitRead{}
	usage.ReconnectRequired = false
	usage.Operation = nil
	usage.CleanupPending = true
}

// stageClaudeUsageCredential stores a sealed envelope under its immutable
// claude-usage reference. The caller holds the account gate.
func (s *Service) stageClaudeUsageCredential(ctx context.Context, account, credential domain.ID, envelope []byte) error {
	vault, err := s.secrets()
	if err != nil {
		return err
	}
	_, err = vault.Put(ctx, credentials.Ref{Owner: account, ID: credential, Purpose: credentials.ClaudeUsage}, envelope)
	return err
}

// openClaudeUsageCredential reads and validates the current bundle. The caller
// holds the account gate and must Clear the bundle.
func (s *Service) openClaudeUsageCredential(ctx context.Context, account domain.ID, generation, credential domain.ID) (subscription.ClaudeUsageBundle, error) {
	vault, err := s.secrets()
	if err != nil {
		return subscription.ClaudeUsageBundle{}, err
	}
	raw, err := vault.Get(ctx, credentials.Ref{Owner: account, ID: credential, Purpose: credentials.ClaudeUsage})
	if err != nil {
		return subscription.ClaudeUsageBundle{}, err
	}
	defer clear(raw)
	return subscription.OpenClaudeUsage(raw, s.Identity.ServerID, account, generation, credential, time.Now().UTC())
}
