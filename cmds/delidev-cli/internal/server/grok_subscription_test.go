// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/golang-jwt/jwt/v4"
)

type grokOAuthFixture struct {
	*subscriptionFixture
	key           *ecdsa.PrivateKey
	nonce         atomic.Value
	user          string
	exchanges     atomic.Int32
	uncertain     bool
	block         chan struct{}
	started       chan struct{}
	devicePending bool
}

func newGrokOAuthFixture(t *testing.T) *grokOAuthFixture {
	t.Helper()
	f := &grokOAuthFixture{subscriptionFixture: newSubscriptionFixture(t), user: "grok-fixture-user-123456", started: make(chan struct{}, 1)}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f.key = key
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.service", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.SubscriptionService = domain.SubscriptionGrok
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	f.service.grokOAuthTransport = oauthHTTPTransport(f.roundTrip)
	f.service.grokSubscriptionAccepted = true
	return f
}

func grokJSONResponse(status int, value any) *http.Response {
	raw, _ := json.Marshal(value)
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw))}
}

func (f *grokOAuthFixture) roundTrip(r *http.Request) (*http.Response, error) {
	switch r.URL.String() {
	case grokDiscovery:
		return grokJSONResponse(200, map[string]any{"issuer": subscription.GrokIssuer, "authorization_endpoint": grokAuthorization, "token_endpoint": grokTokenEndpoint, "device_authorization_endpoint": grokDeviceEndpoint, "userinfo_endpoint": grokUserInfo, "jwks_uri": grokJWKS, "id_token_signing_alg_values_supported": []string{"ES256"}, "code_challenge_methods_supported": []string{"S256"}}), nil
	case grokJWKS:
		return grokJSONResponse(200, map[string]any{"keys": []any{map[string]any{"kty": "EC", "crv": "P-256", "use": "sig", "alg": "ES256", "kid": "grok-fixture", "x": base64.RawURLEncoding.EncodeToString(f.key.X.FillBytes(make([]byte, 32))), "y": base64.RawURLEncoding.EncodeToString(f.key.Y.FillBytes(make([]byte, 32)))}}}), nil
	case grokDeviceEndpoint:
		_, a := f.record()
		if a.Subscription.ServerOperation.GrokOAuth == nil || a.Subscription.ServerOperation.GrokOAuth.Phase != domain.GrokOAuthDeviceSending {
			f.t.Error("device grant preceded its durable claim")
		}
		return grokJSONResponse(200, map[string]any{"device_code": "grok-fixture-protected-device", "user_code": "FIXTURE-123", "verification_uri": grokVerificationURI, "expires_in": 1800, "interval": 1}), nil
	case grokTokenEndpoint:
		f.exchanges.Add(1)
		_, a := f.record()
		phase := a.Subscription.ServerOperation.GrokOAuth.Phase
		if phase != domain.GrokOAuthExchangeSending && phase != domain.GrokOAuthRefreshSending && phase != domain.GrokOAuthPollSending {
			f.t.Error("token send preceded its original durable claim")
		}
		select {
		case f.started <- struct{}{}:
		default:
		}
		if f.block != nil {
			select {
			case <-f.block:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		if f.uncertain {
			return grokJSONResponse(502, map[string]string{"error": "fixture-secret-response"}), nil
		}
		payload, _ := io.ReadAll(r.Body)
		values, _ := url.ParseQuery(string(payload))
		clear(payload)
		if values.Get("client_id") != subscription.GrokClientID || r.GetBody != nil {
			f.t.Error("token transport acquired foreign or replay authority")
		}
		if values.Get("grant_type") == "urn:ietf:params:oauth:grant-type:device_code" && f.devicePending {
			switch f.exchanges.Load() {
			case 1:
				return grokJSONResponse(400, map[string]string{"error": "authorization_pending"}), nil
			case 2:
				return grokJSONResponse(400, map[string]string{"error": "slow_down"}), nil
			}
		}
		nonce, _ := f.nonce.Load().(string)
		if values.Get("grant_type") == "refresh_token" {
			nonce = ""
		}
		claims := jwt.MapClaims{"iss": subscription.GrokIssuer, "aud": subscription.GrokClientID, "sub": f.user, "iat": time.Now().Add(-time.Minute).Unix(), "exp": time.Now().Add(time.Hour).Unix(), "nonce": nonce, "email": "grok-fixture@example.invalid"}
		token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
		token.Header["kid"] = "grok-fixture"
		id, err := token.SignedString(f.key)
		if err != nil {
			return nil, err
		}
		rotation := strconv.Itoa(int(f.exchanges.Load()))
		return grokJSONResponse(200, map[string]any{"access_token": "grok-fixture-access-token-" + rotation, "refresh_token": "grok-fixture-refresh-token-" + rotation, "id_token": id, "token_type": "Bearer", "scope": grokScopes, "expires_in": 3600 + int(f.exchanges.Load())}), nil
	default:
		f.t.Error("foreign OAuth endpoint")
		return grokJSONResponse(404, struct{}{}), nil
	}
}

func (f *grokOAuthFixture) run() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.service.runServerSubscription(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), f.input.AccountID)
	}()
	return done
}

func (f *grokOAuthFixture) waiting(operation string) *pb.GetSubscriptionProgressResponse {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p := f.progressFor(operation)
		if p.State == pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING {
			u, _ := url.Parse(p.Url)
			f.nonce.Store(u.Query().Get("nonce"))
			return p
		}
		if p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_PREPARING {
			f.t.Fatalf("unexpected login state: %s", p.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.t.Fatal("Grok login did not wait")
	return nil
}

func (f *grokOAuthFixture) callback(operation, state string) error {
	_, err := f.client.ForwardSubscriptionCallback(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.ForwardSubscriptionCallbackRequest{AccountId: string(f.input.AccountID), OperationId: operation, CallbackQuery: []byte(url.Values{"code": {"grok-fixture-authorization-code"}, "state": {state}}.Encode())}))
	return err
}

func TestGrokServerBrowserOriginalCallbackAndProtectedSuccess(t *testing.T) {
	f := newGrokOAuthFixture(t)
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	done := f.run()
	p := f.waiting(op.OperationId)
	u, _ := url.Parse(p.Url)
	state := u.Query().Get("state")
	if err := f.callback(op.OperationId, "foreign-original-state-1234"); err == nil {
		t.Fatal("foreign callback accepted")
	}
	if f.exchanges.Load() != 0 {
		t.Fatal("invalid callback sent an exchange")
	}
	if _, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN); err == nil {
		t.Fatal("Worker consumed server-owned Grok login")
	}
	if err := f.callback(op.OperationId, state); err != nil {
		t.Fatal(err)
	}
	awaitServerFixture(t, done)
	if err := f.callback(op.OperationId, state); err == nil {
		t.Fatal("duplicate callback acquired send authority")
	}
	r, a := f.record()
	p = f.progressFor(op.OperationId)
	if f.exchanges.Load() != 1 || a.Subscription.Generation == "" || a.Subscription.Pending != nil || a.Subscription.RecoveryRequired || a.Subscription.ServerOperation.NativeStarted || p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED || p.Diagnostic != nil || p.GrokDiagnostic != nil || p.SuggestedName != "grok-fixture@example.invalid" {
		t.Fatal("original Grok success did not settle exclusively")
	}
	for _, secret := range []string{state, f.nonce.Load().(string), p.SuggestedName, "grok-fixture-access-token", "grok-fixture-refresh-token", "grok-fixture-authorization-code", "auth.x.ai"} {
		if bytes.Contains(r.Data, []byte(secret)) {
			t.Fatal("secret or presentation escaped into account metadata")
		}
	}
	raw, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: r.ID, ID: a.Subscription.Generation, Purpose: credentials.AccountLogin})
	if err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	_, identity, err := subscription.ParseGrok(raw)
	if err != nil || identity.User != f.user {
		t.Fatal("protected native bundle lost verified identity")
	}
	refs, _ := f.secrets.UnremovedReferences(context.Background(), r.ID)
	if len(refs) != 1 || refs[0].ID != a.Subscription.Generation {
		t.Fatal("successful login retained transient protected material")
	}
	logout := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	awaitServerFixture(t, f.run())
	_, a = f.record()
	refs, _ = f.secrets.UnremovedReferences(context.Background(), r.ID)
	if a.Connection != nil || a.Subscription.Generation != "" || len(refs) != 0 || f.progressFor(logout.OperationId).State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED || f.exchanges.Load() != 1 {
		t.Fatal("logout failed joined protected cleanup or sent OAuth")
	}
}

func TestGrokServerUncertainExchangeRetainsOriginalWithoutResend(t *testing.T) {
	f := newGrokOAuthFixture(t)
	f.uncertain = true
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	done := f.run()
	p := f.waiting(op.OperationId)
	u, _ := url.Parse(p.Url)
	if err := f.callback(op.OperationId, u.Query().Get("state")); err != nil {
		t.Fatal(err)
	}
	awaitServerFixture(t, done)
	_, a := f.record()
	p = f.progressFor(op.OperationId)
	if a.Subscription.Pending == nil || !a.Subscription.RecoveryRequired || !a.Subscription.ServerOperation.NativeStarted || a.Subscription.ServerOperation.GrokOAuth.Phase != domain.GrokOAuthExchangeSending || p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED || p.Url != "" || p.UserCode != "" || p.Diagnostic != nil || p.GrokDiagnostic == nil {
		t.Fatal("uncertain exchange lost original fenced evidence")
	}
	refs, _ := f.secrets.UnremovedReferences(context.Background(), f.input.AccountID)
	if len(refs) != 1 {
		t.Fatal("uncertain exchange discarded protected material")
	}
	awaitServerFixture(t, f.run())
	if f.exchanges.Load() != 1 {
		t.Fatal("recovery resent an exchange")
	}
	if _, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: func() uint64 { r, _ := f.record(); return r.Revision }()}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN})); err == nil {
		t.Fatal("fresh login bypassed original recovery")
	}
}

func TestGrokServerCancelBeforeExchangePurgesTransientAuthority(t *testing.T) {
	f := newGrokOAuthFixture(t)
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	done := f.run()
	f.waiting(op.OperationId)
	r, _ := f.record()
	_, err := f.client.CancelSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.CancelSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}}))
	if err != nil {
		t.Fatal(err)
	}
	awaitServerFixture(t, done)
	_, a := f.record()
	refs, _ := f.secrets.UnremovedReferences(context.Background(), f.input.AccountID)
	if f.exchanges.Load() != 0 || a.Connection != nil || a.Subscription.Pending != nil || a.Subscription.RecoveryRequired || len(refs) != 0 || f.progressFor(op.OperationId).State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_CANCELED {
		t.Fatal("pre-send cancellation retained authority or sent OAuth")
	}
}

func TestGrokDevicePollClosedRFC8628Responses(t *testing.T) {
	for _, code := range []string{"authorization_pending", "slow_down", "access_denied", "expired_token", "foreign_error"} {
		t.Run(code, func(t *testing.T) {
			calls := 0
			c := grokOAuthHTTP{transport: oauthHTTPTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != grokTokenEndpoint || r.GetBody != nil {
					t.Error("poll acquired foreign replay authority")
				}
				return grokJSONResponse(400, map[string]string{"error": code, "error_description": "fixture-secret-description"}), nil
			})}
			result, state, err := c.poll(context.Background(), []byte("fixture-device-secret"))
			defer result.clear()
			if calls != 1 || len(result.access) != 0 {
				t.Fatal("poll retried or exposed a token")
			}
			if code == "authorization_pending" && (err != nil || state != oauthDevicePending) || code == "slow_down" && (err != nil || state != oauthDeviceSlowDown) || code != "authorization_pending" && code != "slow_down" && err == nil {
				t.Fatal("foreign poll state acquired retry authority")
			}
			if err != nil && strings.Contains(err.Error(), "fixture-secret") {
				t.Fatal("provider content escaped error metadata")
			}
		})
	}
}

func TestGrokServerDeviceWaitsAndHonorsSlowDown(t *testing.T) {
	f := newGrokOAuthFixture(t)
	f.devicePending = true
	r, _ := f.record()
	op, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, DeviceCode: true}))
	if err != nil {
		t.Fatal(err)
	}
	done := f.run()
	p := f.waiting(op.Msg.OperationId)
	if p.Url != grokVerificationURI || p.UserCode != "FIXTURE-123" || f.exchanges.Load() != 0 {
		t.Fatal("device wait lost its pinned presentation or polled immediately")
	}
	start := time.Now()
	awaitServerFixture(t, done)
	if f.exchanges.Load() != 3 || time.Since(start) < 7*time.Second || f.progressFor(op.Msg.OperationId).State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED {
		t.Fatal("device polling ignored confirmed wait or slow-down")
	}
}

func TestGrokServerCancelDuringExchangeRetainsUncertainty(t *testing.T) {
	f := newGrokOAuthFixture(t)
	f.block = make(chan struct{})
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	done := f.run()
	p := f.waiting(op.OperationId)
	u, _ := url.Parse(p.Url)
	if err := f.callback(op.OperationId, u.Query().Get("state")); err != nil {
		t.Fatal(err)
	}
	awaitServerFixture(t, f.started)
	r, _ := f.record()
	_, err := f.client.CancelSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.CancelSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}}))
	if err != nil {
		t.Fatal(err)
	}
	awaitServerFixture(t, done)
	_, a := f.record()
	if f.exchanges.Load() != 1 || a.Connection != nil || a.Subscription.Pending == nil || !a.Subscription.Pending.Canceled || !a.Subscription.RecoveryRequired || f.progressFor(op.OperationId).State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED {
		t.Fatal("cancel after a possible exchange manufactured confirmed absence")
	}
}

func TestGrokServerRefreshRotatesOnlyOriginalIdentity(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "original", true: "foreign"}[changed], func(t *testing.T) {
			f := newGrokOAuthFixture(t)
			op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
			done := f.run()
			p := f.waiting(op.OperationId)
			u, _ := url.Parse(p.Url)
			if err := f.callback(op.OperationId, u.Query().Get("state")); err != nil {
				t.Fatal(err)
			}
			awaitServerFixture(t, done)
			_, before := f.record()
			if changed {
				f.user = "foreign-fixture-user-123456"
			}
			refresh := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
			awaitServerFixture(t, f.run())
			_, after := f.record()
			p = f.progressFor(refresh.OperationId)
			if changed {
				if !after.Subscription.RecoveryRequired || before.Subscription.Generation != after.Subscription.Generation || p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED {
					t.Fatal("foreign refresh replaced original identity")
				}
			} else if after.Subscription.RecoveryRequired || before.Subscription.Generation == after.Subscription.Generation || before.Subscription.IdentityCommitment != after.Subscription.IdentityCommitment || p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED {
				t.Fatal("original refresh did not rotate exclusively")
			}
		})
	}
}
