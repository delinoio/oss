// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/core"
	"github.com/delinoio/oss/packages/tairouter/inference"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
)

type jevCooldownFixture struct {
	t                                      *testing.T
	a                                      *inferenceAdapter
	key                                    string
	origin                                 string
	choice                                 string
	first                                  providerData
	versions                               map[string]string
	decisionCalls, firstSends, secondSends atomic.Int32
	siblingSends                           atomic.Int32
	decisionBody                           string
	onDecision                             func()
	failFirst                              bool
}

func newJEVCooldownFixture(t *testing.T, modelBackend, pool, quota, pin bool) *jevCooldownFixture {
	t.Helper()
	a, sc, key := dispatchFixture(t)
	return newJEVCooldownFixtureWithAdapter(t, a, sc, key, modelBackend, pool, quota, pin)
}

func newJEVCooldownFixtureWithAdapter(t *testing.T, a *inferenceAdapter, sc core.Scope, key string, modelBackend, pool, quota, pin bool) *jevCooldownFixture {
	t.Helper()
	f := &jevCooldownFixture{t: t, a: a, key: key, versions: map[string]string{}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/systemone" || body["model"] == "decision-model" {
			f.decisionCalls.Add(1)
			encoded, _ := json.Marshal(body)
			f.decisionBody = string(encoded)
			if f.onDecision != nil {
				f.onDecision()
			}
			if r.URL.Path == "/systemone" {
				_, _ = fmt.Fprintf(w, `{"answers":{"route":{"choice":%q}}}`, f.choice)
			} else {
				_, _ = fmt.Fprintf(w, `{"id":"decision","object":"chat.completion","model":"decision-model","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":%q}}]}`, fmt.Sprintf(`{"choice":%q}`, f.choice))
			}
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if body["model"] == "first" {
			f.firstSends.Add(1)
			if r.Header.Get("X-Private-Account") == "sibling" {
				f.siblingSends.Add(1)
			}
			if f.failFirst {
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
				return
			}
		} else if body["model"] == "second" {
			f.secondSends.Add(1)
		} else {
			t.Errorf("unexpected model %v", body["model"])
		}
		_, _ = fmt.Fprintf(w, `{"id":"response","object":"chat.completion","model":%q,"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"selected target"}}]}`, body["model"])
	}))
	t.Cleanup(upstream.Close)
	f.origin = upstream.URL
	for _, id := range []string{"first", "sibling", "second", "model-decision"} {
		d := providerData{Metadata: &v1.ProviderConnection{Id: id, OrganizationId: sc.OrganizationID, ProviderId: "custom", BaseUrl: upstream.URL + "/v1", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, State: v1.ConnectionState_CONNECTION_STATE_READY}, Secret: "fixture-" + id, CustomHeaders: map[string]string{"X-Private-Account": id}}
		putRoutingProvider(t, a, sc, d)
		if id == "first" {
			f.first = d
		}
		_, version, err := a.providerVersion(context.Background(), sc, id)
		if err != nil {
			t.Fatal(err)
		}
		f.versions[id] = version
	}
	wire := v1.ProviderWire_PROVIDER_WIRE_JEV_DECISION
	putRoutingProvider(t, a, sc, providerData{Metadata: &v1.ProviderConnection{Id: "decision", OrganizationId: sc.OrganizationID, ProviderId: "custom", BaseUrl: upstream.URL + "/systemone", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, State: v1.ConnectionState_CONNECTION_STATE_READY, Configuration: &v1.ProviderConfiguration{Wire: &wire}}, Secret: "decision-key"})
	cfg := &v1.JevDecisionConfig{DecisionConnectionId: "decision", DecisionServiceModel: "tev1", DecisionQuotaSignals: quota, DecisionTimeoutMs: 1000}
	if pin {
		cfg.SessionPinTtlSeconds = 60
	}
	if modelBackend {
		cfg.DecisionModel = "decision-route"
		putRoutingRoute(t, a, sc, &v1.Route{Id: "decision-route", ModelId: "decision-route", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: "model-decision"}, ModelId: "decision-model"}}})
	}
	firstTarget := &v1.RouteTarget{Target: &v1.RouteTarget_ConnectionId{ConnectionId: "first"}, ModelId: "first"}
	if pool {
		firstTarget.Target = &v1.RouteTarget_PoolId{PoolId: "pool"}
		members := &v1.Pool{Id: "pool", Strategy: v1.PoolStrategy_POOL_STRATEGY_PRIORITY, Members: []*v1.PoolMember{{ConnectionId: "first", Priority: 0}, {ConnectionId: "sibling", Priority: 1}}}
		encoded, _ := json.Marshal(members)
		if err := a.s.PutPrivate(context.Background(), sc, string(poolKind), "pool", encoded); err != nil {
			t.Fatal(err)
		}
	}
	putRoutingRoute(t, a, sc, &v1.Route{Id: "combo", ModelId: "combo", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_JEV, JevDecision: cfg, Targets: []*v1.RouteTarget{firstTarget, {Target: &v1.RouteTarget_ConnectionId{ConnectionId: "second"}, ModelId: "second"}}})
	p := admitRouting(t, a, key)
	_ = p
	return f
}

func (f *jevCooldownFixture) cool(id, model string, until time.Time) {
	f.t.Helper()
	if err := f.a.s.SetAccountCooldownVersion(context.Background(), f.a.lease, id, f.versions[id], model, until); err != nil {
		f.t.Fatal(err)
	}
}

func (f *jevCooldownFixture) request(session string) *httptest.ResponseRecorder {
	return f.requestModel("combo", session)
}

func (f *jevCooldownFixture) requestModel(model, session string) *httptest.ResponseRecorder {
	f.t.Helper()
	instance := &inferenceAdapter{s: f.a.s, telemetry: f.a.telemetry}
	h, err := inference.New(inference.Config{Authorizer: instance, Router: instance, Credentials: instance, Negotiator: instance, Usage: instance, Destinations: inference.DestinationPolicy{ApprovedOrigins: []string{f.origin}}})
	if err != nil {
		f.t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"Choose"}]}`, model)))
	req.Header.Set("Authorization", "Bearer "+f.key)
	req.Header.Set("Content-Type", "application/json")
	if session != "" {
		req.Header.Set("session-id", session)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestJEVDurableCooldownDecisionAndRevalidation(t *testing.T) {
	for _, backend := range []bool{false, true} {
		for _, during := range []bool{false, true} {
			t.Run(fmt.Sprintf("model=%t/during=%t", backend, during), func(t *testing.T) {
				f := newJEVCooldownFixture(t, backend, false, false, false)
				first, err := targetFor(f.first, "first")
				if err != nil {
					t.Fatal(err)
				}
				f.choice = selectionKey(first) + ":none"
				if during {
					f.onDecision = func() { f.cool("first", "first", time.Now().Add(time.Hour)) }
				} else {
					f.cool("first", "first", time.Now().Add(time.Hour))
				}
				w := f.request("")
				wantDecisions := int32(1)
				if !during && !backend {
					wantDecisions = 0 // A single eligible target needs no decision exchange.
				}
				if w.Code != http.StatusOK || f.firstSends.Load() != 0 || f.secondSends.Load() != 1 || f.decisionCalls.Load() != wantDecisions {
					t.Fatalf("HTTP %d body=%s decision=%d sends=%d/%d", w.Code, w.Body.String(), f.decisionCalls.Load(), f.firstSends.Load(), f.secondSends.Load())
				}
				if !during && strings.Contains(f.decisionBody, "first") {
					t.Fatalf("cooled target disclosed: %s", f.decisionBody)
				}
			})
		}
	}
}

func TestJEVDurableCooldownScopesAndPools(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		pool, quota              bool
		model                    string
		until                    time.Duration
		coolSibling, expectFirst bool
	}{
		{name: "exact", model: "first", until: time.Hour},
		{name: "all-models", model: "", until: time.Hour},
		{name: "unrelated", model: "other", until: time.Hour, expectFirst: true},
		{name: "expired", model: "first", until: -time.Minute, expectFirst: true},
		{name: "replaced-generation", model: "first", until: time.Hour, expectFirst: true},
		{name: "pool-healthy", pool: true, model: "first", until: time.Hour, expectFirst: true},
		{name: "pool-healthy-quota", pool: true, quota: true, model: "first", until: time.Hour, expectFirst: true},
		{name: "pool-all-cooled", pool: true, model: "first", until: time.Hour, coolSibling: true},
		{name: "pool-all-cooled-quota", pool: true, quota: true, model: "first", until: time.Hour, coolSibling: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newJEVCooldownFixture(t, false, tc.pool, tc.quota, false)
			f.choice = "invalid-choice"
			f.cool("first", tc.model, time.Now().Add(tc.until))
			if tc.name == "replaced-generation" {
				f.first.Secret = "replacement-credential"
				putRoutingProvider(t, f.a, f.a.lease.Principal.Scope, f.first)
			}
			if tc.coolSibling {
				f.cool("sibling", "first", time.Now().Add(time.Hour))
			}
			w := f.request("")
			wantFirst, wantSecond := int32(0), int32(1)
			if tc.expectFirst {
				wantFirst, wantSecond = 1, 0
			}
			if w.Code != http.StatusOK || f.firstSends.Load() != wantFirst || f.secondSends.Load() != wantSecond {
				t.Fatalf("HTTP %d body=%s sends=%d/%d", w.Code, w.Body.String(), f.firstSends.Load(), f.secondSends.Load())
			}
			if tc.pool && tc.expectFirst && f.siblingSends.Load() != 1 {
				t.Fatal("cooled pool member served instead of healthy sibling")
			}
		})
	}
}

func TestJEVDurableCooldownMalformedDecisionFailOpen(t *testing.T) {
	f := newJEVCooldownFixture(t, false, false, false, false)
	f.choice = "invalid-choice"
	f.onDecision = func() { f.cool("first", "first", time.Now().Add(time.Hour)) }
	w := f.request("")
	if w.Code != http.StatusOK || f.decisionCalls.Load() != 1 || f.firstSends.Load() != 0 || f.secondSends.Load() != 1 {
		t.Fatalf("fail-open HTTP %d body=%s decision=%d sends=%d/%d", w.Code, w.Body.String(), f.decisionCalls.Load(), f.firstSends.Load(), f.secondSends.Load())
	}
}

func TestJEVDurableCooldownTimedOutDecisionFailOpen(t *testing.T) {
	f := newJEVCooldownFixture(t, false, false, false, false)
	f.onDecision = func() {
		f.cool("first", "first", time.Now().Add(time.Hour))
		time.Sleep(1100 * time.Millisecond)
	}
	w := f.request("")
	if w.Code != http.StatusOK || f.decisionCalls.Load() != 1 || f.firstSends.Load() != 0 || f.secondSends.Load() != 1 {
		t.Fatalf("timeout fail-open HTTP %d body=%s decision=%d sends=%d/%d", w.Code, w.Body.String(), f.decisionCalls.Load(), f.firstSends.Load(), f.secondSends.Load())
	}
}

func TestJEVDurableCooldownAllTargetsAndPin(t *testing.T) {
	f := newJEVCooldownFixture(t, false, false, false, true)
	first, err := targetFor(f.first, "first")
	if err != nil {
		t.Fatal(err)
	}
	f.choice = selectionKey(first) + ":none"
	if w := f.request("pinned"); w.Code != http.StatusOK {
		t.Fatalf("first HTTP %d: %s", w.Code, w.Body.String())
	}
	if w := f.request("pinned"); w.Code != http.StatusOK || f.decisionCalls.Load() != 1 {
		t.Fatalf("healthy pin HTTP %d decision=%d", w.Code, f.decisionCalls.Load())
	}
	putRoutingRoute(t, f.a, f.a.lease.Principal.Scope, &v1.Route{Id: "ordinary", ModelId: "ordinary", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: "first"}, ModelId: "first"}}})
	f.failFirst = true
	if w := f.requestModel("ordinary", ""); w.Code != http.StatusTooManyRequests {
		t.Fatalf("ordinary 429 HTTP %d: %s", w.Code, w.Body.String())
	}
	f.failFirst = false
	until, err := f.a.s.AccountCooldown(context.Background(), f.a.lease.Principal.Scope, "first", "first")
	if err != nil || until == nil || !until.After(time.Now()) {
		t.Fatalf("ordinary 429 did not persist cooldown: %v %v", until, err)
	}
	if w := f.request("pinned"); w.Code != http.StatusOK || f.firstSends.Load() != 3 || f.secondSends.Load() != 1 || f.decisionCalls.Load() != 1 {
		t.Fatalf("stale pin HTTP %d decision=%d sends=%d/%d", w.Code, f.decisionCalls.Load(), f.firstSends.Load(), f.secondSends.Load())
	}
	f.cool("second", "second", time.Now().Add(time.Hour))
	if w := f.request(""); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "no_eligible_credential") || f.firstSends.Load() != 3 || f.secondSends.Load() != 1 {
		t.Fatalf("all cooled HTTP %d body=%s sends=%d/%d", w.Code, w.Body.String(), f.firstSends.Load(), f.secondSends.Load())
	}
}

func TestJEVDurableCooldownReadFailure(t *testing.T) {
	s, schema := serverStoreWithSchema(t)
	a, sc, key := dispatchFixtureWithStore(t, s)
	f := newJEVCooldownFixtureWithAdapter(t, a, sc, key, false, false, false, false)
	f.choice = "invalid-choice"
	db, err := sql.Open("pgx", os.Getenv("TAIROUTER_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// The fixture owns a disposable schema. Removing only its cooldown table
	// forces the eligibility read to fail after route and provider reads succeed.
	if _, err := db.ExecContext(context.Background(), "DROP TABLE "+schema+".account_cooldowns"); err != nil {
		t.Fatal(err)
	}
	w := f.request("")
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "authorization_unavailable") || f.decisionCalls.Load() != 0 || f.firstSends.Load() != 0 || f.secondSends.Load() != 0 {
		t.Fatalf("read failure HTTP %d body=%s decision=%d sends=%d/%d", w.Code, w.Body.String(), f.decisionCalls.Load(), f.firstSends.Load(), f.secondSends.Load())
	}
}
