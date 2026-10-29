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
		name, path, body, evidence, finalModel string
	}{
		{"chat-pin", "/v1/chat/completions", `{"model":"combo","messages":[{"role":"user","content":"Choose the best target"}]}`, "Choose the best target", "second"},
		{"chat-fail-open", "/v1/chat/completions", `{"model":"combo","messages":[{"role":"user","content":"Choose the best target"}]}`, "Choose the best target", "first"},
		{"chat-decision-error", "/v1/chat/completions", `{"model":"combo","messages":[{"role":"user","content":"Choose the best target"}]}`, "Choose the best target", "first"},
		{"messages-image", "/v1/messages", `{"model":"combo","max_tokens":32,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL/nwAAAABJRU5ErkJggg=="}}]}]}`, "has_image", "second"},
		{"messages-tool", "/v1/messages", `{"model":"combo","max_tokens":32,"messages":[{"role":"user","content":"Fix the test"},{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"exec_command","input":{"secret":"HIDDEN_ARGUMENT"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","content":"1 fail: expected 2 got 3"}]}]}`, "1 fail: expected 2 got 3", "second"},
	}
	for _, request := range requests {
		backends := []bool{false, true}
		if request.name == "chat-decision-error" {
			backends = []bool{true}
		}
		for _, modelBackend := range backends {
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
					text := "selected " + request.finalModel
					if b["model"] == "decision-model" {
						decisionSends.Add(1)
						raw, _ := json.Marshal(b)
						decisionEvidence = string(raw)
						if request.name == "chat-decision-error" {
							w.WriteHeader(http.StatusServiceUnavailable)
							_, _ = w.Write([]byte(`{"error":{"message":"fixture unavailable"}}`))
							return
						}
						raw, _ = json.Marshal(map[string]string{"choice": choice})
						text = string(raw)
					} else {
						inferenceSends.Add(1)
						if b["model"] != request.finalModel {
							t.Error("wrong inference model")
						}
					}
					usage := map[string]int{"prompt_tokens": 20, "completion_tokens": 10, "total_tokens": 30}
					if b["model"] == "decision-model" {
						usage = map[string]int{"prompt_tokens": 2, "completion_tokens": 1, "total_tokens": 3}
					}
					raw, _ := json.Marshal(map[string]any{"id": "response", "object": "chat.completion", "model": b["model"], "choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": text}}}, "usage": usage})
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
				if request.name == "chat-fail-open" {
					choice = "invalid-choice"
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
					if w.Code != 200 || !strings.Contains(w.Body.String(), "selected "+request.finalModel) {
						t.Fatalf("composition HTTP %d: %s", w.Code, w.Body.String())
					}
				}
				if decisionSends.Load() != 1 || inferenceSends.Load() != int32(attempts) || !strings.Contains(decisionEvidence, request.evidence) || strings.Contains(decisionEvidence, "HIDDEN_ARGUMENT") || strings.Contains(decisionEvidence, "iVBORw0KGgo") {
					t.Fatalf("decision=%d inference=%d; successful pin was not reused", decisionSends.Load(), inferenceSends.Load())
				}
				rows, _, err := a.s.QueryUsage(ctx, sc, store.UsageFilter{UserID: sc.UserID})
				wantRows := attempts
				if modelBackend {
					wantRows++
				}
				if err != nil || len(rows) != wantRows {
					t.Fatalf("usage rows=%d, want=%d: %v", len(rows), wantRows, err)
				}
				decisionRows, finalRows := 0, 0
				for _, row := range rows {
					switch row.Model {
					case "decision-model":
						decisionRows++
						if request.name == "chat-decision-error" {
							if row.InputTokens != nil || row.OutputTokens != nil || row.Outcome == store.OutcomeSucceeded {
								t.Fatalf("unknown decision usage: %+v", row)
							}
						} else if row.InputTokens == nil || *row.InputTokens != 2 || row.OutputTokens == nil || *row.OutputTokens != 1 {
							t.Fatalf("decision usage: %+v", row)
						}
					case request.finalModel:
						finalRows++
						if row.InputTokens == nil || *row.InputTokens != 20 || row.OutputTokens == nil || *row.OutputTokens != 10 {
							t.Fatalf("final usage: %+v", row)
						}
					default:
						t.Fatalf("unexpected usage model: %s", row.Model)
					}
				}
				if finalRows != attempts || decisionRows != wantRows-attempts {
					t.Fatalf("decision rows=%d final rows=%d", decisionRows, finalRows)
				}
				aggregate, err := a.s.QueryAggregate(ctx, sc, store.UsageFilter{UserID: sc.UserID})
				knownDecisionRows := decisionRows
				if request.name == "chat-decision-error" {
					knownDecisionRows = 0
				}
				if err != nil || aggregate.Requests != int64(wantRows) || aggregate.InputTokens != int64(20*attempts+2*knownDecisionRows) || aggregate.OutputTokens != int64(10*attempts+knownDecisionRows) || aggregate.MissingUsage != int64(decisionRows-knownDecisionRows) {
					t.Fatalf("aggregate: %+v, %v", aggregate, err)
				}
			})
		}
	}
}
