// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func subscriptionClient(ctx context.Context) (domain.Principal, error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice {
		return actor, domain.Fail(domain.PermissionDenied, "Subscription login requires an owner or paired client.", "Use an authenticated product client.")
	}
	return actor, nil
}

func (s *Service) requestServerSubscription(ctx context.Context, req *connect.Request[pb.RequestSubscriptionRequest]) (*connect.Response[pb.RequestSubscriptionResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, err := subscriptionClient(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	action := subscriptionAction(req.Msg.Action)
	if action != domain.SubscriptionLogin && action != domain.SubscriptionRefresh && action != domain.SubscriptionLogout || req.Msg.DeviceCode && action != domain.SubscriptionLogin {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Choose a supported subscription operation.", "Use login, refresh or logout."), c)
	}
	m := req.Msg.Mutation
	input := struct {
		ID         domain.ID
		Revision   uint64
		Action     domain.SubscriptionAction
		DeviceCode bool
		Actor      domain.Principal
	}{domain.ID(m.Id), m.ExpectedRevision, action, req.Msg.DeviceCode, actor}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "subscription.server.request", input, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, input.ID, input.Revision)
		if err != nil {
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
			return nil, domain.Fail(domain.Conflict, "The account already owns a login or lease.", "Finish the original logout and cleanup before a new login.")
		}
		if action != domain.SubscriptionLogin && (a.Connection == nil || state.Generation == "") {
			return nil, subscriptionDenied()
		}
		now := time.Now().UTC()
		state.ServerOperation = &domain.ServerSubscriptionOperation{ID: domain.ID(m.RequestId), Action: action, Epoch: s.subscriptionServerEpoch(), FinishID: domain.NewID(), Actor: actor, State: domain.SubscriptionPreparing, StartedAt: now, ExpiresAt: now.Add(15 * time.Minute)}
		state.Pending = &domain.SubscriptionOperation{ID: domain.ID(m.RequestId), Action: action, Actor: actor, DeviceCode: input.DeviceCode, Phase: domain.SubscriptionQueued}
		if action == domain.SubscriptionLogout {
			a.Health = domain.AccountRevoked
			if state.Observation != nil && state.Observation.Phase == domain.SubscriptionObservationQueued {
				state.Observation.Phase = domain.SubscriptionObservationFailed
				state.Observation.ErrorCode = domain.Canceled
			}
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
	s.logger.InfoContext(ctx, "server_subscription_accepted", "operation_id", m.RequestId, "action", action, "replayed", result.Replayed)
	return connect.NewResponse(&pb.RequestSubscriptionResponse{Account: rpc.Resource(r), OperationId: m.RequestId, Replayed: result.Replayed}), nil
}

func loginState(v domain.SubscriptionLoginState) pb.SubscriptionLoginState {
	switch v {
	case domain.SubscriptionPreparing:
		return pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_PREPARING
	case domain.SubscriptionWaiting:
		return pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING
	case domain.SubscriptionSucceeded:
		return pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED
	case domain.SubscriptionCanceled:
		return pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_CANCELED
	case domain.SubscriptionExpired:
		return pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_EXPIRED
	case domain.SubscriptionUnsupported:
		return pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_UNSUPPORTED
	case domain.SubscriptionRecovery:
		return pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED
	default:
		return pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_FAILED
	}
}

// Called only under accountGate. Suggestions expire with the original login,
// remain memory-only, and cannot be inherited by another actor or generation.
func (s *Service) serverSubscriptionProgress(ctx context.Context, req *pb.GetSubscriptionProgressRequest) (response *pb.GetSubscriptionProgressResponse, matched bool, err error) {
	actor, err := subscriptionClient(ctx)
	if err != nil {
		return nil, false, err
	}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		_, a, err := subscriptionAccount(tx, domain.ID(req.AccountId), 0)
		if err != nil {
			return err
		}
		if a.Subscription == nil || a.Subscription.ServerOperation == nil || string(a.Subscription.ServerOperation.ID) != req.OperationId {
			return nil
		}
		matched = true
		o := a.Subscription.ServerOperation
		if actor != o.Actor || subscriptionActorValid(tx, o.Actor) != nil {
			delete(s.subscriptionProgress, o.ID)
			return subscriptionDenied()
		}
		state := o.State
		if a.Subscription.RecoveryRequired || o.Active() && o.Epoch != s.subscriptionServerEpoch() {
			state = domain.SubscriptionRecovery
		}
		response = &pb.GetSubscriptionProgressResponse{Diagnostic: codexDiagnosticMessage(o.Diagnostic), State: loginState(state), Canceled: state == domain.SubscriptionCanceled || a.Subscription.Pending != nil && a.Subscription.Pending.ID == o.ID && a.Subscription.Pending.Canceled}
		p := s.subscriptionProgress[o.ID]
		if !p.Until.IsZero() && !time.Now().Before(p.Until) {
			delete(s.subscriptionProgress, o.ID)
			p = subscriptionProgress{}
		}
		if state == domain.SubscriptionWaiting && !response.Canceled && o.Epoch == s.subscriptionServerEpoch() {
			response.Url = p.URL
			response.UserCode = p.UserCode
		}
		if state == domain.SubscriptionSucceeded && a.Connection != nil && a.Subscription.Generation == o.FinishID {
			response.Generation = string(o.FinishID)
			if p.Generation == o.FinishID {
				response.SuggestedName = p.Name
			}
		}
		return nil
	})
	return
}

// Startup retains old native/process ownership and never relaunches a login.
func (s *Service) initializeServerSubscriptions(ctx context.Context) error {
	owner := domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice})
	_, err := s.Store.Mutate(owner, domain.NewID(), "subscription.server.restart", struct{}{}, func(tx *store.Tx) (any, error) {
		records, err := all(tx, domain.AccountKind)
		if err != nil {
			return nil, err
		}
		for _, r := range records {
			a, err := store.Decode[domain.Account](r)
			if err != nil {
				return nil, err
			}
			if a.Subscription == nil || a.Subscription.ServerOperation == nil {
				continue
			}
			o := a.Subscription.ServerOperation
			if !o.Active() || o.Epoch == s.subscriptionServerEpoch() || o.State == domain.SubscriptionRecovery && a.Subscription.RecoveryRequired && a.Health == domain.AccountFailed {
				continue
			}
			o.State = domain.SubscriptionRecovery
			a.Subscription.RecoveryRequired = true
			a.Health = domain.AccountFailed
			if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
				return nil, err
			}
		}
		return struct{}{}, nil
	})
	return err
}

func (s *Service) runServerSubscriptions(ctx context.Context) {
	ctx = domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice})
	child, stop := context.WithCancel(ctx)
	defer stop()
	var workers sync.WaitGroup
	done := make(chan domain.ID, 16)
	running := map[domain.ID]bool{}
	cleanupAttempted := map[domain.ID]bool{}
	defer func() { stop(); workers.Wait() }()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		type candidate struct {
			account, operation domain.ID
			cleanup            bool
		}
		var candidates []candidate
		err := s.Store.Read(child, func(tx *store.Tx) error {
			records, err := all(tx, domain.AccountKind)
			if err != nil {
				return err
			}
			for _, r := range records {
				a, err := store.Decode[domain.Account](r)
				if err != nil {
					return err
				}
				if a.Subscription != nil && !a.Subscription.RecoveryRequired && a.Subscription.ServerOperation != nil && a.Subscription.ServerOperation.State == domain.SubscriptionPreparing && !a.Subscription.ServerOperation.NativeStarted && a.Subscription.ServerOperation.Epoch == s.subscriptionServerEpoch() && a.Subscription.Pending != nil && a.Subscription.Pending.Phase == domain.SubscriptionQueued && a.Subscription.Lease == nil {
					candidates = append(candidates, candidate{account: r.ID})
				} else if failedServerLoginNeedsCleanup(a) && !cleanupAttempted[a.Subscription.ServerOperation.ID] {
					candidates = append(candidates, candidate{account: r.ID, operation: a.Subscription.ServerOperation.ID, cleanup: true})
				}
			}
			return nil
		})
		if err != nil && child.Err() == nil {
			s.logger.WarnContext(ctx, "server_subscription_scan_failed", "code", domain.SafeError(err).Code)
		}
		for _, next := range candidates {
			if running[next.account] || len(running) >= 16 {
				continue
			}
			running[next.account] = true
			if next.cleanup {
				// One bounded attempt per original operation in this server epoch.
				// Failed cleanup stays fenced rather than retrying every scan tick.
				cleanupAttempted[next.operation] = true
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				if next.cleanup {
					if err := s.recoverFailedServerLogin(child, next.account, next.operation); err != nil && child.Err() == nil {
						s.logger.WarnContext(child, "server_subscription_cleanup_pending", "operation_id", next.operation, "code", domain.SafeError(err).Code)
					}
				} else {
					s.runServerSubscription(child, next.account)
				}
				done <- next.account
			}()
		}
		select {
		case <-child.Done():
			return
		case id := <-done:
			delete(running, id)
		case <-tick.C:
		}
	}
}

func (s *Service) runServerSubscription(parent context.Context, id domain.ID) {
	var original domain.ServerSubscriptionOperation
	var operation domain.SubscriptionOperation
	err := s.Store.Read(parent, func(tx *store.Tx) error {
		_, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return err
		}
		if a.Subscription == nil || a.Subscription.ServerOperation == nil || a.Subscription.Pending == nil {
			return subscriptionDenied()
		}
		original = *a.Subscription.ServerOperation
		operation = *a.Subscription.Pending
		return nil
	})
	if err != nil {
		return
	}
	unlock, err := s.lockAccounts(parent)
	if err != nil {
		return
	}
	_, err = s.Store.Mutate(parent, domain.NewID(), "subscription.server.claim", struct{ Account, Operation domain.ID }{id, original.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != original.ID || st.ServerOperation.State != domain.SubscriptionPreparing || st.ServerOperation.NativeStarted || st.Pending == nil || st.Pending.ID != original.ID || st.Pending.Phase != domain.SubscriptionQueued || st.ServerObservationActive() || st.Lease != nil || st.RecoveryRequired || st.ServerOperation.Epoch != s.subscriptionServerEpoch() {
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
			operation = *st.Pending
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	unlock()
	if err != nil || !original.NativeStarted {
		return
	}
	ctx, cancel := context.WithDeadline(parent, original.ExpiresAt)
	defer cancel()
	checked := make(chan struct{})
	go func() {
		defer close(checked)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			err := s.Store.Read(ctx, func(tx *store.Tx) error {
				_, a, err := subscriptionAccount(tx, id, 0)
				if err != nil {
					return err
				}
				if a.Subscription == nil || a.Subscription.RecoveryRequired || a.Subscription.Pending == nil || a.Subscription.Pending.ID != original.ID || a.Subscription.Pending.Canceled || subscriptionActorValid(tx, original.Actor) != nil {
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
	var latest []byte
	cleanup, success := false, false
	nativeStarted := false
	version, phase := "", domain.CodexRuntime
	var native serverSubscriptionNative
	_, old, nativeErr := s.serverSubscriptionCredentials(ctx, id, original.Generation)
	if nativeErr != nil && operation.Action == domain.SubscriptionLogin && original.Generation == "" {
		// No opener or native write ran. Verify the narrower pre-native absence
		// proof before allowing a checkpoint, even if the vault must be retried
		// after server restart. Retained evidence remains recovery-owned.
		verifyCtx := ctx
		if parent.Err() != nil {
			var verifyCancel context.CancelFunc
			verifyCtx, verifyCancel = context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 30*time.Second)
			defer verifyCancel()
		}
		if err := reconcileFailedServerLoginPreNative(verifyCtx, s.Store.Root(), original.ID); err == nil {
			cleanup = true
		} else {
			nativeErr = domain.CodexRecoveryFailure(version, domain.CodexCleanup, nativeErr, err)
		}
	}
	if nativeErr == nil {
		opener := s.subscriptionOpen
		if opener == nil {
			opener = openServerSubscription
		}
		native, nativeErr = opener(ctx, s.Store.Root(), original.ID, old, s.logger)
		if nativeErr != nil {
			cleanup = domain.SafeError(nativeErr).Code != domain.RecoveryRequired
		}
	}
	clear(old)
	if native != nil {
		nativeStarted = true
		version, phase = native.Version(), domain.CodexLogin
		if operation.Action == domain.SubscriptionLogin {
			progress, err := native.StartManagedLogin(ctx, operation.DeviceCode)
			nativeErr = err
			if nativeErr == nil {
				nativeErr = s.publishServerSubscriptionProgress(ctx, id, original, progress.URL, progress.UserCode)
			}
			if nativeErr == nil {
				nativeErr = native.WaitManagedLogin(ctx, progress.LoginID)
			}
			if nativeErr != nil && progress.LoginID != "" {
				bounded, stop := context.WithTimeout(context.Background(), 5*time.Second)
				cancelErr := native.CancelManagedLogin(bounded, progress.LoginID)
				stop()
				if cancelErr != nil {
					nativeErr = domain.CodexRecoveryFailure(version, phase, nativeErr, subscriptionDenied())
				}
			}
		}
		if nativeErr == nil {
			if operation.Action == domain.SubscriptionLogout {
				nativeErr = native.LogoutManaged(ctx)
			} else {
				latest, nativeErr = native.ManagedBundle(ctx, operation.Action == domain.SubscriptionRefresh)
			}
			success = nativeErr == nil
		}
		closeErr := native.Close(latest)
		cleanup = closeErr == nil
		if closeErr != nil {
			nativeErr = domain.CodexRecoveryFailure(version, phase, nativeErr, closeErr)
			success = false
		}
	}
	nativeErr = domain.WithCodexDiagnostic(version, phase, nativeErr)
	diagnostic := domain.CodexErrorDiagnostic(nativeErr)
	if diagnostic != nil {
		diagnostic.CorrelationID = string(original.ID)
	}
	cancel()
	<-checked
	state := domain.SubscriptionFailed
	if success {
		state = domain.SubscriptionSucceeded
	} else if parent.Err() != nil || !cleanup || domain.SafeError(nativeErr).Code == domain.RecoveryRequired {
		state = domain.SubscriptionRecovery
		if parent.Err() != nil && cleanup && operation.Action == domain.SubscriptionLogin {
			state = domain.SubscriptionCanceled
		}
	} else if !time.Now().Before(original.ExpiresAt) {
		state = domain.SubscriptionExpired
	} else if errors.Is(ctx.Err(), context.Canceled) && (errors.Is(nativeErr, context.Canceled) || !nativeStarted && parent.Err() != nil) {
		state = domain.SubscriptionCanceled
	} else if domain.SafeError(nativeErr).Code == domain.Unsupported {
		state = domain.SubscriptionUnsupported
	}
	// Explicit refresh/logout uncertainty cannot redistribute an old generation.
	if !success && nativeStarted && operation.Action != domain.SubscriptionLogin {
		state = domain.SubscriptionRecovery
	}
	bounded, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 30*time.Second)
	defer stop()
	if err := s.finishServerSubscription(bounded, id, original, operation, latest, cleanup, state, diagnostic); err != nil {
		if diagnostic == nil {
			diagnostic = domain.CodexErrorDiagnostic(domain.WithCodexDiagnostic(version, domain.CodexCleanup, err))
			diagnostic.CorrelationID = string(original.ID)
		}
		if e := s.markServerSubscriptionRecovery(id, original.ID, diagnostic); e != nil {
			s.logger.Warn("server_subscription_recovery_unconfirmed", "operation_id", original.ID, "code", domain.SafeError(e).Code)
		}
		s.logger.Warn("server_subscription_finish_failed", "operation_id", original.ID, "code", domain.SafeError(err).Code, "version", diagnostic.DetectedVersion, "minimum_version", diagnostic.MinimumVersion, "phase", diagnostic.Phase, "original_code", diagnostic.Code, "correlation_id", diagnostic.CorrelationID, "cleanup_confirmed", cleanup)
	}
	clear(latest)
}

func (s *Service) publishServerSubscriptionProgress(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation, url, code string) error {
	if !validServerLoginProgress(url, code) {
		s.logger.WarnContext(ctx, "server_subscription_login_url_rejected", "operation_id", o.ID, "code", domain.RecoveryRequired)
		return subscriptionDenied()
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(ctx, domain.NewID(), "subscription.server.waiting", struct{ Account, Operation domain.ID }{id, o.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		if a.Subscription == nil || a.Subscription.ServerOperation == nil || a.Subscription.ServerOperation.ID != o.ID || a.Subscription.Pending == nil || a.Subscription.Pending.Canceled || a.Subscription.RecoveryRequired || subscriptionActorValid(tx, o.Actor) != nil {
			return nil, subscriptionDenied()
		}
		a.Subscription.ServerOperation.State = domain.SubscriptionWaiting
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	if err == nil {
		if s.subscriptionProgress == nil {
			s.subscriptionProgress = map[domain.ID]subscriptionProgress{}
		}
		s.subscriptionProgress[o.ID] = subscriptionProgress{URL: url, UserCode: code, Until: o.ExpiresAt}
	}
	return err
}

func suggestedSubscriptionName(identity subscription.Identity) string {
	for _, candidate := range []string{identity.Email, identity.DisplayName, "ChatGPT"} {
		if domain.Text(candidate, "account name", 256, true) == nil {
			return candidate
		}
	}
	return "ChatGPT"
}

func (s *Service) finishServerSubscription(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation, operation domain.SubscriptionOperation, bundle []byte, cleanup bool, result domain.SubscriptionLoginState, diagnostic *domain.CodexDiagnostic) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if diagnostic != nil && diagnostic.Validate() != nil {
		return subscriptionDenied()
	}
	var suggestion *subscriptionProgress
	defer func() {
		delete(s.subscriptionProgress, o.ID)
		if suggestion != nil {
			s.subscriptionProgress[o.ID] = *suggestion
		}
	}()
	var identity subscription.Identity
	var commitment string
	success := result == domain.SubscriptionSucceeded && cleanup
	if success && operation.Action != domain.SubscriptionLogout {
		_, identity, err = subscription.Parse(bundle)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(struct{ Account, User string }{identity.Account, identity.User})
		commitment = s.accountCommitment(s.Identity.ServerID, raw)
		clear(raw)
	}
	var a domain.Account
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, current, e := subscriptionAccount(tx, id, 0)
		a = current
		if e != nil {
			return e
		}
		if a.Subscription == nil || a.Subscription.ServerOperation == nil || a.Subscription.ServerOperation.ID != o.ID || a.Subscription.ServerOperation.Epoch != o.Epoch || !a.Subscription.ServerOperation.NativeStarted || a.Subscription.Generation != o.Generation || a.Subscription.RecoveryRequired {
			return subscriptionDenied()
		}
		if a.Subscription.Pending == nil || a.Subscription.Pending.ID != o.ID {
			return subscriptionDenied()
		}
		if a.Subscription.Pending.Canceled || subscriptionActorValid(tx, o.Actor) != nil {
			success = false
			result = domain.SubscriptionCanceled
			if operation.Action != domain.SubscriptionLogin {
				result = domain.SubscriptionRecovery
			}
		}
		if success && commitment != "" {
			if a.Subscription.IdentityCommitment != "" && commitment != a.Subscription.IdentityCommitment {
				return subscriptionDenied()
			}
			return uniqueSubscriptionIdentity(tx, id, commitment)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !success && cleanup && failedServerLoginOwner(a, o.ID) {
		settled, err := s.cleanupFailedServerLoginLocked(ctx, id, o.ID, true, result, diagnostic)
		if err != nil {
			return err
		}
		s.logServerSubscriptionFinish(ctx, o, settled, cleanup, diagnostic)
		return nil
	}
	vault, err := s.secrets()
	if err != nil {
		return err
	}
	if success && operation.Action != domain.SubscriptionLogout {
		if o.Generation != "" {
			old, e := vault.Get(ctx, credentials.Ref{Owner: id, ID: o.Generation, Purpose: credentials.AccountLogin})
			if e != nil {
				return e
			}
			e = subscription.Refreshed(old, bundle)
			clear(old)
			if e != nil {
				return e
			}
		}
		if _, err := vault.Put(ctx, credentials.Ref{Owner: id, ID: o.FinishID, Purpose: credentials.AccountLogin}, bundle); err != nil {
			return err
		}
	}
	_, err = s.Store.Mutate(ctx, o.FinishID, "subscription.server.finish", struct {
		Account, Operation domain.ID
		Cleanup            bool
		State              domain.SubscriptionLoginState
		Diagnostic         *domain.CodexDiagnostic
	}{id, o.ID, cleanup, result, diagnostic}, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != o.ID || !st.ServerOperation.NativeStarted || st.Generation != o.Generation || st.RecoveryRequired || st.Pending == nil || st.Pending.ID != o.ID {
			return nil, subscriptionDenied()
		}
		if st.Pending.Canceled || subscriptionActorValid(tx, o.Actor) != nil {
			success = false
			result = domain.SubscriptionCanceled
			if operation.Action != domain.SubscriptionLogin {
				result = domain.SubscriptionRecovery
			}
		}
		st.ServerOperation.Diagnostic = diagnostic
		if success {
			st.ServerOperation.Diagnostic = nil
		}
		if !cleanup || result == domain.SubscriptionRecovery {
			st.RecoveryRequired = true
			a.Health = domain.AccountFailed
			st.ServerOperation.State = domain.SubscriptionRecovery
		} else {
			st.ServerOperation.NativeStarted = false
			st.ServerOperation.State = result
			st.Pending = nil
			if success && operation.Action != domain.SubscriptionLogout {
				if err := uniqueSubscriptionIdentity(tx, id, commitment); err != nil {
					return nil, err
				}
				st.Generation = o.FinishID
				st.PaidCredits = nil
				st.IdentityCommitment = commitment
				st.OwnerMachineID = ""
				if a.Connection == nil {
					a.Connection = &domain.AccountConnection{ID: o.FinishID, Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()}
				}
				a.Health = domain.AccountReady
			} else if success && operation.Action == domain.SubscriptionLogout {
				a.Connection = nil
				a.Health = domain.AccountDisconnected
				a.Validation = nil
				a.Catalog = nil
				a.Quota = nil
				a.ConfirmedExhausted = false
				st.Generation = ""
				st.IdentityCommitment = ""
				st.OwnerMachineID = ""
				st.PaidCredits = nil
				st.ResetCredits = nil
				st.QuotaObservedAt = nil
				st.QuotaState = domain.ObservationUnknown
				st.SpendControlReached = nil
				st.SpendControlObservedAt = nil
				if st.Observation != nil && (st.Observation.Phase == domain.SubscriptionObservationSending || st.Observation.Phase == domain.SubscriptionObservationUncertain) {
					st.Observation.Phase = domain.SubscriptionObservationRetiredUncertain
				}
			}
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return accountReceipt{ID: id}, nil
	})
	if err != nil {
		return err
	}
	keep := o.Generation
	if success {
		keep = o.FinishID
		if operation.Action == domain.SubscriptionLogout {
			keep = ""
		}
	}
	if cleanup {
		if err := cleanupSubscriptionReferences(ctx, vault, id, keep); err != nil {
			return err
		}
	}
	if success && cleanup && operation.Action != domain.SubscriptionLogout {
		_, err := s.Store.Mutate(ctx, domain.NewID(), "subscription.server.quota.ready", struct{ Account, Generation domain.ID }{id, o.FinishID}, func(tx *store.Tx) (any, error) {
			r, a, err := subscriptionAccount(tx, id, 0)
			if err != nil {
				return nil, err
			}
			if a.Subscription.Generation != o.FinishID || a.Subscription.RecoveryRequired || a.Subscription.ServerOperation == nil || a.Subscription.ServerOperation.ID != o.ID {
				return nil, subscriptionDenied()
			}
			a.Subscription.ServerQuotaGeneration = o.FinishID
			if _, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
				return nil, err
			}
			// The joined quota lane discovers this new generation only after
			// the final protected-reference cleanup has settled.
			return struct{}{}, nil
		})
		if err != nil {
			return err
		}
	}
	// Publish the transient suggestion only after credential cleanup and the
	// original success commit. It never enters the account document or receipt.
	if success && operation.Action == domain.SubscriptionLogin {
		if s.subscriptionProgress == nil {
			s.subscriptionProgress = map[domain.ID]subscriptionProgress{}
		}
		// The deferred removal applies before this later original-bound publication.
		suggestion = &subscriptionProgress{Name: suggestedSubscriptionName(identity), Generation: o.FinishID, Until: o.ExpiresAt}
	}
	s.logServerSubscriptionFinish(ctx, o, result, cleanup, diagnostic)
	return nil
}

func (s *Service) logServerSubscriptionFinish(ctx context.Context, o domain.ServerSubscriptionOperation, result domain.SubscriptionLoginState, cleanup bool, diagnostic *domain.CodexDiagnostic) {
	s.logger.InfoContext(ctx, "server_subscription_finished", "operation_id", o.ID, "state", result, "cleanup_confirmed", cleanup, "correlation_id", o.ID)
	if diagnostic != nil {
		s.logger.WarnContext(ctx, "server_subscription_native_failed", "version", diagnostic.DetectedVersion, "minimum_version", diagnostic.MinimumVersion, "phase", diagnostic.Phase, "code", diagnostic.Code, "correlation_id", diagnostic.CorrelationID, "state", result, "cleanup_confirmed", cleanup)
	}
}

func (s *Service) markServerSubscriptionRecovery(id, operation domain.ID, diagnostic *domain.CodexDiagnostic) error {
	ctx, cancel := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 5*time.Second)
	defer cancel()
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	delete(s.subscriptionProgress, operation)
	_, err = s.Store.Mutate(ctx, domain.NewID(), "subscription.server.recovery", struct{ Account, Operation domain.ID }{id, operation}, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		if a.Subscription == nil || a.Subscription.ServerOperation == nil || a.Subscription.ServerOperation.ID != operation {
			return nil, subscriptionDenied()
		}
		if a.Subscription.ServerOperation.CleanupPhase != domain.SubscriptionNativeCleanupConfirmed {
			a.Subscription.ServerOperation.State = domain.SubscriptionRecovery
		}
		if a.Subscription.ServerOperation.Diagnostic == nil {
			a.Subscription.ServerOperation.Diagnostic = diagnostic
		}
		a.Subscription.RecoveryRequired = true
		a.Health = domain.AccountFailed
		// A settled metadata publication may precede failed vault cleanup. Retain
		// the operation fence even when its native owner has already joined.
		if a.Subscription.Pending == nil {
			a.Subscription.Pending = operationPlaceholder(a.Subscription.ServerOperation)
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	return err
}
func operationPlaceholder(o *domain.ServerSubscriptionOperation) *domain.SubscriptionOperation {
	return &domain.SubscriptionOperation{ID: o.ID, Action: o.Action, Actor: o.Actor, Phase: domain.SubscriptionQueued}
}

func codexDiagnosticMessage(d *domain.CodexDiagnostic) *pb.CodexDiagnostic {
	if d == nil || d.Validate() != nil {
		return nil
	}
	phases := map[domain.CodexPhase]pb.CodexDiagnosticPhase{
		domain.CodexDiscovery:  pb.CodexDiagnosticPhase_CODEX_DIAGNOSTIC_PHASE_DISCOVERY,
		domain.CodexVersion:    pb.CodexDiagnosticPhase_CODEX_DIAGNOSTIC_PHASE_VERSION,
		domain.CodexProfile:    pb.CodexDiagnosticPhase_CODEX_DIAGNOSTIC_PHASE_PROFILE,
		domain.CodexRuntime:    pb.CodexDiagnosticPhase_CODEX_DIAGNOSTIC_PHASE_RUNTIME,
		domain.CodexLaunch:     pb.CodexDiagnosticPhase_CODEX_DIAGNOSTIC_PHASE_LAUNCH,
		domain.CodexInitialize: pb.CodexDiagnosticPhase_CODEX_DIAGNOSTIC_PHASE_INITIALIZE,
		domain.CodexConfirm:    pb.CodexDiagnosticPhase_CODEX_DIAGNOSTIC_PHASE_CONFIRM,
		domain.CodexLogin:      pb.CodexDiagnosticPhase_CODEX_DIAGNOSTIC_PHASE_LOGIN,
		domain.CodexModels:     pb.CodexDiagnosticPhase_CODEX_DIAGNOSTIC_PHASE_MODELS,
		domain.CodexExecution:  pb.CodexDiagnosticPhase_CODEX_DIAGNOSTIC_PHASE_EXECUTION,
		domain.CodexHistory:    pb.CodexDiagnosticPhase_CODEX_DIAGNOSTIC_PHASE_HISTORY,
		domain.CodexCleanup:    pb.CodexDiagnosticPhase_CODEX_DIAGNOSTIC_PHASE_CLEANUP,
	}
	return &pb.CodexDiagnostic{DetectedVersion: d.DetectedVersion, MinimumVersion: d.MinimumVersion, Phase: phases[d.Phase], Code: string(d.Code), Message: d.Message, Guidance: d.Guidance, CorrelationId: d.CorrelationID}
}

// Initialize the one shared vault and read only the original generation under
// the protected account gate. Native sessions must run after this gate releases.
func (s *Service) serverSubscriptionCredentials(ctx context.Context, account, generation domain.ID) (accountSecrets, []byte, error) {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer unlock()
	vault, err := s.secrets()
	if err != nil || generation == "" {
		return vault, nil, err
	}
	old, err := vault.Get(ctx, credentials.Ref{Owner: account, ID: generation, Purpose: credentials.AccountLogin})
	return vault, old, err
}
