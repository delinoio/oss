// SPDX-License-Identifier: Apache-2.0
package service

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

type nativeOptions struct {
	enabled, cacheAffinity bool
	strategy               v1.NativePoolStrategy
	window                 v1.NativeQuotaWindow
	threshold              float64
	sticky                 int
	excluded               []string
	routes                 []*v1.NativeModelRoute
	members                map[string]*v1.NativePoolMemberOptions
}

func nativePoolOptions(pool *v1.Pool) nativeOptions {
	o := nativeOptions{cacheAffinity: true, threshold: 80, sticky: 1, strategy: v1.NativePoolStrategy_NATIVE_POOL_STRATEGY_QUOTA, window: v1.NativeQuotaWindow_NATIVE_QUOTA_WINDOW_FIVE_HOUR, members: map[string]*v1.NativePoolMemberOptions{}}
	if pool == nil {
		return o
	}
	for _, m := range pool.Members {
		o.members[m.ConnectionId] = m.NativeOptions
	}
	if p := pool.NativeOptions; p != nil {
		o.enabled = p.GetEnabled()
		if p.Strategy != 0 {
			o.strategy = p.Strategy
		}
		if p.StickyLimit != nil {
			o.sticky = int(*p.StickyLimit)
		}
		if p.AutoSwitchThreshold != nil {
			o.threshold = float64(*p.AutoSwitchThreshold)
		}
		if p.CacheAffinity != nil {
			o.cacheAffinity = *p.CacheAffinity
		}
		if p.QuotaWindow != 0 {
			o.window = p.QuotaWindow
		}
		o.excluded = p.ExcludedPlans
		o.routes = p.Routes
	}
	return o
}
func (o nativeOptions) thresholdFor(id string) float64 {
	m := o.members[id]
	if m != nil {
		if m.UseRemainingQuota {
			return 0
		}
		if m.AutoSwitchThreshold != nil {
			return float64(*m.AutoSwitchThreshold)
		}
	}
	return o.threshold
}
func (o nativeOptions) score(provider, model, id string, q nativeQuotaSnapshot, now time.Time) (float64, bool) {
	if provider == "openai" {
		score, known := nativeUsageScore(provider, model, q, now)
		if m := o.members[id]; m != nil && m.AllowCredits && known && q.spendableCredits(now) {
			score = math.Min(score, 99)
		}
		return score, known
	}
	short, long, family := -1.0, -1.0, -1.0
	for _, w := range q.Windows {
		if !nativeWindowCurrent(w, now) {
			continue
		}
		switch w.Window {
		case "5h":
			short = w.Percent
		case "weekly":
			long = w.Percent
		default:
			if w.Window == nativeModelFamily(model) {
				family = w.Percent
			}
		}
	}
	score := short
	switch o.window {
	case v1.NativeQuotaWindow_NATIVE_QUOTA_WINDOW_WEEKLY:
		score = long
	case v1.NativeQuotaWindow_NATIVE_QUOTA_WINDOW_MAX_UTILIZATION:
		score = math.Max(short, long)
	}
	score = math.Max(score, family)
	if score < 0 {
		return 100, false
	}
	return score, true
}
func (o nativeOptions) headroom(provider, model, id string, q nativeQuotaSnapshot, now time.Time) bool {
	threshold := o.thresholdFor(id)
	if threshold <= 0 {
		return true
	}
	score, known := o.score(provider, model, id, q, now)
	if provider != "openai" && o.window == v1.NativeQuotaWindow_NATIVE_QUOTA_WINDOW_WEEKLY {
		for _, w := range q.Windows {
			if w.Window == "5h" && nativeWindowCurrent(w, now) && w.Percent >= 100 {
				return false
			}
		}
	}
	return !known || score < threshold
}
func (o nativeOptions) tier(cs []store.SelectionCandidate, quotas map[string]nativeQuotaSnapshot, provider, model string, now time.Time) []store.SelectionCandidate {
	if len(cs) < 2 {
		return cs
	}
	sorted := append([]store.SelectionCandidate(nil), cs...)
	// Explicit native priority overrides the ordinary ordinal. Larger native
	// values win; absent values keep their existing AIPRouter ordinal ordering.
	priority := func(c store.SelectionCandidate) int {
		if m := o.members[c.ID]; m != nil && m.Priority != nil {
			return -int(*m.Priority)
		}
		return c.Priority
	}
	sort.SliceStable(sorted, func(i, j int) bool { return priority(sorted[i]) < priority(sorted[j]) })
	for start := 0; start < len(sorted); {
		end := start + 1
		for end < len(sorted) && priority(sorted[end]) == priority(sorted[start]) {
			end++
		}
		for _, c := range sorted[start:end] {
			if o.headroom(provider, model, c.ID, quotas[c.ID], now) {
				return sorted[start:end]
			}
		}
		start = end
	}
	return cs
}
func nativeGlob(pattern, value string) bool {
	p, v, star, resume := 0, 0, -1, 0
	for v < len(value) {
		if p < len(pattern) && (pattern[p] == '?' || pattern[p] == value[v]) {
			p++
			v++
		} else if p < len(pattern) && pattern[p] == '*' {
			star = p
			p++
			resume = v
		} else if star >= 0 {
			p = star + 1
			resume++
			v = resume
		} else {
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}
func (o nativeOptions) route(cs []store.SelectionCandidate, model string) []store.SelectionCandidate {
	for _, r := range o.routes {
		if !nativeGlob(r.Match, model) {
			continue
		}
		out := []store.SelectionCandidate{}
		for _, id := range r.ConnectionIds {
			for _, c := range cs {
				if c.ID == id {
					out = append(out, c)
					break
				}
			}
		}
		if len(out) > 0 || !r.Fallback {
			return out
		}
		return cs
	}
	return cs
}
func (o nativeOptions) planExcluded(plan string) bool {
	for _, p := range o.excluded {
		if strings.EqualFold(strings.TrimSpace(plan), strings.TrimSpace(p)) {
			return true
		}
	}
	return false
}
