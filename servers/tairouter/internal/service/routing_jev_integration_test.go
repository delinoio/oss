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

	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/providers"
	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
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
	requests := []struct {
		name, path, body, evidence string
	}{
		{"chat-pin", "/v1/chat/completions", `{"model":"combo","messages":[{"role":"user","content":"Choose the best target"}]}`, "Choose the best target"},
		{"messages-image", "/v1/messages", `{"model":"combo","max_tokens":32,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL/nwAAAABJRU5ErkJggg=="}}]}]}`, "has_image"},
		{"messages-tool", "/v1/messages", `{"model":"combo","max_tokens":32,"messages":[{"role":"user","content":"Fix the test"},{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"exec_command","input":{"secret":"HIDDEN_ARGUMENT"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","content":"1 fail: expected 2 got 3"}]}]}`, "1 fail: expected 2 got 3"},
	}
	for _, request := range requests {
		for _, modelBackend := range []bool{false, true} {
			t.Run(fmt.Sprint(request.name, "/model=", modelBackend), func(t *testing.T) {
				a, sc, key := dispatchFixture(t)
				ctx := context.Background()
				var decisionSends, inferenceSends atomic.Int32
				var choice string
				var decisionEvidence string
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var b map[string]any
					if json.NewDecoder(r.Body).Decode(&b) != nil {
						t.Error("invalid request")
					}
					w.Header().Set("Content-Type", "application/json")
					if r.URL.Path == "/systemone" {
						decisionSends.Add(1)
						raw, _ := json.Marshal(b)
						decisionEvidence = string(raw)
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
						raw, _ := json.Marshal(b)
						decisionEvidence = string(raw)
						raw, _ = json.Marshal(map[string]string{"choice": choice})
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
				cfg := &v1.JevDecisionConfig{DecisionConnectionId: "decision", DecisionServiceModel: "tev1"}
				if request.name == "chat-pin" {
					cfg.SessionPinTtlSeconds = 60
				}
				if modelBackend {
					cfg.DecisionModel = "decision-route"
					putRoutingRoute(t, a, sc, &v1.Route{Id: "decision-route", ModelId: "decision-route", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: "model-decision"}, ModelId: "decision-model"}}})
				}
				putRoutingRoute(t, a, sc, &v1.Route{Id: "combo", ModelId: "combo", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_JEV, JevDecision: cfg, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: "first"}, ModelId: "first"}, {Target: &v1.RouteTarget_ConnectionId{ConnectionId: "second"}, ModelId: "second"}}})
				attempts := 1
				if request.name == "chat-pin" {
					attempts = 2
				}
				for i := 0; i < attempts; i++ {
					instance := &inferenceAdapter{s: a.s, telemetry: a.telemetry}
					h, e := inference.New(inference.Config{Authorizer: instance, Router: instance, Credentials: instance, Negotiator: instance, Usage: instance, Destinations: inference.DestinationPolicy{ApprovedOrigins: []string{upstream.URL}}})
					if e != nil {
						t.Fatal(e)
					}
					req := httptest.NewRequest("POST", request.path, strings.NewReader(request.body)).WithContext(ctx)
					req.Header.Set("Authorization", "Bearer "+key)
					req.Header.Set("Content-Type", "application/json")
					if request.name == "chat-pin" {
						req.Header.Set("session-id", "fixture-session")
					}
					w := httptest.NewRecorder()
					h.ServeHTTP(w, req)
					if w.Code != 200 || !strings.Contains(w.Body.String(), "selected second") {
						t.Fatalf("composition HTTP %d: %s", w.Code, w.Body.String())
					}
				}
				if decisionSends.Load() != 1 || inferenceSends.Load() != int32(attempts) || !strings.Contains(decisionEvidence, request.evidence) || strings.Contains(decisionEvidence, "HIDDEN_ARGUMENT") || strings.Contains(decisionEvidence, "iVBORw0KGgo") {
					t.Fatalf("decision=%d inference=%d; successful pin was not reused", decisionSends.Load(), inferenceSends.Load())
				}
			})
		}
	}
}

func TestJEVSemanticSelectorsSurviveEligibilityAndDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, selector                                 string
		retention, fast, modelBackend, mixed, failOpen bool
	}{
		{name: "retention-service-mixed", selector: "combo", retention: true, mixed: true},
		{name: "retention-model", selector: "combo", retention: true, modelBackend: true},
		{name: "retention-fail-open", selector: "combo", retention: true, mixed: true, failOpen: true},
		{name: "fast-service", selector: "combo--fast", fast: true, mixed: true},
		{name: "ordinary-control", selector: "combo", mixed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, sc, key := dispatchFixture(t)
			var decisions, sends atomic.Int32
			choice := "invalid-choice"
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/systemone" {
					decisions.Add(1)
					_, _ = fmt.Fprintf(w, `{"answers":{"route":{"choice":%q}}}`, choice)
					return
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["model"] == "decision-model" {
					decisions.Add(1)
					_, _ = fmt.Fprintf(w, `{"id":"decision","object":"chat.completion","model":"decision-model","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":%q}}]}`, fmt.Sprintf(`{"choice":%q}`, choice))
					return
				}
				sends.Add(1)
				if r.URL.Path != "/v1/chat/completions" || body["model"] != "selected" {
					t.Errorf("unexpected inference request: %s %v", r.URL.Path, body)
				}
				if tc.fast && body["service_tier"] != "priority" {
					t.Errorf("fast policy missing from wire: %v", body)
				}
				_, _ = w.Write([]byte(`{"id":"response","object":"chat.completion","model":"selected","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"selected target"}}]}`))
			}))
			defer upstream.Close()
			d := providerData{Metadata: &v1.ProviderConnection{Id: "selected", OrganizationId: sc.OrganizationID, ProviderId: "custom", BaseUrl: upstream.URL + "/v1", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, State: v1.ConnectionState_CONNECTION_STATE_READY}, Secret: "fixture-key"}
			if tc.fast {
				d.Metadata.Configuration = &v1.ProviderConfiguration{Policy: &v1.ProviderPolicyOverrides{SupportsServiceTier: proto.Bool(true)}}
			}
			putRoutingProvider(t, a, sc, d)
			target, err := targetForContext(context.Background(), d, "selected")
			if err != nil {
				t.Fatal(err)
			}
			var retention *v1.ReasoningRetentionConfig
			if tc.retention {
				retention = &v1.ReasoningRetentionConfig{Enabled: true}
				if err := applyReasoningRetention(retention, &target); err != nil {
					t.Fatal(err)
				}
			}
			if tc.fast {
				if err := applyFastTarget(context.Background(), d, "selected", &target); err != nil {
					t.Fatal(err)
				}
			}
			if !tc.failOpen {
				choice = selectionKey(target) + ":none"
			}
			decisionWire := v1.ProviderWire_PROVIDER_WIRE_JEV_DECISION
			putRoutingProvider(t, a, sc, providerData{Metadata: &v1.ProviderConnection{Id: "decision", OrganizationId: sc.OrganizationID, ProviderId: "custom", BaseUrl: upstream.URL + "/systemone", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, State: v1.ConnectionState_CONNECTION_STATE_READY, Configuration: &v1.ProviderConfiguration{Wire: &decisionWire}}, Secret: "decision-key"})
			cfg := &v1.JevDecisionConfig{DecisionConnectionId: "decision", DecisionServiceModel: "tev1", SessionPinTtlSeconds: 60}
			if tc.modelBackend {
				cfg.DecisionModel = "decision-route"
				putRoutingProvider(t, a, sc, providerData{Metadata: &v1.ProviderConnection{Id: "model-decision", OrganizationId: sc.OrganizationID, ProviderId: "custom", BaseUrl: upstream.URL + "/v1", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, State: v1.ConnectionState_CONNECTION_STATE_READY}, Secret: "decision-key"})
				putRoutingRoute(t, a, sc, &v1.Route{Id: "decision-route", ModelId: "decision-route", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: "model-decision"}, ModelId: "decision-model"}}})
			}
			targets := []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: "selected"}, ModelId: "selected"}}
			if tc.mixed {
				control := providerData{Metadata: &v1.ProviderConnection{Id: "control", OrganizationId: sc.OrganizationID, ProviderId: "custom", BaseUrl: upstream.URL + "/v1", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, State: v1.ConnectionState_CONNECTION_STATE_READY}, Secret: "control-key"}
				if tc.fast {
					control.Metadata.Configuration = &v1.ProviderConfiguration{Policy: &v1.ProviderPolicyOverrides{SupportsServiceTier: proto.Bool(true)}}
				}
				putRoutingProvider(t, a, sc, control)
				targets = append(targets, &v1.RouteTarget{Target: &v1.RouteTarget_ConnectionId{ConnectionId: "control"}, ModelId: "control"})
			}
			putRoutingRoute(t, a, sc, &v1.Route{Id: "combo", ModelId: "combo", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_JEV, JevDecision: cfg, ReasoningRetention: retention, Targets: targets})
			for i := 0; i < 2; i++ {
				instance := &inferenceAdapter{s: a.s, telemetry: a.telemetry}
				h, err := inference.New(inference.Config{Authorizer: instance, Router: instance, Credentials: instance, Negotiator: instance, Usage: instance, Destinations: inference.DestinationPolicy{ApprovedOrigins: []string{upstream.URL}}})
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"Choose"}]}`, tc.selector)))
				req.Header.Set("Authorization", "Bearer "+key)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("session-id", "semantic-selector-session")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, req)
				if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "selected target") {
					t.Fatalf("inference HTTP %d: %s", w.Code, w.Body.String())
				}
			}
			wantDecisions := int32(1)
			if tc.failOpen {
				wantDecisions = 2 // Invalid decisions fail open, but do not create a reusable pin.
			}
			if decisions.Load() != wantDecisions || sends.Load() != 2 {
				t.Fatalf("decision=%d inference=%d; pin or dispatch failed", decisions.Load(), sends.Load())
			}
			if tc.fast {
				d.Metadata.Configuration.Policy.SupportsServiceTier = proto.Bool(false)
				putRoutingProvider(t, a, sc, d)
				instance := &inferenceAdapter{s: a.s, telemetry: a.telemetry}
				h, err := inference.New(inference.Config{Authorizer: instance, Router: instance, Credentials: instance, Negotiator: instance, Usage: instance, Destinations: inference.DestinationPolicy{ApprovedOrigins: []string{upstream.URL}}})
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"combo--fast","messages":[{"role":"user","content":"Choose"}]}`))
				req.Header.Set("Authorization", "Bearer "+key)
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, req)
				if w.Code < 400 || sends.Load() != 2 {
					t.Fatalf("removed Fast support still dispatched: HTTP %d, sends %d", w.Code, sends.Load())
				}
			}
		})
	}
}

func TestJEVNativeMessagesPreferenceSurvivesEligibility(t *testing.T) {
	for _, preference := range []*bool{nil, proto.Bool(false), proto.Bool(true)} {
		name := "default"
		if preference != nil {
			name = fmt.Sprint(*preference)
		}
		t.Run(name, func(t *testing.T) {
			a, sc, key := dispatchFixture(t)
			ctx := context.Background()
			d := providerData{Metadata: &v1.ProviderConnection{Id: "anthropic", OrganizationId: sc.OrganizationID, ProviderId: "anthropic", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH, State: v1.ConnectionState_CONNECTION_STATE_READY}, Credential: &providers.Credential{Scope: sc, Provider: "anthropic", AccessToken: "fixture-token"}}
			putRoutingProvider(t, a, sc, d)
			pool := &v1.Pool{Id: "pool", NativeOptions: &v1.NativePoolOptions{Enabled: proto.Bool(true), NativeMessages: preference}, Members: []*v1.PoolMember{{ConnectionId: d.Metadata.Id}}}
			raw, err := json.Marshal(pool)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.s.PutPrivate(ctx, sc, string(poolKind), pool.Id, raw); err != nil {
				t.Fatal(err)
			}
			wire := v1.ProviderWire_PROVIDER_WIRE_JEV_DECISION
			putRoutingProvider(t, a, sc, providerData{Metadata: &v1.ProviderConnection{Id: "decision", OrganizationId: sc.OrganizationID, ProviderId: "custom", BaseUrl: "https://decision.example/systemone", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, State: v1.ConnectionState_CONNECTION_STATE_READY, Configuration: &v1.ProviderConfiguration{Wire: &wire}}, Secret: "decision-key"})
			route := &v1.Route{Id: "combo", ModelId: "combo", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_JEV, JevDecision: &v1.JevDecisionConfig{DecisionConnectionId: "decision", DecisionServiceModel: "tev1"}, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_PoolId{PoolId: pool.Id}, ModelId: "claude-opus-5"}}}
			putRoutingRoute(t, a, sc, route)
			p := admitRouting(t, a, key)
			target, err := targetForContext(ctx, d, "claude-opus-5")
			if err != nil {
				t.Fatal(err)
			}
			target.ForceMessagesTranslation = preference != nil && !*preference
			_, version, err := a.providerVersion(ctx, sc, d.Metadata.Id)
			if err != nil {
				t.Fatal(err)
			}
			request := store.SelectionRequest{GroupID: "route/combo/pool/pool", Model: target.Model, Version: selectionVersion(struct {
				Route *v1.Route
				Pool  *v1.Pool
			}{route, pool}), Candidates: []store.SelectionCandidate{{ID: d.Metadata.Id, Version: version}}}
			selection := routeSelection{request: request, target: target, provider: "anthropic", pool: pool}
			selections := map[string]routeSelection{selectionKey(target): selection}
			out, err := a.selectJEVRouteInput(ctx, p, route, inference.Route{Targets: []inference.Target{target}}, selections, json.RawMessage(`{"model":"combo","messages":[{"role":"user","content":"hello"}]}`), inference.DestinationPolicy{}, nil)
			if err != nil || len(out.Targets) != 1 || out.Targets[0].ForceMessagesTranslation != target.ForceMessagesTranslation {
				t.Fatalf("JEV eligibility lost pool translation: target=%+v err=%v", out.Targets, err)
			}
			credential, err := credentialFor(ctx, d, out.Targets[0])
			if err != nil || credential.AccountID != d.Metadata.Id {
				t.Fatalf("selected credential lost pool translation: %+v %v", credential, err)
			}
		})
	}
}
