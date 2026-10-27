// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

func recoveryFailure(status int, kind, message string, code any) inference.HTTPFailure {
	errorBody := map[string]any{"type": kind, "message": message}
	if code != nil {
		errorBody["code"] = code
	}
	body, _ := json.Marshal(map[string]any{"type": "error", "error": errorBody})
	return inference.HTTPFailure{Status: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: body, Attempt: 1}
}
func TestPinnedRecoveryExactRefusals(t *testing.T) {
	credit := "Your credit balance is too low to access the Anthropic API."
	tests := []struct {
		name, provider string
		auth           inference.AuthMode
		failure        inference.HTTPFailure
		action         inference.RecoveryAction
		delay          time.Duration
		model          string
		reauth         bool
	}{
		{"api billing", "anthropic", inference.APIKey, recoveryFailure(400, "invalid_request_error", credit, nil), inference.RecoveryRotate, time.Hour, "", false},
		{"api payment required", "anthropic2", inference.APIKey, recoveryFailure(402, "billing_error", credit+" Please go to Plans & Billing to upgrade or purchase credits.", "insufficient_quota"), inference.RecoveryRotate, time.Hour, "", false},
		{"quoted billing", "anthropic", inference.APIKey, recoveryFailure(400, "invalid_request_error", "Quoted: "+credit, nil), inference.RecoveryDefault, 0, "", false},
		{"billing wrong code", "anthropic", inference.APIKey, recoveryFailure(400, "invalid_request_error", credit, "invalid_model"), inference.RecoveryDefault, 0, "", false},
		{"billing wrong type", "anthropic", inference.APIKey, recoveryFailure(400, "authentication_error", credit, nil), inference.RecoveryDefault, 0, "", false},
		{"billing other provider", "other", inference.APIKey, recoveryFailure(400, "billing_error", credit, nil), inference.RecoveryDefault, 0, "", false},
		{"api unauthorized", "anthropic", inference.APIKey, recoveryFailure(401, "authentication_error", "invalid key", nil), inference.RecoveryRotate, 10 * time.Minute, "", false},
		{"revoked", "anthropic2", inference.OAuth, recoveryFailure(401, "authentication_error", "OAuth access token has been revoked.", nil), inference.RecoveryRotate, 0, "", true},
		{"revoked quoted", "anthropic", inference.OAuth, recoveryFailure(401, "authentication_error", "Diagnostic: OAuth access token has been revoked.", nil), inference.RecoveryDefault, 0, "", false},
		{"revoked code", "anthropic", inference.OAuth, recoveryFailure(401, "authentication_error", "OAuth access token has been revoked.", "invalid_grant"), inference.RecoveryDefault, 0, "", false},
		{"subscription", "anthropic", inference.OAuth, recoveryFailure(403, "permission_error", "Your Claude subscription has expired.", nil), inference.RecoveryRotate, 10 * time.Minute, "", false},
		{"organization", "anthropic2", inference.OAuth, recoveryFailure(403, "oauth_org_not_allowed", "Organization policy", nil), inference.RecoveryRotate, 10 * time.Minute, "", false},
		{"subscription quoted", "anthropic", inference.OAuth, recoveryFailure(403, "permission_error", "The server said: Your Claude subscription has expired.", nil), inference.RecoveryDefault, 0, "", false},
		{"request refusal", "anthropic", inference.OAuth, recoveryFailure(403, "permission_error", "Your account does not have access to this model.", nil), inference.RecoveryDefault, 0, "", false},
		{"oauth wrong billing status", "anthropic", inference.OAuth, recoveryFailure(400, "billing_error", credit, nil), inference.RecoveryDefault, 0, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := inference.AdapterInput{Target: inference.Target{Auth: tt.auth, Model: "model"}}
			d, h := pinnedRecovery(tt.provider, in, tt.failure, time.Now())
			if d.Action != tt.action {
				t.Fatalf("action=%s want=%s", d.Action, tt.action)
			}
			if tt.action == inference.RecoveryDefault {
				if h != nil {
					t.Fatal("nonmatch acquired account authority")
				}
				return
			}
			if h == nil || h.delay != tt.delay || h.model != tt.model || h.reauth != tt.reauth {
				t.Fatalf("hold=%+v", h)
			}
		})
	}
}
func TestPinnedRecoveryRejectsMalformedEnvelopes(t *testing.T) {
	for _, body := range []string{
		`{"error":{"type":"authentication_error","message":"OAuth access token has been revoked."}}`,
		`{"type":"error","error":[{"type":"authentication_error","message":"OAuth access token has been revoked."}]}`,
		`{"type":"error","error":{"type":"authentication_error","message":"OAuth access token has been revoked."`,
		`{"type":"error","error":{"type":"authentication_error","message":"OAuth access token has been revoked.","code":123}}`,
	} {
		if anthropicRevoked(inference.HTTPFailure{Status: 401, Body: []byte(body)}) {
			t.Fatal("malformed refusal accepted")
		}
	}
	billing := recoveryFailure(400, "billing_error", "Your credit balance is too low to access the Anthropic API.", nil)
	billing.Header.Set("Content-Type", "text/plain")
	if anthropicBilling(billing) {
		t.Fatal("non-JSON billing accepted")
	}
	billing.Header.Set("Content-Type", "application/json")
	billing.Body = append(billing.Body, []byte(strings.Repeat(" ", 8192))...)
	if anthropicBilling(billing) {
		t.Fatal("oversized billing accepted")
	}
}
func TestPinnedRecoveryCodexModelScope(t *testing.T) {
	sentence := "The 'gpt-example' model is not supported when using Codex with a ChatGPT account."
	for _, tt := range []struct {
		name  string
		body  any
		match bool
	}{
		{"detail", map[string]any{"detail": sentence}, true},
		{"socket", map[string]any{"error": map[string]any{"message": sentence}}, true},
		{"normalized", map[string]any{"detail": "  THE 'gpt-example' model is not supported\nwhen using Codex with a ChatGPT account. "}, true},
		{"quoted", map[string]any{"detail": "Quoted: " + sentence}, false},
		{"other model", map[string]any{"detail": strings.ReplaceAll(sentence, "gpt-example", "other")}, false},
		{"competing", map[string]any{"detail": sentence, "error": nil}, false},
		{"wrong code type", map[string]any{"error": map[string]any{"message": sentence, "code": 7}}, false},
		{"echo elsewhere", map[string]any{"request": sentence, "detail": "invalid input"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.body)
			f := inference.HTTPFailure{Status: 400, Body: body}
			in := inference.AdapterInput{Target: inference.Target{Auth: inference.OAuth, Model: "gpt-example"}}
			d, h := pinnedRecovery("openai", in, f, time.Now())
			if (d.Action == inference.RecoveryRotate) != tt.match {
				t.Fatalf("decision=%+v", d)
			}
			if h != nil {
				t.Fatalf("soft model denial acquired hard cooldown: %+v", h)
			}
			in.Target.Auth = inference.APIKey
			if d, _ := pinnedRecovery("openai", in, f, time.Now()); d.Action != inference.RecoveryDefault {
				t.Fatal("API key entered Codex OAuth policy")
			}
		})
	}
}
func TestPinnedRecoveryQuotaDates(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		message string
		code    any
		want    time.Duration
		ok      bool
	}{
		{"Weekly Limit Exhausted. It will reset at 2026-01-02 03:30:06", "rate_limit_error", 27*time.Hour + 30*time.Minute + 6*time.Second, true},
		{"Monthly Limit Exhausted; resets at 2026-02-01T00:00:00Z", 429, 31 * 24 * time.Hour, true},
		{"Monthly Limit Exhausted; resets at 2999-01-01", "rate_limit_error", 32 * 24 * time.Hour, true},
		{"Monthly Limit Exhausted; resets at 2026-02-30T00:00:00Z", "rate_limit_error", 0, false},
		{"Monthly Limit Exhausted; resets at 2025-12-31", "rate_limit_error", 0, false},
		{"Quoted: Monthly Limit Exhausted; resets at 2026-01-02", "rate_limit_error", 0, false},
		{"Monthly Limit Exhausted; resets at 2026-01-02", "other", 0, false},
	} {
		f := recoveryFailure(429, "rate_limit_error", tt.message, tt.code)
		delay, ok := keyQuotaReset(f.Body, now)
		if ok != tt.ok || delay != tt.want {
			t.Fatalf("%s: %v,%v", tt.message, delay, ok)
		}
	}
	for _, tt := range []struct {
		raw  string
		want time.Duration
		ok   bool
	}{
		{"0", time.Millisecond, true}, {"0.5", 500 * time.Millisecond, true}, {now.Add(-time.Hour).Format(http.TimeFormat), time.Millisecond, true}, {"invalid", 0, false}, {"-1", 0, false}, {"1e4", 0, false}, {"6000", 10 * time.Minute, true},
	} {
		f := recoveryFailure(429, "rate_limit_error", "rate limited", nil)
		f.Header.Set("Retry-After", tt.raw)
		in := inference.AdapterInput{Target: inference.Target{Auth: inference.APIKey, Retry: inference.RetryPolicy{RotateOn429: true}}}
		_, h := pinnedRecovery("other", in, f, now)
		want := tt.want
		if !tt.ok {
			want = time.Minute
		}
		if h == nil || h.delay != want {
			t.Fatalf("Retry-After %q: %+v", tt.raw, h)
		}
	}
}
func TestPinnedRecoveryAnthropicSharedQuota(t *testing.T) {
	now := time.Unix(1800000000, 0)
	in := inference.AdapterInput{Target: inference.Target{Auth: inference.OAuth, Model: "claude"}}
	f := inference.HTTPFailure{Status: 429, Header: http.Header{}, Attempt: 1}
	f.Header.Set("anthropic-ratelimit-unified-5h-status", "rejected")
	f.Header.Set("anthropic-ratelimit-unified-5h-reset", "1800003600")
	f.Header.Set("anthropic-ratelimit-unified-7d-status", "rejected")
	f.Header.Set("anthropic-ratelimit-unified-7d-reset", "1800007200")
	d, h := pinnedRecovery("anthropic2", in, f, now)
	if d.Action != inference.RecoveryRotate || h == nil || h.model != "" || h.delay != 2*time.Hour {
		t.Fatalf("shared: %+v %+v", d, h)
	}
	f.Header.Set("Retry-After", "30")
	_, h = pinnedRecovery("anthropic", in, f, now)
	if h.delay != 30*time.Second {
		t.Fatalf("retry precedence: %+v", h)
	}
	for _, header := range []http.Header{{}, {"Retry-After": []string{"2"}}, {"Anthropic-Ratelimit-Unified-7d_oi-Status": []string{"rejected"}}} {
		f.Header = header
		_, h := pinnedRecovery("anthropic", in, f, now)
		if h != nil {
			t.Fatal("non-shared throttle acquired shared account hold")
		}
	}
}

type recoveryDelegate struct {
	decision inference.RecoveryDecision
	err      error
	calls    *int
}

func (p recoveryDelegate) RecoverHTTP(context.Context, inference.AdapterInput, inference.HTTPFailure) (inference.RecoveryDecision, error) {
	*p.calls++
	return p.decision, p.err
}
func TestRecoveryPoliciesPreserveDelegates(t *testing.T) {
	for _, action := range []inference.RecoveryAction{inference.RecoveryStop, inference.RecoveryRetry, inference.RecoveryRefresh, inference.RecoveryRotate, inference.RecoveryDefault} {
		calls := 0
		delegate := recoveryDelegate{decision: inference.RecoveryDecision{Action: action, Delay: time.Second}, calls: &calls}
		base := map[inference.Wire]inference.HTTPRecoveryPolicy{"fixture": delegate}
		a := &inferenceAdapter{}
		policies := a.recoveryPolicies(base)
		d, err := policies["fixture"].RecoverHTTP(context.Background(), inference.AdapterInput{}, inference.HTTPFailure{Status: 429})
		if err != nil || calls != 1 || d.Action != action {
			t.Fatalf("delegate %s: %+v %v", action, d, err)
		}
		if len(base) != 1 || len(policies) != 4 {
			t.Fatal("base map mutated or standard wires missing")
		}
	}
	sentinel := errors.New("delegate failure")
	calls := 0
	a := &inferenceAdapter{}
	policies := a.recoveryPolicies(map[inference.Wire]inference.HTTPRecoveryPolicy{"fixture": recoveryDelegate{err: sentinel, calls: &calls}})
	if _, err := policies["fixture"].RecoverHTTP(context.Background(), inference.AdapterInput{}, inference.HTTPFailure{}); !errors.Is(err, sentinel) {
		t.Fatal("delegate error lost")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := policies["fixture"].RecoverHTTP(ctx, inference.AdapterInput{}, inference.HTTPFailure{}); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("cancellation called delegate")
	}
}

func TestRecoveryHoldVersionFence(t *testing.T) {
	a, sc, rawKey := dispatchFixture(t)
	ctx := context.Background()
	p, release, err := a.Admit(ctx, rawKey)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	data := providerData{Metadata: &v1.ProviderConnection{Id: "recovery", ProviderId: "anthropic", State: v1.ConnectionState_CONNECTION_STATE_READY}, Secret: "fixture-secret"}
	save := func() {
		t.Helper()
		raw, _ := json.Marshal(data)
		if err := a.s.PutPrivate(ctx, sc, string(providerKind), "recovery", raw); err != nil {
			t.Fatal(err)
		}
	}
	save()
	_, version, err := a.s.GetPrivateVersion(ctx, sc, string(providerKind), "recovery")
	if err != nil {
		t.Fatal(err)
	}
	a.versions = map[string]string{"recovery": version}
	in := inference.AdapterInput{Principal: p, Credential: inference.Credential{AccountID: "recovery"}}
	if err := a.recordRecoveryHold(ctx, in, recoveryHold{model: "model-a", delay: time.Hour}); err != nil {
		t.Fatal(err)
	}
	hold, err := a.s.AccountCooldown(ctx, sc, "recovery", "model-a")
	if err != nil || hold == nil {
		t.Fatalf("model hold missing: %v", err)
	}
	hold, err = a.s.AccountCooldown(ctx, sc, "recovery", "model-b")
	if err != nil || hold != nil {
		t.Fatalf("model denial leaked to sibling: %v", err)
	}
	data.Secret = "replacement-secret"
	save()
	for _, h := range []recoveryHold{{delay: time.Hour}, {reauth: true}} {
		if err := a.recordRecoveryHold(ctx, in, h); err == nil {
			t.Fatal("stale refusal modified replacement credential")
		}
	}
	current, version, err := a.s.GetPrivateVersion(ctx, sc, string(providerKind), "recovery")
	if err != nil {
		t.Fatal(err)
	}
	var unchanged providerData
	if json.Unmarshal(current, &unchanged) != nil || unchanged.Metadata.State != v1.ConnectionState_CONNECTION_STATE_READY {
		t.Fatal("stale refusal marked replacement as revoked")
	}
	a.versions["recovery"] = version
	if err := a.recordRecoveryHold(ctx, in, recoveryHold{reauth: true}); err != nil {
		t.Fatal(err)
	}
	current, _, err = a.s.GetPrivateVersion(ctx, sc, string(providerKind), "recovery")
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(current, &unchanged) != nil || unchanged.Metadata.State != v1.ConnectionState_CONNECTION_STATE_AUTHORIZATION_REQUIRED {
		t.Fatal("revoked credential was not marked")
	}
}

func TestRecoveryPoliciesUseTargetSelectionKey(t *testing.T) {
	target := inference.Target{Provider: "shared", Model: "model-a", Wire: inference.Anthropic, Auth: inference.APIKey}
	other := target
	other.Model = "model-b"
	a := &inferenceAdapter{selections: map[string]routeSelection{selectionKey(target): {provider: "anthropic"}, selectionKey(other): {provider: "other"}}}
	policies := a.recoveryPolicies(nil)
	f := recoveryFailure(400, "billing_error", "Your credit balance is too low to access the Anthropic API.", nil)
	// The matching route reaches the version fence; no store operation is permitted
	// without a captured version. A different model retains its own provider policy.
	if _, err := policies[inference.Anthropic].RecoverHTTP(context.Background(), inference.AdapterInput{Target: target, Credential: inference.Credential{AccountID: "selected"}}, f); !errors.Is(err, inference.ErrNoCredential) {
		t.Fatalf("matching route did not reach version fence: %v", err)
	}
	if d, err := policies[inference.Anthropic].RecoverHTTP(context.Background(), inference.AdapterInput{Target: other}, f); err != nil || d.Action != inference.RecoveryDefault {
		t.Fatalf("sibling model inherited policy: %+v %v", d, err)
	}
}
