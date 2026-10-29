// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/core"
	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
)

type closedLiveSocket struct{}

func (closedLiveSocket) Read(context.Context) ([]byte, error) { return nil, io.EOF }
func (closedLiveSocket) Write(context.Context, []byte) error  { return nil }
func (closedLiveSocket) Close() error                         { return nil }

type recordingLiveDialer struct {
	endpoint string
	headers  http.Header
}

func (d *recordingLiveDialer) Dial(_ context.Context, endpoint string, headers http.Header, _ *http.Client) (inference.Socket, error) {
	d.endpoint, d.headers = endpoint, headers.Clone()
	return closedLiveSocket{}, nil
}

func TestLiveOfferAliasJoinsOriginalAccount(t *testing.T) {
	var f *providerAcceptanceFixture
	f = newProviderAcceptance(t, "custom", "gpt-live-1-codex", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/realtime/calls" || r.Header.Get("Authorization") != "Bearer "+f.secret {
			t.Errorf("unexpected offer route or account: %s", r.URL.Path)
		}
		w.Header().Set("Location", "/v1/realtime/calls/call_original")
		w.Header().Set("Content-Type", "application/sdp")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "answer")
	})
	offer := f.call("/v1/realtime/calls", `{"sdp":"offer","session":{"model":"fixture"}}`, nil)
	if offer.Code != http.StatusCreated || !strings.HasPrefix(offer.Header().Get("Location"), "/v1/realtime/calls/") {
		t.Fatalf("offer status=%d location=%s body=%s", offer.Code, offer.Header().Get("Location"), offer.Body.String())
	}
	alias := strings.TrimPrefix(offer.Header().Get("Location"), "/v1/realtime/calls/")
	for _, tc := range []struct{ name, path string }{
		{"realtime-call", "/v1/realtime/calls/" + alias},
		{"realtime-query", "/v1/realtime?call_id=" + alias},
		{"live-call", "/v1/live/" + alias},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := &inferenceAdapter{s: f.s, telemetry: f.srv.telemetry}
			dialer := &recordingLiveDialer{}
			h, err := inference.New(inference.Config{Authorizer: adapter, Router: adapter, Credentials: adapter, Usage: adapter, LiveBindings: adapter, SocketDialer: dialer, Destinations: *f.srv.inferenceDestinations})
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = h.ServeRealtimeSocket(ctx, f.key, u.Path, u.Query(), closedLiveSocket{})
			if dialer.endpoint != "https://provider.example/v1/realtime?call_id=call_original" || dialer.headers.Get("Authorization") != "Bearer "+f.secret {
				t.Fatalf("alias joined wrong call or account: endpoint=%s authorization=%s", dialer.endpoint, dialer.headers.Get("Authorization"))
			}
		})
	}
}

func TestLiveBindingRestoresCompiledPolicy(t *testing.T) {
	for _, fast := range []bool{false, true} {
		t.Run(map[bool]string{false: "custom", true: "fast-policy"}[fast], func(t *testing.T) {
			a, sc, key := dispatchFixture(t)
			d := saveRoutingFixture(t, a, sc, "live-account", "gpt-live-1-codex")
			if fast {
				yes := true
				d.Metadata.Configuration = &v1.ProviderConfiguration{Policy: &v1.ProviderPolicyOverrides{SupportsServiceTier: &yes}}
				putRoutingProvider(t, a, sc, d)
			}
			p := admitRouting(t, a, key)
			ctx := context.Background()
			target, err := targetForContext(ctx, d, "wire-model")
			if err != nil || target.Policy == nil {
				t.Fatalf("normal target: %+v, %v", target, err)
			}
			if fast {
				if err = applyFastTarget(ctx, d, "wire-model", &target); err != nil {
					t.Fatal(err)
				}
			}
			target.ForceMessagesTranslation = true
			target.ReasoningRetention = &inference.ReasoningRetentionPolicy{Enabled: true, MaxTokens: 100}
			selected, err := a.SelectBound(ctx, p, target, d.Metadata.Id)
			if err != nil || selected.CredentialVersion == "" {
				t.Fatalf("direct selection: %+v, %v", selected, err)
			}
			binding := inference.LiveBinding{Target: target, AccountID: d.Metadata.Id, UpstreamCallID: "call_original", ServedCredentialVersion: selected.CredentialVersion, ExpiresAt: time.Now().Add(5 * time.Minute)}
			if err = a.SaveBinding(ctx, p, "alias", binding); err != nil {
				t.Fatal(err)
			}
			for _, adapter := range []*inferenceAdapter{a, {s: a.s}} {
				principal := p
				if adapter != a {
					principal = admitRouting(t, adapter, key)
				}
				got, credential, err := adapter.ResolveBinding(ctx, principal, "alias")
				if err != nil || credential.AccountID != binding.AccountID || credential.CredentialVersion != binding.ServedCredentialVersion || !sameSavedLiveLane(binding.Target, got.Target) || policyIdentity(got.Target) != policyIdentity(binding.Target) || got.UpstreamCallID != binding.UpstreamCallID || got.Target.Policy == nil {
					t.Fatalf("restored binding: %+v, credential=%+v, error=%v", got, credential, err)
				}
			}
		})
	}
}

func TestLiveBindingRestorationRejectsInvalidState(t *testing.T) {
	for _, variant := range []string{"expired", "missing", "other-key", "replaced", "paused", "deleted", "corrupt-endpoint", "corrupt-model", "corrupt-fast-policy"} {
		t.Run(variant, func(t *testing.T) {
			a, sc, key := dispatchFixture(t)
			d := saveRoutingFixture(t, a, sc, "live-account", "gpt-live-1-codex")
			p := admitRouting(t, a, key)
			ctx := context.Background()
			target, err := targetForContext(ctx, d, "wire-model")
			if err != nil {
				t.Fatal(err)
			}
			credential, err := a.SelectBound(ctx, p, target, d.Metadata.Id)
			if err != nil {
				t.Fatal(err)
			}
			binding := inference.LiveBinding{Target: target, AccountID: d.Metadata.Id, UpstreamCallID: "call_original", ServedCredentialVersion: credential.CredentialVersion, ExpiresAt: time.Now().Add(5 * time.Minute)}
			if err = a.SaveBinding(ctx, p, "alias", binding); err != nil {
				t.Fatal(err)
			}
			lookup := p
			lookupKey := key
			switch variant {
			case "expired", "corrupt-endpoint", "corrupt-model", "corrupt-fast-policy":
				if variant == "expired" {
					binding.ExpiresAt = time.Now().Add(-time.Second)
				} else if variant == "corrupt-endpoint" {
					binding.Target.Endpoint = "https://other.example/v1"
				} else if variant == "corrupt-model" {
					binding.Target.Model = "other-model"
				} else {
					binding.Target.FastPolicy = &inference.FastPolicy{Eligible: true}
				}
				raw, marshalErr := json.Marshal(storedLiveBinding{Version: liveBindingEnvelopeVersion, Binding: binding})
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				if err = a.s.PutState(ctx, sc, core.LiveAlias, "live/"+p.KeyID+"/alias", raw); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err = a.s.DeleteState(ctx, sc, core.LiveAlias, "live/"+p.KeyID+"/alias"); err != nil {
					t.Fatal(err)
				}
			case "other-key":
				_, raw, issueErr := a.s.IssueKey(ctx, sc, "other", nil)
				if issueErr != nil {
					t.Fatal(issueErr)
				}
				other := &inferenceAdapter{s: a.s, telemetry: a.telemetry}
				lookup = admitRouting(t, other, raw)
				lookupKey = raw
				a = other
			case "replaced":
				d.Secret = "replacement"
				putRoutingProvider(t, a, sc, d)
			case "paused":
				d.Metadata.State = v1.ConnectionState_CONNECTION_STATE_DISABLED
				putRoutingProvider(t, a, sc, d)
			case "deleted":
				if err = a.s.DeletePrivate(ctx, sc, string(providerKind), d.Metadata.Id); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err = a.ResolveBinding(ctx, lookup, "alias"); !errors.Is(err, inference.ErrContinuation) {
				t.Fatalf("invalid binding accepted: %v", err)
			}
			dialer := &recordingLiveDialer{}
			fresh := &inferenceAdapter{s: a.s, telemetry: a.telemetry}
			h, err := inference.New(inference.Config{Authorizer: fresh, Router: fresh, Credentials: fresh, Usage: fresh, LiveBindings: fresh, SocketDialer: dialer, Destinations: inference.DestinationPolicy{ApprovedOrigins: []string{"https://provider.example/v1"}}})
			if err != nil {
				t.Fatal(err)
			}
			_ = h.ServeRealtimeSocket(ctx, lookupKey, "/v1/realtime/calls/alias", nil, closedLiveSocket{})
			if dialer.endpoint != "" {
				t.Fatalf("invalid binding dialed upstream: %s", dialer.endpoint)
			}
		})
	}
}

func liveBindingFixture(t *testing.T) (*inferenceAdapter, inference.Principal, core.Scope, providerData, inference.LiveBinding) {
	t.Helper()
	a, sc, key := dispatchFixture(t)
	d := saveRoutingFixture(t, a, sc, "account", "model")
	p := admitRouting(t, a, key)
	_, version, err := a.s.GetPrivateVersion(context.Background(), sc, string(providerKind), d.Metadata.Id)
	if err != nil {
		t.Fatal(err)
	}
	b := inference.LiveBinding{
		Target:                  inference.Target{Provider: d.Metadata.Id, BaseURL: d.Metadata.BaseUrl, Model: "wire-model", Wire: inference.Responses, Auth: inference.APIKey},
		AccountID:               d.Metadata.Id,
		UpstreamCallID:          "call_original",
		ServedCredentialVersion: version,
		ExpiresAt:               time.Now().Add(5 * time.Minute),
	}
	return a, p, sc, d, b
}

func TestLiveBindingPublicationGenerationAndEnvelope(t *testing.T) {
	a, p, sc, d, binding := liveBindingFixture(t)
	ctx := context.Background()
	if err := a.SaveBinding(ctx, p, "alias", binding); err != nil {
		t.Fatal(err)
	}
	raw, err := a.s.GetState(ctx, sc, core.LiveAlias, "live/"+p.KeyID+"/alias")
	if err != nil {
		t.Fatal(err)
	}
	var saved storedLiveBinding
	if err = json.Unmarshal(raw, &saved); err != nil || saved.Version != liveBindingEnvelopeVersion || saved.Binding.ServedCredentialVersion != binding.ServedCredentialVersion || saved.Binding.UpstreamCallID != binding.UpstreamCallID || !saved.Binding.ExpiresAt.Equal(binding.ExpiresAt) {
		t.Fatalf("saved binding lacks serving proof: %+v, %v", saved, err)
	}
	d.Secret = "fixture-v2"
	putRoutingProvider(t, a, sc, d)
	if err = a.SaveBinding(ctx, p, "stale", binding); !errors.Is(err, inference.ErrContinuation) {
		t.Fatalf("replaced account published alias: %v", err)
	}
	if _, err = a.s.GetState(ctx, sc, core.LiveAlias, "live/"+p.KeyID+"/stale"); !errors.Is(err, store.ErrContinuation) {
		t.Fatalf("stale alias became usable: %v", err)
	}
	if _, _, err = a.ResolveBinding(ctx, p, "alias"); !errors.Is(err, inference.ErrContinuation) {
		t.Fatalf("replacement did not invalidate saved alias: %v", err)
	}
}

func TestLiveBindingPublicationRace(t *testing.T) {
	a, p, sc, d, binding := liveBindingFixture(t)
	ctx := context.Background()
	start := make(chan struct{})
	var wg sync.WaitGroup
	var publishErr, replaceErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		publishErr = a.SaveBinding(ctx, p, "race", binding)
	}()
	go func() {
		defer wg.Done()
		<-start
		d.Secret = "fixture-v2"
		raw, err := json.Marshal(d)
		if err == nil {
			replaceErr = a.s.PutPrivate(ctx, sc, string(providerKind), d.Metadata.Id, raw)
		} else {
			replaceErr = err
		}
	}()
	close(start)
	wg.Wait()
	if replaceErr != nil {
		t.Fatal(replaceErr)
	}
	if publishErr != nil && !errors.Is(publishErr, inference.ErrContinuation) {
		t.Fatal(publishErr)
	}
	raw, err := a.s.GetState(ctx, sc, core.LiveAlias, "live/"+p.KeyID+"/race")
	if publishErr != nil {
		if !errors.Is(err, store.ErrContinuation) {
			t.Fatalf("failed publication left state: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	var saved storedLiveBinding
	if err = json.Unmarshal(raw, &saved); err != nil || saved.Binding.ServedCredentialVersion != binding.ServedCredentialVersion {
		t.Fatalf("race relabeled the served call: %+v, %v", saved, err)
	}
	if _, _, err = a.ResolveBinding(ctx, p, "race"); !errors.Is(err, inference.ErrContinuation) {
		t.Fatalf("replacement did not invalidate race winner: %v", err)
	}
}

func TestLiveBindingProoflessEnvelopeAndCredentialMismatch(t *testing.T) {
	a, p, sc, _, binding := liveBindingFixture(t)
	ctx := context.Background()
	if err := a.SaveBinding(ctx, p, "missing", inference.LiveBinding{AccountID: binding.AccountID, ExpiresAt: binding.ExpiresAt}); !errors.Is(err, inference.ErrContinuation) {
		t.Fatalf("missing serving proof accepted: %v", err)
	}
	for name, value := range map[string]any{
		"legacy": struct {
			Binding           inference.LiveBinding
			CredentialVersion string
		}{binding, binding.ServedCredentialVersion},
		"no-proof": storedLiveBinding{Version: liveBindingEnvelopeVersion, Binding: inference.LiveBinding{AccountID: binding.AccountID, ExpiresAt: binding.ExpiresAt}},
	} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err = a.s.PutState(ctx, sc, core.Responses, "live/"+p.KeyID+"/"+name, raw); err != nil {
			t.Fatal(err)
		}
		if _, _, err = a.ResolveBinding(ctx, p, name); !errors.Is(err, inference.ErrContinuation) {
			t.Fatalf("%s was accepted: %v", name, err)
		}
	}
	if matchesServedCredential(binding, inference.Credential{AccountID: binding.AccountID, CredentialVersion: "new-generation"}) || matchesServedCredential(binding, inference.Credential{AccountID: "other", CredentialVersion: binding.ServedCredentialVersion}) || !matchesServedCredential(binding, inference.Credential{AccountID: binding.AccountID, CredentialVersion: binding.ServedCredentialVersion}) {
		t.Fatal("lookup did not bind selected credentials to served generation and account")
	}
}

func TestLiveOfferRejectsReplacementAfterUpstreamHeaders(t *testing.T) {
	for _, path := range []string{"/v1/realtime/calls", "/v1/live"} {
		t.Run(path, func(t *testing.T) {
			headers := make(chan string, 1)
			resume := make(chan struct{})
			var release sync.Once
			unblock := func() { release.Do(func() { close(resume) }) }
			defer unblock()
			f := newProviderAcceptance(t, "custom", "gpt-live-1-codex", func(w http.ResponseWriter, r *http.Request) {
				served := r.Header.Get("Authorization")
				w.Header().Set("Location", "/v1/realtime/calls/call_original")
				w.Header().Set("Content-Type", "application/sdp")
				w.WriteHeader(http.StatusCreated)
				w.(http.Flusher).Flush()
				headers <- served
				<-resume
				_, _ = io.WriteString(w, "answer")
			})
			result := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"sdp":"offer","session":{"model":"fixture"}}`))
				r.Header.Set("Authorization", "Bearer "+f.key)
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				f.srv.handler().ServeHTTP(w, r)
				result <- w
			}()
			select {
			case got := <-headers:
				if got != "Bearer "+f.secret {
					t.Fatalf("offer used a different credential")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("upstream did not flush offer headers")
			}
			f.data.Secret = "fixture-v2"
			f.save()
			unblock()
			select {
			case w := <-result:
				if w.Code != http.StatusBadRequest || w.Header().Get("Location") != "" {
					t.Fatalf("stale offer returned alias: %d %s", w.Code, w.Header().Get("Location"))
				}
			case <-time.After(10 * time.Second):
				t.Fatal("offer did not complete")
			}
			var count int
			if err := f.db.QueryRow(`SELECT count(*) FROM "`+f.schema+`_temp".states WHERE org_id=$1 AND user_id=$2 AND id LIKE $3`, f.scope.OrganizationID, f.scope.UserID, "live/"+f.keyID+"/%").Scan(&count); err != nil || count != 0 {
				t.Fatalf("stale offer persisted an alias: count=%d err=%v", count, err)
			}
		})
	}
}
