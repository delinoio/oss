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
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	"google.golang.org/protobuf/proto"
)

func TestJEVPinCompletionFailureFenceAndCancellation(t *testing.T) {
	a, sc, key := dispatchFixture(t)
	p := admitRouting(t, a, key)
	ctx := context.Background()
	route := &v1.Route{Id: "pin-route", ModelId: "pin-route", Enabled: true}
	putRoutingRoute(t, a, sc, route)
	id, epoch, config := "pin-state", newJEVEpoch(), "config-version"
	target := inference.Target{Provider: "provider", Model: "model"}
	candidate := jevCandidate{Key: "target", Provider: "provider", Model: "model"}
	makeScope := func() *jevRequestPin {
		return &jevRequestPin{principal: p, id: id, epoch: epoch, config: config, ttl: time.Minute, route: proto.Clone(route).(*v1.Route), target: target, candidate: candidate, decision: jevDecision{TargetKey: "target", Gate: jevApply, Backend: "typesafe"}, eligible: func(context.Context, jevCandidate) (bool, error) { return true, nil }}
	}
	if err := a.UpdateProviderState(ctx, p, jevPinNamespace, id, time.Now().Add(24*time.Hour), func([]byte) ([]byte, error) {
		return json.Marshal(jevStoredPin{Epoch: epoch, Config: config})
	}); err != nil {
		t.Fatal(err)
	}
	read := func() jevStoredPin {
		t.Helper()
		v, err := a.LoadProviderState(ctx, p, jevPinNamespace, id)
		if err != nil {
			t.Fatal(err)
		}
		var s jevStoredPin
		if json.Unmarshal(v.Data, &s) != nil {
			t.Fatal("invalid stored pin")
		}
		return s
	}
	a.jev = makeScope()
	if err := a.finishJEV(ctx, inference.UsageRecord{Outcome: inference.OutcomeCanceled, Sends: 1}); err != nil {
		t.Fatal(err)
	}
	if read().Decision != nil {
		t.Fatal("cancellation published pin")
	}
	a.jev = makeScope()
	if err := a.finishJEV(ctx, inference.UsageRecord{Outcome: inference.OutcomeSuccess, Provider: "provider", Model: "model", Sends: 1}); err != nil {
		t.Fatal(err)
	}
	saved := read()
	if saved.Decision == nil || saved.Decision.TargetKey != "target" || time.Until(saved.IdleExpires) > time.Minute {
		t.Fatal("successful completion not pinned")
	}
	// A successful retry cannot republish the pin after a real target failure.
	a.jev = makeScope()
	if err := a.failJEV(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.finishJEV(ctx, inference.UsageRecord{Outcome: inference.OutcomeSuccess, Provider: "provider", Model: "model", Sends: 2}); err != nil {
		t.Fatal(err)
	}
	if read().Decision != nil {
		t.Fatal("successful retry resurrected failed pin")
	}
	epoch = read().Epoch
	// Two instances capture the same epoch. A failure wins over the older success.
	older := &inferenceAdapter{s: a.s, lease: a.lease, jev: makeScope()}
	a.jev = makeScope()
	if err := a.finishJEV(ctx, inference.UsageRecord{Outcome: inference.OutcomeUpstreamError, Sends: 1}); err != nil {
		t.Fatal(err)
	}
	if err := older.finishJEV(ctx, inference.UsageRecord{Outcome: inference.OutcomeSuccess, Provider: "provider", Model: "model", Sends: 1}); err != nil {
		t.Fatal(err)
	}
	saved = read()
	if saved.Decision != nil || saved.Epoch == epoch {
		t.Fatal("in-flight completion resurrected failed pin")
	}
	// A configuration replacement invalidates an otherwise current success.
	epoch = saved.Epoch
	a.jev = makeScope()
	route.Enabled = false
	putRoutingRoute(t, a, sc, route)
	if err := a.finishJEV(ctx, inference.UsageRecord{Outcome: inference.OutcomeSuccess, Provider: "provider", Model: "model", Sends: 1}); err != nil {
		t.Fatal(err)
	}
	if read().Decision != nil {
		t.Fatal("changed route published pin")
	}
}

func TestJEVComposedServiceAndModelRouting(t *testing.T) {
	for _, modelBackend := range []bool{false, true} {
		t.Run(fmt.Sprint("model=", modelBackend), func(t *testing.T) {
			a, sc, key := dispatchFixture(t)
			ctx := context.Background()
			var decisionSends, inferenceSends atomic.Int32
			var choice string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var b map[string]any
				if json.NewDecoder(r.Body).Decode(&b) != nil {
					t.Error("invalid request")
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
			cfg := &v1.JevDecisionConfig{DecisionConnectionId: "decision", DecisionServiceModel: "tev1", SessionPinTtlSeconds: 60}
			if modelBackend {
				cfg.DecisionModel = "decision-route"
				putRoutingRoute(t, a, sc, &v1.Route{Id: "decision-route", ModelId: "decision-route", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: "model-decision"}, ModelId: "decision-model"}}})
			}
			putRoutingRoute(t, a, sc, &v1.Route{Id: "combo", ModelId: "combo", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_JEV, JevDecision: cfg, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: "first"}, ModelId: "first"}, {Target: &v1.RouteTarget_ConnectionId{ConnectionId: "second"}, ModelId: "second"}}})
			for i := 0; i < 2; i++ {
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
			if decisionSends.Load() != 1 || inferenceSends.Load() != 2 {
				t.Fatalf("decision=%d inference=%d; successful pin was not reused", decisionSends.Load(), inferenceSends.Load())
			}
		})
	}
}
