// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

func jevTestCandidates() []jevCandidate {
	return []jevCandidate{{Key: "first", Provider: "p", Model: "m1", Efforts: []jevEffort{jevLow}}, {Key: "second", Provider: "p", Model: "m2", Efforts: []jevEffort{jevHigh}}}
}
func TestJEVBoundedStateAndProviderQuestions(t *testing.T) {
	body := json.RawMessage(`{"input":[{"role":"user","content":"<system-reminder>private envelope</system-reminder>Fix the failing test"},{"role":"assistant","content":[{"type":"output_text","text":"I will run the test first."}]},{"type":"function_call","call_id":"c","name":"exec_command","arguments":"secret arguments"},{"type":"function_call_output","call_id":"c","output":"1 fail: expected 2 got 3"}],"instructions":"system secret","api_key":"credential"}`)
	candidates := jevTestCandidates()
	state := buildJEVState(body, candidates)
	expected := map[string]any{"task": "Fix the failing test", "signals": map[string]any{"has_image": false, "tool_history": true}, "step": map[string]any{"type": "tool_step", "last_tool_output_tail": "1 fail: expected 2 got 3", "tool_call": map[string]any{"name": "exec_command"}}, "previous_assistant": "I will run the test first."}
	if !reflect.DeepEqual(state, expected) {
		t.Fatalf("state=%v", state)
	}
	choices, err := jevChoices(candidates)
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{jevCanonicalURL, "https://custom.test/systemone", "https://ai-gateway.vercel.sh/v1/decisions"} {
		raw, err := jevRequest("jev-latest", endpoint, endpoint != jevCanonicalURL, state, choices)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"private envelope", "secret arguments", "system secret", "credential"} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("decision leaked excluded content")
			}
		}
		var request map[string]any
		_ = json.Unmarshal(raw, &request)
		if endpoint == jevCanonicalURL {
			criteria := request["questions"].(map[string]any)["route"].(map[string]any)["criteria"].(map[string]any)
			if criteria["second:high"].(map[string]any)["reasoning_effort"] != "high" {
				t.Fatal("missing structured criterion")
			}
		}
		if endpoint == "https://custom.test/systemone" {
			criteria := request["questions"].(map[string]any)["route"].(map[string]any)["criteria"].(map[string]any)
			if _, ok := criteria["first:low"].(string); !ok {
				t.Fatal("SystemOne received structured criteria")
			}
		}
		if strings.Contains(endpoint, "vercel") {
			if !strings.Contains(request["input"].(string), "Tool Output: 1 fail") {
				t.Fatal("missing decisions evidence")
			}
		}
	}
	if got := jevTask(strings.Repeat("x", 1000)); len(got) != 500 || !strings.Contains(got, "\n[...]\n") {
		t.Fatal("task clipping")
	}
	if jevVisible("<system-reminder>unclosed secret", true) != "" {
		t.Fatal("unclosed envelope leaked")
	}
	if jevVisible("<codex_internal_context>goal</codex_internal_context>", true) != "goal" {
		t.Fatal("goal lost")
	}
	if jevVisible("<system-reminder><codex_internal_context>not goal</codex_internal_context></system-reminder>", true) != "" {
		t.Fatal("nested reminder became task")
	}
}
func TestJEVResponseAllowlistAndDistribution(t *testing.T) {
	choices, _ := jevChoices(jevTestCandidates())
	good := []string{`{"answers":{"route":{"choice":"second:high","probabilities":{"first:low":0.1,"second:high":0.9}}}}`, `{"answers":[{"name":"route","value":"second:high","probabilities":[{"choice":"first:low","probability":0.1},{"choice":"second:high","probability":0.9}]}]}`}
	for _, raw := range good {
		got, err := parseJEVDecision([]byte(raw), choices)
		if err != nil || got.TargetKey != "second" || got.Effort != jevHigh || got.ChosenProbability == nil {
			t.Fatalf("decision=%v %v", got, err)
		}
	}
	bad := []string{`{"answers":{"route":{"choice":"foreign:none"}}}`, `{"answers":{"route":{"choice":"second:high","probabilities":{"second:high":1}}}}`, `{"answers":{"route":{"choice":"first:low","probabilities":{"first:low":0.1,"second:high":0.9}}}}`, `{"answers":[{"name":"route","choice":"first:low"},{"name":"route","choice":"second:high"}]}`}
	for _, raw := range bad {
		if _, err := parseJEVDecision([]byte(raw), choices); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, text := range []string{`{"choice":"second:high"}`, "<think>internal</think>\n```json\n{\"choice\":\"second:high\"}\n```", `"second:high"`} {
		if got, err := parseJEVModel(text, choices); err != nil || got.candidate.Key != "second" {
			t.Fatal("model decision rejected")
		}
	}
	if _, err := parseJEVModel(strings.Repeat("x", 4097), choices); err == nil {
		t.Fatal("model output cap bypassed")
	}
}
func TestJEVEndpointIsolation(t *testing.T) {
	for _, raw := range []string{"https://host.test/route/", "http://127.0.0.1:11434/v1/systemone/", "https://ai-gateway.vercel.sh/v1/systemone"} {
		if _, err := jevEndpointURL(raw); err != nil {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{"https://host.test?", "https://user@host.test/path", "http://public.test/systemone", "http://127.0.0.1/api/chat", "https://host.test/#"} {
		if _, err := jevEndpointURL(raw); err == nil {
			t.Fatal(raw)
		}
	}
	d := providerData{Metadata: &v1.ProviderConnection{ProviderId: "jev", BaseUrl: "https://attacker.test/systemone"}, Secret: "typesafe-secret"}
	if _, _, _, err := jevEndpoint(d, "model"); err == nil {
		t.Fatal("retargeted TypeSafe credential accepted")
	}
}
func TestJEVServiceStoreBoundedFailOpenAndRevocation(t *testing.T) {
	a, sc, key := dispatchFixture(t)
	p := admitRouting(t, a, key)
	var sends atomic.Int32
	var mode atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		if r.URL.Path != "/systemone" || r.Header.Get("Authorization") != "Bearer own-service-key" {
			t.Error("wrong destination credential")
		}
		b, _ := io.ReadAll(r.Body)
		if len(b) > jevMaxBytes || strings.Contains(string(b), "system-secret") {
			t.Error("unsafe decision request")
		}
		switch mode.Load() {
		case 1:
			w.WriteHeader(500)
		case 2:
			_, _ = io.WriteString(w, strings.Repeat("x", jevMaxBytes+1))
		default:
			_, _ = io.WriteString(w, `{"answers":{"route":{"choice":"second:high"}}}`)
		}
	}))
	defer server.Close()
	wire := v1.ProviderWire_PROVIDER_WIRE_JEV_DECISION
	d := providerData{Metadata: &v1.ProviderConnection{Id: "decision", ProviderId: "custom", OrganizationId: sc.OrganizationID, BaseUrl: server.URL + "/systemone", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, State: v1.ConnectionState_CONNECTION_STATE_READY, Configuration: &v1.ProviderConfiguration{Wire: &wire}}, Secret: "own-service-key"}
	putRoutingProvider(t, a, sc, d)
	o := jevOptions{ConnectionID: "decision", Model: "tev1", Body: json.RawMessage(`{"input":"choose model","instructions":"system-secret"}`), Candidates: jevTestCandidates(), Eligible: func(context.Context, jevCandidate) (bool, error) { return true, nil }}
	policy := inference.DestinationPolicy{ApprovedOrigins: []string{server.URL}}
	budget := 0
	ctx := inference.WithPhysicalSendBudget(context.Background(), func(c context.Context, start func(context.Context) error) error { budget++; return start(c) })
	got, err := a.resolveJEV(ctx, p, o, policy)
	if err != nil || got.TargetKey != "second" || got.Gate != jevApply || budget != 1 {
		t.Fatalf("service result=%v error=%v sends=%d", got, err, budget)
	}
	mode.Store(1)
	got, err = a.resolveJEV(ctx, p, o, policy)
	if err != nil || got.TargetKey != "first" || got.Gate != jevHTTP {
		t.Fatalf("fallback=%v %v", got, err)
	}
	mode.Store(2)
	got, err = a.resolveJEV(ctx, p, o, policy)
	if err != nil || got.TargetKey != "first" || got.Gate != jevMalformed {
		t.Fatalf("bounded fallback=%v %v", got, err)
	}
	mode.Store(0)
	before := sends.Load()
	denied := inference.WithPhysicalSendBudget(context.Background(), func(context.Context, func(context.Context) error) error {
		return &inference.Error{Status: 429, Code: "request_send_budget_exhausted"}
	})
	if _, err = a.resolveJEV(denied, p, o, policy); err == nil || sends.Load() != before {
		t.Fatal("budget denial fell open or sent")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = a.resolveJEV(canceled, p, o, policy); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	// Changing eligibility after response cannot authorize the selected target.
	checks := 0
	o.Eligible = func(_ context.Context, c jevCandidate) (bool, error) {
		checks++
		return !(checks > 2 && c.Key == "second"), nil
	}
	got, err = a.resolveJEV(ctx, p, o, policy)
	if err != nil || got.TargetKey != "first" || got.Gate != jevInvalid {
		t.Fatalf("changed eligibility=%v %v", got, err)
	}
}
func TestJEVRouteValidationRejectsSelfAndDecisionCombos(t *testing.T) {
	a, sc, _ := dispatchFixture(t)
	ordinary := &v1.Route{Id: "ordinary", ModelId: "ordinary", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: []*v1.RouteTarget{{ModelId: "concrete"}}}
	putRoutingRoute(t, a, sc, ordinary)
	proposed := &v1.Route{Id: "combo", ModelId: "combo", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_JEV, Targets: []*v1.RouteTarget{{ModelId: "concrete"}}, JevDecision: &v1.JevDecisionConfig{DecisionModel: "ordinary"}}
	if err := validateJEVRoute(context.Background(), a.s, sc, proposed); err != nil {
		t.Fatal(err)
	}
	proposed.JevDecision.DecisionModel = "combo"
	if err := validateJEVRoute(context.Background(), a.s, sc, proposed); err == nil {
		t.Fatal("self accepted")
	}
	putRoutingRoute(t, a, sc, proposed)
	proposed = &v1.Route{Id: "other", ModelId: "other", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_JEV, Targets: []*v1.RouteTarget{{ModelId: "concrete"}}, JevDecision: &v1.JevDecisionConfig{DecisionModel: "combo"}}
	if err := validateJEVRoute(context.Background(), a.s, sc, proposed); err == nil {
		t.Fatal("combo chain accepted")
	}
	if jevTimeoutValue(0) != 4*time.Second || jevTimeoutValue(121*time.Second) != 4*time.Second {
		t.Fatal("timeout default changed")
	}
}

func TestJEVModelBackendRechecksRouteAndUsesBoundedEnvelope(t *testing.T) {
	a, sc, key := dispatchFixture(t)
	p := admitRouting(t, a, key)
	route := &v1.Route{Id: "decision-model", ModelId: "ordinary", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: []*v1.RouteTarget{{ModelId: "model"}}}
	putRoutingRoute(t, a, sc, route)
	calls := 0
	o := jevOptions{DecisionModel: "ordinary", Candidates: jevTestCandidates(), Body: json.RawMessage(`{"input":"user task","instructions":"private system"}`), Eligible: func(context.Context, jevCandidate) (bool, error) { return true, nil }, InvokeModel: func(ctx context.Context, alias string, raw json.RawMessage) (string, error) {
		calls++
		var body map[string]any
		if json.Unmarshal(raw, &body) != nil || alias != "ordinary" || body["store"] != false || body["max_output_tokens"] != float64(1024) || strings.Contains(string(raw), "private system") {
			t.Fatal("unsafe decision model envelope")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("model invocation has no timeout")
		}
		return `{"choice":"second:high"}`, nil
	}}
	got, err := a.resolveJEV(context.Background(), p, o, inference.DestinationPolicy{})
	if err != nil || got.TargetKey != "second" || got.Backend != "model" || calls != 1 {
		t.Fatalf("model decision=%v %v", got, err)
	}
	route.Strategy = v1.RoutingStrategy_ROUTING_STRATEGY_JEV
	route.JevDecision = &v1.JevDecisionConfig{DecisionModel: "ordinary"}
	putRoutingRoute(t, a, sc, route)
	got, err = a.resolveJEV(context.Background(), p, o, inference.DestinationPolicy{})
	if err != nil || got.TargetKey != "first" || got.Gate != jevInvalid || calls != 1 {
		t.Fatal("nested decision route invoked or did not fail open")
	}
}
