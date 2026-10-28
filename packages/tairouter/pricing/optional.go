// SPDX-License-Identifier: Apache-2.0
package pricing

import "strings"

func normalizeTier(tier string) string {
	tier = strings.ToLower(strings.TrimSpace(tier))
	if tier == "fast" {
		return "priority"
	}
	return tier
}
func overrideKey(provider, model, tier string) string {
	if tier == "" {
		return key(provider, model)
	}
	return key(provider, model) + "\x00" + tier
}
func optionalValues(r *OptionalRates) []*float64 {
	return []*float64{r.Input, r.Output, r.CacheRead, r.CacheWrite, r.CacheWriteShort, r.CacheWriteLong}
}
func copyOptional(r *OptionalRates) *OptionalRates {
	if r == nil {
		return nil
	}
	v := *r
	for _, p := range []**float64{&v.Input, &v.Output, &v.CacheRead, &v.CacheWrite, &v.CacheWriteShort, &v.CacheWriteLong} {
		if *p != nil {
			n := **p
			*p = &n
		}
	}
	return &v
}

// ResolveForTier selects an exact tier override before an all-tier override.
// A response tier overrides requested/configured tiers. Missing tier means default.
func (s *Snapshot) ResolveForTier(provider, model string, opts Resolution, tier Tier) (Price, bool) {
	normalized := tier.normalized()
	selected := "default"
	if t := normalized.effective(); t != nil && normalizeTier(*t) != "" {
		selected = normalizeTier(*t)
	}
	namespace := provider
	if !opts.ConfiguredProviders[provider] {
		if mapped, ok := opts.AccountProviders[provider]; ok {
			namespace = mapped
		} else {
			namespace = baseProvider(provider)
		}
	}
	for _, p := range []string{provider, namespace} {
		if o, ok := s.overrides[overrideKey(p, model, selected)]; ok {
			return overridePrice(o), true
		}
		if o, ok := s.overrides[key(p, model)]; ok {
			return overridePrice(o), true
		}
	}
	return s.Resolve(provider, model, opts)
}
func calculateOptional(t Tokens, r *OptionalRates) (Breakdown, bool) {
	anyKnown := false
	for _, rate := range optionalValues(r) {
		anyKnown = anyKnown || rate != nil
	}
	if !anyKnown {
		return Breakdown{}, false
	}
	short, long := r.CacheWriteShort, r.CacheWriteLong
	if short == nil {
		short = r.CacheWrite
	}
	if long == nil {
		long = r.CacheWrite
	}
	counts := []int64{t.Input, t.Output, t.CacheRead, t.CacheWrite - t.CacheWrite1h, t.CacheWrite1h}
	rates := []*float64{r.Input, r.Output, r.CacheRead, short, long}
	amounts := make([]float64, len(counts))
	for i, count := range counts {
		if count == 0 {
			continue
		}
		if rates[i] == nil {
			return Breakdown{}, false
		}
		amounts[i] = float64(count) * *rates[i] / 1e6
	}
	b := Breakdown{Input: amounts[0], Output: amounts[1], CacheRead: amounts[2], CacheWrite: amounts[3] + amounts[4]}
	b.Total = b.Input + b.Output + b.CacheRead + b.CacheWrite
	return b, true
}
