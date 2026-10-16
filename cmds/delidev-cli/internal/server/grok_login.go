// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
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

// The server-owned Grok browser login. The server implements the OAuth
// authorization-code flow itself (PKCE S256, state, nonce, public client) against
// the fixed xAI profile; no native Grok process takes part. One runner owns one
// operation: it binds a single-use loopback listener, publishes the authorization
// URL only through the in-memory progress map, waits for exactly one callback,
// writes the durable exchange claim and only then sends the one token request.
// The URL, state, nonce, verifier, code and tokens exist only in this runner's
// memory and are never logged, stored in a document or returned in an error.

const (
	// grokLoginPhase is the phase every structured record of this flow carries.
	grokLoginPhase          = "LOGIN"
	grokLoginPollInterval   = 250 * time.Millisecond
	grokLoginSettleTimeout  = 30 * time.Second
	grokLoginClaimTimeout   = 10 * time.Second
	grokCallbackQueryLimit  = 16 << 10
	grokCallbackCodeLimit   = 8192
	grokCallbackServerGrace = 2 * time.Second
)

// grokSubscriptionLoginAdvertised is the single switch for System capability 39
// (GROK_SUBSCRIPTION_LOGIN_V1). It stays false until the opt-in real-account run
// has recorded the token-to-bundle mapping and the loopback-port acceptance.
const grokSubscriptionLoginAdvertised = false

var (
	grokOpaqueValue   = regexp.MustCompile(`^[A-Za-z0-9_.~-]{1,512}$`)
	grokRedirectValue = regexp.MustCompile(`^http://127\.0\.0\.1:([1-9][0-9]{3,4})/callback$`)
)

// grokAuthorizationTarget validates the authorization URL the flow publishes and
// returns the loopback port and the state it carries. It is the shape the
// desktop receiver pins: https://auth.x.ai/oauth2/authorize with the fixed client
// and only the observed query keys.
func grokAuthorizationTarget(raw string) (port int, state string, ok bool) {
	if len(raw) > 4096 {
		return 0, "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "auth.x.ai" || u.Path != subscription.GrokAuthorizationPath || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return 0, "", false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return 0, "", false
	}
	allowed := map[string]bool{"response_type": true, "client_id": true, "redirect_uri": true, "scope": true, "code_challenge": true, "code_challenge_method": true, "state": true, "nonce": true, "referrer": true}
	for key, values := range q {
		if !allowed[key] || len(values) != 1 {
			return 0, "", false
		}
	}
	match := grokRedirectValue.FindStringSubmatch(q.Get("redirect_uri"))
	if match == nil || q.Get("response_type") != "code" || q.Get("client_id") != subscription.GrokClientID || q.Get("code_challenge_method") != "S256" ||
		len(q.Get("code_challenge")) != 43 || !grokOpaqueValue.MatchString(q.Get("state")) || !grokOpaqueValue.MatchString(q.Get("nonce")) || q.Get("referrer") != subscription.GrokReferrer {
		return 0, "", false
	}
	port, err = strconv.Atoi(match[1])
	if err != nil || port < 1024 || port > 65535 {
		return 0, "", false
	}
	return port, q.Get("state"), true
}

// grokLoginClient returns the outbound client of the flow and a function that
// releases its idle connections.
func (s *Service) grokLoginClient() (*http.Client, func()) {
	if s.grokLoginTransport != nil {
		return &http.Client{Transport: s.grokLoginTransport, Timeout: 20 * time.Second}, func() {}
	}
	base := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 20 * time.Second}).DialContext, TLSHandshakeTimeout: 20 * time.Second, ResponseHeaderTimeout: 20 * time.Second, MaxResponseHeaderBytes: 32 << 10, DisableKeepAlives: true, DisableCompression: true}
	return &http.Client{Transport: &outbound.Transport{Base: base, Resolve: s.outboundResolver()}, Timeout: 20 * time.Second}, base.CloseIdleConnections
}

func (s *Service) registerGrokLogin(id domain.ID, cancel context.CancelFunc) {
	s.grokLoginsMu.Lock()
	defer s.grokLoginsMu.Unlock()
	if s.grokLogins == nil {
		s.grokLogins = map[domain.ID]context.CancelFunc{}
	}
	s.grokLogins[id] = cancel
}

func (s *Service) unregisterGrokLogin(id domain.ID) {
	s.grokLoginsMu.Lock()
	defer s.grokLoginsMu.Unlock()
	delete(s.grokLogins, id)
}

// nudgeGrokLogin stops the running login of an operation promptly after its
// cancellation committed. The runner then settles from the stored state.
func (s *Service) nudgeGrokLogin(id domain.ID) {
	s.grokLoginsMu.Lock()
	cancel := s.grokLogins[id]
	s.grokLoginsMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// grokCallbackResult is the single accepted callback.
type grokCallbackResult struct {
	code   string
	reason subscription.GrokOAuthReason
}

// grokCallbackServer is the single-use loopback listener of one login.
type grokCallbackServer struct {
	listener net.Listener
	server   *http.Server
	host     string
	port     int
	state    string
	results  chan grokCallbackResult
	consumed atomic.Bool
	served   chan struct{}
	once     sync.Once
}

func (s *Service) startGrokCallbackServer(state string) (*grokCallbackServer, error) {
	listen := s.grokListen
	if listen == nil {
		listen = func() (net.Listener, error) { return net.Listen("tcp4", "127.0.0.1:0") }
	}
	listener, err := listen()
	if err != nil {
		return nil, err
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !address.IP.Equal(net.IPv4(127, 0, 0, 1)) {
		_ = listener.Close()
		return nil, errors.New("callback listener is not bound to the IPv4 loopback address")
	}
	c := &grokCallbackServer{listener: listener, host: net.JoinHostPort("127.0.0.1", strconv.Itoa(address.Port)), port: address.Port, state: state, results: make(chan grokCallbackResult, 1), served: make(chan struct{})}
	c.server = &http.Server{Handler: c, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: time.Second, MaxHeaderBytes: 16 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	go func() {
		defer close(c.served)
		_ = c.server.Serve(listener)
	}()
	return c, nil
}

// close stops the listener and waits for the serving goroutine. It is safe to
// call on every exit path and more than once.
func (c *grokCallbackServer) close() {
	c.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), grokCallbackServerGrace)
		defer cancel()
		if c.server.Shutdown(ctx) != nil {
			_ = c.server.Close()
		}
		_ = c.listener.Close()
		<-c.served
	})
}

func grokLoopbackPeer(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.Equal(net.IPv4(127, 0, 0, 1))
}

func grokCallbackPage(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, text)
}

// ServeHTTP accepts exactly one callback. A request outside the exact path,
// host or peer is rejected and ignored: it neither consumes the single slot nor
// reaches the exchange. The first request that passes those checks is the one
// callback, whatever it carries; a second one is refused without any action.
func (c *grokCallbackServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != subscription.GrokCallbackPath {
		grokCallbackPage(w, http.StatusNotFound, "Not found.")
		return
	}
	if r.Host != c.host || !grokLoopbackPeer(r.RemoteAddr) {
		grokCallbackPage(w, http.StatusForbidden, "Sign-in could not be completed.")
		return
	}
	if r.Method != http.MethodGet || len(r.URL.RawQuery) > grokCallbackQueryLimit {
		grokCallbackPage(w, http.StatusMethodNotAllowed, "Sign-in could not be completed.")
		return
	}
	if !c.consumed.CompareAndSwap(false, true) {
		grokCallbackPage(w, http.StatusConflict, "This sign-in link was already used.")
		return
	}
	result := c.evaluate(r.URL.RawQuery)
	c.results <- result
	if result.code == "" {
		grokCallbackPage(w, http.StatusBadRequest, "Sign-in could not be completed. Return to DeliDev and try again.")
		return
	}
	grokCallbackPage(w, http.StatusOK, "Sign-in received. You can close this window and return to DeliDev.")
}

func (c *grokCallbackServer) evaluate(rawQuery string) grokCallbackResult {
	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		return grokCallbackResult{reason: subscription.GrokReasonCallback}
	}
	for _, key := range []string{"state", "code", "error"} {
		if len(q[key]) > 1 {
			return grokCallbackResult{reason: subscription.GrokReasonCallback}
		}
	}
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(c.state)) != 1 {
		return grokCallbackResult{reason: subscription.GrokReasonCallback}
	}
	if q.Has("error") {
		if q.Get("error") == "access_denied" {
			return grokCallbackResult{reason: subscription.GrokReasonDenied}
		}
		return grokCallbackResult{reason: subscription.GrokReasonCallback}
	}
	code := q.Get("code")
	if code == "" || len(code) > grokCallbackCodeLimit || strings.IndexFunc(code, unicode.IsControl) >= 0 {
		return grokCallbackResult{reason: subscription.GrokReasonCallback}
	}
	return grokCallbackResult{code: code}
}

// grokLoginOutcome is what one runner decided before settlement.
type grokLoginOutcome struct {
	state  domain.SubscriptionLoginState
	reason subscription.GrokOAuthReason
	code   domain.Code
	bundle subscription.GrokBundle
}

func grokReasonCode(reason subscription.GrokOAuthReason) domain.Code {
	switch reason {
	case subscription.GrokReasonDiscovery, subscription.GrokReasonJWKS, subscription.GrokReasonUserinfo, subscription.GrokReasonRandom:
		return domain.Unavailable
	case subscription.GrokReasonDenied:
		return domain.PermissionDenied
	case subscription.GrokReasonCallback:
		return domain.InvalidArgument
	default:
		return domain.Unauthenticated
	}
}

// runGrokLogin owns one server-owned Grok browser login from claim to settlement.
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
	_, err = s.Store.Mutate(parent, domain.NewID(), "grok.login.claim", struct{ Account, Operation domain.ID }{id, original.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != original.ID || st.ServerOperation.State != domain.SubscriptionPreparing || st.ServerOperation.NativeStarted || st.Pending == nil || st.Pending.ID != original.ID || st.Pending.Phase != domain.SubscriptionQueued || st.Lease != nil || st.RecoveryRequired || st.ServerOperation.Epoch != s.subscriptionServerEpoch() {
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
	s.logger.InfoContext(parent, "grok_login_started", "phase", grokLoginPhase, "account_id", id, "operation_id", original.ID, "correlation_id", original.ID)
	ctx, cancel := context.WithDeadline(parent, original.ExpiresAt)
	defer cancel()
	s.registerGrokLogin(original.ID, cancel)
	defer s.unregisterGrokLogin(original.ID)
	var cancelSeen atomic.Bool
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		ticker := time.NewTicker(grokLoginPollInterval)
		defer ticker.Stop()
		for {
			err := s.Store.Read(ctx, func(tx *store.Tx) error {
				_, a, err := grokSubscriptionAccount(tx, id, 0)
				if err != nil {
					return err
				}
				st := a.Subscription
				if st == nil || st.RecoveryRequired || st.Pending == nil || st.Pending.ID != original.ID || st.Pending.Canceled || subscriptionActorValid(tx, original.Actor) != nil {
					return subscriptionDenied()
				}
				return nil
			})
			if err != nil {
				if ctx.Err() == nil {
					cancelSeen.Store(true)
				}
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
	out := s.performGrokLogin(ctx, id, original, &cancelSeen)
	cancel()
	<-watched
	// Deadline and cancellation are classified after the exchange stopped.
	if out.state == domain.SubscriptionFailed && out.code == "" {
		out.code = grokReasonCode(out.reason)
	}
	bounded, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), grokLoginSettleTimeout)
	defer stop()
	if err := s.finishGrokLogin(bounded, id, original, &out, parent.Err() != nil, cancelSeen.Load()); err != nil {
		if e := s.markGrokLoginRecovery(id, original.ID); e != nil {
			s.logger.Warn("grok_login_recovery_unconfirmed", "phase", grokLoginPhase, "operation_id", original.ID, "correlation_id", original.ID, "code", domain.SafeError(e).Code)
		}
		s.logger.Warn("grok_login_finish_failed", "phase", grokLoginPhase, "operation_id", original.ID, "correlation_id", original.ID, "code", domain.SafeError(err).Code)
	}
	out.bundle.Clear()
}

// performGrokLogin runs the browser flow and returns the decided outcome. The
// listener and every secret value are released before it returns.
func (s *Service) performGrokLogin(ctx context.Context, id domain.ID, original domain.ServerSubscriptionOperation, cancelSeen *atomic.Bool) grokLoginOutcome {
	profile := subscription.DefaultGrokOAuthProfile()
	client, release := s.grokLoginClient()
	defer release()
	claimed := false
	// interrupted classifies a stop of the flow: before the exchange claim it is
	// a cancellation or an expiry; after it the token outcome is unknown.
	interrupted := func() grokLoginOutcome {
		switch {
		case claimed:
			return grokLoginOutcome{state: domain.SubscriptionRecovery, reason: subscription.GrokReasonTokenUncertain}
		case !time.Now().Before(original.ExpiresAt) && !cancelSeen.Load():
			return grokLoginOutcome{state: domain.SubscriptionExpired}
		default:
			return grokLoginOutcome{state: domain.SubscriptionCanceled}
		}
	}
	failed := func(err error) grokLoginOutcome {
		if ctx.Err() != nil {
			return interrupted()
		}
		reason, uncertain := subscription.GrokOAuthReasonOf(err)
		if uncertain {
			return grokLoginOutcome{state: domain.SubscriptionRecovery, reason: reason}
		}
		if reason == "" {
			return grokLoginOutcome{state: domain.SubscriptionFailed, reason: subscription.GrokReasonCallback, code: domain.SafeError(err).Code}
		}
		return grokLoginOutcome{state: domain.SubscriptionFailed, reason: reason}
	}
	endpoints, err := subscription.DiscoverGrokEndpoints(ctx, client, profile)
	if err != nil {
		return failed(err)
	}
	authorization, err := subscription.NewGrokAuthorization(nil)
	if err != nil {
		return failed(err)
	}
	defer authorization.Clear()
	callbacks, err := s.startGrokCallbackServer(authorization.State)
	if err != nil {
		s.logger.WarnContext(ctx, "grok_login_listener_unavailable", "phase", grokLoginPhase, "operation_id", original.ID, "correlation_id", original.ID)
		return grokLoginOutcome{state: domain.SubscriptionFailed, reason: subscription.GrokReasonCallback, code: domain.Unavailable}
	}
	// The listener is released on every exit path before the outcome settles.
	defer func() {
		callbacks.close()
		s.logger.InfoContext(ctx, "grok_login_listener_closed", "phase", grokLoginPhase, "operation_id", original.ID, "correlation_id", original.ID)
	}()
	redirect := subscription.GrokRedirectURI(callbacks.port)
	if err := s.publishGrokLoginProgress(ctx, id, original, profile.AuthorizationURL(endpoints, redirect, authorization)); err != nil {
		return failed(err)
	}
	s.logger.InfoContext(ctx, "grok_login_waiting", "phase", grokLoginPhase, "operation_id", original.ID, "correlation_id", original.ID, "port", callbacks.port)
	var callback grokCallbackResult
	select {
	case callback = <-callbacks.results:
	case <-ctx.Done():
		return interrupted()
	}
	if callback.code == "" {
		return failed(&subscription.GrokOAuthError{Reason: callback.reason})
	}
	// The keys are read before the claim so a keys failure cannot burn the code.
	keys, err := subscription.FetchGrokJWKS(ctx, client, endpoints)
	if err != nil {
		return failed(err)
	}
	if err := s.claimGrokExchange(ctx, id, original); err != nil {
		s.logger.WarnContext(ctx, "grok_login_claim_refused", "phase", grokLoginPhase, "operation_id", original.ID, "correlation_id", original.ID, "code", domain.SafeError(err).Code)
		if ctx.Err() != nil {
			return interrupted()
		}
		return grokLoginOutcome{state: domain.SubscriptionFailed, reason: subscription.GrokReasonCallback, code: domain.Conflict}
	}
	claimed = true
	s.logger.InfoContext(ctx, "grok_login_exchange_claimed", "phase", grokLoginPhase, "operation_id", original.ID, "correlation_id", original.ID)
	tokens, err := subscription.ExchangeGrokCode(ctx, client, profile, endpoints, callback.code, authorization.Verifier(), redirect)
	callback.code = ""
	defer tokens.Clear()
	if err != nil {
		// The request was sent: an interrupted exchange is uncertain.
		return failed(err)
	}
	now := time.Now()
	claims, err := subscription.VerifyGrokIDToken(tokens.IDToken, keys, profile, authorization.Nonce, now)
	if err != nil {
		return failed(err)
	}
	info, err := subscription.FetchGrokUserinfo(ctx, client, endpoints, tokens.AccessToken, claims)
	if err != nil {
		return failed(err)
	}
	bundle, err := subscription.BuildGrokBundle(tokens, claims, info, now)
	if err != nil {
		return failed(err)
	}
	return grokLoginOutcome{state: domain.SubscriptionSucceeded, bundle: bundle}
}

// publishGrokLoginProgress moves the operation to WAITING and exposes the
// authorization URL only through the in-memory progress map.
func (s *Service) publishGrokLoginProgress(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation, authorization string) error {
	if _, _, ok := grokAuthorizationTarget(authorization); !ok {
		s.logger.WarnContext(ctx, "grok_login_url_rejected", "phase", grokLoginPhase, "operation_id", o.ID, "correlation_id", o.ID, "code", domain.RecoveryRequired)
		return subscriptionDenied()
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(ctx, domain.NewID(), "grok.login.waiting", struct{ Account, Operation domain.ID }{id, o.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != o.ID || st.Pending == nil || st.Pending.Canceled || st.RecoveryRequired || subscriptionActorValid(tx, o.Actor) != nil {
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
		s.subscriptionProgress[o.ID] = subscriptionProgress{URL: authorization, Until: o.ExpiresAt}
	}
	return err
}

// claimGrokExchange writes the durable exchange claim. The token request may be
// sent only after this commit returned; it is never written again or cleared.
func (s *Service) claimGrokExchange(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation) error {
	bounded, cancel := context.WithTimeout(ctx, grokLoginClaimTimeout)
	defer cancel()
	unlock, err := s.lockAccounts(bounded)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(bounded, domain.NewID(), "grok.login.exchange-claim", struct{ Account, Operation domain.ID }{id, o.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		cur := (*domain.ServerSubscriptionOperation)(nil)
		if st != nil {
			cur = st.ServerOperation
		}
		if cur == nil || cur.ID != o.ID || cur.Epoch != o.Epoch || cur.State != domain.SubscriptionWaiting || !cur.NativeStarted || cur.ExchangeClaimed || !time.Now().Before(cur.ExpiresAt) ||
			st.RecoveryRequired || st.Pending == nil || st.Pending.ID != o.ID || st.Pending.Canceled || subscriptionActorValid(tx, cur.Actor) != nil {
			return nil, subscriptionDenied()
		}
		cur.ExchangeClaimed = true
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	return err
}

// finishGrokLogin settles the operation in the Finish-equivalent server
// transaction: it stages the sealed bundle under the protected vault, then
// publishes the generation and identity commitment, or records the terminal
// state. A cancellation, a revoked initiator or a recovery fence that appeared
// after the exchange claim always becomes recovery-required and never publishes.
func (s *Service) finishGrokLogin(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation, out *grokLoginOutcome, shuttingDown, canceledSeen bool) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	defer delete(s.subscriptionProgress, o.ID)
	success := out.state == domain.SubscriptionSucceeded
	commitment := ""
	if success {
		if commitment = s.grokIdentityCommitment(out.bundle); commitment == "" {
			success, out.state, out.reason, out.code = false, domain.SubscriptionFailed, subscription.GrokReasonMapping, domain.Unauthenticated
		}
	}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, e := grokSubscriptionAccount(tx, id, 0)
		if e != nil {
			return e
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != o.ID || st.ServerOperation.Epoch != o.Epoch || !st.ServerOperation.NativeStarted || st.RecoveryRequired || st.Pending == nil || st.Pending.ID != o.ID {
			return subscriptionDenied()
		}
		if success {
			if st.IdentityCommitment != "" && commitment != st.IdentityCommitment {
				success, out.state, out.reason, out.code = false, domain.SubscriptionFailed, subscription.GrokReasonMapping, domain.PermissionDenied
				return nil
			}
			if e := uniqueSubscriptionIdentity(tx, id, commitment); e != nil {
				success, out.state, out.reason, out.code = false, domain.SubscriptionFailed, subscription.GrokReasonMapping, domain.SafeError(e).Code
				s.logger.WarnContext(ctx, "grok_login_identity_duplicate", "phase", grokLoginPhase, "operation_id", o.ID, "correlation_id", o.ID, "code", out.code)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	var vault accountSecrets
	staged := false
	if success {
		vault, err = s.secrets()
		if err != nil {
			return err
		}
		sealed, e := subscription.SealGrokBundle(s.Identity.ServerID, id, o.FinishID, out.bundle)
		if e != nil {
			return e
		}
		// Set before the write: a write that reports an error may still have left
		// the staged generation behind.
		staged = true
		_, e = vault.Put(ctx, credentials.Ref{Owner: id, ID: o.FinishID, Purpose: credentials.AccountLogin}, sealed)
		clear(sealed)
		if e != nil {
			s.retireUnpublishedGrokGeneration(ctx, vault, id, o.ID, o.FinishID)
			return e
		}
	}
	final := out.state
	_, err = s.Store.Mutate(ctx, o.FinishID, "grok.login.finish", struct {
		Account, Operation domain.ID
		State              domain.SubscriptionLoginState
	}{id, o.ID, out.state}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != o.ID || st.ServerOperation.Epoch != o.Epoch || !st.ServerOperation.NativeStarted || st.RecoveryRequired || st.Pending == nil || st.Pending.ID != o.ID {
			return nil, subscriptionDenied()
		}
		op := st.ServerOperation
		claimed := op.ExchangeClaimed
		stopped := st.Pending.Canceled || subscriptionActorValid(tx, op.Actor) != nil
		state := out.state
		publish := success && !stopped
		switch {
		case claimed && (state == domain.SubscriptionCanceled || state == domain.SubscriptionExpired || stopped && state != domain.SubscriptionRecovery):
			// After the claim the provider may hold a credential this server never
			// stored: cancellation or loss of the initiator is recovery.
			state = domain.SubscriptionRecovery
		case !claimed && stopped && state != domain.SubscriptionSucceeded:
			state = domain.SubscriptionCanceled
		case !claimed && state == domain.SubscriptionSucceeded:
			state = domain.SubscriptionRecovery
		}
		if state == domain.SubscriptionSucceeded && !publish {
			state = domain.SubscriptionRecovery
		}
		final = state
		diagnostic := (*domain.GrokDiagnostic)(nil)
		switch state {
		case domain.SubscriptionRecovery:
			diagnostic = grokLocalDiagnostic("", domain.GrokLogin, domain.RecoveryRequired)
		case domain.SubscriptionFailed:
			code := out.code
			if code == "" {
				code = grokReasonCode(out.reason)
			}
			diagnostic = grokLocalDiagnostic("", domain.GrokLogin, code)
		}
		if diagnostic != nil {
			diagnostic.CorrelationID = string(op.ID)
		}
		if state == domain.SubscriptionRecovery {
			st.RecoveryRequired = true
			a.Health = domain.AccountFailed
			op.State = domain.SubscriptionRecovery
			op.GrokDiagnostic = diagnostic
		} else {
			op.NativeStarted = false
			op.State = state
			op.GrokDiagnostic = diagnostic
			st.Pending = nil
			if state == domain.SubscriptionSucceeded {
				op.Generation = o.FinishID
				st.Generation = o.FinishID
				st.IdentityCommitment = commitment
				st.OwnerMachineID = ""
				st.AutomaticCreditConsent = nil
				if a.Connection == nil {
					a.Connection = &domain.AccountConnection{ID: o.FinishID, Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()}
				}
				a.Health = domain.AccountReady
			}
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: id}, err
	})
	if err != nil {
		if staged {
			s.retireUnpublishedGrokGeneration(ctx, vault, id, o.ID, o.FinishID)
		}
		return err
	}
	out.state = final
	if staged && final != domain.SubscriptionSucceeded {
		s.retireUnpublishedGrokGeneration(ctx, vault, id, o.ID, o.FinishID)
	}
	if final == domain.SubscriptionSucceeded {
		if err := cleanupSubscriptionReferences(ctx, vault, id, o.FinishID); err != nil {
			s.logger.WarnContext(ctx, "grok_login_cleanup_pending", "phase", grokLoginPhase, "operation_id", o.ID, "correlation_id", o.ID, "code", domain.SafeError(err).Code)
			return err
		}
	}
	s.logger.InfoContext(ctx, "grok_login_finished", "phase", grokLoginPhase, "account_id", id, "operation_id", o.ID, "correlation_id", o.ID, "state", final, "reason", out.reason, "shutting_down", shuttingDown, "cancel_observed", canceledSeen)
	return nil
}

// markGrokLoginRecovery fences the account when settlement itself failed. It
// never clears the exchange claim and never resends a code.
func (s *Service) markGrokLoginRecovery(id, operation domain.ID) error {
	ctx, cancel := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 5*time.Second)
	defer cancel()
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	delete(s.subscriptionProgress, operation)
	_, err = s.Store.Mutate(ctx, domain.NewID(), "grok.login.recovery", struct{ Account, Operation domain.ID }{id, operation}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != operation {
			return nil, subscriptionDenied()
		}
		o := st.ServerOperation
		o.State = domain.SubscriptionRecovery
		if o.GrokDiagnostic == nil {
			d := grokLocalDiagnostic("", domain.GrokLogin, domain.RecoveryRequired)
			d.CorrelationID = string(o.ID)
			o.GrokDiagnostic = d
		}
		st.RecoveryRequired = true
		a.Health = domain.AccountFailed
		if st.Pending == nil {
			st.Pending = operationPlaceholder(o)
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	return err
}

// serverLoginAccount resolves the account of a server-owned login request: a
// Grok account uses the Grok profile and every other service keeps the Codex
// profile.
func serverLoginAccount(tx *store.Tx, id domain.ID, revision uint64) (store.Record, domain.Account, error) {
	r, a, err := accountFromTx(tx, id, revision)
	if err == nil && a.Type == domain.SubscriptionAccount && a.SubscriptionService == domain.SubscriptionGrok {
		return grokSubscriptionAccount(tx, id, revision)
	}
	if err != nil {
		return r, a, err
	}
	return subscriptionAccount(tx, id, revision)
}

// grokLoginCandidate selects a Grok account whose server-owned browser login was
// accepted and not yet claimed by a runner.
func grokLoginCandidate(a domain.Account) bool {
	st := a.Subscription
	if a.Type != domain.SubscriptionAccount || a.SubscriptionService != domain.SubscriptionGrok || st == nil || st.RecoveryRequired || st.Lease != nil || st.ServerOperation == nil || st.Pending == nil {
		return false
	}
	o := st.ServerOperation
	return o.Action == domain.SubscriptionLogin && o.State == domain.SubscriptionPreparing && !o.NativeStarted && st.Pending.ID == o.ID && st.Pending.Phase == domain.SubscriptionQueued && st.Pending.MachineID == ""
}

// requestGrokSubscription admits the Grok browser login: LOGIN with
// device_code false and no machine. Device code, refresh and logout have no
// server-owned Grok path yet and stay unsupported.
func (s *Service) requestGrokSubscription(ctx context.Context, req *connect.Request[pb.RequestSubscriptionRequest]) (*connect.Response[pb.RequestSubscriptionResponse], error) {
	if req.Msg.MachineId != "" || req.Msg.DeviceCode || subscriptionAction(req.Msg.Action) != domain.SubscriptionLogin {
		return nil, rpc.Error(domain.Fail(domain.Unsupported, "This Grok operation is not available.", "Start the Grok browser login without a Runner Device; device code, refresh and logout are not available."), req.Header().Get(rpc.CorrelationHeader))
	}
	response, err := s.requestServerSubscription(ctx, req)
	if err == nil {
		s.logger.InfoContext(ctx, "grok_login_accepted", "phase", grokLoginPhase, "account_id", req.Msg.Mutation.Id, "operation_id", req.Msg.Mutation.RequestId, "correlation_id", req.Msg.Mutation.RequestId, "replayed", response.Msg.Replayed)
	}
	return response, err
}

// cancelGrokSubscription cancels the account's pending Grok login. A queued
// login settles as canceled at once; a running login is flagged and stopped
// promptly, and its runner closes the listener and settles from stored state.
func (s *Service) cancelGrokSubscription(ctx context.Context, req *connect.Request[pb.CancelSubscriptionRequest]) (*connect.Response[pb.CancelSubscriptionResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	var operation domain.ID
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "grok.subscription.cancel", disconnectAccountInput{domain.ID(m.Id), m.ExpectedRevision}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, domain.ID(m.Id), m.ExpectedRevision)
		if err != nil {
			return nil, err
		}
		if a.Subscription != nil && a.Subscription.Pending != nil {
			operation = a.Subscription.Pending.ID
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
	if operation != "" {
		s.nudgeGrokLogin(operation)
		s.logger.InfoContext(ctx, "grok_login_cancel_accepted", "phase", grokLoginPhase, "account_id", m.Id, "operation_id", operation, "correlation_id", operation, "replayed", result.Replayed)
	}
	r, err := s.accountRecord(ctx, domain.ID(m.Id))
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	return connect.NewResponse(&pb.CancelSubscriptionResponse{Account: rpc.Resource(r), Replayed: result.Replayed}), nil
}
