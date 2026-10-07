// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

type grokOAuthMaterial struct {
	Verifier []byte `json:"verifier,omitempty"`
	State    string `json:"state,omitempty"`
	Nonce    string `json:"nonce,omitempty"`
	Callback string `json:"callback,omitempty"`
	Device   []byte `json:"device,omitempty"`
}

func (s *Service) grokOAuthClient() grokOAuthHTTP {
	return grokOAuthHTTP{route: s.outboundResolver(), transport: s.grokOAuthTransport}
}

// Each transition checks the same original server/account/generation/actor.
// Network mutations run only after this transaction has durably committed.
func (s *Service) advanceGrokOAuth(ctx context.Context, id domain.ID, original domain.ServerSubscriptionOperation, expected domain.GrokOAuthPhase, next domain.GrokOAuthOperation) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(ctx, domain.NewID(), "subscription.grok.phase", struct {
		Account, Operation domain.ID
		Expected           domain.GrokOAuthPhase
		Next               domain.GrokOAuthOperation
	}{id, original.ID, expected, next}, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if a.SubscriptionService != domain.SubscriptionGrok || st == nil || st.ServerOperation == nil || st.ServerOperation.ID != original.ID || st.ServerOperation.Epoch != original.Epoch || original.Epoch != s.subscriptionServerEpoch() || !st.ServerOperation.NativeStarted || st.ServerOperation.Generation != original.Generation || st.Generation != original.Generation || st.RecoveryRequired || st.Lease != nil || st.Pending == nil || st.Pending.ID != original.ID || st.Pending.Canceled || subscriptionActorValid(tx, original.Actor) != nil || !time.Now().Before(st.ServerOperation.ExpiresAt) {
			return nil, domain.Fail(domain.Canceled, "The original Grok login no longer permits a send.", "Inspect its current state.")
		}
		previous := st.ServerOperation.GrokOAuth
		if expected == "" && previous != nil || expected != "" && (previous == nil || previous.Phase != expected) || previous != nil && (next.Phase == domain.GrokOAuthWaiting || next.Phase == domain.GrokOAuthPollSending) && next.PollSequence < previous.PollSequence {
			return nil, subscriptionDenied()
		}
		st.ServerOperation.GrokOAuth = &next
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if err == nil {
		s.logger.InfoContext(ctx, "grok_subscription_phase", "operation_id", original.ID, "phase", next.Phase, "poll_sequence", next.PollSequence)
	}
	return err
}

func (s *Service) retainGrokMaterial(ctx context.Context, vault accountSecrets, account domain.ID, value grokOAuthMaterial) (domain.ID, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", grokOAuthProblem()
	}
	defer clear(raw)
	ref := domain.NewID()
	_, err = vault.Put(ctx, credentials.Ref{Owner: account, ID: ref, Purpose: credentials.AccountLogin}, raw)
	return ref, err
}

func (s *Service) publishGrokProgress(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation, authorization, code string) error {
	if !validGrokProgress(authorization, code) {
		return grokOAuthProblem()
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(ctx, domain.NewID(), "subscription.grok.waiting", struct{ Account, Operation domain.ID }{id, o.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if a.SubscriptionService != domain.SubscriptionGrok || st == nil || st.ServerOperation == nil || st.ServerOperation.ID != o.ID || st.ServerOperation.Epoch != o.Epoch || st.RecoveryRequired || st.Pending == nil || st.Pending.ID != o.ID || st.Pending.Canceled || subscriptionActorValid(tx, o.Actor) != nil {
			return nil, subscriptionDenied()
		}
		st.ServerOperation.State = domain.SubscriptionWaiting
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if err == nil {
		if s.subscriptionProgress == nil {
			s.subscriptionProgress = map[domain.ID]subscriptionProgress{}
		}
		s.subscriptionProgress[o.ID] = subscriptionProgress{URL: authorization, UserCode: code, Until: o.ExpiresAt}
	}
	return err
}

func (s *Service) runGrokServerSubscription(parent context.Context, id domain.ID) {
	var original domain.ServerSubscriptionOperation
	var operation domain.SubscriptionOperation
	unlock, err := s.lockAccounts(parent)
	if err != nil {
		return
	}
	_, err = s.Store.Mutate(parent, domain.NewID(), "subscription.grok.claim", struct{ Account domain.ID }{id}, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if a.SubscriptionService != domain.SubscriptionGrok || st == nil || st.ServerOperation == nil || st.ServerOperation.State != domain.SubscriptionPreparing || st.ServerOperation.Epoch != s.subscriptionServerEpoch() || st.ServerOperation.NativeStarted || st.RecoveryRequired || st.Lease != nil || st.Pending == nil || st.Pending.ID != st.ServerOperation.ID || st.Pending.Phase != domain.SubscriptionQueued {
			return nil, subscriptionDenied()
		}
		if st.Pending.Action != domain.SubscriptionLogout && !s.grokSubscriptionAccepted {
			return nil, domain.Fail(domain.Unsupported, "Grok subscription support is awaiting profile acceptance.", "Retain the original operation without sending OAuth.")
		}
		o := st.ServerOperation
		if st.Pending.Canceled || subscriptionActorValid(tx, o.Actor) != nil || !time.Now().Before(o.ExpiresAt) {
			o.State = domain.SubscriptionCanceled
			if !time.Now().Before(o.ExpiresAt) {
				o.State = domain.SubscriptionExpired
			}
			st.Pending = nil
		} else {
			o.NativeStarted = true // The disjoint server credential lease; no Grok process is launched for OAuth.
			o.Generation = st.Generation
			st.Pending.Phase = domain.SubscriptionClaimed
			original, operation = *o, *st.Pending
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	unlock()
	if err != nil || !original.NativeStarted {
		return
	}
	ctx, cancel := context.WithDeadline(parent, original.ExpiresAt)
	checked := make(chan struct{})
	go func() {
		defer close(checked)
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			err := s.Store.Read(ctx, func(tx *store.Tx) error {
				_, a, e := subscriptionAccount(tx, id, 0)
				if e != nil {
					return e
				}
				st := a.Subscription
				if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != original.ID || st.RecoveryRequired || st.Pending == nil || st.Pending.ID != original.ID || st.Pending.Canceled || subscriptionActorValid(tx, original.Actor) != nil {
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
			case <-tick.C:
			}
		}
	}()
	phase := domain.GrokLogin
	if operation.Action == domain.SubscriptionRefresh {
		phase = domain.GrokRefresh
	}
	if operation.Action == domain.SubscriptionLogout {
		phase = domain.GrokLogout
	}
	vault, problem := s.secrets()
	var bundle []byte
	if problem == nil {
		switch operation.Action {
		case domain.SubscriptionLogin:
			bundle, problem = s.grokServerLogin(ctx, vault, id, original, operation.DeviceCode)
		case domain.SubscriptionRefresh:
			bundle, problem = s.grokServerRefresh(ctx, vault, id, original)
		case domain.SubscriptionLogout:
			// The scanner admits this lane only after the original Worker returns
			// its lease with confirmed process/file cleanup. Revoked health already
			// blocks all new executions. Protected deletion is joined by Finish.
		default:
			problem = subscriptionDenied()
		}
	}
	cancel()
	<-checked
	result := domain.SubscriptionSucceeded
	if problem != nil {
		code := domain.SafeError(problem).Code
		result = domain.SubscriptionFailed
		switch {
		case code == domain.RecoveryRequired:
			result = domain.SubscriptionRecovery
		case errors.Is(problem, context.Canceled) || code == domain.Canceled || parent.Err() != nil:
			result = domain.SubscriptionCanceled
		case code == domain.CursorExpired || !time.Now().Before(original.ExpiresAt):
			result = domain.SubscriptionExpired
		case code == domain.Unsupported:
			result = domain.SubscriptionUnsupported
		}
		// A refresh may have rotated the only usable refresh token. Only its
		// original validated sealed result can remove this uncertainty fence.
		if operation.Action == domain.SubscriptionRefresh {
			result = domain.SubscriptionRecovery
		}
		original.GrokDiagnostic = domain.NewGrokDiagnostic("", phase, code, original.ID)
	}
	bounded, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 30*time.Second)
	defer stop()
	if e := s.finishServerSubscription(bounded, id, original, operation, bundle, true, result, nil); e != nil {
		if e := s.markGrokRecovery(bounded, id, original, phase, e); e != nil {
			s.logger.Warn("grok_subscription_recovery_unconfirmed", "operation_id", original.ID, "code", domain.SafeError(e).Code)
		}
	}
	clear(bundle)
}

func (s *Service) markGrokRecovery(ctx context.Context, account domain.ID, original domain.ServerSubscriptionOperation, phase domain.GrokPhase, cause error) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	delete(s.subscriptionProgress, original.ID)
	_, err = s.Store.Mutate(ctx, domain.NewID(), "subscription.grok.recovery", struct{ Account, Operation domain.ID }{account, original.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, account, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != original.ID {
			return nil, subscriptionDenied()
		}
		st.ServerOperation.State = domain.SubscriptionRecovery
		if st.ServerOperation.GrokDiagnostic == nil {
			st.ServerOperation.GrokDiagnostic = original.GrokDiagnostic
			if st.ServerOperation.GrokDiagnostic == nil {
				st.ServerOperation.GrokDiagnostic = domain.NewGrokDiagnostic("", phase, domain.SafeError(cause).Code, original.ID)
			}
		}
		st.RecoveryRequired, a.Health = true, domain.AccountFailed
		if st.Pending == nil {
			st.Pending = operationPlaceholder(st.ServerOperation)
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	return err
}

func grokRandom() ([]byte, error) {
	raw := make([]byte, 32)
	defer clear(raw)
	if _, err := rand.Read(raw); err != nil {
		return nil, grokOAuthProblem()
	}
	return []byte(base64.RawURLEncoding.EncodeToString(raw)), nil
}

func (s *Service) grokServerLogin(ctx context.Context, vault accountSecrets, account domain.ID, original domain.ServerSubscriptionOperation, device bool) ([]byte, error) {
	c := s.grokOAuthClient()
	if err := c.verifyDiscovery(ctx); err != nil {
		return nil, err
	}
	var result grokTokenResult
	if device {
		if err := s.advanceGrokOAuth(ctx, account, original, "", domain.GrokOAuthOperation{Phase: domain.GrokOAuthDeviceSending}); err != nil {
			return nil, err
		}
		grant, err := c.device(ctx)
		if err != nil {
			return nil, err
		}
		defer clear(grant.device)
		ref, err := s.retainGrokMaterial(ctx, vault, account, grokOAuthMaterial{Device: grant.device})
		if err != nil {
			return nil, grokOAuthProblem()
		}
		next := domain.GrokOAuthOperation{Phase: domain.GrokOAuthWaiting, ProtectedRef: ref}
		if err := s.advanceGrokOAuth(ctx, account, original, domain.GrokOAuthDeviceSending, next); err != nil {
			return nil, grokOAuthProblem()
		}
		if err := s.publishGrokProgress(ctx, account, original, grokVerificationURI, grant.user); err != nil {
			return nil, err
		}
		for {
			timer := time.NewTimer(grant.interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
			if !time.Now().Before(grant.expires) {
				return nil, domain.Fail(domain.CursorExpired, "Grok sign-in expired.", "Start another login after this operation settles.")
			}
			next.Phase, next.PollSequence = domain.GrokOAuthPollSending, next.PollSequence+1
			if err := s.advanceGrokOAuth(ctx, account, original, domain.GrokOAuthWaiting, next); err != nil {
				return nil, err
			}
			value, state, err := c.poll(ctx, grant.device)
			if err != nil {
				return nil, err
			}
			if state == oauthDeviceIssued {
				result = value
				break
			}
			next.Phase = domain.GrokOAuthWaiting
			if err := s.advanceGrokOAuth(ctx, account, original, domain.GrokOAuthPollSending, next); err != nil {
				return nil, grokOAuthProblem()
			}
			if state == oauthDeviceSlowDown {
				grant.interval += 5 * time.Second
			}
		}
	} else {
		value, err := s.grokBrowserLogin(ctx, vault, account, original, c)
		if err != nil {
			return nil, err
		}
		result = value
	}
	defer result.clear()
	return s.sealGrokResult(ctx, vault, account, original, result, nil)
}

func (s *Service) grokBrowserLogin(ctx context.Context, vault accountSecrets, account domain.ID, original domain.ServerSubscriptionOperation, c grokOAuthHTTP) (result grokTokenResult, returned error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return result, domain.Fail(domain.Unavailable, "Grok's browser callback is unavailable.", "Check Connection & diagnostics.")
	}
	callback := "http://" + listener.Addr().String() + "/callback"
	verifier, err := grokRandom()
	if err != nil {
		listener.Close()
		return result, err
	}
	defer clear(verifier)
	state, err := grokRandom()
	if err != nil {
		listener.Close()
		return result, err
	}
	defer clear(state)
	nonce, err := grokRandom()
	if err != nil {
		listener.Close()
		return result, err
	}
	defer clear(nonce)
	authorization, err := grokAuthorizeURL(callback, verifier, string(state), string(nonce))
	if err != nil {
		listener.Close()
		return result, err
	}
	ref, err := s.retainGrokMaterial(ctx, vault, account, grokOAuthMaterial{Verifier: verifier, State: string(state), Nonce: string(nonce), Callback: callback})
	if err != nil {
		listener.Close()
		return result, err
	}
	if err := s.advanceGrokOAuth(ctx, account, original, "", domain.GrokOAuthOperation{Phase: domain.GrokOAuthPrepared, ProtectedRef: ref}); err != nil {
		listener.Close()
		return result, err
	}
	code := make(chan []byte, 1)
	var accepted atomic.Bool
	var connections sync.WaitGroup
	u, _ := url.Parse(callback)
	host := u.Host
	server := &http.Server{ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: time.Second, MaxHeaderBytes: 24 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	server.ConnState = func(_ net.Conn, state http.ConnState) {
		switch state {
		case http.StateNew:
			connections.Add(1)
		case http.StateClosed, http.StateHijacked:
			connections.Done()
		}
	}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		if r.Method != http.MethodGet || r.Host != host || r.URL.Path != "/callback" || r.URL.RawPath != "" || r.URL.IsAbs() || ctx.Err() != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		query, ok := grokCallbackQuery([]byte(r.URL.RawQuery), string(state))
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if !accepted.CompareAndSwap(false, true) {
			w.WriteHeader(http.StatusConflict)
			return
		}
		values, _ := url.ParseQuery(query)
		if values.Get("error") != "" {
			code <- nil
		} else {
			code <- []byte(values.Get("code"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "Sign-in received. Return to DeliDev.")
	})
	joined := make(chan struct{})
	go func() { defer close(joined); _ = server.Serve(listener) }()
	defer func() {
		closeErr := server.Close()
		<-joined
		// Serve has joined, so every accepted connection was registered before
		// this Wait. Close alone does not join handlers that own callback bytes.
		connections.Wait()
		select {
		case value := <-code:
			clear(value)
		default:
		}
		if closeErr != nil {
			result.clear()
			returned = grokOAuthProblem()
		}
	}()
	if err := s.advanceGrokOAuth(ctx, account, original, domain.GrokOAuthPrepared, domain.GrokOAuthOperation{Phase: domain.GrokOAuthWaiting, ProtectedRef: ref}); err != nil {
		return result, err
	}
	if err := s.publishGrokProgress(ctx, account, original, authorization, ""); err != nil {
		return result, err
	}
	var value []byte
	select {
	case <-ctx.Done():
		return result, ctx.Err()
	case value = <-code:
	}
	defer clear(value)
	if len(value) == 0 {
		return result, domain.Fail(domain.PermissionDenied, "Grok sign-in was declined.", "Start a new login after this operation settles.")
	}
	if err := s.advanceGrokOAuth(ctx, account, original, domain.GrokOAuthWaiting, domain.GrokOAuthOperation{Phase: domain.GrokOAuthExchangeSending, ProtectedRef: ref}); err != nil {
		return result, err
	}
	return c.token(ctx, map[string][]byte{"grant_type": []byte("authorization_code"), "client_id": []byte(subscription.GrokClientID), "redirect_uri": []byte(callback), "code_verifier": verifier, "code": value}, string(nonce), "")
}

func (s *Service) grokServerRefresh(ctx context.Context, vault accountSecrets, account domain.ID, original domain.ServerSubscriptionOperation) ([]byte, error) {
	old, err := vault.Get(ctx, credentials.Ref{Owner: account, ID: original.Generation, Purpose: credentials.AccountLogin})
	if err != nil {
		return nil, err
	}
	defer clear(old)
	auth, identity, err := subscription.ParseGrok(old)
	if err != nil {
		return nil, err
	}
	c := s.grokOAuthClient()
	if err := c.verifyDiscovery(ctx); err != nil {
		return nil, err
	}
	if err := s.advanceGrokOAuth(ctx, account, original, "", domain.GrokOAuthOperation{Phase: domain.GrokOAuthRefreshSending, ProtectedRef: original.Generation}); err != nil {
		return nil, err
	}
	refresh := []byte(auth.Refresh)
	defer clear(refresh)
	result, err := c.token(ctx, map[string][]byte{"grant_type": []byte("refresh_token"), "client_id": []byte(subscription.GrokClientID), "refresh_token": refresh}, "", identity.User)
	if err != nil {
		return nil, err
	}
	defer result.clear()
	if result.identity.PrincipalType != identity.PrincipalType || result.identity.PrincipalID != identity.PrincipalID {
		return nil, grokOAuthProblem()
	}
	if len(result.refresh) == 0 {
		result.refresh = append([]byte(nil), refresh...)
	}
	return s.sealGrokResult(ctx, vault, account, original, result, &auth)
}

func (s *Service) sealGrokResult(ctx context.Context, vault accountSecrets, account domain.ID, original domain.ServerSubscriptionOperation, result grokTokenResult, previous *subscription.GrokAuth) ([]byte, error) {
	auth := subscription.GrokAuth{Mode: subscription.GrokOIDC, Issuer: subscription.GrokIssuer, ClientID: subscription.GrokClientID}
	if previous != nil {
		auth = *previous
	}
	auth.Key, auth.Refresh, auth.Created, auth.Expires, auth.User = string(result.access), string(result.refresh), time.Now().UTC(), result.expires, result.identity.User
	if result.identity.Email != "" {
		auth.Email = &result.identity.Email
	}
	raw, err := json.Marshal(map[string]subscription.GrokAuth{subscription.GrokScope: auth})
	if err != nil {
		return nil, grokOAuthProblem()
	}
	identity, err := subscription.ParseService(domain.SubscriptionGrok, raw)
	if err != nil || identity.User != result.identity.User || identity.PrincipalType != result.identity.PrincipalType || identity.PrincipalID != result.identity.PrincipalID {
		clear(raw)
		return nil, grokOAuthProblem()
	}
	if _, err := vault.Put(ctx, credentials.Ref{Owner: account, ID: original.FinishID, Purpose: credentials.AccountLogin}, raw); err != nil {
		clear(raw)
		return nil, grokOAuthProblem()
	}
	var expected domain.GrokOAuthPhase
	if previous != nil {
		expected = domain.GrokOAuthRefreshSending
	} else {
		err := s.Store.Read(ctx, func(tx *store.Tx) error {
			_, a, err := subscriptionAccount(tx, account, 0)
			if err != nil {
				return err
			}
			if a.Subscription == nil || a.Subscription.ServerOperation == nil || a.Subscription.ServerOperation.ID != original.ID || a.Subscription.ServerOperation.GrokOAuth == nil {
				return subscriptionDenied()
			}
			expected = a.Subscription.ServerOperation.GrokOAuth.Phase
			if expected != domain.GrokOAuthExchangeSending && expected != domain.GrokOAuthPollSending {
				return subscriptionDenied()
			}
			return nil
		})
		if err != nil {
			clear(raw)
			return nil, grokOAuthProblem()
		}
	}
	if err := s.advanceGrokOAuth(ctx, account, original, expected, domain.GrokOAuthOperation{Phase: domain.GrokOAuthSealed, ProtectedRef: original.FinishID}); err != nil {
		clear(raw)
		return nil, grokOAuthProblem()
	}
	return raw, nil
}
