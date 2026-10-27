// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

func TestJEVQuotaComposedPayloadAndNearExhaustedPinRelease(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, modelBackend := range []bool{false, true} {
			t.Run(fmt.Sprint("model=", modelBackend, "/enabled=", enabled), func(t *testing.T) {
				a, sc, key := dispatchFixture(t)
				ctx := context.Background()
				var decisionSends, inferenceSends atomic.Int32
				var choice string
				var quotaPayloads atomic.Int32
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var b map[string]any
					if json.NewDecoder(r.Body).Decode(&b) != nil {
						t.Error("invalid request")
					}
					if r.URL.Path == "/systemone" || b["model"] == "decision-model" {
						raw, _ := json.Marshal(b)
						if strings.Contains(string(raw), "Quota healthy (20.5% of weekly used).") || strings.Contains(string(raw), "Quota nearly_exhausted (95% of weekly used).") {
							quotaPayloads.Add(1)
						} else if enabled {
							t.Error("missing persisted quota advisory")
						}
					}
					w.Header().Set("Content-Type", "application/json")
					if r.URL.Path == "/systemone" {
						decisionSends.Add(1)
						_, _ = fmt.Fprintf(w, `{"answers":{"route":{"choice":%q}}}`, choice)
						return
					}
					if r.URL.Path != "/v1/chat/completions" {
						t.Errorf("unexpected path %s", r.URL.Path)
						w.WriteHeader(404)
						return
					}
					text := "selected second"
					if b["model"] == "decision-model" {
						decisionSends.Add(1)
						raw, _ := json.Marshal(map[string]string{"choice": choice})
						text = string(raw)
					} else {
						inferenceSends.Add(1)
						if b["model"] != "second" {
							t.Error("wrong inference model")
						}
					}
					raw, _ := json.Marshal(map[string]any{"id": "response", "object": "chat.completion", "model": b["model"], "choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": text}}}, "usage": map[string]int{"prompt_tokens": 2, "completion_tokens": 1, "total_tokens": 3}})
					_, _ = w.Write(raw)
				}))
				defer upstream.Close()
				for _, id := range []string{"first", "second", "model-decision"} {
					d := providerData{Metadata: &v1.ProviderConnection{Id: id, OrganizationId: sc.OrganizationID, ProviderId: "custom", BaseUrl: upstream.URL + "/v1", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, State: v1.ConnectionState_CONNECTION_STATE_READY}, Secret: "fixture-key"}
					putRoutingProvider(t, a, sc, d)
					if id == "second" {
						target, e := targetFor(d, "second")
						if e != nil {
							t.Fatal(e)
						}
						choice = selectionKey(target) + ":none"
					}
				}
				wire := v1.ProviderWire_PROVIDER_WIRE_JEV_DECISION
				putRoutingProvider(t, a, sc, providerData{Metadata: &v1.ProviderConnection{Id: "decision", OrganizationId: sc.OrganizationID, ProviderId: "custom", BaseUrl: upstream.URL + "/systemone", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, State: v1.ConnectionState_CONNECTION_STATE_READY, Configuration: &v1.ProviderConfiguration{Wire: &wire}}, Secret: "fixture-decision-key"})
				cfg := &v1.JevDecisionConfig{DecisionConnectionId: "decision", DecisionServiceModel: "tev1", SessionPinTtlSeconds: 60, DecisionQuotaSignals: enabled}
				if modelBackend {
					cfg.DecisionModel = "decision-route"
					putRoutingRoute(t, a, sc, &v1.Route{Id: "decision-route", ModelId: "decision-route", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: "model-decision"}, ModelId: "decision-model"}}})
				}
				putRoutingRoute(t, a, sc, &v1.Route{Id: "combo", ModelId: "combo", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_JEV, JevDecision: cfg, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: "first"}, ModelId: "first"}, {Target: &v1.RouteTarget_ConnectionId{ConnectionId: "second"}, ModelId: "second"}}})
				p := admitRouting(t, a, key)
				_, version, err := a.providerVersion(ctx, sc, "second")
				if err != nil {
					t.Fatal(err)
				}
				publish := func(percent float64) {
					t.Helper()
					err := a.writeNativeQuota(ctx, p, "second", version, nativeQuotaSnapshot{Windows: []nativeQuotaWindow{{Window: "weekly", Percent: percent, ObservedAt: time.Now()}}})
					if err != nil {
						t.Fatal(err)
					}
				}
				publish(20.5)
				for i := 0; i < 3; i++ {
					if i == 2 {
						publish(95)
					}
					instance := &inferenceAdapter{s: a.s, telemetry: a.telemetry}
					h, e := inference.New(inference.Config{Authorizer: instance, Router: instance, Credentials: instance, Negotiator: instance, Usage: instance, Destinations: inference.DestinationPolicy{ApprovedOrigins: []string{upstream.URL}}})
					if e != nil {
						t.Fatal(e)
					}
					req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"combo","messages":[{"role":"user","content":"Choose the best target"}]}`)).WithContext(ctx)
					req.Header.Set("Authorization", "Bearer "+key)
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("session-id", "fixture-session")
					w := httptest.NewRecorder()
					h.ServeHTTP(w, req)
					if w.Code != 200 || !strings.Contains(w.Body.String(), "selected second") {
						t.Fatalf("composition HTTP %d: %s", w.Code, w.Body.String())
					}
				}
				wantDecisions, wantQuota := int32(1), int32(0)
				if enabled {
					wantDecisions, wantQuota = 2, 2
				}
				if decisionSends.Load() != wantDecisions || inferenceSends.Load() != 3 || quotaPayloads.Load() != wantQuota {
					t.Fatalf("decision=%d inference=%d; healthy pin reuse or near-exhausted pin release failed", decisionSends.Load(), inferenceSends.Load())
				}
			})
		}
	}

}

func TestJEVQuotaCredentialGenerationAndMissingPoolMember(t *testing.T) {
	a, sc, key := dispatchFixture(t)
	p := admitRouting(t, a, key)
	ctx := context.Background()
	d := providerData{Metadata: &v1.ProviderConnection{Id: "quota-account", OrganizationId: sc.OrganizationID, ProviderId: "custom", BaseUrl: "https://example.test/v1", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, State: v1.ConnectionState_CONNECTION_STATE_READY}, Secret: "fixture-key"}
	putRoutingProvider(t, a, sc, d)
	_, version, err := a.providerVersion(ctx, sc, d.Metadata.Id)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.writeNativeQuota(ctx, p, d.Metadata.Id, version, nativeQuotaSnapshot{Windows: []nativeQuotaWindow{{Window: "weekly", Percent: 95, ObservedAt: time.Now()}}}); err != nil {
		t.Fatal(err)
	}
	tiers, _ := resolveJEVQuotaTiers(nil)
	selection := routeSelection{provider: "custom", target: inference.Target{Auth: inference.APIKey, Model: "m"}, request: store.SelectionRequest{Model: "m", Candidates: []store.SelectionCandidate{{ID: d.Metadata.Id, Version: version}}}}
	got, err := a.jevQuotaForSelection(ctx, p, selection, tiers)
	if err != nil || got.Tier != jevQuotaNearlyExhausted {
		t.Fatal(got, err)
	}
	d.Secret = "replacement-fixture-key"
	putRoutingProvider(t, a, sc, d)
	_, version, err = a.providerVersion(ctx, sc, d.Metadata.Id)
	if err != nil {
		t.Fatal(err)
	}
	selection.request.Candidates[0].Version = version
	got, err = a.jevQuotaForSelection(ctx, p, selection, tiers)
	if err != nil || got.Tier != jevQuotaUnknown {
		t.Fatal("old generation evidence reused", got, err)
	}
	selection.request.Candidates = append(selection.request.Candidates, store.SelectionCandidate{ID: "missing"})
	got, err = a.jevQuotaForSelection(ctx, p, selection, tiers)
	if err != nil || got.Tier != jevQuotaUnknown {
		t.Fatal("multi-key evidence accepted", got, err)
	}
}
