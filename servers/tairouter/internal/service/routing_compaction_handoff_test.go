// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/packages/tairouter/inference"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
)

func TestCompactionHandoffQuotaClassification(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   bool
	}{
		{429, "", true}, {402, "", true}, {403, `{"error":{"code":"insufficient_quota"}}`, true},
		{500, "usage limit reached", true}, {404, "insufficient_quota", false}, {401, "invalid api key", false},
		{429, "upstream_reset_replay_refused", false}, {429, "request_send_budget_exhausted", false},
		{400, "context length exceeded, quota exhausted", false}, {200, "quota exhausted", false},
	} {
		if got := compactHandoffQuota(inference.HTTPFailure{Status: tc.status, Body: []byte(tc.body)}); got != tc.want {
			t.Errorf("status=%d body=%q got=%v", tc.status, tc.body, got)
		}
	}
	if !compactHandoffModel(strings.Repeat("x", 512)) || compactHandoffModel(strings.Repeat("😀", 257)) || compactHandoffModel("") {
		t.Fatal("UTF16 model limit")
	}
}

func TestCompactionHandoffComposed(t *testing.T) {
	for _, mode := range []string{"quota", "body-quota", "route-changed", "account-changed", "native-success-clears", "native404", "failed-publication", "lane-isolation", "key-isolation", "key-revoked", "account-deleted", "budget", "route-before-write", "account-before-write", "pool-before-write"} {
		t.Run(mode, func(t *testing.T) {
			a, sc, key := dispatchFixture(t)
			ctx := context.Background()
			var routedSends, nativeSends, syntheticSends, mutations atomic.Int32
			var nativeStatus atomic.Int32
			nativeStatus.Store(429)
			if mode == "body-quota" {
				nativeStatus.Store(500)
			}
			if mode == "native404" {
				nativeStatus.Store(404)
			}
			if mode == "native-success-clears" {
				nativeStatus.Store(200)
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid request")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/routed/chat/completions":
					routedSends.Add(1)
					if body["model"] != "physical-routed" {
						t.Error("alias used as serving identity")
					}
					summary := "fixture summary"
					if mode == "failed-publication" {
						summary = ""
					}
					_, _ = fmt.Fprintf(w, `{"id":"r","object":"chat.completion","model":"physical-routed","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":%q}}],"usage":{"prompt_tokens":2,"completion_tokens":2,"total_tokens":4}}`, summary)
				case "/native/responses/compact":
					nativeSends.Add(1)
					status := int(nativeStatus.Load())
					w.WriteHeader(status)
					if status == 200 {
						_, _ = w.Write([]byte(`{"output":[{"type":"compaction","encrypted_content":"native-opaque"}]}`))
					} else {
						_, _ = w.Write([]byte(`{"error":{"message":"usage limit reached","code":"insufficient_quota"}}`))
					}
				case "/native/responses":
					syntheticSends.Add(1)
					if body["model"] != "physical-native" {
						t.Error("404 switched physical model")
					}
					_, _ = w.Write([]byte(`{"id":"r","object":"response","status":"completed","model":"physical-native","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"native synthetic summary"}]}],"usage":{"input_tokens":2,"output_tokens":2,"total_tokens":4}}`))
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer upstream.Close()
			routed := providerData{Metadata: &v1.ProviderConnection{Id: "routed-account", OrganizationId: sc.OrganizationID, ProviderId: "custom", BaseUrl: upstream.URL + "/routed", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, State: v1.ConnectionState_CONNECTION_STATE_READY}, Secret: "fixture-routed"}
			putRoutingProvider(t, a, sc, routed)
			responses := v1.ProviderWire_PROVIDER_WIRE_RESPONSES
			putRoutingProvider(t, a, sc, providerData{Metadata: &v1.ProviderConnection{Id: "native-account", OrganizationId: sc.OrganizationID, ProviderId: "custom", BaseUrl: upstream.URL + "/native", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, State: v1.ConnectionState_CONNECTION_STATE_READY, Configuration: &v1.ProviderConfiguration{Wire: &responses}}, Secret: "fixture-native"})
			route := &v1.Route{Id: "routed-route", ModelId: "routed-alias", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: "routed-account"}, ModelId: "physical-routed"}}}
			pool := &v1.Pool{Id: "remembered-pool", Strategy: v1.PoolStrategy_POOL_STRATEGY_PRIORITY, Members: []*v1.PoolMember{{ConnectionId: "routed-account", Weight: 1}}}
			if mode == "pool-before-write" {
				raw, _ := json.Marshal(pool)
				if err := a.s.PutPrivate(ctx, sc, string(poolKind), pool.Id, raw); err != nil {
					t.Fatal(err)
				}
				route.Targets[0].Target = &v1.RouteTarget_PoolId{PoolId: pool.Id}
			}
			putRoutingRoute(t, a, sc, route)
			putRoutingRoute(t, a, sc, &v1.Route{Id: "native-route", ModelId: "native-alias", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: "native-account"}, ModelId: "physical-native"}}})
			call := func(model, lane, token string) *httptest.ResponseRecorder {
				t.Helper()
				instance := &inferenceAdapter{s: a.s, telemetry: a.telemetry}
				var router inference.Router = instance
				if mode == "budget" {
					router = compactionBudgetRouter{instance}
				}
				var handoffs inference.CompactionHandoffs = compactionHandoffs{a: instance}
				if mode == "route-before-write" || mode == "account-before-write" || mode == "pool-before-write" {
					handoffs = compactionHandoffMutation{compactionHandoffs: compactionHandoffs{a: instance}, mutate: func() {
						mutations.Add(1)
						if mode == "pool-before-write" {
							pool.Members = nil
							raw, _ := json.Marshal(pool)
							if err := a.s.PutPrivate(ctx, sc, string(poolKind), pool.Id, raw); err != nil {
								t.Fatal(err)
							}
						} else if mode == "route-before-write" {
							route.Enabled = false
							putRoutingRoute(t, a, sc, route)
						} else {
							routed.Secret = "replaced-before-write"
							putRoutingProvider(t, a, sc, routed)
						}
					}}
				}
				h, err := inference.New(inference.Config{Authorizer: instance, Router: router, Credentials: instance, Usage: instance, ProviderStates: instance, Continuations: instance, CompactionHandoffs: handoffs, Destinations: inference.DestinationPolicy{ApprovedOrigins: []string{upstream.URL}}})
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest("POST", "/v1/responses/compact", strings.NewReader(fmt.Sprintf(`{"model":%q,"input":[{"role":"user","content":"fixture history"}]}`, model))).WithContext(ctx)
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("session-id", lane)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, req)
				return w
			}
			first := call("routed-alias", "lane", key)
			if mode == "failed-publication" {
				if first.Code == 200 {
					t.Fatal("empty summary accepted")
				}
			} else if first.Code != 200 {
				t.Fatalf("capture HTTP%d %s", first.Code, first.Body.String())
			}
			lane := "lane"
			switch mode {
			case "key-revoked":
				p := admitRouting(t, a, key)
				if err := a.s.RevokeKey(ctx, sc, p.KeyID); err != nil {
					t.Fatal(err)
				}
			case "account-deleted":
				if err := a.s.DeletePrivate(ctx, sc, string(providerKind), routed.Metadata.Id); err != nil {
					t.Fatal(err)
				}
			case "route-changed":
				route.Enabled = false
				putRoutingRoute(t, a, sc, route)
			case "account-changed":
				routed.Secret = "replacement"
				putRoutingProvider(t, a, sc, routed)
			case "lane-isolation":
				lane = "different"
			case "key-isolation":
				_, other, err := a.s.IssueKey(ctx, sc, "other", nil)
				if err != nil {
					t.Fatal(err)
				}
				key = other
			}
			result := call("native-alias", lane, key)
			want := 429
			if mode == "key-revoked" {
				want = 401
			}
			if mode == "quota" || mode == "body-quota" || mode == "native404" || mode == "native-success-clears" {
				want = 200
			}
			if mode == "route-before-write" || mode == "account-before-write" || mode == "pool-before-write" {
				if mutations.Load() != 1 {
					t.Fatal("test did not reach lookup/write boundary")
				}
				if result.Code == 200 {
					t.Fatal("stale bound handoff sent")
				}
			} else if result.Code != want {
				t.Fatalf("handoff HTTP%d want%d: %s", result.Code, want, result.Body.String())
			}
			wantRouted := int32(1)
			if mode == "quota" || mode == "body-quota" {
				wantRouted = 2
			}
			if routedSends.Load() != wantRouted {
				t.Fatalf("routed sends=%d want%d", routedSends.Load(), wantRouted)
			}
			if mode == "native404" && syntheticSends.Load() != 1 {
				t.Fatal("native404 synthetic fallback changed")
			}
			if mode == "native-success-clears" {
				nativeStatus.Store(429)
				result = call("native-alias", "lane", key)
				if result.Code != 429 || routedSends.Load() != 1 {
					t.Fatalf("native success did not forget handoff: %d", result.Code)
				}
			}
		})
	}
}

// The wrapper changes only the authorized ledger cap; normal resolution still
// captures the provider/config selection needed for publication.
type compactionBudgetRouter struct{ *inferenceAdapter }

func (r compactionBudgetRouter) Resolve(ctx context.Context, p inference.Principal, model string, op inference.Operation) (inference.Route, error) {
	route, err := r.inferenceAdapter.Resolve(ctx, p, model, op)
	route.MaxSends = 1
	return route, err
}
func TestCompactionHandoffPhysicalFastVariant(t *testing.T) {
	logical := inference.Target{BaseURL: "https://api.example/v1", Model: "logical", Wire: inference.Chat, Auth: inference.OAuth, Fast: true, FastPolicy: &inference.FastPolicy{Eligible: true, Kind: inference.FastModelVariant, Model: "actual-fast"}}
	if compactServingModel(logical) != "actual-fast" {
		t.Fatal("logical model retained")
	}
	changed := logical
	policy := *logical.FastPolicy
	policy.Model = "different-fast"
	changed.FastPolicy = &policy
	if compactPhysicalIdentity(logical) == compactPhysicalIdentity(changed) {
		t.Fatal("physical fast model absent from fence")
	}
}

type compactionHandoffMutation struct {
	compactionHandoffs
	mutate func()
}

func (h compactionHandoffMutation) Lookup(ctx context.Context, p inference.Principal, t inference.Target, f inference.HTTPFailure) (*inference.CompactionHandoff, error) {
	found, err := h.compactionHandoffs.Lookup(ctx, p, t, f)
	if err == nil && found != nil {
		h.mutate()
	}
	return found, err
}
