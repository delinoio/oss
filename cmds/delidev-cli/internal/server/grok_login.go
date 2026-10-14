// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outbound"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Server-owned Grok browser login. The Go server implements the OAuth
// authorization-code flow itself (PKCE S256, state, nonce, token exchange and
// ID-token validation) against the fixed xAI profile and produces a sealed
// subscription.GrokBundle. No native Grok process takes part in login.
//
// One login owns one IPv4 loopback listener on an ephemeral port, bound for the
// life of the operation and closed on every exit path. The authorization URL
// is returned only through the transient progress response. The single
// callback reaches the flow either directly (the browser is local to the
// server) or through ForwardSubscriptionCallback (a remote desktop). The
// exchange claim is persisted before the one token request is sent; an
// uncertain exchange ends in recovery and is never resent.

// grokLoginPhase is the structured-log phase of every Grok login record.
type grokLoginPhase string

const grokLogPhaseLogin grokLoginPhase = "LOGIN"

// grokLoginStep names the step a log record belongs to.
type grokLoginStep string

const (
	grokStepClaim     grokLoginStep = "claim"
	grokStepDiscovery grokLoginStep = "discovery"
	grokStepListen    grokLoginStep = "listen"
	grokStepPublish   grokLoginStep = "publish"
	grokStepCallback  grokLoginStep = "callback"
	grokStepExchange  grokLoginStep = "exchange"
	grokStepIDToken   grokLoginStep = "id_token"
	grokStepUserInfo  grokLoginStep = "userinfo"
	grokStepMapping   grokLoginStep = "mapping"
	grokStepFinish    grokLoginStep = "finish"
)

// grokCallbackWindow bounds how long a callback request may take to arrive.
const grokCallbackWindow = 5 * time.Second

// grokCallback is the single accepted authorization response.
type grokCallback struct {
	code   []byte
	denied bool
}

// grokLoginFlow is the in-memory half of one login: the listener and the
// one-shot callback gate. The state value never leaves this struct except in
// the authorization URL and is compared in constant time.
type grokLoginFlow struct {
	operation domain.ID
	port      int
	state     string
	listener  net.Listener
	server    *http.Server
	served    chan struct{}
	accepted  atomic.Bool
	callback  chan grokCallback
	closeOnce sync.Once
}

// offer hands the one callback to the flow. It reports false for a wrong state,
// a second callback or a flow that already ended; none of those reach the token
// endpoint.
func (f *grokLoginFlow) offer(state string, cb grokCallback) bool {
	if subtle.ConstantTimeCompare([]byte(state), []byte(f.state)) != 1 {
		return false
	}
	if !f.accepted.CompareAndSwap(false, true) {
		return false
	}
	f.callback <- cb
	return true
}

// close stops the listener and waits for the server goroutine to return. It is
// idempotent and safe on every exit path.
func (f *grokLoginFlow) close() {
	f.closeOnce.Do(func() {
		_ = f.server.Close()
		<-f.served
	})
}

// allowedGrokCallbackKeys are the only query keys a callback may carry.
var allowedGrokCallbackKeys = map[string]bool{"code": true, "state": true, "scope": true, "error": true, "error_description": true, "error_uri": true, "iss": true}

func grokQueryValue(values []string) (string, bool) {
	if len(values) != 1 || len(values[0]) > 8192 || strings.IndexFunc(values[0], unicode.IsControl) >= 0 {
		return "", false
	}
	return values[0], true
}

func loopbackRemote(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ServeHTTP accepts exactly one callback with the exact method, host, path and
// state. Anything else is refused without consuming the one-shot gate and
// without any outbound request.
func (f *grokLoginFlow) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if r.Method != http.MethodGet || r.URL.Path != subscription.GrokCallbackPath || r.Host != net.JoinHostPort(subscription.GrokCallbackHost, strconv.Itoa(f.port)) || !loopbackRemote(r.RemoteAddr) {
		http.Error(w, "Not found.", http.StatusNotFound)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(r.URL.RawQuery) > 16<<10 {
		http.Error(w, "Invalid callback.", http.StatusBadRequest)
		return
	}
	for key := range query {
		if !allowedGrokCallbackKeys[key] {
			http.Error(w, "Invalid callback.", http.StatusBadRequest)
			return
		}
	}
	state, stateOK := grokQueryValue(query["state"])
	if !stateOK {
		http.Error(w, "Invalid callback.", http.StatusBadRequest)
		return
	}
	cb := grokCallback{}
	if values, present := query["error"]; present {
		if _, ok := grokQueryValue(values); !ok {
			http.Error(w, "Invalid callback.", http.StatusBadRequest)
			return
		}
		cb.denied = true
	} else {
		code, ok := grokQueryValue(query["code"])
		if !ok || code == "" {
			http.Error(w, "Invalid callback.", http.StatusBadRequest)
			return
		}
		cb.code = []byte(code)
	}
	if !f.offer(state, cb) {
		clear(cb.code)
		http.Error(w, "This sign-in link is no longer valid.", http.StatusConflict)
		return
	}
	if cb.denied {
		http.Error(w, "Sign-in was not completed. You can close this tab.", http.StatusForbidden)
		return
	}
	_, _ = io.WriteString(w, "Sign-in received. You can close this tab and return to DeliDev.")
}

// newGrokLoginFlow binds the IPv4 loopback listener on an ephemeral port and
// starts serving it.
func newGrokLoginFlow(operation domain.ID, state string) (*grokLoginFlow, error) {
	listener, err := net.Listen("tcp4", net.JoinHostPort(subscription.GrokCallbackHost, "0"))
	if err != nil {
		return nil, err
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || address.Port < subscription.GrokMinCallbackPort {
		_ = listener.Close()
		return nil, errors.New("loopback listener has no usable port")
	}
	f := &grokLoginFlow{operation: operation, port: address.Port, state: state, listener: listener, served: make(chan struct{}), callback: make(chan grokCallback, 1)}
	f.server = &http.Server{Handler: f, ReadHeaderTimeout: grokCallbackWindow, ReadTimeout: grokCallbackWindow, WriteTimeout: grokCallbackWindow, IdleTimeout: time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0)}
	go func() { defer close(f.served); _ = f.server.Serve(listener) }()
	return f, nil
}

func (s *Service) registerGrokFlow(f *grokLoginFlow) {
	s.grokLoginMu.Lock()
	defer s.grokLoginMu.Unlock()
	if s.grokLogins == nil {
		s.grokLogins = map[domain.ID]*grokLoginFlow{}
	}
	s.grokLogins[f.operation] = f
}

func (s *Service) grokFlow(operation domain.ID) *grokLoginFlow {
	s.grokLoginMu.Lock()
	defer s.grokLoginMu.Unlock()
	return s.grokLogins[operation]
}

// releaseGrokFlow unregisters and closes the flow's listener.
func (s *Service) releaseGrokFlow(f *grokLoginFlow) {
	s.grokLoginMu.Lock()
	if s.grokLogins[f.operation] == f {
		delete(s.grokLogins, f.operation)
	}
	s.grokLoginMu.Unlock()
	f.close()
}

// grokOAuthTransport returns the transport of one login's outbound calls: the
// server's explicit route, never an ambient proxy or a redirect.
func (s *Service) grokOAuthRoundTripper() (http.RoundTripper, func()) {
	if s.grokOAuthTransport != nil {
		return s.grokOAuthTransport, func() {}
	}
	base := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 20 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 20 * time.Second, ResponseHeaderTimeout: 20 * time.Second, MaxResponseHeaderBytes: 32 << 10, DisableKeepAlives: true, DisableCompression: true}
	return &outbound.Transport{Base: base, Resolve: s.outboundResolver()}, base.CloseIdleConnections
}

// serverSubscriptionAccount is the gate of the service-neutral server
// operation path. ChatGPT keeps the Codex gate; a Grok account is admitted for
// server-owned browser login. Every other service stays refused.
func serverSubscriptionAccount(tx *store.Tx, id domain.ID, revision uint64) (store.Record, domain.Account, error) {
	r, a, err := accountFromTx(tx, id, revision)
	if err == nil && a.Type == domain.SubscriptionAccount && a.SubscriptionService == domain.SubscriptionGrok && a.ProviderID == "" {
		return r, a, nil
	}
	return subscriptionAccount(tx, id, revision)
}

func grokLoginUnsupportedDevice() *domain.Error {
	return domain.Fail(domain.Unsupported, "Grok device-code login is not available.", "Use browser login; methods never switch automatically.")
}

func grokLoginUnsupportedAction() *domain.Error {
	return domain.Fail(domain.Unsupported, "Only Grok login and refresh are available on the server.", "Use login or refresh; logout is separate lifecycle work.")
}

// grokOutcome is the settled result of the OAuth steps, before it is written.
type grokOutcome struct {
	bundle subscription.GrokBundle
	state  domain.SubscriptionLoginState
	code   domain.Code
	step   grokLoginStep
	// claimed reports that the exchange claim was persisted.
	claimed bool
	// rejected reports that the provider definitively refused a refresh token.
	rejected bool
}

func (o grokOutcome) failed() bool {
	return o.state != domain.SubscriptionSucceeded
}

// needsDiagnostic reports whether progress carries a diagnostic: a failure or
// uncertainty, never a user cancellation or the expiry window.
func (o grokOutcome) needsDiagnostic() bool {
	return o.state == domain.SubscriptionFailed || o.state == domain.SubscriptionRecovery || o.state == domain.SubscriptionUnsupported
}

func (s *Service) logGrok(ctx context.Context, level slog.Level, message string, o domain.ServerSubscriptionOperation, step grokLoginStep, attrs ...any) {
	phase := grokLogPhaseLogin
	if o.Action == domain.SubscriptionRefresh {
		phase = grokLogPhaseRefresh
	}
	base := []any{"phase", phase, "operation_id", o.ID, "correlation_id", o.ID, "step", step}
	s.logger.Log(ctx, level, message, append(base, attrs...)...)
}

// runGrokLogin owns one server-owned Grok browser login from claim to its
// settled outcome. It never sends a token request without a persisted claim and
// never sends one twice.
func (s *Service) runGrokLogin(parent context.Context, id domain.ID) {
	var original domain.ServerSubscriptionOperation
	err := s.Store.Read(parent, func(tx *store.Tx) error {
		_, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return err
		}
		if a.Subscription == nil || a.Subscription.ServerOperation == nil || a.Subscription.Pending == nil {
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
	_, err = s.Store.Mutate(parent, domain.NewID(), "grok.server.claim", struct{ Account, Operation domain.ID }{id, original.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != original.ID || st.ServerOperation.State != domain.SubscriptionPreparing || st.ServerOperation.NativeStarted || st.Pending == nil || st.Pending.ID != original.ID || st.Pending.Phase != domain.SubscriptionQueued || st.ServerObservationActive() || st.Lease != nil || st.RecoveryRequired || st.ServerOperation.Epoch != s.subscriptionServerEpoch() || st.ServerOperation.Action != domain.SubscriptionLogin {
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
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	unlock()
	if err == nil {
		err = s.grokFault(grokFaultAfterLease)
	}
	if err != nil {
		if original.NativeStarted {
			// The lease transaction ran but its outcome is unknown: the durable
			// operation decides whether it is fenced for the scanner.
			s.fenceUnsettledGrokOperation(parent, id, original)
		}
		return
	}
	if !original.NativeStarted {
		return
	}
	s.logGrok(parent, slog.LevelInfo, "grok_login_started", original, grokStepClaim, "account_id", id)
	ctx, cancel := context.WithDeadline(parent, original.ExpiresAt)
	defer cancel()
	checked := make(chan struct{})
	go func() {
		defer close(checked)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			err := s.Store.Read(ctx, func(tx *store.Tx) error {
				_, a, err := grokSubscriptionAccount(tx, id, 0)
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
	outcome := s.grokLoginSteps(ctx, parent, id, original)
	cancel()
	<-checked
	bounded, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 30*time.Second)
	defer stop()
	if err := s.finishGrokLogin(bounded, id, original, &outcome); err != nil {
		s.logGrok(bounded, slog.LevelWarn, "grok_login_finish_failed", original, grokStepFinish, "code", domain.SafeError(err).Code)
		s.fenceUnsettledGrokOperation(bounded, id, original)
	}
	outcome.bundle.Clear()
}

// grokLoginSteps runs discovery, the loopback callback, the single exchange and
// the identity checks. The listener is closed before it returns on every path.
func (s *Service) grokLoginSteps(ctx, parent context.Context, id domain.ID, original domain.ServerSubscriptionOperation) (outcome grokOutcome) {
	fail := func(state domain.SubscriptionLoginState, code domain.Code, step grokLoginStep) grokOutcome {
		return grokOutcome{state: state, code: code, step: step, claimed: outcome.claimed}
	}
	// interrupted classifies the end of the wait: server shutdown and explicit
	// cancellation are canceled, the 15-minute window is expired.
	interrupted := func(step grokLoginStep) grokOutcome {
		if parent.Err() == nil && !time.Now().Before(original.ExpiresAt) {
			return fail(domain.SubscriptionExpired, domain.Canceled, step)
		}
		return fail(domain.SubscriptionCanceled, domain.Canceled, step)
	}
	transport, closeTransport := s.grokOAuthRoundTripper()
	defer closeTransport()
	client := subscription.GrokOAuthClient{Transport: transport}
	endpoints, err := client.Discover(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return interrupted(grokStepDiscovery)
		}
		s.logGrok(ctx, slog.LevelWarn, "grok_login_discovery_failed", original, grokStepDiscovery)
		return fail(domain.SubscriptionFailed, domain.Unavailable, grokStepDiscovery)
	}
	authorization, err := subscription.NewGrokAuthorization(s.grokRandom)
	if err != nil {
		return fail(domain.SubscriptionFailed, domain.Internal, grokStepListen)
	}
	defer authorization.Clear()
	flow, err := newGrokLoginFlow(original.ID, authorization.State)
	if err != nil {
		s.logGrok(ctx, slog.LevelWarn, "grok_login_listener_failed", original, grokStepListen)
		return fail(domain.SubscriptionFailed, domain.Unavailable, grokStepListen)
	}
	s.registerGrokFlow(flow)
	defer s.releaseGrokFlow(flow)
	redirect := subscription.GrokRedirectURI(flow.port)
	if err := s.publishGrokLoginProgress(ctx, id, original, endpoints.AuthorizationURL(redirect, authorization), flow); err != nil {
		if ctx.Err() != nil {
			return interrupted(grokStepPublish)
		}
		s.logGrok(ctx, slog.LevelWarn, "grok_login_publish_failed", original, grokStepPublish, "code", domain.SafeError(err).Code)
		return fail(domain.SubscriptionFailed, domain.Internal, grokStepPublish)
	}
	var callback grokCallback
	select {
	case callback = <-flow.callback:
	case <-ctx.Done():
		return interrupted(grokStepCallback)
	}
	// The one callback arrived: the listener is no longer needed and stays
	// closed while the exchange runs.
	flow.close()
	defer clear(callback.code)
	if callback.denied {
		s.logGrok(ctx, slog.LevelInfo, "grok_login_denied", original, grokStepCallback)
		return fail(domain.SubscriptionFailed, domain.PermissionDenied, grokStepCallback)
	}
	if err := s.claimGrokExchange(ctx, id, original); err != nil {
		if ctx.Err() != nil {
			return interrupted(grokStepExchange)
		}
		s.logGrok(ctx, slog.LevelWarn, "grok_login_claim_failed", original, grokStepExchange, "code", domain.SafeError(err).Code)
		return fail(domain.SubscriptionFailed, domain.SafeError(err).Code, grokStepExchange)
	}
	outcome.claimed = true
	s.logGrok(ctx, slog.LevelInfo, "grok_login_exchange_claimed", original, grokStepExchange)
	tokens, err := client.ExchangeCode(ctx, endpoints, callback.code, authorization.Verifier, redirect)
	clear(callback.code)
	if err != nil {
		reason, _ := subscription.GrokOAuthFailure(err)
		if reason == subscription.GrokReasonTokenRejected {
			s.logGrok(ctx, slog.LevelWarn, "grok_login_exchange_rejected", original, grokStepExchange)
			return fail(domain.SubscriptionFailed, domain.PermissionDenied, grokStepExchange)
		}
		// An uncertain exchange may have redeemed the code. It is never resent.
		s.logGrok(ctx, slog.LevelWarn, "grok_login_exchange_uncertain", original, grokStepExchange, "reason", reason)
		return fail(domain.SubscriptionRecovery, domain.RecoveryRequired, grokStepExchange)
	}
	claims, err := client.ValidateIDToken(ctx, endpoints, tokens.IDToken, authorization.Nonce)
	if err != nil {
		reason, _ := subscription.GrokOAuthFailure(err)
		s.logGrok(ctx, slog.LevelWarn, "grok_login_id_token_rejected", original, grokStepIDToken, "reason", reason)
		return fail(domain.SubscriptionFailed, domain.Unauthenticated, grokStepIDToken)
	}
	info, err := client.UserInfo(ctx, endpoints, tokens.AccessToken, claims)
	if err != nil {
		s.logGrok(ctx, slog.LevelWarn, "grok_login_userinfo_failed", original, grokStepUserInfo)
		return fail(domain.SubscriptionFailed, domain.Unavailable, grokStepUserInfo)
	}
	bundle, err := subscription.BuildGrokBundle(time.Now(), tokens, claims, info)
	if err != nil {
		s.logGrok(ctx, slog.LevelWarn, "grok_login_mapping_failed", original, grokStepMapping)
		return fail(domain.SubscriptionFailed, domain.InvalidArgument, grokStepMapping)
	}
	return grokOutcome{bundle: bundle, state: domain.SubscriptionSucceeded, step: grokStepMapping, claimed: true}
}

// publishGrokLoginProgress validates the authorization URL against the fixed
// profile, then makes the login visible as waiting. The URL stays in memory.
func (s *Service) publishGrokLoginProgress(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation, authorization string, flow *grokLoginFlow) error {
	state, port, ok := subscription.GrokAuthorizationDetails(authorization)
	if !ok || port != flow.port || state != flow.state {
		s.logGrok(ctx, slog.LevelWarn, "grok_login_url_rejected", o, grokStepPublish, "code", domain.RecoveryRequired)
		return subscriptionDenied()
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(ctx, domain.NewID(), "grok.server.waiting", struct{ Account, Operation domain.ID }{id, o.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != o.ID || st.Pending == nil || st.Pending.Canceled || st.RecoveryRequired || subscriptionActorValid(tx, o.Actor) != nil {
			return nil, subscriptionDenied()
		}
		st.ServerOperation.State = domain.SubscriptionWaiting
		st.ServerOperation.LoginMethod = domain.SubscriptionBrowserCallback
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	if err == nil {
		if s.subscriptionProgress == nil {
			s.subscriptionProgress = map[domain.ID]subscriptionProgress{}
		}
		s.subscriptionProgress[o.ID] = subscriptionProgress{URL: authorization, Until: o.ExpiresAt}
		s.logGrok(ctx, slog.LevelInfo, "grok_login_waiting", o, grokStepPublish, "port", port)
	}
	return err
}

// claimGrokExchange persists the exchange claim before the token request is
// sent. After it commits, the operation can only ever settle as an outcome of
// that one request; a crash or cancellation leaves recovery, never a resend.
func (s *Service) claimGrokExchange(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(ctx, domain.NewID(), "grok.server.exchange-claim", struct{ Account, Operation domain.ID }{id, o.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		current := st.ServerOperation
		if st.RecoveryRequired || st.Pending == nil || st.Pending.Canceled || current == nil || current.ID != o.ID || current.Epoch != o.Epoch || current.State != domain.SubscriptionWaiting || !current.NativeStarted || current.CodeConsumed || !time.Now().Before(current.ExpiresAt) || subscriptionActorValid(tx, current.Actor) != nil {
			return nil, subscriptionDenied()
		}
		if current.CodeSubmissionID == "" {
			current.CodeSubmissionID = domain.NewID()
		}
		current.LoginMethod = domain.SubscriptionBrowserCallback
		current.CodeConsumed = true
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	return err
}

// grokDiagnosticFor builds the durable diagnostic of a failed login.
func grokDiagnosticFor(o domain.ServerSubscriptionOperation, outcome grokOutcome) *domain.GrokDiagnostic {
	if !outcome.needsDiagnostic() {
		return nil
	}
	return &domain.GrokDiagnostic{Phase: domain.GrokDiagnosticLogin, Code: outcome.code, CorrelationID: o.ID}
}

// finishGrokLogin writes the settled outcome. A success seals and stages the
// bundle in the protected vault, then publishes the generation, the identity
// commitment and the connection in one commit. Failures never touch the vault.
func (s *Service) finishGrokLogin(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation, outcome *grokOutcome) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	var suggestion *subscriptionProgress
	defer func() {
		delete(s.subscriptionProgress, o.ID)
		if suggestion != nil {
			s.subscriptionProgress[o.ID] = *suggestion
		}
	}()
	success := outcome.state == domain.SubscriptionSucceeded
	result, code := outcome.state, outcome.code
	commitment := ""
	if success {
		commitment = s.grokIdentityCommitment(outcome.bundle)
		if commitment == "" {
			success, result, code = false, domain.SubscriptionFailed, domain.InvalidArgument
		}
	}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, e := grokSubscriptionAccount(tx, id, 0)
		if e != nil {
			return e
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != o.ID || st.ServerOperation.Epoch != o.Epoch || !st.ServerOperation.NativeStarted || st.Generation != o.Generation || st.RecoveryRequired || st.Pending == nil || st.Pending.ID != o.ID {
			return subscriptionDenied()
		}
		// A cancellation never hides an uncertain claimed exchange: that outcome
		// stays in recovery so the claim is never forgotten or resent.
		if (st.Pending.Canceled || subscriptionActorValid(tx, o.Actor) != nil) && result != domain.SubscriptionRecovery {
			success, result, code = false, domain.SubscriptionCanceled, domain.Canceled
		}
		if success {
			if st.IdentityCommitment != "" && commitment != st.IdentityCommitment {
				success, result, code = false, domain.SubscriptionFailed, domain.Conflict
				return nil
			}
			if err := uniqueSubscriptionIdentity(tx, id, commitment); err != nil {
				// A duplicate identity is a login failure, not an infrastructure fault.
				success, result, code = false, domain.SubscriptionFailed, domain.SafeError(err).Code
			}
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
	if success {
		envelope, e := subscription.SealGrokBundle(s.Identity.ServerID, id, o.FinishID, outcome.bundle)
		if e != nil {
			return e
		}
		_, e = vault.Put(ctx, credentials.Ref{Owner: id, ID: o.FinishID, Purpose: credentials.AccountLogin}, envelope)
		clear(envelope)
		if e != nil {
			return e
		}
	}
	diagnostic := grokDiagnosticFor(o, grokOutcome{state: result, code: code})
	_, err = s.Store.Mutate(ctx, o.FinishID, "grok.server.finish", struct {
		Account, Operation domain.ID
		State              domain.SubscriptionLoginState
		Code               domain.Code
	}{id, o.ID, result, code}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != o.ID || !st.ServerOperation.NativeStarted || st.Generation != o.Generation || st.RecoveryRequired || st.Pending == nil || st.Pending.ID != o.ID {
			return nil, subscriptionDenied()
		}
		if (st.Pending.Canceled || subscriptionActorValid(tx, o.Actor) != nil) && result != domain.SubscriptionRecovery {
			// Canceled before publication: no credential is kept.
			success, result, code = false, domain.SubscriptionCanceled, domain.Canceled
			diagnostic = nil
		}
		st.ServerOperation.GrokDiagnostic = diagnostic
		if success {
			st.ServerOperation.GrokDiagnostic = nil
		}
		if result == domain.SubscriptionRecovery {
			st.RecoveryRequired = true
			a.Health = domain.AccountFailed
			st.ServerOperation.State = domain.SubscriptionRecovery
		} else {
			st.ServerOperation.NativeStarted = false
			st.ServerOperation.State = result
			st.Pending = nil
			if success {
				if err := uniqueSubscriptionIdentity(tx, id, commitment); err != nil {
					return nil, err
				}
				st.AutomaticCreditConsent = nil
				st.Generation = o.FinishID
				st.IdentityCommitment = commitment
				st.OwnerMachineID = ""
				if a.Connection == nil {
					a.Connection = &domain.AccountConnection{ID: o.FinishID, Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()}
				}
				a.Health = domain.AccountReady
			} else {
				a.Health = domain.AccountDisconnected
				a.Validation, a.Catalog, a.Quota = nil, nil, nil
				a.ConfirmedExhausted = false
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
	// Only a committed success keeps its staged generation; every other outcome
	// removes any generation the vault holds for the account.
	if result != domain.SubscriptionRecovery {
		keep := o.Generation
		if success {
			keep = o.FinishID
		}
		if err := cleanupSubscriptionReferences(ctx, vault, id, keep); err != nil {
			return err
		}
	}
	if success {
		if s.subscriptionProgress == nil {
			s.subscriptionProgress = map[domain.ID]subscriptionProgress{}
		}
		suggestion = &subscriptionProgress{Name: outcome.bundle.SuggestedName(), Generation: o.FinishID, Until: o.ExpiresAt}
	}
	level := slog.LevelInfo
	if !success {
		level = slog.LevelWarn
	}
	s.logGrok(ctx, level, "grok_login_finished", o, grokStepFinish, "state", result, "code", code, "claimed", outcome.claimed)
	return nil
}

// grokDiagnosticMessage presents the durable diagnostic with fixed safe text.
func grokDiagnosticMessage(d *domain.GrokDiagnostic) *pb.GrokDiagnostic {
	if d == nil || d.Validate() != nil {
		return nil
	}
	message, guidance := "Grok sign-in did not complete.", "Start a new Grok sign-in."
	switch {
	case d.Phase == domain.GrokDiagnosticRefresh:
		message, guidance = "Grok credential refresh did not complete.", "Start a new refresh later."
		switch d.Code {
		case domain.RecoveryRequired:
			message, guidance = "Grok credential refresh has an uncertain outcome.", "Do not retry the refresh. Sign in to the account again after inspecting it."
		case domain.Unauthenticated:
			message, guidance = "Grok no longer accepts the stored credential.", "Sign in to the account again."
		case domain.Unavailable:
			message, guidance = "Grok refresh services are unavailable.", "Check the server's network access to the Grok sign-in service, then try again."
		case domain.Conflict:
			message, guidance = "The stored Grok credential does not match the account.", "Sign in to the account again."
		}
	case d.Code == domain.RecoveryRequired:
		message, guidance = "Grok sign-in has an uncertain outcome.", "Do not retry the original sign-in. Inspect the account before starting another."
	case d.Code == domain.PermissionDenied:
		message, guidance = "Grok did not authorize this sign-in.", "Start a new Grok sign-in and approve the request."
	case d.Code == domain.Unauthenticated:
		message, guidance = "Grok sign-in could not be verified.", "Start a new Grok sign-in."
	case d.Code == domain.Unavailable:
		message, guidance = "Grok sign-in services are unavailable.", "Check the server's network access to the Grok sign-in service, then try again."
	case d.Code == domain.Conflict:
		message, guidance = "This Grok account is already connected.", "Use the existing account instead of adding it again."
	}
	return &pb.GrokDiagnostic{DetectedVersion: d.DetectedVersion, SupportedVersion: d.SupportedVersion, Phase: pb.GrokDiagnosticPhase(d.Phase), Code: string(d.Code), Message: message, Guidance: guidance, CorrelationId: string(d.CorrelationID)}
}

// grokCallbackQuery validates a forwarded callback: only code, state and scope,
// each exactly once and bounded.
func grokCallbackQuery(raw []byte) (code []byte, state string, ok bool) {
	if len(raw) < 1 || len(raw) > 16<<10 {
		return nil, "", false
	}
	q, err := url.ParseQuery(string(raw))
	if err != nil {
		return nil, "", false
	}
	for key, values := range q {
		if key != "code" && key != "state" && key != "scope" {
			return nil, "", false
		}
		if _, valid := grokQueryValue(values); !valid {
			return nil, "", false
		}
	}
	state = q.Get("state")
	if q.Get("code") == "" || !subscription.GrokOpaqueValue(state) {
		return nil, "", false
	}
	return []byte(q.Get("code")), state, true
}

// forwardGrokCallback delivers one forwarded Grok callback to the in-process
// login. The operation, actor, epoch, state and loopback port must all match
// this login; a duplicate, late, wrong-state or wrong-operation delivery is
// refused and never reaches the token endpoint.
func (s *Service) forwardGrokCallback(ctx context.Context, correlation string, actor domain.Principal, account, operation domain.ID, query []byte) (*connect.Response[pb.ForwardSubscriptionCallbackResponse], error) {
	code, state, ok := grokCallbackQuery(query)
	if !ok {
		return nil, rpc.Error(subscriptionDenied(), correlation)
	}
	defer clear(code)
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var flow *grokLoginFlow
	var original domain.ServerSubscriptionOperation
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		_, a, err := grokSubscriptionAccount(tx, account, 0)
		if err != nil {
			return err
		}
		st := a.Subscription
		if st == nil || st.RecoveryRequired || st.ServerOperation == nil || st.Pending == nil {
			return subscriptionDenied()
		}
		o := st.ServerOperation
		if o.ID != operation || o.Actor != actor || o.Epoch != s.subscriptionServerEpoch() || o.State != domain.SubscriptionWaiting || !o.NativeStarted || o.CallbackForwarded || o.CodeConsumed || !time.Now().Before(o.ExpiresAt) || st.Pending.ID != o.ID || st.Pending.Canceled || subscriptionActorValid(tx, o.Actor) != nil {
			return subscriptionDenied()
		}
		flow = s.grokFlow(o.ID)
		published, port, valid := subscription.GrokAuthorizationDetails(s.subscriptionProgress[o.ID].URL)
		if flow == nil || !valid || port != flow.port || published != state || published != flow.state {
			return subscriptionDenied()
		}
		original = *o
		return nil
	})
	if err == nil {
		// Record dispatch ownership before delivery, as the Codex forwarder does:
		// a duplicate or restart can inspect progress but never repeats delivery.
		_, err = s.Store.Mutate(ctx, domain.NewID(), "grok.server.callback", struct{ Account, Operation domain.ID }{account, operation}, func(tx *store.Tx) (any, error) {
			r, a, err := grokSubscriptionAccount(tx, account, 0)
			if err != nil {
				return nil, err
			}
			st := a.Subscription
			o := st.ServerOperation
			if o == nil || o.ID != original.ID || o.CallbackForwarded || o.CodeConsumed || o.Epoch != original.Epoch || o.State != domain.SubscriptionWaiting || st.RecoveryRequired || st.Pending == nil || st.Pending.Canceled || subscriptionActorValid(tx, actor) != nil {
				return nil, subscriptionDenied()
			}
			o.CallbackForwarded = true
			o.LoginMethod = domain.SubscriptionBrowserCallback
			o.CodeSubmissionID = domain.NewID()
			if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
				return nil, err
			}
			return struct{}{}, nil
		})
	}
	unlock()
	if err != nil {
		s.logGrok(ctx, slog.LevelWarn, "grok_login_callback_refused", original, grokStepCallback, "code", domain.SafeError(err).Code)
		return nil, rpc.Error(err, correlation)
	}
	delivered := append([]byte(nil), code...)
	if !flow.offer(state, grokCallback{code: delivered}) {
		clear(delivered)
		s.logGrok(ctx, slog.LevelWarn, "grok_login_callback_unconfirmed", original, grokStepCallback)
		return nil, rpc.Error(domain.Fail(domain.RecoveryRequired, "Callback delivery was not confirmed.", "Inspect the original login. The callback is never sent again."), correlation)
	}
	s.logGrok(ctx, slog.LevelInfo, "grok_login_callback_forwarded", original, grokStepCallback)
	return connect.NewResponse(&pb.ForwardSubscriptionCallbackResponse{Accepted: true}), nil
}

// grokLoginNeedsRecovery selects an interrupted Grok login whose exchange was
// never claimed: a server restart or an unconfirmed finish left it fenced, but
// no token request was ever sent, so it settles as a failed login. A claimed
// exchange stays in recovery and is never resolved automatically.
func grokLoginNeedsRecovery(a domain.Account) bool {
	st := a.Subscription
	if a.Type != domain.SubscriptionAccount || a.SubscriptionService != domain.SubscriptionGrok || a.Removal != nil || a.Connection != nil || st == nil || st.Generation != "" || st.IdentityCommitment != "" || st.Lease != nil || !st.RecoveryRequired {
		return false
	}
	o := st.ServerOperation
	return o != nil && o.Action == domain.SubscriptionLogin && o.State == domain.SubscriptionRecovery && !o.CodeConsumed && !o.CallbackForwarded && st.Pending != nil && st.Pending.ID == o.ID
}

// recoverGrokLogin settles an unclaimed interrupted login. It holds the account
// gate, so it cannot race the original goroutine's finish.
func (s *Service) recoverGrokLogin(ctx context.Context, id, operation domain.ID) error {
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	unlock, err := s.lockAccounts(bounded)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(bounded, domain.NewID(), "grok.server.recover", struct{ Account, Operation domain.ID }{id, operation}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		if !grokLoginNeedsRecovery(a) || a.Subscription.ServerOperation.ID != operation {
			return nil, subscriptionDenied()
		}
		st := a.Subscription
		o := st.ServerOperation
		o.State = domain.SubscriptionFailed
		if st.Pending.Canceled {
			o.State = domain.SubscriptionCanceled
		}
		o.NativeStarted = false
		o.GrokDiagnostic = &domain.GrokDiagnostic{Phase: domain.GrokDiagnosticLogin, Code: domain.Unavailable, CorrelationID: o.ID}
		if o.State == domain.SubscriptionCanceled {
			o.GrokDiagnostic = nil
		}
		st.Pending = nil
		st.RecoveryRequired = false
		a.Health = domain.AccountDisconnected
		a.Validation, a.Catalog, a.Quota = nil, nil, nil
		a.ConfirmedExhausted = false
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	if err == nil {
		delete(s.subscriptionProgress, operation)
		s.logger.InfoContext(ctx, "grok_login_recovered", "phase", grokLogPhaseLogin, "operation_id", operation, "correlation_id", operation, "claimed", false)
	}
	return err
}
