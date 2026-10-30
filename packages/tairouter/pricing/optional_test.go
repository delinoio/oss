// SPDX-License-Identifier: Apache-2.0
package pricing

import (
	"math"
	"testing"
	"time"
)

func TestOptionalTierOverrides(t *testing.T) {
	standard := Override{Provider: "openai-apikey", Model: "gpt-6-astra", OptionalRates: &OptionalRates{Input: pointer(2.0), Output: pointer(3.0), CacheRead: pointer(0.1), CacheWriteShort: pointer(4.0), CacheWriteLong: pointer(9.0)}}
	priority := standard
	priority.ServiceTier = "priority"
	priority.OptionalRates = copyOptional(standard.OptionalRates)
	priority.OptionalRates.Input = pointer(7.0)
	s := snapshot(t, standard, priority)
	u := Usage{InputTokens: pointer(int64(300000)), OutputTokens: pointer(int64(10)), CacheReadInputTokens: pointer(int64(20)), CacheCreationInputTokens: pointer(int64(30)), CacheCreation1hInputTokens: pointer(int64(10))}
	req := Request{Provider: standard.Provider, Model: standard.Model, Status: Reported, Usage: &u, Tier: Tier{Requested: pointer("fast")}}
	e := s.Estimate(req, time.Time{})
	if !e.Known || e.Price.ServiceTier != "priority" || e.PriorityMultiplier != 1 || e.ContextTier != "" {
		t.Fatal(e)
	}
	near(t, "input", e.Cost.Input, 299950*7.0/1e6)
	near(t, "cache ttl", e.Cost.CacheWrite, (20*4.0+10*9.0)/1e6)
	req.Tier.Response = pointer("default")
	e = s.Estimate(req, time.Time{})
	near(t, "response precedence", e.Cost.Input, 299950*2.0/1e6)
	*priority.OptionalRates.Input = 100
	req.Tier.Response = nil
	e = s.Estimate(req, time.Time{})
	near(t, "immutable input", *e.EffectiveOptionalRates.Input, 7)
	*e.Price.OptionalRates.Input = 200
	*e.EffectiveOptionalRates.Input = 300
	e = s.Estimate(req, time.Time{})
	near(t, "immutable result", *e.Price.OptionalRates.Input, 7)
	if _, err := NewSnapshot([]Override{priority, {Provider: standard.Provider, Model: standard.Model, ServiceTier: "FAST"}}); err == nil {
		t.Fatal("duplicate canonical tier accepted")
	}
}
func TestOptionalUnknownAndZero(t *testing.T) {
	base := Override{Provider: "p", Model: "m", OptionalRates: &OptionalRates{Input: pointer(0.0)}}
	req := Request{Provider: "p", Model: "m", Status: Reported, Usage: &Usage{InputTokens: pointer(int64(10)), OutputTokens: pointer(int64(0))}}
	s := snapshot(t, base)
	e := s.Estimate(req, time.Time{})
	if !e.Known || e.Cost.Total != 0 || e.Price.OptionalRates.Output != nil {
		t.Fatal(e)
	}
	req.Usage.OutputTokens = pointer(int64(1))
	e = s.Estimate(req, time.Time{})
	if e.Known || e.UnknownReason != MissingPrice || e.Price.OptionalRates.Output != nil {
		t.Fatal(e)
	}
	base.OptionalRates = &OptionalRates{}
	s = snapshot(t, base)
	if s.Estimate(req, time.Time{}).Known {
		t.Fatal("empty optional rates silently free")
	}
	if snapshot(t, Override{Provider: "p", Model: "m", OptionalRates: &OptionalRates{Input: pointer(0.0)}}).ID() == s.ID() {
		t.Fatal("missing and zero snapshot IDs equal")
	}
	for _, r := range []*OptionalRates{{Currency: "EUR"}, {CacheWriteLong: pointer(math.NaN())}, {CacheWriteShort: pointer(-1.0)}, {Output: pointer(math.Inf(1))}} {
		if _, err := NewSnapshot([]Override{{Provider: "p", Model: "m", OptionalRates: r}}); err == nil {
			t.Fatal("invalid rates accepted", r)
		}
	}
}
func TestOptionalCacheFallbackAndTierIsolation(t *testing.T) {
	r := &OptionalRates{CacheWrite: pointer(5.0), CacheWriteShort: pointer(2.0)}
	b, ok := calculateOptional(Tokens{CacheWrite: 10, CacheWrite1h: 4}, r)
	if !ok {
		t.Fatal("explicit fallback ignored")
	}
	near(t, "no inferred long premium", b.CacheWrite, 32.0/1e6)
	r.CacheWrite = nil
	if _, ok := calculateOptional(Tokens{CacheWrite: 10, CacheWrite1h: 4}, r); ok {
		t.Fatal("missing long rate inferred")
	}
	s := snapshot(t, Override{Provider: "p", Model: "m", ServiceTier: "priority", OptionalRates: &OptionalRates{Input: pointer(1.0)}}, Override{Provider: "p", Model: "m", ServiceTier: "default", OptionalRates: &OptionalRates{Input: pointer(2.0)}})
	p, ok := s.ResolveForTier("p", "m", Resolution{}, Tier{})
	if !ok || *p.OptionalRates.Input != 2 {
		t.Fatal(p)
	}
	if _, ok := s.ResolveForTier("p", "m", Resolution{}, Tier{Response: pointer("flex")}); ok {
		t.Fatal("tier override leaked to another tier")
	}
}
