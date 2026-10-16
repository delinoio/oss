// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Usage-login mutations. Every mutation carries a UUID-v7 request ID and the
// original account revision, binds to the server-allocated usage generation and
// operation, and is replay-safe through the durable receipt. Progress reads
// never relaunch, advance or confirm work.

// claudeUsageReceipt is the secret-free durable result of a usage mutation.
type claudeUsageReceipt struct {
	Account    domain.ID `json:"account"`
	Operation  domain.ID `json:"operation,omitempty"`
	Generation domain.ID `json:"generation,omitempty"`
}

func claudeUsageIDs(operation, generation string) (domain.ID, domain.ID, error) {
	op, gen := domain.ID(operation), domain.ID(generation)
	if err := op.Validate(); err != nil {
		return "", "", err
	}
	if err := gen.Validate(); err != nil {
		return "", "", err
	}
	return op, gen, nil
}

func claudeUsageUnavailable() *domain.Error {
	return domain.Fail(domain.Unavailable, "Claude usage tracking cleanup is still pending.", "Wait for the server to finish removing the previous usage credential, then retry.")
}

func claudeUsageConflict(message string) *domain.Error {
	return domain.Fail(domain.Conflict, message, "Read current account status; the usage generation or operation changed.")
}

type claudeUsageStartInput struct {
	Account  domain.ID        `json:"account"`
	Revision uint64           `json:"revision"`
	Actor    domain.Principal `json:"actor"`
}

func (s *Service) startClaudeUsageLogin(ctx context.Context, req *connect.Request[pb.StartClaudeUsageLoginRequest]) (*connect.Response[pb.StartClaudeUsageLoginResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := subscriptionClient(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	m := req.Msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	account, operation := domain.ID(m.Id), domain.ID(m.RequestId)
	input := claudeUsageStartInput{account, m.ExpectedRevision, actor}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	respond := func(receipt claudeUsageReceipt, replayed bool) (*connect.Response[pb.StartClaudeUsageLoginResponse], error) {
		r, err := s.accountRecord(ctx, account)
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		return connect.NewResponse(&pb.StartClaudeUsageLoginResponse{Account: rpc.Resource(r), OperationId: string(receipt.Operation), UsageGeneration: string(receipt.Generation), Replayed: replayed}), nil
	}
	if replay, found, err := s.Store.Replay(ctx, operation, "claude.usage.start", input); err != nil {
		return nil, rpc.Error(err, c)
	} else if found {
		var receipt claudeUsageReceipt
		if err := json.Unmarshal(replay.Data, &receipt); err != nil {
			return nil, rpc.Error(err, c)
		}
		return respond(receipt, true)
	}
	if s.stopping.Load() {
		return nil, rpc.Error(domain.Fail(domain.Unavailable, "The server is stopping.", "Retry after the server restarts."), c)
	}
	// The live login is registered before it is committed, so overlapping starts
	// and a start during shutdown are refused without durable state.
	now := time.Now().UTC()
	run, runCtx, err := s.claudeUsage.reserve(account, operation, now.Add(domain.MaxClaudeUsageLoginWindow))
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	var started domain.ClaudeUsageOperation
	var generation domain.ID
	result, err := s.Store.Mutate(ctx, operation, "claude.usage.start", input, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, account, input.Revision)
		if err != nil {
			return nil, err
		}
		if !claudeUsageStartable(a) || subscriptionActorValid(tx, actor) != nil {
			return nil, subscriptionDenied()
		}
		current := a.Subscription.ClaudeUsage
		if current != nil && current.Connection != domain.ClaudeUsageReconnectRequired {
			return nil, claudeUsageConflict("Cancel or disconnect the current Claude usage connection first.")
		}
		if current != nil && current.ProfileCleanup {
			return nil, claudeUsageUnavailable()
		}
		started = domain.ClaudeUsageOperation{ID: operation, Actor: actor, Epoch: s.subscriptionServerEpoch(), State: domain.SubscriptionPreparing, StartedAt: now, ExpiresAt: now.Add(domain.MaxClaudeUsageLoginWindow)}
		generation = domain.NewID()
		next, err := domain.NewClaudeUsageLogin(current, generation, started)
		if err != nil {
			return nil, err
		}
		a.Subscription.ClaudeUsage = next
		// A new generation never inherits earlier evidence.
		domain.ClearClaudeUsageQuota(&a)
		if err := a.Validate(); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return claudeUsageReceipt{Account: account, Operation: operation, Generation: generation}, nil
	})
	if err != nil {
		s.claudeUsage.release(run)
		return nil, rpc.Error(err, c)
	}
	if result.Replayed {
		// Another request committed the receipt between the replay check and the
		// mutation: the original run owns the work.
		s.claudeUsage.release(run)
		var receipt claudeUsageReceipt
		if err := json.Unmarshal(result.Data, &receipt); err != nil {
			return nil, rpc.Error(err, c)
		}
		return respond(receipt, true)
	}
	s.launchClaudeUsage(run, runCtx, started, generation)
	s.logger.InfoContext(ctx, "claude_usage_login_accepted", "account_id", account, "operation_id", operation)
	return respond(claudeUsageReceipt{Account: account, Operation: operation, Generation: generation}, false)
}

func (s *Service) claudeUsageProgress(ctx context.Context, req *connect.Request[pb.GetClaudeUsageLoginProgressRequest]) (*connect.Response[pb.GetClaudeUsageLoginProgressResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := subscriptionClient(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	account := domain.ID(req.Msg.AccountId)
	operation, generation, err := claudeUsageIDs(req.Msg.OperationId, req.Msg.UsageGeneration)
	if err != nil || account.Validate() != nil {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	response := &pb.GetClaudeUsageLoginProgressResponse{OperationId: string(operation), UsageGeneration: string(generation)}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := claudeUsageAccountTx(tx, account, 0)
		if err != nil {
			return err
		}
		usage := a.Subscription.ClaudeUsage
		if usage == nil || usage.Generation != generation || usage.Operation == nil || usage.Operation.ID != operation || usage.Operation.Actor != actor {
			return subscriptionDenied()
		}
		if err := subscriptionActorValid(tx, usage.Operation.Actor); err != nil {
			s.claudeUsage.clearInput(operation)
			return err
		}
		o := usage.Operation
		state := o.State
		now := time.Now().UTC()
		if usage.Connection == domain.ClaudeUsageConnecting && o.Active() && (o.Epoch != s.subscriptionServerEpoch() || !s.claudeUsage.live(account) && state != domain.SubscriptionRecovery) {
			state = domain.SubscriptionRecovery
		}
		if (state == domain.SubscriptionPreparing || state == domain.SubscriptionWaiting) && !o.ExpiresAt.After(now) {
			state = domain.SubscriptionExpired
		}
		response.State = claudeUsageLoginState(usage.Connection, state, usage.Published)
		response.Diagnostic = wireNativeSubscriptionDiagnostic(o.Diagnostic)
		switch response.State {
		case pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING:
			if p := s.claudeUsage.url(operation); p.URL != "" && now.Before(p.Until) && o.Epoch == s.subscriptionServerEpoch() {
				response.Url = p.URL
			}
			response.CodeRequested = o.CodeSubmissionID == ""
		case pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_AWAITING_CONFIRMATION:
			if !o.ExpiresAt.After(now) {
				response.State = pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_EXPIRED
				break
			}
			if usage.Identity != nil {
				response.Identity = &pb.ClaudeUsageIdentityReview{Email: usage.Identity.Email, Organization: usage.Identity.Organization, Plan: usage.Identity.Plan, IdentityCommitment: usage.Identity.Commitment}
			}
		}
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	return connect.NewResponse(response), nil
}

// claudeUsageLoginState maps the durable section and operation state to the
// closed wire state.
func claudeUsageLoginState(connection domain.ClaudeUsageConnection, state domain.SubscriptionLoginState, published bool) pb.ClaudeUsageLoginState {
	switch connection {
	case domain.ClaudeUsageConfirming:
		return pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_AWAITING_CONFIRMATION
	case domain.ClaudeUsageLinked:
		return pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_LINKED
	}
	switch state {
	case domain.SubscriptionPreparing:
		return pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_PREPARING
	case domain.SubscriptionWaiting:
		return pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING
	case domain.SubscriptionCanceled, domain.SubscriptionSucceeded:
		// A succeeded operation under a reconnect-required section was retired by
		// a disconnect or an execution logout.
		return pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_CANCELED
	case domain.SubscriptionExpired:
		return pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_EXPIRED
	case domain.SubscriptionUnsupported:
		return pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_UNSUPPORTED
	case domain.SubscriptionRecovery:
		return pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_RECOVERY_REQUIRED
	}
	return pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_FAILED
}

func (s *Service) submitClaudeUsageCode(ctx context.Context, req *connect.Request[pb.SubmitClaudeUsageApprovalCodeRequest]) (*connect.Response[pb.SubmitClaudeUsageApprovalCodeResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := subscriptionClient(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	m := req.Msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	operation, generation, err := claudeUsageIDs(req.Msg.OperationId, req.Msg.UsageGeneration)
	if err != nil || !claude.LoginCodeValid(req.Msg.Code) {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	// The receipt input carries only a keyed commitment of the code.
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
		if usage == nil || usage.Generation != generation || !claudeUsageCurrent(a, operation, generation) || usage.Operation.Actor != actor || usage.Operation.Epoch != s.subscriptionServerEpoch() || !usage.Operation.ExpiresAt.After(time.Now().UTC()) || !s.claudeUsage.live(input.Account) || subscriptionActorValid(tx, actor) != nil {
			return nil, subscriptionDenied()
		}
		if err := usage.ClaimCode(domain.ID(m.RequestId)); err != nil {
			return nil, subscriptionDenied()
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return claudeUsageReceipt{Account: input.Account, Operation: operation, Generation: generation}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if !result.Replayed {
		s.claudeUsage.setCode(operation, req.Msg.Code)
	}
	s.logger.InfoContext(ctx, "claude_usage_code_claimed", "operation_id", operation, "replayed", result.Replayed)
	return connect.NewResponse(&pb.SubmitClaudeUsageApprovalCodeResponse{Accepted: true, Replayed: result.Replayed}), nil
}

type claudeUsageConfirmInput struct {
	Account, Operation, Generation domain.ID
	Revision                       uint64
	Actor                          domain.Principal
	Commitment                     string
}

func (s *Service) confirmClaudeUsageLink(ctx context.Context, req *connect.Request[pb.ConfirmClaudeUsageLinkRequest]) (*connect.Response[pb.ConfirmClaudeUsageLinkResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := subscriptionClient(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	m := req.Msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	operation, generation, err := claudeUsageIDs(req.Msg.OperationId, req.Msg.UsageGeneration)
	if err != nil || !domain.NativeIdentityCommitmentValid(req.Msg.IdentityCommitment) {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	input := claudeUsageConfirmInput{domain.ID(m.Id), operation, generation, m.ExpectedRevision, actor, req.Msg.IdentityCommitment}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	if _, replayed, err := s.Store.Replay(ctx, domain.ID(m.RequestId), "claude.usage.confirm", input); err != nil {
		return nil, rpc.Error(err, c)
	} else if replayed {
		r, err := s.accountRecord(ctx, input.Account)
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		return connect.NewResponse(&pb.ConfirmClaudeUsageLinkResponse{Account: rpc.Resource(r), Replayed: true}), nil
	}
	// The staged credential must still be readable under exactly this server,
	// account, generation and reviewed identity before authority is published.
	var credential domain.ID
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := claudeUsageAccountTx(tx, input.Account, input.Revision)
		if err != nil {
			return err
		}
		usage := claudeConfirmable(a, operation, generation, actor, req.Msg.IdentityCommitment, tx)
		if usage == nil {
			return claudeUsageConflict("The Claude usage identity review is no longer current.")
		}
		credential = usage.CredentialID
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if err := s.verifyClaudeUsageCredential(ctx, input.Account, credential, req.Msg.IdentityCommitment); err != nil {
		s.logger.WarnContext(ctx, "claude_usage_confirm_credential_unavailable", "operation_id", operation, "code", domain.SafeError(err).Code)
		return nil, rpc.Error(err, c)
	}
	_, err = s.Store.Mutate(ctx, domain.ID(m.RequestId), "claude.usage.confirm", input, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, input.Account, input.Revision)
		if err != nil {
			return nil, err
		}
		usage := claudeConfirmable(a, operation, generation, actor, req.Msg.IdentityCommitment, tx)
		if usage == nil || usage.CredentialID != credential {
			return nil, claudeUsageConflict("The Claude usage identity review is no longer current.")
		}
		if err := usage.Link(time.Now().UTC()); err != nil {
			return nil, err
		}
		if err := a.Validate(); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return claudeUsageReceipt{Account: input.Account, Operation: operation, Generation: generation}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	r, err := s.accountRecord(ctx, input.Account)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "claude_usage_linked", "account_id", input.Account, "operation_id", operation)
	return connect.NewResponse(&pb.ConfirmClaudeUsageLinkResponse{Account: rpc.Resource(r)}), nil
}

// claudeConfirmable returns the section when it is the exact, unexpired identity
// review that actor reviewed, or nil.
func claudeConfirmable(a domain.Account, operation, generation domain.ID, actor domain.Principal, commitment string, tx *store.Tx) *domain.ClaudeServerUsageState {
	if !a.ManagedClaudeAccount() || a.Connection == nil {
		return nil
	}
	usage := a.Subscription.ClaudeUsage
	if usage == nil || usage.Connection != domain.ClaudeUsageConfirming || usage.Generation != generation || usage.Operation == nil || usage.Operation.ID != operation || usage.Operation.Actor != actor || usage.Identity == nil || usage.Identity.Commitment != commitment || usage.CredentialID == "" || usage.Published || !usage.Operation.ExpiresAt.After(time.Now().UTC()) || subscriptionActorValid(tx, actor) != nil {
		return nil
	}
	return usage
}

// verifyClaudeUsageCredential opens the staged envelope for exactly this server,
// account, credential generation and reviewed identity, then clears it.
func (s *Service) verifyClaudeUsageCredential(ctx context.Context, account, credential domain.ID, commitment string) error {
	vault, err := s.secrets()
	if err != nil {
		return err
	}
	raw, err := vault.Get(ctx, claudeUsageRef(account, credential))
	if err != nil {
		return err
	}
	defer clear(raw)
	bundle, err := subscription.OpenClaudeUsageBundle(raw, s.Identity.ServerID, account, credential, commitment)
	if err != nil {
		return err
	}
	bundle.Clear()
	return nil
}

type claudeUsageCancelInput struct {
	Account, Operation, Generation domain.ID
	Revision                       uint64
	Actor                          domain.Principal
}

func (s *Service) cancelClaudeUsageLogin(ctx context.Context, req *connect.Request[pb.CancelClaudeUsageLoginRequest]) (*connect.Response[pb.CancelClaudeUsageLoginResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := subscriptionClient(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	m := req.Msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	operation, generation, err := claudeUsageIDs(req.Msg.OperationId, req.Msg.UsageGeneration)
	if err != nil {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	input := claudeUsageCancelInput{domain.ID(m.Id), operation, generation, m.ExpectedRevision, actor}
	respond := func(replayed bool) (*connect.Response[pb.CancelClaudeUsageLoginResponse], error) {
		r, err := s.accountRecord(ctx, input.Account)
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		return connect.NewResponse(&pb.CancelClaudeUsageLoginResponse{Account: rpc.Resource(r), Replayed: replayed}), nil
	}
	if _, replayed, err := s.Store.Replay(ctx, domain.ID(m.RequestId), "claude.usage.cancel", input); err != nil {
		return nil, rpc.Error(err, c)
	} else if replayed {
		return respond(true)
	}
	// Validate before touching live work, without holding the account gate: the
	// login's own settlement needs it.
	if err := s.validateClaudeUsageTarget(ctx, input.Account, input.Revision, operation, generation, actor, true); err != nil {
		return nil, rpc.Error(err, c)
	}
	if err := s.claudeUsage.cancelAndJoin(ctx, input.Account, operation, errClaudeUsageCanceled); err != nil {
		return nil, rpc.Error(err, c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	_, err = s.Store.Mutate(ctx, domain.ID(m.RequestId), "claude.usage.cancel", input, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, input.Account, 0)
		if err != nil {
			return nil, err
		}
		usage := a.Subscription.ClaudeUsage
		if usage == nil {
			// No section remains (it was retired with the account): the
			// cancellation is achieved.
			return claudeUsageReceipt{Account: input.Account, Operation: operation, Generation: generation}, nil
		}
		if usage.Generation != generation || usage.Operation == nil || usage.Operation.ID != operation || usage.Operation.Actor != actor {
			return nil, subscriptionDenied()
		}
		switch usage.Connection {
		case domain.ClaudeUsageConnecting:
			// The joined run normally settled itself; an unowned login is settled
			// here and its owed cleanup stays recorded.
			if err := usage.Settle(domain.SubscriptionCanceled, nil); err != nil {
				return nil, err
			}
		case domain.ClaudeUsageConfirming:
			if err := usage.DiscardConfirmation(domain.SubscriptionCanceled); err != nil {
				return nil, err
			}
		case domain.ClaudeUsageReconnectRequired:
			// Already canceled or ended; the request is idempotent.
			return claudeUsageReceipt{Account: input.Account, Operation: operation, Generation: generation}, nil
		default:
			return nil, claudeUsageConflict("A linked Claude usage connection is disconnected, not canceled.")
		}
		if err := a.Validate(); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return claudeUsageReceipt{Account: input.Account, Operation: operation, Generation: generation}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.claudeUsage.clearInput(operation)
	if err := s.cleanupClaudeUsageLocked(ctx, input.Account); err != nil {
		s.logger.WarnContext(ctx, "claude_usage_cleanup_pending", "operation_id", operation, "code", domain.SafeError(err).Code)
	}
	s.logger.InfoContext(ctx, "claude_usage_login_canceled", "account_id", input.Account, "operation_id", operation)
	return respond(false)
}

// validateClaudeUsageTarget proves, without mutating anything, that the request
// targets the account's exact current usage operation and generation.
func (s *Service) validateClaudeUsageTarget(ctx context.Context, account domain.ID, revision uint64, operation, generation domain.ID, actor domain.Principal, loginOnly bool) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := claudeUsageAccountTx(tx, account, revision)
		if err != nil {
			return err
		}
		usage := a.Subscription.ClaudeUsage
		if usage == nil || usage.Generation != generation {
			return claudeUsageConflict("The Claude usage generation changed.")
		}
		if operation != "" && (usage.Operation == nil || usage.Operation.ID != operation || usage.Operation.Actor != actor) {
			return subscriptionDenied()
		}
		if loginOnly && usage.Connection == domain.ClaudeUsageLinked {
			return claudeUsageConflict("A linked Claude usage connection is disconnected, not canceled.")
		}
		return subscriptionActorValid(tx, actor)
	})
}

type claudeUsageDisconnectInput struct {
	Account, Generation domain.ID
	Revision            uint64
	Actor               domain.Principal
}

func (s *Service) disconnectClaudeUsage(ctx context.Context, req *connect.Request[pb.DisconnectClaudeUsageRequest]) (*connect.Response[pb.DisconnectClaudeUsageResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := subscriptionClient(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	m := req.Msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	generation := domain.ID(req.Msg.UsageGeneration)
	if generation.Validate() != nil {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	input := claudeUsageDisconnectInput{domain.ID(m.Id), generation, m.ExpectedRevision, actor}
	respond := func(replayed bool) (*connect.Response[pb.DisconnectClaudeUsageResponse], error) {
		r, err := s.accountRecord(ctx, input.Account)
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		return connect.NewResponse(&pb.DisconnectClaudeUsageResponse{Account: rpc.Resource(r), Replayed: replayed}), nil
	}
	_, replayed, err := s.Store.Replay(ctx, domain.ID(m.RequestId), "claude.usage.disconnect", input)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if !replayed {
		// Validate before canceling anything live; the revision fences stale
		// clients and the generation fences a replaced connection.
		if err := s.validateClaudeUsageTarget(ctx, input.Account, input.Revision, "", generation, actor, false); err != nil {
			return nil, rpc.Error(err, c)
		}
		if err := s.claudeUsage.cancelAndJoin(ctx, input.Account, "", errClaudeUsageCanceled); err != nil {
			return nil, rpc.Error(err, c)
		}
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	if !replayed {
		_, err = s.Store.Mutate(ctx, domain.ID(m.RequestId), "claude.usage.disconnect", input, func(tx *store.Tx) (any, error) {
			r, a, err := claudeUsageAccountTx(tx, input.Account, 0)
			if err != nil {
				return nil, err
			}
			usage := a.Subscription.ClaudeUsage
			if usage == nil {
				// No section remains: the disconnect is already achieved.
				return claudeUsageReceipt{Account: input.Account, Generation: generation}, nil
			}
			if usage.Generation != generation || subscriptionActorValid(tx, actor) != nil {
				return nil, claudeUsageConflict("The Claude usage generation changed.")
			}
			if err := usage.Disconnect(); err != nil {
				return nil, err
			}
			domain.ClearClaudeUsageQuota(&a)
			if err := a.Validate(); err != nil {
				return nil, err
			}
			if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
				return nil, err
			}
			return claudeUsageReceipt{Account: input.Account, Generation: generation}, nil
		})
		if err != nil {
			return nil, rpc.Error(err, c)
		}
	}
	// Reads are already revoked. The disconnect reports cleanup complete only
	// after the usage credentials and native storage were removed; a failure
	// stays recorded for the scanner and is reported to the caller.
	if err := s.cleanupClaudeUsageLocked(ctx, input.Account); err != nil {
		s.logger.WarnContext(ctx, "claude_usage_disconnect_cleanup_pending", "account_id", input.Account, "code", domain.SafeError(err).Code)
		return nil, rpc.Error(claudeUsageUnavailable(), c)
	}
	s.logger.InfoContext(ctx, "claude_usage_disconnected", "account_id", input.Account, "replayed", replayed)
	return respond(replayed)
}
