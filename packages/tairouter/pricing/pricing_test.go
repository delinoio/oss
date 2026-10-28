// SPDX-License-Identifier: Apache-2.0
package pricing

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func pointer[T any](v T) *T { return &v }
func snapshot(t *testing.T, overrides ...Override) *Snapshot {
	t.Helper()
	s, err := NewSnapshot(overrides)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func near(t *testing.T, label string, a, b float64) {
	t.Helper()
	if math.Abs(a-b) > 1e-10*math.Max(1, math.Abs(b)) {
		t.Fatalf("%s: got %.12g want %.12g", label, a, b)
	}
}
func TestPinnedReferenceFixtures(t *testing.T) {
	s := snapshot(t)
	paths, err := filepath.Glob("testdata/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatal("fixtures missing", err)
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var fixtures []struct {
			Input struct {
				Provider, Model string
				Usage           Usage
				UsageStatus     Status
				ServiceTier     struct{ ResponseServiceTier, RequestedServiceTier, ConfiguredServiceTier *string }
			}
			Expected *struct {
				Cost               struct{ Input, Output, CacheRead, CacheWrite, Total float64 }
				Estimated          bool
				PriorityMultiplier float64
				ContextTier        string
				PriorityLowerBound bool
				Price              struct {
					Cost4  Rates
					Source Source
					Status Verification
				}
			}
		}
		if err = json.Unmarshal(raw, &fixtures); err != nil {
			t.Fatal(err)
		}
		for _, f := range fixtures {
			t.Run(filepath.Base(path)+"/"+f.Input.Provider+"/"+f.Input.Model, func(t *testing.T) {
				r := Request{Provider: f.Input.Provider, Model: f.Input.Model, Usage: &f.Input.Usage, Status: f.Input.UsageStatus, Tier: Tier{Response: f.Input.ServiceTier.ResponseServiceTier, Requested: f.Input.ServiceTier.RequestedServiceTier, Configured: f.Input.ServiceTier.ConfiguredServiceTier}}
				got := s.Estimate(r, time.Unix(0, 0))
				if f.Expected == nil {
					if got.Known {
						t.Fatalf("expected unknown, got %+v", got)
					}
					return
				}
				want := f.Expected
				if !got.Known {
					t.Fatalf("expected known: %+v", got)
				}
				near(t, "total", got.Cost.Total, want.Cost.Total)
				near(t, "cache write", got.Cost.CacheWrite, want.Cost.CacheWrite)
				if got.Estimated != want.Estimated || got.ContextTier != want.ContextTier || got.PriorityLowerBound != want.PriorityLowerBound || got.Price.Source != want.Price.Source || got.Price.Status != want.Price.Status {
					t.Fatalf("provenance mismatch got %+v want %+v", got, want)
				}
				mult := want.PriorityMultiplier
				if mult == 0 {
					mult = 1
				}
				near(t, "priority", got.PriorityMultiplier, mult)
				if got.Price.Rates != want.Price.Cost4 {
					t.Fatalf("base rates mismatch %+v %+v", got.Price.Rates, want.Price.Cost4)
				}
			})
		}
	}
}
func TestUnknownZeroOverridesAndHistory(t *testing.T) {
	u := Usage{InputTokens: pointer(int64(0)), OutputTokens: pointer(int64(0))}
	r := Request{Provider: "custom", Model: "model", Usage: &u, Status: Reported}
	s := snapshot(t)
	if s.Estimate(r, time.Time{}).Known {
		t.Fatal("unknown price became free")
	}
	o := Override{Provider: "custom", Model: "model", Rates: Rates{}, PromptPolicy: &PromptPolicy{Mode: Flat}, SourceRef: "operator change"}
	free := snapshot(t, o)
	fixed := free.Estimate(r, time.Time{})
	if !fixed.Known || fixed.Cost.Total != 0 {
		t.Fatal(fixed)
	}
	o.Rates.Input = 99
	o.PromptPolicy.Mode = Custom
	if free.Estimate(r, time.Time{}).Price.PromptPolicy.Mode != Flat {
		t.Fatal("constructor retained mutable caller policy")
	}
	fixed.Price.PromptPolicy.Mode = Custom
	if free.Estimate(r, time.Time{}).Price.PromptPolicy.Mode != Flat {
		t.Fatal("estimate mutates snapshot")
	}
	paid := snapshot(t, Override{Provider: "custom", Model: "model", Rates: Rates{Input: 5}})
	if paid.ID() == free.ID() {
		t.Fatal("override omitted from snapshot identity")
	}
	r.Usage = nil
	missing := paid.Estimate(r, time.Time{})
	if missing.Known || missing.UnknownReason != MissingUsage {
		t.Fatal(missing)
	}
	var a Aggregate
	a.Add(fixed)
	a.Add(missing)
	if a.KnownRecords != 1 || a.MissingRecords != 1 || a.KnownCost != 0 {
		t.Fatal(a)
	}
	r.Usage = &u
	r.Status = Unreported
	if paid.Estimate(r, time.Time{}).Known {
		t.Fatal("partial unreported usage priced")
	}
	r.Status = Reported
	c := paid.EstimateCombo([]Request{r, {Provider: "unknown", Usage: &u, Status: Reported}}, time.Time{})
	if c.Known || c.Cost.Total != 0 || c.MissingAttempts != 1 {
		t.Fatal(c)
	}
}
func TestNormalization(t *testing.T) {
	tests := []struct {
		name  string
		u     Usage
		want  Tokens
		known bool
	}{
		{"inclusive", Usage{InputTokens: pointer(int64(100)), OutputTokens: pointer(int64(20)), CacheReadInputTokens: pointer(int64(30)), CacheCreationInputTokens: pointer(int64(10)), CacheCreation1hInputTokens: pointer(int64(5))}, Tokens{60, 20, 30, 10, 5}, true},
		{"legacy", Usage{InputTokens: pointer(int64(100)), OutputTokens: pointer(int64(20)), CachedInputTokens: pointer(int64(90)), CacheCreationInputTokens: pointer(int64(30))}, Tokens{10, 20, 60, 30, 0}, true},
		{"contradiction", Usage{InputTokens: pointer(int64(100)), OutputTokens: pointer(int64(20)), CacheReadInputTokens: pointer(int64(90)), CacheCreationInputTokens: pointer(int64(30))}, Tokens{}, false},
		{"missing", Usage{OutputTokens: pointer(int64(0))}, Tokens{}, false},
		{"negative", Usage{InputTokens: pointer(int64(-1)), OutputTokens: pointer(int64(0))}, Tokens{}, false},
		{"overflow", Usage{InputTokens: pointer(int64(math.MaxInt64)), OutputTokens: pointer(int64(0)), CacheReadInputTokens: pointer(int64(math.MaxInt64)), CacheCreationInputTokens: pointer(int64(math.MaxInt64))}, Tokens{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Normalize(tt.u)
			if ok != tt.known || got != tt.want {
				t.Fatalf("%+v %v", got, ok)
			}
		})
	}
	b := Calculate(Tokens{CacheWrite: 10, CacheWrite1h: 5}, Rates{CacheWrite: 1.25})
	near(t, "1h", b.Total, 16.25/1e6)
}
func TestProviderIdentityAndOverrides(t *testing.T) {
	s := snapshot(t, Override{Provider: "anthropic-pabcdef", Model: "claude-opus-5", Rates: Rates{Input: 17}})
	p, ok := s.Resolve("anthropic-pabcdef", "claude-opus-5", Resolution{})
	if !ok || p.Rates.Input != 17 {
		t.Fatal(p)
	}
	p, ok = s.Resolve("openai-main", "gpt-6-astra", Resolution{})
	if !ok || p.Provider != "openai" {
		t.Fatal(p)
	}
	p, ok = s.Resolve("openai-main", "gpt-6-astra", Resolution{ConfiguredProviders: map[string]bool{"openai-main": true}})
	if !ok || p.Provider != "openai-main" {
		t.Fatal(p)
	}
	if _, ok = s.Resolve("custom", "openai/claude-opus-5", Resolution{}); ok {
		t.Fatal("vendor disagreement accepted")
	}
	if _, ok = s.Resolve("custom", "anthropic/claude-opus-5", Resolution{DisableModelFallback: true}); ok {
		t.Fatal("disabled fallback used")
	}
	for _, v := range []float64{-1, math.NaN(), math.Inf(1), 1e6 + 1} {
		if _, err := NewSnapshot([]Override{{Provider: "p", Model: "m", Rates: Rates{Input: v}}}); err == nil {
			t.Fatal("invalid rate accepted")
		}
	}
}
func TestCustomThresholdAndTierOutcome(t *testing.T) {
	s := snapshot(t, Override{Provider: "xai", Model: "grok-4.6", Rates: Rates{Input: 1}, PromptPolicy: &PromptPolicy{Mode: Custom, ThresholdTokens: 100, Comparison: GreaterEqual, Rates: Rates{Input: 3}}})
	r := Request{Provider: "xai", Model: "grok-4.6", Status: Reported, Usage: &Usage{InputTokens: pointer(int64(100)), OutputTokens: pointer(int64(0))}, Tier: Tier{Outcome: &TierOutcome{Canonical: "priority", Confirmation: "confirmed", FastOutcome: "applied"}}}
	e := s.Estimate(r, time.Time{})
	if !e.CustomThresholdApplied || !e.PriorityLowerBound || e.PriorityMultiplier != 1 {
		t.Fatal(e)
	}
	r.Tier.Outcome.ResponseTierAuthoritative = pointer(false)
	e = s.Estimate(r, time.Time{})
	if e.PriorityLowerBound {
		t.Fatal("non-authoritative echo confirmed")
	}
	s = snapshot(t)
	r.Provider = "openrouter"
	r.Model = "anthropic/claude-opus-5"
	r.Tier.Outcome.Confirmation = "assumed"
	e = s.Estimate(r, time.Time{})
	if !e.PriorityLowerBound {
		t.Fatal(e)
	}
}
