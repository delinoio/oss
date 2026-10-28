// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/core"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/inference/policy"
	"github.com/delinoio/oss/packages/aiprouter/providers"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

func fastTestData(t *testing.T, provider string) providerData {
	t.Helper()
	preset, ok := providers.Lookup(provider)
	if !ok && provider != "custom" {
		t.Fatal("missing preset")
	}
	d := providerData{Metadata: &v1.ProviderConnection{Id: "account", ProviderId: provider, BaseUrl: preset.BaseURL, AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, State: v1.ConnectionState_CONNECTION_STATE_READY}, Secret: "fixture"}
	if provider == "custom" {
		d.Metadata.BaseUrl = "https://provider.example/v1"
	}
	if preset.Auth == providers.OAuth || preset.Auth == providers.Forward {
		d.Metadata.AuthKind = v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH
		d.Credential = &providers.Credential{AccessToken: "fixture", Provider: provider, Scope: core.Scope{OrganizationID: "org", UserID: "user", Generation: 1}}
	}
	return d
}

func TestRoutingFastAuthoritativePolicyMatrix(t *testing.T) {
	for _, tc := range []struct {
		provider, model string
		optIn           bool
		want            bool
		kind            inference.FastWireKind
	}{
		{"openai", "gpt-6-sol", false, true, inference.FastServiceTier},
		{"openai-apikey", "gpt-6-sol", false, true, inference.FastServiceTier},
		{"xai", "grok-4.7", false, true, inference.FastModelVariant},
		{"xai", "grok-4.6", false, true, inference.FastServiceTier},
		{"xai", "future-model", false, false, inference.FastServiceTier},
		{"cursor", "claude-opus-5", false, true, inference.FastCursorVariant},
		{"cursor", "composer-2.5-fast", false, false, inference.FastCursorVariant},
		{"anthropic", "claude-opus-5", false, false, inference.FastAnthropicSpeed},
		{"anthropic", "claude-opus-5", true, true, inference.FastAnthropicSpeed},
		{"anthropic2", "claude-opus-4-8", true, true, inference.FastAnthropicSpeed},
		{"anthropic-apikey", "claude-opus-5-5", true, true, inference.FastAnthropicSpeed},
		{"anthropic-apikey", "claude-sonnet-4-6", true, false, inference.FastAnthropicSpeed},
		{"openrouter", "openai/gpt-6-sol", false, true, inference.FastServiceTier},
		{"deepseek", "deepseek-chat", false, false, inference.FastServiceTier},
		{"volcengine-coding-plan", "ark-code-latest", false, false, inference.FastServiceTier},
		{"volcengine-agent-plan", "ark-code-latest", false, false, inference.FastServiceTier},
	} {
		t.Run(tc.provider+"/"+tc.model+map[bool]string{true: "/opt-in"}[tc.optIn], func(t *testing.T) {
			d := fastTestData(t, tc.provider)
			if tc.optIn {
				enabled := true
				d.Metadata.Configuration = &v1.ProviderConfiguration{Policy: &v1.ProviderPolicyOverrides{FastEnabled: &enabled}}
			}
			_, resolved, err := resolvedProviderRoute(context.Background(), d, tc.model)
			if err != nil {
				t.Fatal(err)
			}
			fast, _ := resolvedFastPolicy(resolved)
			if fast.Eligible != tc.want || fast.Kind != tc.kind {
				t.Fatalf("fast=%+v want eligible=%v kind=%s", fast, tc.want, tc.kind)
			}
			target, err := targetFor(d, tc.model)
			if err != nil {
				t.Fatal(err)
			}
			err = applyFastTarget(context.Background(), d, tc.model, &target)
			if (err == nil) != tc.want {
				t.Fatalf("fast target accepted=%v want=%v", err == nil, tc.want)
			}
			if tc.want && (!fastTargetRequested(target) || target.Model != tc.model || target.FastPolicy == nil) {
				t.Fatal("fast lost logical model or intent")
			}
			if tc.want && tc.kind == inference.FastModelVariant && target.FastPolicy.Model != "grok-4.7-build-fast" {
				t.Fatal("xAI fast lane lost")
			}
		})
	}
}

func TestRoutingFastCustomEvidence(t *testing.T) {
	d := fastTestData(t, "custom")
	if got := fastModelEvidence(d, "wire-model", time.Now()); got != nil {
		t.Fatal("custom unknown invented capability")
	}
	yes, no := true, false
	d.Metadata.Configuration = &v1.ProviderConfiguration{Policy: &v1.ProviderPolicyOverrides{SupportsServiceTier: &yes}}
	if got := fastModelEvidence(d, "wire-model", time.Now()); got == nil || !*got {
		t.Fatal("custom true omitted")
	}
	d.Metadata.Configuration.Policy.ModelSupportsServiceTier = map[string]bool{"WIRE-MODEL": false}
	if got := fastModelEvidence(d, "wire-model", time.Now()); got == nil || *got {
		t.Fatal("folded model false lost")
	}
	d.Metadata.Configuration.Policy.SupportsServiceTier = &no
	d.Metadata.Configuration.Policy.ModelSupportsServiceTier = map[string]bool{"wire-model": true}
	if got := fastModelEvidence(d, "wire-model", time.Now()); got == nil || *got {
		t.Fatal("provider false overridden by model true")
	}
	d.Metadata.Configuration.Policy.SupportsServiceTier = &yes
	d.Metadata.Configuration.Policy.FastEnabled = &no
	if got := fastModelEvidence(d, "wire-model", time.Now()); got == nil || *got {
		t.Fatal("fast off ignored")
	}
}

func TestRoutingFastDestinationAndAuthEvidence(t *testing.T) {
	d := fastTestData(t, "xai")
	d.Metadata.AuthKind = v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY
	d.Credential = nil
	target, err := targetFor(d, "grok-4.7")
	if err != nil {
		t.Fatal(err)
	}
	if err = applyFastTarget(context.Background(), d, "grok-4.7", &target); err != nil {
		t.Fatal(err)
	}
	if target.FastPolicy.Kind != inference.FastServiceTier || target.FastPolicy.Model != "" {
		t.Fatal("API-key auth incorrectly selected OAuth model lane")
	}
	d = fastTestData(t, "openrouter")
	d.Metadata.BaseUrl = "https://provider.example/v1"
	if got := fastModelEvidence(d, "openai/gpt-6-sol", time.Now()); got != nil {
		t.Fatal("custom endpoint inherited canonical OpenRouter tier capability")
	}
}

func TestRoutingFastWireAuthority(t *testing.T) {
	for _, raw := range []string{
		`{"supportsServiceTier":true,"fastWire":null}`,
		`{"supportsServiceTier":true,"fastWire":{"kind":"anthropic-speed","canonicalToWire":{"priority":"fast"}}}`,
	} {
		resolved, err := policy.Resolve(policy.Config{ProviderID: "custom", Model: "m", BaseURL: "https://provider.example/v1", Wire: providers.Chat, Overrides: json.RawMessage(raw)})
		if err != nil {
			t.Fatal(err)
		}
		fast, _ := resolvedFastPolicy(&resolved)
		if fast.Eligible {
			t.Fatalf("unavailable final fast wire accepted: %s", raw)
		}
	}
}

func TestRoutingFastNativeCatalogEvidence(t *testing.T) {
	d := fastTestData(t, "openai")
	now := time.Now()
	for _, tc := range []struct {
		raw  string
		age  time.Duration
		want bool
	}{
		{`{"slug":"gpt-6-sol","additional_speed_tiers":["fast"]}`, 0, true},
		{`{"slug":"gpt-6-sol","additional_speed_tiers":[]}`, 0, false},
		{`{"slug":"another","additional_speed_tiers":["fast"]}`, 0, false},
		{`{"slug":"gpt-6-sol","additional_speed_tiers":["fast"]}`, 7 * time.Hour, false},
		{`{"slug":"gpt-6-sol","additional_speed_tiers":["fast"]}`, -time.Hour, false},
	} {
		d.CodexCatalog = []json.RawMessage{json.RawMessage(tc.raw)}
		d.ModelsDiscoveredAt = now.Add(-tc.age)
		got := fastModelEvidence(d, "gpt-6-sol", now)
		if got == nil || *got != tc.want {
			t.Fatalf("native fast evidence=%v want=%v", got, tc.want)
		}
	}
}

func TestRoutingFastComposedServiceTier(t *testing.T) {
	var calls atomic.Int32
	f := newProviderAcceptance(t, "custom", "wire-model", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body := providerAcceptanceBody(t, r)
		if body["model"] != "wire-model" || body["service_tier"] != "priority" || r.URL.Path != "/v1/chat/completions" {
			t.Error("advertised fast did not serialize final model/tier/endpoint")
		}
		providerAcceptanceResponse(w, providerAcceptanceChat)
	})
	yes, no := true, false
	f.data.Metadata.Configuration = &v1.ProviderConfiguration{Policy: &v1.ProviderPolicyOverrides{SupportsServiceTier: &yes}}
	f.save()
	a := &inferenceAdapter{s: f.s, telemetry: f.srv.telemetry}
	p := admitRouting(t, a, f.key)
	models, err := a.clientModels(context.Background(), f.scope)
	if err != nil {
		t.Fatal(err)
	}
	advertised := false
	for _, model := range models {
		if model.Id == "fixture" && model.FastRowAvailable != nil && *model.FastRowAvailable {
			advertised = true
		}
	}
	if !advertised {
		t.Fatal("authoritative fast not advertised")
	}
	for _, selector := range []string{"fixture--fast", cliRouteAlias("fixture--fast"), desktopRouteAlias("fixture--fast")} {
		body, _ := json.Marshal(map[string]any{"model": selector, "messages": []any{map[string]any{"role": "user", "content": "hello"}}, "service_tier": "default"})
		providerAcceptanceStatus(t, f.call("/v1/chat/completions", string(body), nil), 200)
	}
	route, err := a.Resolve(context.Background(), p, "fixture--fast", inference.Generate)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := a.Select(context.Background(), p, route.Targets[0], "")
	if err != nil || credential.CredentialVersion == "" {
		t.Fatal("selected fast credential lacks generation fence")
	}
	f.data.Metadata.Configuration.Policy.SupportsServiceTier = &no
	f.save()
	sent := false
	if err := a.Dispatch(context.Background(), p, func(context.Context) error { sent = true; return nil }); err == nil || sent {
		t.Fatal("stale fast credential crossed physical dispatch fence")
	}
	if _, err := a.SelectBound(context.Background(), p, route.Targets[0], f.data.Metadata.Id); err == nil {
		t.Fatal("replaced fast policy retained old selected authority")
	}
	denied := f.call("/v1/chat/completions", `{"model":"fixture--fast","messages":[{"role":"user","content":"hello"}]}`, nil)
	if denied.Code < 400 || calls.Load() != 3 {
		t.Fatalf("unsupported fast dispatched: status=%d calls=%d", denied.Code, calls.Load())
	}
	f.data.Metadata.Configuration = nil
	f.save()
	denied = f.call("/v1/chat/completions", `{"model":"fixture--fast","messages":[{"role":"user","content":"hello"}]}`, nil)
	if denied.Code < 400 || calls.Load() != 3 {
		t.Fatal("unknown custom policy dispatched synthetic fast")
	}
}

func TestRoutingFastComposedExplicitSelector(t *testing.T) {
	var calls atomic.Int32
	f := newProviderAcceptance(t, "custom", "wire-model", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body := providerAcceptanceBody(t, r)
		if body["model"] != "real--fast" || body["service_tier"] != nil {
			t.Error("literal selector was rewritten as synthetic fast")
		}
		providerAcceptanceResponse(w, providerAcceptanceChat)
	})
	putRoutingRoute(t, &inferenceAdapter{s: f.s}, f.scope, &v1.Route{Id: "literal", ModelId: "fixture--fast", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: f.data.Metadata.Id}, ModelId: "real--fast"}}})
	for _, selector := range []string{"fixture--fast", cliRouteAlias("fixture--fast"), desktopRouteAlias("fixture--fast")} {
		body, _ := json.Marshal(map[string]any{"model": selector, "messages": []any{map[string]any{"role": "user", "content": "hello"}}})
		providerAcceptanceStatus(t, f.call("/v1/chat/completions", string(body), nil), 200)
	}
	if calls.Load() != 3 {
		t.Fatal("literal selector sends missing")
	}
}

func TestRoutingFastComposedFinalWireAndCredential(t *testing.T) {
	var calls atomic.Int32
	f := newProviderAcceptance(t, "custom", "wire-model", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body := providerAcceptanceBody(t, r)
		if body["model"] != "wire-model" || body["service_tier"] != "priority" || r.URL.Path != "/v1/custom-responses" {
			t.Error("fast final Responses wire/endpoint/tier lost")
		}
		if r.Header.Get("Authorization") == "" {
			t.Error("selected final-wire credential missing")
		}
		providerAcceptanceResponse(w, providerAcceptanceResponses)
	})
	yes := true
	path := "/custom-responses"
	f.data.Metadata.Configuration = &v1.ProviderConfiguration{Policy: &v1.ProviderPolicyOverrides{SupportsServiceTier: &yes}, ModelWires: map[string]v1.ProviderWire{"wire-model": v1.ProviderWire_PROVIDER_WIRE_RESPONSES}, ResponsesPath: &path}
	f.save()
	providerAcceptanceStatus(t, f.call("/v1/responses", `{"model":"fixture--fast","input":"hello","service_tier":"default"}`, nil), 200)
	if calls.Load() != 1 {
		t.Fatal("fast Responses send missing")
	}
}

func TestRoutingFastSelectorPrecedence(t *testing.T) {
	base := &v1.Route{Id: "base", ModelId: "route/with~escape", Enabled: true}
	literal := &v1.Route{Id: "literal", ModelId: base.ModelId + fastRowSuffix, Enabled: true}
	for _, alias := range []func(string) string{func(s string) string { return s }, cliRouteAlias, desktopRouteAlias} {
		for _, routes := range [][]*v1.Route{{base, literal}, {literal, base}} {
			got, fast, err := chooseFastRoute(routes, alias(literal.ModelId))
			if err != nil || got != literal || fast {
				t.Fatalf("explicit selector lost precedence: route=%v fast=%v err=%v", got, fast, err)
			}
		}
		got, fast, err := chooseFastRoute([]*v1.Route{base}, alias(base.ModelId+fastRowSuffix))
		if err != nil || got != base || !fast {
			t.Fatalf("synthetic alias failed: route=%v fast=%v err=%v", got, fast, err)
		}
		// An explicitly configured suffix-shaped base can itself have a fast row.
		got, fast, err = chooseFastRoute([]*v1.Route{literal}, alias(literal.ModelId+fastRowSuffix))
		if err != nil || got != literal || !fast {
			t.Fatalf("explicit suffix base failed: route=%v fast=%v err=%v", got, fast, err)
		}
	}
	for _, selector := range []string{base.ModelId + "-fast", base.ModelId + "--high--fast", base.ModelId + "--fast--high", base.ModelId + "--fast--fast", "--fast"} {
		_, _, err := chooseFastRoute([]*v1.Route{base}, selector)
		var failure *inference.Error
		if !errors.As(err, &failure) || failure.Status != 404 {
			t.Fatalf("unsupported grammar %q resolved: %v", selector, err)
		}
	}
	duplicate := &v1.Route{Id: "duplicate", ModelId: base.ModelId, Enabled: true}
	_, _, err := chooseFastRoute([]*v1.Route{base, duplicate}, base.ModelId+fastRowSuffix)
	var failure *inference.Error
	if !errors.As(err, &failure) || failure.Status != 409 {
		t.Fatalf("ambiguous selector was not refused: %v", err)
	}
}

func TestRoutingFastExportEvidenceAndCollision(t *testing.T) {
	views := []routeModelView{
		{ID: "eligible", Name: "Eligible", Metadata: map[string]any{"fast_row_available": true, "supports_tools": true}},
		{ID: "unknown", Metadata: map[string]any{}},
		{ID: "denied", Metadata: map[string]any{"fast_row_available": false}},
		{ID: "shadowed", Metadata: map[string]any{"fast_row_available": true}},
		{ID: "shadowed--fast", Name: "Explicit"},
	}
	out := expandFastModelViews(views)
	if len(out) != 6 || out[5].ID != "eligible--fast" || out[5].Name != "Eligible Fast" || out[5].Metadata["fast_row_available"] != false || out[5].Metadata["supports_tools"] != true {
		t.Fatalf("wrong fast expansion: %#v", out)
	}
	if out[3].Metadata["fast_row_available"] != false || out[4].Name != "Explicit" {
		t.Fatal("explicit selector collision was advertised")
	}
	if views[0].Metadata["fast_row_available"] != true || views[3].Metadata["fast_row_available"] != true {
		t.Fatal("expansion mutated source evidence")
	}
	if len(expandFastModelViews(out)) != len(out) {
		t.Fatal("repeated export recursively synthesized fast selectors")
	}
}
