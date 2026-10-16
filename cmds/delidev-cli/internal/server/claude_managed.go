// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Server-owned Claude subscriptions use the original `claude setup-token`
// child on this server, seal its one emitted token in the existing AccountLogin
// vault ownership and deliver it only through the protected execution lease to a
// Worker that advertises the managed-Claude adapter. No Runner Device, native
// profile or provider relay participates in login.

// managedClaudeOperationWindow bounds one server operation. A login runs from
// acceptance; a logout queued behind an execution lease starts it only when
// that lease clears.
const managedClaudeOperationWindow = 15 * time.Minute

// managedClaudeLogin is the native setup-token child owned by one operation.
type managedClaudeLogin interface {
	Progress() (claude.SetupTokenProgress, error)
	Submit([]byte) error
	Done() <-chan struct{}
	Wait() error
	Token() ([]byte, error)
	Close() error
}

// An opener error leaves the permanent account profile and every credential
// file it holds in place: routine server operations retain credentials, and only
// explicit logout or account deletion removes them. A recovery error retains the
// original operation for inspection. The returned string
// is the observed executable version (empty when discovery did not reach a
// detected installation), never the pinned baseline.
type managedClaudeOpener func(context.Context, nativeScope, domain.ID, *slog.Logger) (managedClaudeLogin, string, error)

// managedClaudeDiscoveryError preserves the failing native phase while keeping
// the original closed error code available to the subscription diagnostic.
type managedClaudeDiscoveryError struct{ cause error }

func (e managedClaudeDiscoveryError) Error() string { return "Claude Code protocol discovery failed" }
func (e managedClaudeDiscoveryError) Unwrap() error { return e.cause }

type serverClaudeRuntime struct {
	*claude.SetupTokenLogin
	home   string
	lease  *nativeprofile.Lease
	owner  domain.ID
	logger *slog.Logger
	once   sync.Once
	result error
}

func openManagedClaudeLogin(ctx context.Context, scope nativeScope, owner domain.ID, logger *slog.Logger) (returned managedClaudeLogin, observed string, err error) {
	runtimeRoot := filepath.Join(scope.Root, "subscription-runtime")
	lease, e := acquireAccountProfile(ctx, scope, domain.ClaudeCode, nativeprofile.PurposeAuth)
	if e != nil {
		return nil, "", e
	}
	home := lease.Root
	defer func() {
		if err != nil {
			_ = lease.Close()
		}
	}()
	retained, e := profileCredentialAdmission(scope, lease, owner, logger)
	if e != nil {
		return nil, "", e
	}
	if retained {
		// The setup-token login has no published generation to restore, and a
		// retained file is never authority. Replace it through the profile-owned
		// removal before the native child starts so it cannot be mistaken for the
		// new login's result.
		if e := subscription.RetireCredentialFiles(home); e != nil {
			logSubscriptionRuntimeCleanup(logger, owner, e)
			return nil, "", subscriptionDenied()
		}
		nativeprofile.Log(logger, "native_profile_credential_replaced", lease.Key, lease.ID, nativeprofile.ReasonCredentialRetained, "operation_id", owner)
	}
	env, e := harness.PrivateRuntimeEnvironment(home)
	if e != nil {
		return nil, "", subscriptionDenied()
	}
	installation, e := harness.DiscoverClaude(ctx, harness.DiscoveryConfig{Root: runtimeRoot, OwnerID: owner, Logger: logger, Profiles: scope.Profiles})
	if e != nil {
		return nil, "", managedClaudeDiscoveryError{e}
	}
	if installation.State != domain.InstallationDetected {
		return nil, "", managedClaudeDiscoveryError{domain.InstallationProblem(installation.State)}
	}
	// Discovery already admitted this version through the baseline predicate and
	// the native protocol checks; the observed value is the diagnostic authority.
	observed = installation.Version
	if !domain.HarnessVersionAllowed(domain.ClaudeCode, installation.Version) {
		return nil, observed, domain.Fail(domain.Unsupported, "The Claude Code version installed on the server is not supported.", "Update Claude Code on the server, then try again.")
	}
	if !installation.ProtocolVerified || installation.Protocol == nil || installation.Protocol.State != domain.ProtocolVerified || installation.Problem != nil || installation.ResolvedPath == "" {
		return nil, observed, managedClaudeDiscoveryError{domain.Fail(domain.Unsupported, "The installed Claude Code protocol is unavailable.", "Refresh native discovery on the server.")}
	}
	login, e := claude.StartSetupToken(ctx, claude.SetupTokenConfig{Version: installation.Version, Home: filepath.Join(home, "claude"), Process: process.Config{Directory: filepath.Join(runtimeRoot, "processes"), OwnerID: owner, Executable: installation.ResolvedPath, Cwd: home, Env: env, Logger: logger}})
	if e != nil {
		return nil, observed, e
	}
	return &serverClaudeRuntime{SetupTokenLogin: login, home: home, lease: lease, owner: owner, logger: logger}, observed, nil
}

// Close joins the original child and wipes its screen buffer. The permanent
// account profile and its credential files are retained. A failure keeps the
// operation fenced.
func (r *serverClaudeRuntime) Close() error {
	r.once.Do(func() {
		defer r.lease.Close()
		if err := r.SetupTokenLogin.Close(); err != nil {
			r.result = err
			return
		}
		nativeprofile.Log(r.logger, "native_profile_credential_retained", r.lease.Key, r.lease.ID, nativeprofile.ReasonCredentialRetained, "operation_id", r.owner)
	})
	return r.result
}

func managedClaudeAccount(tx *store.Tx, id domain.ID, revision uint64) (store.Record, domain.Account, error) {
	r, a, err := accountFromTx(tx, id, revision)
	if err == nil && (a.Type != domain.SubscriptionAccount || a.SubscriptionService != domain.SubscriptionClaude || a.ProviderID != "" || a.Subscription == nil || !a.Subscription.ManagedClaude()) {
		err = subscriptionDenied()
	}
	return r, a, err
}

// claudeAccountMode reports whether id is a Claude subscription and whether it
// is already a server-token account. It never inspects credential material.
func (s *Service) claudeAccountMode(ctx context.Context, id string) (claudeAccount, managed bool, err error) {
	if domain.ID(id).Validate() != nil {
		return false, false, subscriptionDenied()
	}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := accountFromTx(tx, domain.ID(id), 0)
		if err != nil {
			return err
		}
		claudeAccount = a.Type == domain.SubscriptionAccount && a.SubscriptionService == domain.SubscriptionClaude
		managed = claudeAccount && a.Subscription != nil && a.Subscription.ManagedClaude()
		return nil
	})
	return
}

// freshManagedClaudeLogin reports whether an account may begin its first
// server-token login. A previously connected, logged-out or device-owned
// account never changes credential mode, so reconnection is new-account-only.
func freshManagedClaudeLogin(a domain.Account) bool {
	st := a.Subscription
	if st == nil {
		return true
	}
	if st.ManagedClaude() {
		o := st.ServerOperation
		return a.Connection == nil && st.Generation == "" && st.Lease == nil && (o == nil || o.Action == domain.SubscriptionLogin && o.State != domain.SubscriptionSucceeded)
	}
	return a.Connection == nil && st.Generation == "" && st.Lease == nil && st.NativeProfileID == "" && st.OwnerMachineID == "" && st.NativeOperation == nil && st.ServerOperation == nil && st.Pending == nil
}

// routesToManagedClaude decides whether a Claude RequestSubscription belongs to
// the server-token lane. Device-owned accounts and machine-bound requests keep
// the original native-profile lane.
func (s *Service) routesToManagedClaude(ctx context.Context, req *pb.RequestSubscriptionRequest) (bool, error) {
	_, managed, err := s.claudeAccountMode(ctx, req.Mutation.Id)
	if err != nil || managed {
		return managed, err
	}
	if req.MachineId != "" || subscriptionAction(req.Action) != domain.SubscriptionLogin {
		return false, nil
	}
	fresh := false
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := accountFromTx(tx, domain.ID(req.Mutation.Id), 0)
		fresh = err == nil && freshManagedClaudeLogin(a)
		return err
	})
	return fresh, err
}

func (s *Service) requestManagedClaude(ctx context.Context, req *connect.Request[pb.RequestSubscriptionRequest]) (*connect.Response[pb.RequestSubscriptionResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := subscriptionClient(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	action := subscriptionAction(req.Msg.Action)
	// A machine-bound sign-in is retired for every Claude account, including an
	// existing server-token account that routing sends here first. Report the
	// same typed retirement result as the Runner lane instead of a generic
	// invalid-argument error.
	if req.Msg.MachineId != "" && (action == domain.SubscriptionLogin || action == domain.SubscriptionRefresh) {
		s.logger.WarnContext(ctx, "claude_runner_signin_rejected", "operation_id", req.Msg.Mutation.RequestId, "account_id", req.Msg.Mutation.Id, "machine_id", req.Msg.MachineId, "action", action, "code", domain.Unsupported, "lane", "managed")
		return nil, rpc.Error(claudeRunnerSignInRetired(), c)
	}
	if action != domain.SubscriptionLogin && action != domain.SubscriptionLogout || req.Msg.DeviceCode || req.Msg.MachineId != "" {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Choose a supported Claude server operation.", "Use login or logout without a Runner Device; reconnection connects a new account."), c)
	}
	m := req.Msg.Mutation
	input := struct {
		ID       domain.ID
		Revision uint64
		Action   domain.SubscriptionAction
		Actor    domain.Principal
	}{domain.ID(m.Id), m.ExpectedRevision, action, actor}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "claude.managed.request", input, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, input.ID, input.Revision)
		if err != nil {
			return nil, err
		}
		if a.Type != domain.SubscriptionAccount || a.SubscriptionService != domain.SubscriptionClaude || a.ProviderID != "" {
			return nil, subscriptionDenied()
		}
		if a.Subscription == nil {
			a.Subscription = &domain.SubscriptionState{}
		}
		state := a.Subscription
		if state.Pending != nil || state.RecoveryRequired || a.Removal != nil || state.Lease != nil && action == domain.SubscriptionLogin {
			return nil, subscriptionDenied()
		}
		if action == domain.SubscriptionLogin {
			if !freshManagedClaudeLogin(a) {
				return nil, domain.Fail(domain.Conflict, "This Claude account cannot start another login.", "Connect a new Claude account; an existing account's credential is never replaced.")
			}
			state.ClaudeCredential = domain.ClaudeCredentialServerToken
		} else if !state.ManagedClaude() || a.Connection == nil || state.Generation == "" {
			return nil, subscriptionDenied()
		}
		now := time.Now().UTC()
		state.ServerOperation = &domain.ServerSubscriptionOperation{ID: domain.ID(m.RequestId), Action: action, Epoch: s.subscriptionServerEpoch(), FinishID: domain.NewID(), Actor: actor, State: domain.SubscriptionPreparing, StartedAt: now, ExpiresAt: now.Add(managedClaudeOperationWindow)}
		state.Pending = &domain.SubscriptionOperation{ID: domain.ID(m.RequestId), Action: action, Actor: actor, Phase: domain.SubscriptionQueued}
		if action == domain.SubscriptionLogout {
			// Readiness is revoked at acceptance. Admitted work is canceled and the
			// logout claim waits for the original Worker lease and cleanup.
			a.Health = domain.AccountRevoked
			if err := cancelAccountExecutions(tx, r.ID); err != nil {
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
	s.logger.InfoContext(ctx, "claude_managed_accepted", "account_id", input.ID, "operation_id", m.RequestId, "action", action, "replayed", result.Replayed)
	return connect.NewResponse(&pb.RequestSubscriptionResponse{Account: rpc.Resource(r), OperationId: m.RequestId, Replayed: result.Replayed}), nil
}

func (s *Service) cancelManagedClaude(ctx context.Context, req *connect.Request[pb.CancelSubscriptionRequest]) (*connect.Response[pb.CancelSubscriptionResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	actor, err := subscriptionClient(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	var operation domain.ID
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "claude.managed.cancel", disconnectAccountInput{domain.ID(m.Id), m.ExpectedRevision}, func(tx *store.Tx) (any, error) {
		r, a, err := managedClaudeAccount(tx, domain.ID(m.Id), m.ExpectedRevision)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st.Pending == nil || st.ServerOperation == nil || st.ServerOperation.ID != st.Pending.ID || st.ServerOperation.Actor != actor {
			return nil, domain.Fail(domain.Conflict, "No Claude login is pending.", "Read current account status; cancellation never starts another operation.")
		}
		if st.Pending.Action != domain.SubscriptionLogin {
			return nil, domain.Fail(domain.Unsupported, "Only an active login can be canceled.", "Let logout finish under exclusive ownership.")
		}
		operation = st.Pending.ID
		if st.Pending.Phase == domain.SubscriptionQueued {
			st.ServerOperation.State = domain.SubscriptionCanceled
			st.Pending = nil
		} else {
			st.Pending.Canceled = true
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return accountReceipt{ID: r.ID}, nil
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

// Called with no account gate held. Presentation stays memory-only and bound to
// the original operation, actor, epoch and expiry.
func (s *Service) managedClaudeProgress(ctx context.Context, req *connect.Request[pb.GetSubscriptionProgressRequest]) (*connect.Response[pb.GetSubscriptionProgressResponse], error) {
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
	response := &pb.GetSubscriptionProgressResponse{}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := managedClaudeAccount(tx, domain.ID(req.Msg.AccountId), 0)
		if err != nil {
			return err
		}
		st := a.Subscription
		o := st.ServerOperation
		if o == nil || string(o.ID) != req.Msg.OperationId || o.Actor != actor {
			return subscriptionDenied()
		}
		if err := subscriptionActorValid(tx, o.Actor); err != nil {
			s.clearClaudeLoginInput(o.ID)
			return err
		}
		state := o.State
		if st.RecoveryRequired || o.Active() && o.Epoch != s.subscriptionServerEpoch() {
			state = domain.SubscriptionRecovery
		}
		queuedLogout := o.Action == domain.SubscriptionLogout && st.Lease != nil && !st.RecoveryRequired && state == domain.SubscriptionPreparing
		if (state == domain.SubscriptionPreparing || state == domain.SubscriptionWaiting) && !queuedLogout && !o.ExpiresAt.After(time.Now().UTC()) {
			state = domain.SubscriptionExpired
		}
		response.State = loginState(state)
		response.LoginMethod = pb.SubscriptionLoginMethod(o.LoginMethod)
		response.NativeDiagnostic = wireNativeSubscriptionDiagnostic(o.ClaudeDiagnostic)
		response.Canceled = state == domain.SubscriptionCanceled || st.Pending != nil && st.Pending.ID == o.ID && st.Pending.Canceled
		if state == domain.SubscriptionWaiting && !response.Canceled {
			response.Url = s.subscriptionProgress[o.ID].URL
		}
		if state == domain.SubscriptionSucceeded && o.Action == domain.SubscriptionLogin && a.Connection != nil && st.Generation == o.FinishID {
			response.Generation = string(o.FinishID)
			// The default name never derives from opaque token contents.
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

func (s *Service) submitManagedClaudeCode(ctx context.Context, req *connect.Request[pb.SubmitSubscriptionLoginCodeRequest]) (*connect.Response[pb.SubmitSubscriptionLoginCodeResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	actor, err := subscriptionClient(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	operation := domain.ID(req.Msg.OperationId)
	if operation.Validate() != nil || !claude.LoginCodeValid(req.Msg.Code) {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
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
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "claude.managed.login-code", input, func(tx *store.Tx) (any, error) {
		r, a, err := managedClaudeAccount(tx, input.Account, input.Revision)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		o := st.ServerOperation
		if st.RecoveryRequired || st.Pending == nil || st.Pending.Canceled || o == nil || o.ID != operation || o.Actor != actor || o.Action != domain.SubscriptionLogin || !o.NativeStarted || o.Epoch != s.subscriptionServerEpoch() || o.State != domain.SubscriptionWaiting || o.LoginMethod != domain.SubscriptionBrowserCode || o.CodeSubmissionID != "" || !o.ExpiresAt.After(time.Now().UTC()) || subscriptionActorValid(tx, actor) != nil {
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
	s.logger.InfoContext(ctx, "claude_managed_login_input_claimed", "operation_id", operation, "replayed", result.Replayed)
	return connect.NewResponse(&pb.SubmitSubscriptionLoginCodeResponse{Accepted: true}), nil
}

// claudeTokenCommitment proves possession of one exact managed credential. It
// is keyed by the server and carries no provider identity.
func (s *Service) claudeTokenCommitment(token []byte) string {
	mac := hmac.New(sha256.New, []byte(s.Identity.Token))
	mac.Write([]byte("delidev/claude-token/v1\x00" + string(s.Identity.ServerID) + "\x00"))
	mac.Write(token)
	return hex.EncodeToString(mac.Sum(nil))
}

func managedClaudeCandidate(a domain.Account) bool {
	st := a.Subscription
	return a.ManagedClaudeAccount() && !st.RecoveryRequired && st.ServerOperation != nil && st.ServerOperation.State == domain.SubscriptionPreparing && !st.ServerOperation.NativeStarted && st.Pending != nil && st.Pending.Phase == domain.SubscriptionQueued && st.Lease == nil
}

// managedClaudeCredentialCleanupPending selects a settled login or logout whose
// durable checkpoint says obsolete vault references still need retirement. It
// never reopens native work and preserves the settled connection state.
func managedClaudeCredentialCleanupPending(a domain.Account) bool {
	if !a.ManagedClaudeAccount() || a.Removal != nil {
		return false
	}
	o := a.Subscription.ServerOperation
	return o != nil && o.CleanupPhase == domain.SubscriptionCredentialCleanupPending && o.State == domain.SubscriptionSucceeded && !o.NativeStarted && a.Subscription.Pending == nil
}

// managedClaudeRestoredGeneration selects a server-token account whose
// connection was cleared by a database restore while its historical generation
// and recovery fence were retained. The vault is outside the restored image, so
// the old generation is never authorized again: recovery only retires it.
func managedClaudeRestoredGeneration(a domain.Account) bool {
	st := a.Subscription
	return a.ManagedClaudeAccount() && a.Removal == nil && st.ServerOperation != nil && st.Lease == nil && st.RecoveryRequired && a.Connection == nil && st.Generation != ""
}

// managedClaudeNeedsRecovery selects an interrupted server operation whose
// native child is no longer owned by a live goroutine in this server epoch.
func managedClaudeNeedsRecovery(a domain.Account) bool {
	if managedClaudeRestoredGeneration(a) || managedClaudeCredentialCleanupPending(a) {
		return true
	}
	st := a.Subscription
	if !a.ManagedClaudeAccount() || st.ServerOperation == nil || st.Lease != nil || !st.RecoveryRequired || a.Removal != nil {
		return false
	}
	o := st.ServerOperation
	return o.Action == domain.SubscriptionLogin && a.Connection == nil && st.Generation == "" || o.Action == domain.SubscriptionLogout && o.Active()
}

func claudeDiagnostic(op domain.ID, phase domain.NativeSubscriptionPhase, version string, err error) *domain.NativeSubscriptionDiagnostic {
	if err == nil {
		return nil
	}
	code := domain.SafeError(err).Code
	switch code {
	case domain.NotFound, domain.Unsupported, domain.Unavailable, domain.PermissionDenied, domain.Unauthenticated, domain.RecoveryRequired, domain.Canceled, domain.Conflict, domain.InvalidArgument, domain.Internal, domain.ResourceExhausted:
	default:
		code = domain.Internal
	}
	return &domain.NativeSubscriptionDiagnostic{DetectedVersion: version, RequiredVersion: domain.ClaudeProtocolVersion, Phase: phase, Code: code, CorrelationID: op}
}

func (s *Service) runManagedClaude(parent context.Context, id domain.ID) {
	var original domain.ServerSubscriptionOperation
	err := s.Store.Read(parent, func(tx *store.Tx) error {
		_, a, err := managedClaudeAccount(tx, id, 0)
		if err != nil {
			return err
		}
		if a.Subscription.ServerOperation == nil || a.Subscription.Pending == nil {
			return subscriptionDenied()
		}
		original = *a.Subscription.ServerOperation
		return nil
	})
	if err != nil {
		return
	}
	unlock, err := s.lockAccounts(parent)
	if err != nil {
		return
	}
	_, err = s.Store.Mutate(parent, domain.NewID(), "claude.managed.claim", struct{ Account, Operation domain.ID }{id, original.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := managedClaudeAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st.ServerOperation == nil || st.ServerOperation.ID != original.ID || st.ServerOperation.State != domain.SubscriptionPreparing || st.ServerOperation.NativeStarted || st.Pending == nil || st.Pending.ID != original.ID || st.Pending.Phase != domain.SubscriptionQueued || st.Lease != nil || st.RecoveryRequired || st.ServerOperation.Epoch != s.subscriptionServerEpoch() {
			return nil, subscriptionDenied()
		}
		o := st.ServerOperation
		if subscriptionActorValid(tx, o.Actor) != nil || st.Pending.Canceled || !time.Now().Before(o.ExpiresAt) {
			o.State = domain.SubscriptionCanceled
			if !time.Now().Before(o.ExpiresAt) {
				o.State = domain.SubscriptionExpired
			}
			st.Pending = nil
		} else {
			o.NativeStarted = true
			o.Generation = st.Generation
			st.Pending.Phase = domain.SubscriptionClaimed
			original = *o
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	unlock()
	if err != nil || !original.NativeStarted {
		return
	}
	s.logger.InfoContext(parent, "claude_managed_started", "account_id", id, "operation_id", original.ID, "action", original.Action)
	ctx, cancel := context.WithDeadline(parent, original.ExpiresAt)
	defer cancel()
	checked := make(chan struct{})
	go func() {
		defer close(checked)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			err := s.Store.Read(ctx, func(tx *store.Tx) error {
				_, a, err := managedClaudeAccount(tx, id, 0)
				if err != nil {
					return err
				}
				st := a.Subscription
				if st.RecoveryRequired || st.Pending == nil || st.Pending.ID != original.ID || st.Pending.Canceled || subscriptionActorValid(tx, original.Actor) != nil {
					return subscriptionDenied()
				}
				return nil
			})
			if err != nil {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	var token []byte
	cleanup, success := true, false
	nativeStarted := false
	phase := domain.NativeSubscriptionRuntime
	version := ""
	var nativeErr error
	if original.Action == domain.SubscriptionLogin {
		token, nativeStarted, cleanup, version, phase, nativeErr = s.runManagedClaudeLogin(ctx, id, original)
		success = nativeErr == nil && len(token) != 0
	} else {
		success = true
		phase = domain.NativeSubscriptionCleanup
	}
	cancel()
	<-checked
	state := domain.SubscriptionFailed
	switch {
	case success:
		state = domain.SubscriptionSucceeded
	case parent.Err() != nil || !cleanup:
		state = domain.SubscriptionRecovery
		if parent.Err() != nil && cleanup && original.Action == domain.SubscriptionLogin {
			state = domain.SubscriptionCanceled
		}
	case !time.Now().Before(original.ExpiresAt):
		state = domain.SubscriptionExpired
	case errors.Is(ctx.Err(), context.Canceled) && (errors.Is(nativeErr, context.Canceled) || !nativeStarted && parent.Err() != nil):
		state = domain.SubscriptionCanceled
	case domain.SafeError(nativeErr).Code == domain.Unsupported:
		state = domain.SubscriptionUnsupported
	}
	if !success && nativeStarted && original.Action != domain.SubscriptionLogin {
		state = domain.SubscriptionRecovery
	}
	diagnostic := claudeDiagnostic(original.ID, phase, version, nativeErr)
	bounded, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 30*time.Second)
	defer stop()
	if err := s.finishManagedClaude(bounded, id, original, token, cleanup, state, diagnostic); err != nil {
		if e := s.markManagedClaudeRecovery(id, original.ID, diagnostic); e != nil {
			s.logger.Warn("claude_managed_recovery_unconfirmed", "operation_id", original.ID, "code", domain.SafeError(e).Code)
		}
		s.logger.Warn("claude_managed_finish_failed", "operation_id", original.ID, "code", domain.SafeError(err).Code, "cleanup_confirmed", cleanup)
	}
	clear(token)
}

// runManagedClaudeLogin owns exactly one setup-token child. It returns the one
// emitted token only after the child exited successfully and was joined.
func (s *Service) runManagedClaudeLogin(ctx context.Context, id domain.ID, original domain.ServerSubscriptionOperation) (token []byte, started, cleanup bool, version string, phase domain.NativeSubscriptionPhase, err error) {
	phase = domain.NativeSubscriptionRuntime
	opener := s.claudeSetupOpen
	if opener == nil {
		opener = openManagedClaudeLogin
	}
	login, observed, err := opener(ctx, s.scopeFor(id), original.ID, s.logger)
	if err != nil {
		// A non-recovery opener error proves joined native and private-file cleanup.
		phase := domain.NativeSubscriptionLaunch
		var discovery managedClaudeDiscoveryError
		if errors.As(err, &discovery) {
			phase = domain.NativeSubscriptionDiscovery
		}
		return nil, false, domain.SafeError(err).Code != domain.RecoveryRequired, observed, phase, err
	}
	started, version, phase = true, observed, domain.NativeSubscriptionLogin
	published := false
	// delivered records that the one approval code reached the original child.
	// The durable consumption mark is single-use, so the loop must never ask for
	// the code again while that child is still working on it.
	delivered := false
	var runErr error
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
loop:
	for {
		progress, perr := login.Progress()
		if perr != nil {
			runErr = perr
			break
		}
		if progress.URL != "" && !published {
			if runErr = s.publishManagedClaudeProgress(ctx, id, original, progress.URL); runErr != nil {
				break
			}
			published = true
		}
		if published && !delivered {
			code, claimed, cerr := s.takeManagedClaudeCode(ctx, id, original)
			if cerr != nil {
				runErr = cerr
				break
			}
			if claimed {
				if runErr = login.Submit(code); runErr != nil {
					break
				}
				delivered = true
				s.logger.InfoContext(ctx, "claude_managed_code_delivered", "operation_id", original.ID)
			}
		}
		select {
		case <-ctx.Done():
			runErr = ctx.Err()
			break loop
		case <-ticker.C:
		case <-login.Done():
			if runErr = login.Wait(); runErr == nil {
				phase = domain.NativeSubscriptionStatus
				token, runErr = login.Token()
			}
			break loop
		}
	}
	// Join the original child before any state can claim cleanup.
	if closeErr := login.Close(); closeErr != nil {
		clear(token)
		return nil, started, false, version, domain.NativeSubscriptionCleanup, domain.Fail(domain.RecoveryRequired, "The Claude login child could not be confirmed stopped.", "Retain the original operation and inspect the private runtime.")
	}
	if runErr != nil {
		clear(token)
		return nil, started, true, version, phase, runErr
	}
	return token, started, true, version, phase, nil
}

func (s *Service) publishManagedClaudeProgress(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation, url string) error {
	if claude.ValidateSetupTokenURL(url) != nil {
		s.logger.WarnContext(ctx, "claude_managed_login_url_rejected", "operation_id", o.ID, "code", domain.Unsupported)
		return domain.Fail(domain.Unsupported, "The original Claude login address is unsupported.", "Keep the original native operation; do not substitute another authentication flow.")
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.managed.waiting", struct{ Account, Operation domain.ID }{id, o.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := managedClaudeAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st.ServerOperation == nil || st.ServerOperation.ID != o.ID || st.Pending == nil || st.Pending.Canceled || st.RecoveryRequired || subscriptionActorValid(tx, o.Actor) != nil {
			return nil, subscriptionDenied()
		}
		st.ServerOperation.State = domain.SubscriptionWaiting
		st.ServerOperation.LoginMethod = domain.SubscriptionBrowserCode
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if err == nil {
		if s.subscriptionProgress == nil {
			s.subscriptionProgress = map[domain.ID]subscriptionProgress{}
		}
		s.subscriptionProgress[o.ID] = subscriptionProgress{URL: url, Until: o.ExpiresAt}
	}
	return err
}

// takeManagedClaudeCode records durable consumption before releasing bytes. An
// uncertain step never permits a second delivery of the same approval code.
func (s *Service) takeManagedClaudeCode(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation) ([]byte, bool, error) {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, false, err
	}
	defer unlock()
	var ready bool
	observe := func(tx *store.Tx, consume bool) (any, error) {
		r, a, err := managedClaudeAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		current := st.ServerOperation
		if st.RecoveryRequired || st.Pending == nil || st.Pending.Canceled || current == nil || current.ID != o.ID || current.State != domain.SubscriptionWaiting || subscriptionActorValid(tx, current.Actor) != nil {
			return nil, subscriptionDenied()
		}
		if current.CodeConsumed {
			return nil, subscriptionDenied()
		}
		if current.CodeSubmissionID == "" {
			return accountReceipt{ID: r.ID}, nil
		}
		if len(s.claudeLoginCodes[o.ID]) == 0 {
			return nil, subscriptionDenied()
		}
		ready = true
		if !consume {
			return accountReceipt{ID: r.ID}, nil
		}
		current.CodeConsumed = true
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, err
	}
	if err := s.Store.Read(ctx, func(tx *store.Tx) error { _, err := observe(tx, false); return err }); err != nil || !ready {
		return nil, false, err
	}
	if _, err := s.Store.Mutate(ctx, domain.NewID(), "claude.managed.take-code", struct{ Account, Operation domain.ID }{id, o.ID}, func(tx *store.Tx) (any, error) { return observe(tx, true) }); err != nil {
		return nil, false, err
	}
	code := slices.Clone(s.claudeLoginCodes[o.ID])
	clear(s.claudeLoginCodes[o.ID])
	delete(s.claudeLoginCodes, o.ID)
	return code, true, nil
}

func (s *Service) finishManagedClaude(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation, token []byte, cleanup bool, result domain.SubscriptionLoginState, diagnostic *domain.NativeSubscriptionDiagnostic) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if diagnostic != nil && diagnostic.Validate() != nil {
		return subscriptionDenied()
	}
	// The approval code is single-use input. Whatever the outcome, the finished
	// operation retains neither it nor its presentation.
	defer s.clearClaudeLoginInput(o.ID)
	success := result == domain.SubscriptionSucceeded && cleanup
	login := o.Action == domain.SubscriptionLogin
	commitment := ""
	if success && login {
		commitment = s.claudeTokenCommitment(token)
	}
	var vaultPut bool
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, e := managedClaudeAccount(tx, id, 0)
		if e != nil {
			return e
		}
		st := a.Subscription
		if st.ServerOperation == nil || st.ServerOperation.ID != o.ID || st.ServerOperation.Epoch != o.Epoch || !st.ServerOperation.NativeStarted || st.Generation != o.Generation || st.RecoveryRequired || st.Pending == nil || st.Pending.ID != o.ID {
			return subscriptionDenied()
		}
		if st.Pending.Canceled || subscriptionActorValid(tx, o.Actor) != nil {
			success = false
			result = domain.SubscriptionCanceled
			if !login {
				result = domain.SubscriptionRecovery
			}
		}
		if success && login {
			return uniqueSubscriptionIdentity(tx, id, commitment)
		}
		return nil
	})
	if err != nil {
		return err
	}
	vault, err := s.secrets()
	if err != nil {
		return err
	}
	if success && login {
		envelope, e := subscription.SealClaudeToken(s.Identity.ServerID, id, o.FinishID, token)
		if e != nil {
			return e
		}
		_, e = vault.Put(ctx, credentials.Ref{Owner: id, ID: o.FinishID, Purpose: credentials.AccountLogin}, envelope)
		clear(envelope)
		if e != nil {
			return e
		}
		vaultPut = true
	}
	_, err = s.Store.Mutate(ctx, o.FinishID, "claude.managed.finish", struct {
		Account, Operation domain.ID
		Cleanup            bool
		State              domain.SubscriptionLoginState
		Diagnostic         *domain.NativeSubscriptionDiagnostic
	}{id, o.ID, cleanup, result, diagnostic}, func(tx *store.Tx) (any, error) {
		r, a, err := managedClaudeAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st.ServerOperation == nil || st.ServerOperation.ID != o.ID || !st.ServerOperation.NativeStarted || st.Generation != o.Generation || st.RecoveryRequired || st.Pending == nil || st.Pending.ID != o.ID {
			return nil, subscriptionDenied()
		}
		if st.Pending.Canceled || subscriptionActorValid(tx, o.Actor) != nil {
			success = false
			result = domain.SubscriptionCanceled
			if !login {
				result = domain.SubscriptionRecovery
			}
		}
		st.ServerOperation.ClaudeDiagnostic = diagnostic
		if success {
			st.ServerOperation.ClaudeDiagnostic = nil
		}
		if !cleanup || result == domain.SubscriptionRecovery {
			st.RecoveryRequired = true
			a.Health = domain.AccountFailed
			st.ServerOperation.State = domain.SubscriptionRecovery
		} else {
			st.ServerOperation.NativeStarted = false
			st.ServerOperation.State = result
			st.Pending = nil
			switch {
			case success && login:
				if err := uniqueSubscriptionIdentity(tx, id, commitment); err != nil {
					return nil, err
				}
				// Obsolete generations are retained: only the published generation
				// authorizes use, and no deletion checkpoint is recorded.
				st.Generation = o.FinishID
				st.IdentityCommitment = commitment
				a.Connection = &domain.AccountConnection{ID: o.FinishID, Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()}
				a.Health = domain.AccountReady
			case success && !login:
				// The settled explicit logout still owes the removal of every retained
				// generation and credential file.
				st.ServerOperation.CleanupPhase = domain.SubscriptionCredentialCleanupPending
				a.Connection = nil
				a.Health = domain.AccountDisconnected
				a.Validation, a.Catalog, a.Quota = nil, nil, nil
				a.ConfirmedExhausted = false
				st.Generation = ""
				st.IdentityCommitment = ""
			default:
				// A failed first login joined its child and wrote no credential.
				a.Health = domain.AccountDisconnected
			}
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: id}, err
	})
	if err == nil && s.claudeFinishAck != nil {
		// Test seam for a commit whose acknowledgment never reached this goroutine.
		err = s.claudeFinishAck()
	}
	if err != nil {
		if vaultPut {
			// The metadata commit may or may not have happened. Read the account
			// before touching the staged token: a published generation must keep it.
			published, e := s.reconcileManagedClaudeStaging(id, o, vault)
			switch {
			case e != nil:
				// Unproven either way, so nothing is deleted. A committed login keeps its
				// cleanup checkpoint; an uncommitted one is fenced and recovery removes
				// every reference of the account.
				s.logger.Warn("claude_managed_staging_cleanup_pending", "operation_id", o.ID, "code", domain.SafeError(e).Code)
			case published:
				s.logger.WarnContext(ctx, "claude_managed_finish_commit_confirmed", "operation_id", o.ID, "code", domain.SafeError(err).Code)
				err = nil
			}
		}
		if err != nil {
			return err
		}
	}
	if success && !login {
		// The settled state and its removal checkpoint are committed. A failed or
		// interrupted removal is retried from the checkpoint, never by fencing or
		// rewriting the settled operation.
		if err := s.retireManagedClaudeReferences(ctx, id, o.ID); err != nil {
			s.logger.WarnContext(ctx, "claude_managed_cleanup_pending", "operation_id", o.ID, "code", domain.SafeError(err).Code)
		}
	} else if cleanup {
		s.logger.InfoContext(ctx, "server_credential_retained", "account_id", id, "operation_id", o.ID, "action", o.Action, "state", result)
	}
	s.logger.InfoContext(ctx, "claude_managed_finished", "account_id", id, "operation_id", o.ID, "action", o.Action, "state", result, "cleanup_confirmed", cleanup)
	return nil
}

// managedClaudePublished reports whether the login receipt of o is committed:
// the account is connected under exactly the staged generation of o.
func managedClaudePublished(a domain.Account, o domain.ServerSubscriptionOperation) bool {
	if !a.ManagedClaudeAccount() || o.Action != domain.SubscriptionLogin || a.Connection == nil {
		return false
	}
	st := a.Subscription
	current := st.ServerOperation
	return current != nil && current.ID == o.ID && current.State == domain.SubscriptionSucceeded && st.Generation == o.FinishID && a.Connection.ID == o.FinishID
}

// reconcileManagedClaudeStaging resolves an ambiguous finish commit from the
// stored account on a fresh bounded context. A published generation keeps its
// vault token. An unpublished staged token is removed. The caller holds the
// account gate, so no other managed operation can change the account between
// the read and the deletion. A read or deletion error leaves the token in place.
func (s *Service) reconcileManagedClaudeStaging(id domain.ID, o domain.ServerSubscriptionOperation, vault accountSecrets) (published bool, err error) {
	ctx, cancel := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 10*time.Second)
	defer cancel()
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, e := accountFromTx(tx, id, 0)
		if e != nil {
			return e
		}
		published = managedClaudePublished(a, o)
		return nil
	})
	if err != nil {
		return false, err
	}
	if published {
		return true, nil
	}
	// Only the generation this operation staged is removed, and only because its
	// publication is provably absent. Every other generation is retained.
	return false, vault.Delete(ctx, credentials.Ref{Owner: id, ID: o.FinishID, Purpose: credentials.AccountLogin})
}

// retireManagedClaudeReferences is the explicit-logout removal of a managed
// Claude account: it deletes every retained credential file and AccountLogin
// generation, then clears the operation's removal checkpoint. The caller holds
// the account gate. The checkpoint is cleared only after removal is confirmed.
func (s *Service) retireManagedClaudeReferences(ctx context.Context, id, operation domain.ID) error {
	if err := s.removeAccountCredentials(ctx, id, operation, true); err != nil {
		return err
	}
	return s.clearManagedClaudeCleanupCheckpoint(ctx, id, operation)
}

func (s *Service) clearManagedClaudeCleanupCheckpoint(ctx context.Context, id, operation domain.ID) error {
	_, err := s.Store.Mutate(ctx, domain.NewID(), "claude.managed.credential-cleanup-confirmed", struct{ Account, Operation domain.ID }{id, operation}, func(tx *store.Tx) (any, error) {
		r, a, err := managedClaudeAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		if !managedClaudeCredentialCleanupPending(a) || a.Subscription.ServerOperation.ID != operation {
			return struct{}{}, nil
		}
		a.Subscription.ServerOperation.CleanupPhase = ""
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	return err
}

func (s *Service) markManagedClaudeRecovery(id, operation domain.ID, diagnostic *domain.NativeSubscriptionDiagnostic) error {
	ctx, cancel := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 5*time.Second)
	defer cancel()
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	delete(s.subscriptionProgress, operation)
	s.clearClaudeLoginInput(operation)
	_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.managed.recovery", struct{ Account, Operation domain.ID }{id, operation}, func(tx *store.Tx) (any, error) {
		r, a, err := managedClaudeAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st.ServerOperation == nil || st.ServerOperation.ID != operation {
			return nil, subscriptionDenied()
		}
		if st.ServerOperation.State == domain.SubscriptionSucceeded {
			// A settled operation keeps its connected state; any remaining credential
			// cleanup is selected by its durable checkpoint instead of a fence.
			return struct{}{}, nil
		}
		st.ServerOperation.State = domain.SubscriptionRecovery
		if st.ServerOperation.ClaudeDiagnostic == nil {
			st.ServerOperation.ClaudeDiagnostic = diagnostic
		}
		st.RecoveryRequired = true
		a.Health = domain.AccountFailed
		if st.Pending == nil {
			st.Pending = operationPlaceholder(st.ServerOperation)
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	return err
}

// recoverManagedClaude finishes an interrupted operation after a restart. A
// login only reconciles the old private runtime and never relaunches or replays
// anything; a logout only completes its explicit revocation and vault removal.
func (s *Service) recoverManagedClaude(parent context.Context, id, operation domain.ID) error {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	var o domain.ServerSubscriptionOperation
	var generation domain.ID
	checkpoint, restored := false, false
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		_, a, err := managedClaudeAccount(tx, id, 0)
		if err != nil {
			return err
		}
		if !managedClaudeNeedsRecovery(a) || a.Subscription.ServerOperation.ID != operation {
			return subscriptionDenied()
		}
		o = *a.Subscription.ServerOperation
		generation = a.Subscription.Generation
		// A restored generation outranks its checkpoint: every reference is retired.
		restored = managedClaudeRestoredGeneration(a)
		checkpoint = !restored && managedClaudeCredentialCleanupPending(a)
		return nil
	})
	if err != nil {
		return err
	}
	if checkpoint {
		// A settled operation only owes vault cleanup. The settled generation is
		// preserved; no native runtime, fence or connection state is touched.
		expected := domain.ID("")
		if o.Action == domain.SubscriptionLogin {
			expected = o.FinishID
		}
		if generation != expected {
			// The settled generation changed after publication. Nothing here may
			// decide which reference is current, so delete nothing.
			s.logger.WarnContext(ctx, "claude_managed_cleanup_checkpoint_stale", "operation_id", operation)
			return s.clearManagedClaudeCleanupCheckpoint(ctx, id, operation)
		}
		if o.Action == domain.SubscriptionLogin {
			// A settled login written by an earlier build still carries the removed
			// routine retirement checkpoint. Its original native child joined before
			// the settlement, the account, operation and generation match, and the
			// published generation stays authoritative: only the checkpoint is
			// released and every retained generation and file is kept.
			s.logger.InfoContext(ctx, "server_subscription_legacy_transition", "account_id", id, "operation_id", operation, "action", o.Action, "result", credentialRemovalConfirmed)
			return s.clearManagedClaudeCleanupCheckpoint(ctx, id, operation)
		}
		if err := s.retireManagedClaudeReferences(ctx, id, operation); err != nil {
			return err
		}
		s.logger.InfoContext(ctx, "claude_managed_credential_cleanup_confirmed", "account_id", id, "operation_id", operation, "action", o.Action)
		return nil
	}
	if o.Action == domain.SubscriptionLogin && !(restored && !o.Active()) {
		// A restored settled login never owned a native child in this image.
		if err := reconcileFailedServerLoginRuntime(ctx, s.scopeFor(id), o); err != nil {
			return err
		}
	}
	// Only an explicit logout or the retirement of a restored generation removes
	// retained credentials. An interrupted first login never published a
	// generation, so its retained files and references stay.
	if restored || o.Action == domain.SubscriptionLogout {
		if err := s.removeAccountCredentials(ctx, id, operation, true); err != nil {
			return err
		}
	}
	_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.managed.recovered", struct{ Account, Operation domain.ID }{id, operation}, func(tx *store.Tx) (any, error) {
		r, a, err := managedClaudeAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if !managedClaudeNeedsRecovery(a) || st.ServerOperation.ID != operation {
			return nil, subscriptionDenied()
		}
		st.RecoveryRequired = false
		st.Pending = nil
		st.ServerOperation.NativeStarted = false
		st.ServerOperation.CleanupPhase = ""
		if restored && !st.ServerOperation.Active() {
			// The restored operation already settled; only its retired generation and
			// recovery fence are removed, so the account reads as a logged-out account.
			a.Connection = nil
			a.Health = domain.AccountDisconnected
			a.Validation, a.Catalog, a.Quota = nil, nil, nil
			a.ConfirmedExhausted = false
			st.Generation = ""
			st.IdentityCommitment = ""
		} else if o.Action == domain.SubscriptionLogin {
			st.ServerOperation.State = domain.SubscriptionFailed
			if st.ServerOperation.ClaudeDiagnostic == nil {
				st.ServerOperation.ClaudeDiagnostic = claudeDiagnostic(operation, domain.NativeSubscriptionCleanup, "", domain.Fail(domain.RecoveryRequired, "Interrupted.", "Connect a new account."))
			}
			a.Health = domain.AccountDisconnected
		} else {
			st.ServerOperation.State = domain.SubscriptionSucceeded
			st.ServerOperation.ClaudeDiagnostic = nil
			a.Connection = nil
			a.Health = domain.AccountDisconnected
			a.Validation, a.Catalog, a.Quota = nil, nil, nil
			a.ConfirmedExhausted = false
			st.Generation = ""
			st.IdentityCommitment = ""
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if err == nil {
		delete(s.subscriptionProgress, operation)
		s.logger.InfoContext(ctx, "claude_managed_recovered", "account_id", id, "operation_id", operation, "action", o.Action)
	}
	return err
}
