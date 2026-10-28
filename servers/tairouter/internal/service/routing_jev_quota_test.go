// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"errors"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
	"math"
	"strings"
	"testing"
	"time"
)

func TestJEVQuotaThresholds(t *testing.T) {
	ptr := func(n float64) *float64 { return &n }
	for _, tc := range []struct {
		name  string
		cfg   *v1.JevQuotaTiers
		valid bool
	}{
		{"defaults", nil, true}, {"zero", &v1.JevQuotaTiers{Moderate: ptr(0)}, true},
		{"equal", &v1.JevQuotaTiers{Moderate: ptr(70)}, false},
		{"effective", &v1.JevQuotaTiers{Limited: ptr(90)}, false},
		{"nan", &v1.JevQuotaTiers{Moderate: ptr(math.NaN())}, false},
		{"infinity", &v1.JevQuotaTiers{NearlyExhausted: ptr(math.Inf(1))}, false},
		{"negative", &v1.JevQuotaTiers{Moderate: ptr(-1)}, false},
		{"above", &v1.JevQuotaTiers{NearlyExhausted: ptr(101)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveJEVQuotaTiers(tc.cfg)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
	tiers, _ := resolveJEVQuotaTiers(nil)
	if tiers.Moderate != nil || jevTier(69.9, tiers) != jevQuotaHealthy || jevTier(70, tiers) != jevQuotaLimited || jevTier(90, tiers) != jevQuotaNearlyExhausted {
		t.Fatal("default/inclusive thresholds")
	}
	tiers, _ = resolveJEVQuotaTiers(&v1.JevQuotaTiers{Moderate: ptr(0)})
	if jevTier(0, tiers) != jevQuotaModerate {
		t.Fatal("explicit zero lost")
	}
}
func TestJEVQuotaWindowValidityAndFamilies(t *testing.T) {
	now := time.Now()
	tiers, _ := resolveJEVQuotaTiers(nil)
	expired := now
	invalid := time.Time{}
	future := now.Add(1500 * time.Millisecond)
	for _, tc := range []struct {
		name, model string
		w           nativeQuotaWindow
		want        jevQuotaTier
	}{
		{"fractional", "m", nativeQuotaWindow{Window: "weekly", Percent: 70.25, ObservedAt: now}, jevQuotaLimited},
		{"boundary", "m", nativeQuotaWindow{Window: "weekly", Percent: 95, ObservedAt: now.Add(-30 * time.Minute)}, jevQuotaNearlyExhausted},
		{"stale", "m", nativeQuotaWindow{Window: "weekly", Percent: 95, ObservedAt: now.Add(-30*time.Minute - time.Millisecond)}, jevQuotaUnknown},
		{"future", "m", nativeQuotaWindow{Window: "weekly", Percent: 95, ObservedAt: future}, jevQuotaUnknown},
		{"reset", "m", nativeQuotaWindow{Window: "weekly", Percent: 95, ObservedAt: now, ResetAt: &expired}, jevQuotaUnknown},
		{"invalid-reset", "m", nativeQuotaWindow{Window: "weekly", Percent: 95, ObservedAt: now, ResetAt: &invalid}, jevQuotaUnknown},
		{"range", "m", nativeQuotaWindow{Window: "weekly", Percent: 100.01, ObservedAt: now}, jevQuotaUnknown},
		{"family", "claude-opus-4", nativeQuotaWindow{Window: "opus", Percent: 95, ObservedAt: now}, jevQuotaNearlyExhausted},
		{"other-family", "claude-sonnet-4", nativeQuotaWindow{Window: "opus", Percent: 95, ObservedAt: now}, jevQuotaUnknown},
		{"gem", "gemini-pro", nativeQuotaWindow{Window: "weekly", Family: "gem", Percent: 95, ObservedAt: now}, jevQuotaNearlyExhausted},
		{"argon", "gemini-argon", nativeQuotaWindow{Window: "weekly", Family: "gem", Percent: 95, ObservedAt: now}, jevQuotaUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := jevQuotaFromWindows([]nativeQuotaWindow{tc.w}, tc.model, now, tiers)
			if got.Tier != tc.want {
				t.Fatalf("%+v", got)
			}
		})
	}
	got := jevQuotaFromWindows([]nativeQuotaWindow{{Window: "weekly", Percent: 20.5, ObservedAt: now, ResetAt: &future}}, "m", now, tiers)
	if *got.UsedPercent != 20.5 || *got.ResetsInSeconds != 2 {
		t.Fatal(got)
	}
	unknown := jevQuotaSignal{Tier: jevQuotaUnknown}
	healthy := jevQuotaSignal{Tier: jevQuotaHealthy}
	near := jevQuotaSignal{Tier: jevQuotaNearlyExhausted}
	if jevQuotaPool([]jevQuotaSignal{near, unknown}).Tier != jevQuotaUnknown || jevQuotaPool([]jevQuotaSignal{healthy, unknown}).Tier != jevQuotaHealthy {
		t.Fatal("pool ordering")
	}
}
func TestJEVQuotaPayloadBudgetAndSummary(t *testing.T) {
	q := jevQuotaSignal{Tier: jevQuotaNearlyExhausted}
	choices := []jevOption{{candidate: jevCandidate{Key: "a", Quota: &q}}}
	calls := 0
	raw, used, err := jevBoundedQuotaPayload(choices, func(rows []jevOption) (json.RawMessage, error) {
		calls++
		if jevHasQuota(rows) {
			return json.RawMessage(strings.Repeat("x", jevMaxBytes+1)), nil
		}
		return json.RawMessage(`{"allowlist":["a"]}`), nil
	})
	if err != nil || calls != 2 || jevHasQuota(used) || !strings.Contains(string(raw), "allowlist") || !jevHasQuota(choices) {
		t.Fatal("quota-only retry failed")
	}
	d := withJEVQuotaSummary(jevDecision{TargetKey: "a"}, []jevCandidate{{Key: "a", Quota: &q}, {Key: "b"}})
	if d.Quota == nil || d.Quota.NearlyExhausted != 1 || d.Quota.Unknown != 1 || d.Quota.Selected != jevQuotaNearlyExhausted {
		t.Fatal(d)
	}
	if withJEVQuotaSummary(d, []jevCandidate{{Key: "a"}}).Quota != nil {
		t.Fatal("disabled summary")
	}
}

func TestJEVQuotaBackendProjection(t *testing.T) {
	percent := 72.5
	seconds := int64(20)
	q := jevQuotaSignal{Tier: jevQuotaLimited, UsedPercent: &percent, Window: "weekly", ResetsInSeconds: &seconds}
	choices := []jevOption{{key: "a:none", candidate: jevCandidate{Key: "a", Provider: "provider", Model: "model", Quota: &q}}, {key: "b:none", candidate: jevCandidate{Key: "b", Provider: "provider", Model: "other", Quota: &jevQuotaSignal{Tier: jevQuotaUnknown}}}}
	state := map[string]any{"task": "fixture task"}
	raw, err := jevRequest("jev-latest", "https://typesafe.example", false, state, choices)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Questions map[string]struct {
			Criteria map[string]struct {
				Quota *jevQuotaSignal `json:"quota"`
			}
			Instructions map[string]any
		}
	}
	if json.Unmarshal(raw, &body) != nil {
		t.Fatal("invalid payload")
	}
	got := body.Questions["route"].Criteria["a:none"].Quota
	if got == nil || *got.UsedPercent != 72.5 || *got.ResetsInSeconds != 20 || body.Questions["route"].Instructions["quota"] != jevQuotaInstruction {
		t.Fatalf("invalid structured projection: %s", raw)
	}
	for _, endpoint := range []string{"https://systemone.example", "https://ai-gateway.vercel.sh/v1/decisions"} {
		raw, err = jevRequest("fixture", endpoint, true, state, choices)
		if err != nil || !strings.Contains(string(raw), "Quota limited (72.5% of weekly used).") || !strings.Contains(string(raw), "Quota unknown.") {
			t.Fatal("missing description", err)
		}
	}
	raw, err = jevModelRequest("model", state, choices)
	if err != nil || !strings.Contains(string(raw), jevQuotaInstruction) || !strings.Contains(string(raw), "Quota limited") {
		t.Fatal("model quota projection", err)
	}
	raw, err = jevRequest("jev-latest", "https://typesafe.example", false, state, jevWithoutQuota(choices))
	if err != nil || strings.Contains(string(raw), `"quota"`) {
		t.Fatal("disabled projection", err)
	}
}

func TestJEVModelInvocationRestoresNativeReceipts(t *testing.T) {
	a := &inferenceAdapter{native: map[string]nativeSelectionState{"main": {Group: "outer"}}}
	failure := errors.New("fixture failure")
	invoke := a.withJEVModelInvocation(func(context.Context, string, json.RawMessage) (string, error) {
		if len(a.native) != 0 {
			t.Fatal("outer native receipt visible to nested completion")
		}
		a.native = map[string]nativeSelectionState{"decision": {Group: "inner"}}
		return "", failure
	})
	if _, err := invoke(context.Background(), "decision", nil); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if len(a.native) != 1 || a.native["main"].Group != "outer" {
		t.Fatal("native receipt not restored")
	}
}
