// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/store"
)

// The window observations are shared by native selection and JEV. Each window
// keeps its own clock: a partial response must not renew omitted quota evidence.
type nativeQuotaWindow struct {
	Window     string     `json:"window"`
	Family     string     `json:"family,omitempty"`
	Percent    float64    `json:"percent"`
	ObservedAt time.Time  `json:"observed_at"`
	ResetAt    *time.Time `json:"reset_at,omitempty"`
}
type nativeQuotaCredits struct {
	HasCredits, Unlimited, OverageLimitReached, Allowed *bool
	Balance                                             *float64
	ObservedAt                                          time.Time
}

func (q nativeQuotaSnapshot) spendableCredits(now time.Time) bool {
	c := q.Credits
	if c == nil || c.ObservedAt.After(now) || now.Sub(c.ObservedAt) > 5*time.Minute {
		return false
	}
	if c.HasCredits != nil && !*c.HasCredits || c.Allowed != nil && !*c.Allowed || c.OverageLimitReached != nil && *c.OverageLimitReached {
		return false
	}
	return c.Unlimited != nil && *c.Unlimited || c.HasCredits != nil && *c.HasCredits && c.Balance != nil && *c.Balance > 0
}

type nativeQuotaSnapshot struct {
	MonthlyPrimaryObservedAt time.Time            `json:"monthly_primary_observed_at,omitempty"`
	RejectionCleared         map[string]time.Time `json:"rejection_cleared,omitempty"`
	Plan                     string               `json:"plan,omitempty"`
	Credits                  *nativeQuotaCredits  `json:"credits,omitempty"`
	CreditsObservedAt        time.Time            `json:"credits_observed_at,omitempty"`
	Windows                  []nativeQuotaWindow  `json:"windows"`
	// Rejections are admission facts, separate from advisory percentage evidence.
	Rejected map[string]time.Time `json:"rejected,omitempty"`
}

func (a *inferenceAdapter) readNativeQuota(ctx context.Context, p inference.Principal, id, version string) (nativeQuotaSnapshot, bool, error) {
	var out nativeQuotaSnapshot
	l, err := a.current(p)
	if err != nil {
		return out, false, err
	}
	raw, err := a.s.GetNativeQuota(ctx, l, id, version)
	if errors.Is(err, store.ErrNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, inferenceError(err)
	}
	if json.Unmarshal(raw, &out) != nil {
		return out, false, inference.ErrUnavailable
	}
	return out, true, nil
}
func mergeNativeQuota(old, update nativeQuotaSnapshot) nativeQuotaSnapshot {
	if !update.MonthlyPrimaryObservedAt.IsZero() && !update.MonthlyPrimaryObservedAt.Before(old.MonthlyPrimaryObservedAt) {
		old.MonthlyPrimaryObservedAt = update.MonthlyPrimaryObservedAt
		kept := old.Windows[:0]
		for _, w := range old.Windows {
			if w.Window != "weekly" || w.ObservedAt.After(update.MonthlyPrimaryObservedAt) {
				kept = append(kept, w)
			}
		}
		old.Windows = kept
	}
	if !update.CreditsObservedAt.IsZero() && !update.CreditsObservedAt.Before(old.CreditsObservedAt) {
		old.Credits = update.Credits
		old.CreditsObservedAt = update.CreditsObservedAt
	}
	if update.Plan != "" {
		old.Plan = update.Plan
	}
	for _, w := range update.Windows {
		// A monthly primary supersedes earlier weekly evidence even after its
		// previous weekly window was removed. Equal-time secondary evidence remains valid.
		if w.Window == "weekly" && w.ObservedAt.Before(old.MonthlyPrimaryObservedAt) {
			continue
		}
		found := false
		for i, previous := range old.Windows {
			if previous.Window == w.Window && previous.Family == w.Family {
				found = true
				if !w.ObservedAt.Before(previous.ObservedAt) {
					old.Windows[i] = w
				}
				break
			}
		}
		if !found && len(old.Windows) < 64 {
			old.Windows = append(old.Windows, w)
		}
	}
	if old.RejectionCleared == nil {
		old.RejectionCleared = map[string]time.Time{}
	}
	for family, at := range update.RejectionCleared {
		if at.After(old.RejectionCleared[family]) {
			old.RejectionCleared[family] = at
		}
	}
	if old.Rejected == nil {
		old.Rejected = map[string]time.Time{}
	}
	for family, at := range update.Rejected {
		if at.After(old.Rejected[family]) {
			old.Rejected[family] = at
		}
	}
	return old
}
func (a *inferenceAdapter) writeNativeQuota(ctx context.Context, p inference.Principal, id, version string, update nativeQuotaSnapshot) error {
	if len(update.Windows) > 64 {
		return inference.ErrUnavailable
	}
	l, err := a.current(p)
	if err != nil {
		return err
	}
	return inferenceError(a.s.UpdateNativeQuota(ctx, l, id, version, func(raw []byte) ([]byte, error) {
		var previous nativeQuotaSnapshot
		if len(raw) > 0 && json.Unmarshal(raw, &previous) != nil {
			return nil, inference.ErrUnavailable
		}
		return json.Marshal(mergeNativeQuota(previous, update))
	}))
}
func nativeNumber(s string) (float64, bool) {
	if strings.TrimSpace(s) == "" {
		return 0, false
	}
	n, e := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return n, e == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
}
func nativeReset(raw string) *time.Time {
	if raw == "" {
		return nil
	}
	// An invalid explicit reset must not become an undated valid observation.
	t := time.Time{}
	if n, ok := nativeNumber(raw); ok && n > 0 && n < 1e15 {
		if n < 1e10 {
			n *= 1000
		}
		t = time.UnixMilli(int64(n)).UTC()
	}
	return &t
}

// Preserve explicit empty/invalid reset evidence for the advisory reader.
func nativeResetHeader(h http.Header, key string) string {
	value := h.Get(key)
	if value == "" && len(h.Values(key)) > 0 {
		return "invalid"
	}
	return value
}

// Header classification follows the pinned quota.ts duration rules. These are
// observations from the dispatched model response, never support-call headers.
func parseNativeQuota(provider, model string, status int, h http.Header, now time.Time) nativeQuotaSnapshot {
	out := nativeQuotaSnapshot{}
	add := func(window, percent, reset string, scale float64) {
		n, ok := nativeNumber(percent)
		if !ok || n < 0 || n*scale > 100 {
			return
		}
		out.Windows = append(out.Windows, nativeQuotaWindow{Window: window, Percent: n * scale, ObservedAt: now, ResetAt: nativeReset(reset)})
	}
	if provider == "openai" {
		parts := strings.Split(strings.ToLower(strings.TrimSpace(model)), "/")
		if parts[len(parts)-1] == "gpt-5.3-codex-spark" {
			return out
		}
		prefix := "x-codex-"
		primary, secondary := h.Get(prefix+"primary-used-percent"), h.Get(prefix+"secondary-used-percent")
		minutes, _ := nativeNumber(h.Get(prefix + "primary-window-minutes"))
		_, primaryKnown := nativeNumber(primary)
		switch {
		case minutes >= 28*24*60 && primary != "":
			if n, ok := nativeNumber(primary); ok && n >= 0 && n <= 100 {
				out.MonthlyPrimaryObservedAt = now
			}
			add("monthly", primary, nativeResetHeader(h, prefix+"primary-reset-at"), 1)
			add("weekly", secondary, nativeResetHeader(h, prefix+"secondary-reset-at"), 1)
		case minutes > 0 && minutes < 24*60:
			add("5h", primary, nativeResetHeader(h, prefix+"primary-reset-at"), 1)
			add("weekly", secondary, nativeResetHeader(h, prefix+"secondary-reset-at"), 1)
		case primaryKnown:
			add("weekly", primary, nativeResetHeader(h, prefix+"primary-reset-at"), 1)
		default:
			add("weekly", secondary, nativeResetHeader(h, prefix+"secondary-reset-at"), 1)
		}
		if !(minutes >= 28*24*60 && primaryKnown) {
			add("monthly", h.Get(prefix+"tertiary-used-percent"), nativeResetHeader(h, prefix+"tertiary-reset-at"), 1)
		}
	} else if provider == "anthropic" || provider == "anthropic2" {
		for _, pair := range [][2]string{{"5h", "5h"}, {"weekly", "7d"}, {"fable", "7d_oi"}} {
			prefix := "anthropic-ratelimit-unified-" + pair[1]
			add(pair[0], h.Get(prefix+"-utilization"), nativeResetHeader(h, prefix+"-reset"), 100)
			if status == 429 && strings.TrimSpace(h.Get(prefix+"-status")) == "rejected" {
				if out.Rejected == nil {
					out.Rejected = map[string]time.Time{}
				}
				out.Rejected[pair[0]] = now
			}
		}
	}
	return out
}
func nativeWindowCurrent(w nativeQuotaWindow, now time.Time) bool {
	return !w.ObservedAt.IsZero() && !w.ObservedAt.After(now) && now.Sub(w.ObservedAt) <= 24*time.Hour && w.Percent >= 0 && w.Percent <= 100 && !math.IsNaN(w.Percent) && !math.IsInf(w.Percent, 0) && (w.ResetAt == nil || w.ResetAt.After(now))
}
func nativeModelFamily(model string) string {
	model = strings.ToLower(model)
	if i := strings.LastIndex(model, "/"); i >= 0 {
		model = model[i+1:]
	}
	switch {
	case model == "claude-fable-5" || strings.HasPrefix(model, "claude-fable-5-"):
		return "fable"
	case strings.HasPrefix(model, "claude-opus-"):
		return "opus"
	case strings.HasPrefix(model, "claude-sonnet-"):
		return "sonnet"
	}
	return ""
}

// Codex requires a governing long window. A fresh full short window is the
// sole exception; a partial zero-percent burst reading does not imply capacity.
func nativeUsageScore(provider, model string, q nativeQuotaSnapshot, now time.Time) (float64, bool) {
	long, short, family := -1.0, -1.0, -1.0
	for _, w := range q.Windows {
		if !nativeWindowCurrent(w, now) {
			continue
		}
		switch w.Window {
		case "weekly", "monthly":
			if provider == "openai" && w.Window == "weekly" && (strings.EqualFold(q.Plan, "free") || strings.EqualFold(q.Plan, "go")) {
				continue
			}
			long = math.Max(long, w.Percent)
		case "5h":
			if provider != "openai" || w.ResetAt != nil || now.Sub(w.ObservedAt) <= 5*time.Minute {
				short = w.Percent
			}
		default:
			if w.Window == nativeModelFamily(model) {
				family = w.Percent
			}
		}
	}
	if provider == "openai" {
		if long < 0 {
			if short >= 100 {
				return 100, true
			}
			return 101, false
		}
		return math.Max(long, short), true
	}
	// The native Anthropic default is five-hour, with matching model-family
	// utilization as an additional floor. Weekly exhaustion still blocks affinity.
	score := math.Max(short, family)
	if score < 0 {
		return 100, false
	}
	return score, true
}
func nativeExhausted(provider, model string, q nativeQuotaSnapshot, now time.Time) bool {
	if provider == "openai" {
		score, known := nativeUsageScore(provider, model, q, now)
		return known && score >= 100
	}
	for _, w := range q.Windows {
		if nativeWindowCurrent(w, now) && w.Percent >= 100 && (w.Window == "5h" || w.Window == "weekly" || w.Window == nativeModelFamily(model)) {
			return true
		}
	}
	return false
}
func (a *inferenceAdapter) observeNativeResponse(ctx context.Context, p inference.Principal, t inference.Target, c inference.Credential, status int, h http.Header) error {
	if err := a.observeNativeFamily(ctx, p, t, c, status, h); err != nil {
		return err
	}
	if t.Auth != inference.OAuth {
		return nil
	}
	a.mu.Lock()
	d := a.connections[c.AccountID]
	a.mu.Unlock()
	if d.Metadata == nil {
		return nil
	}
	q := parseNativeQuota(d.Metadata.ProviderId, t.Model, status, h, time.Now())
	if (d.Metadata.ProviderId == "anthropic" || d.Metadata.ProviderId == "anthropic2") && status >= 200 && status < 300 {
		if family := nativeModelFamily(t.Model); family != "" {
			q.RejectionCleared = map[string]time.Time{family: time.Now()}
		}
	}
	if len(q.Windows) == 0 && len(q.Rejected) == 0 && len(q.RejectionCleared) == 0 {
		return nil
	}
	return a.writeNativeQuota(ctx, p, c.AccountID, c.CredentialVersion, q)
}

// parseNativeQuotaBody handles already-fetched usage responses. The caller must
// supply the dispatched credential version; this helper never performs a probe.
func parseNativeQuotaBody(provider string, raw []byte, now time.Time) (nativeQuotaSnapshot, error) {
	var root map[string]json.RawMessage
	if len(raw) > 1<<20 || json.Unmarshal(raw, &root) != nil {
		return nativeQuotaSnapshot{}, inference.ErrUnavailable
	}
	out := nativeQuotaSnapshot{}
	if provider == "openai" {
		_ = json.Unmarshal(root["plan_type"], &out.Plan)
		var limits struct{ Primary, Secondary, Tertiary json.RawMessage }
		var rate map[string]json.RawMessage
		_ = json.Unmarshal(root["rate_limit"], &rate)
		limits.Primary = rate["primary_window"]
		limits.Secondary = rate["secondary_window"]
		limits.Tertiary = rate["tertiary_window"]
		h := http.Header{}
		for _, item := range []struct {
			name string
			raw  json.RawMessage
		}{{"primary", limits.Primary}, {"secondary", limits.Secondary}, {"tertiary", limits.Tertiary}} {
			var w struct {
				Percent *float64 `json:"used_percent"`
				Reset   *float64 `json:"reset_at"`
				Seconds *float64 `json:"limit_window_seconds"`
			}
			if json.Unmarshal(item.raw, &w) != nil {
				continue
			}
			if w.Percent != nil {
				h.Set("x-codex-"+item.name+"-used-percent", strconv.FormatFloat(*w.Percent, 'f', -1, 64))
			}
			if w.Reset != nil {
				h.Set("x-codex-"+item.name+"-reset-at", strconv.FormatFloat(*w.Reset, 'f', -1, 64))
			}
			if w.Seconds != nil {
				h.Set("x-codex-"+item.name+"-window-minutes", strconv.FormatFloat(*w.Seconds/60, 'f', -1, 64))
			}
		}
		parsed := parseNativeQuota(provider, "", 200, h, now)
		out.Windows = parsed.Windows
		out.MonthlyPrimaryObservedAt = parsed.MonthlyPrimaryObservedAt
		if strings.EqualFold(out.Plan, "free") || strings.EqualFold(out.Plan, "go") {
			filtered := out.Windows[:0]
			for _, w := range out.Windows {
				if w.Window != "weekly" {
					filtered = append(filtered, w)
				}
			}
			out.Windows = filtered
		}
		if b, exists := root["credits"]; exists {
			out.CreditsObservedAt = now
			var c struct {
				HasCredits *bool           `json:"has_credits"`
				Unlimited  *bool           `json:"unlimited"`
				Balance    json.RawMessage `json:"balance"`
				Reached    *bool           `json:"overage_limit_reached"`
			}
			if string(b) != "null" && json.Unmarshal(b, &c) == nil {
				out.Credits = &nativeQuotaCredits{HasCredits: c.HasCredits, Unlimited: c.Unlimited, OverageLimitReached: c.Reached, ObservedAt: now}
				var n float64
				if json.Unmarshal(c.Balance, &n) != nil {
					var text string
					_ = json.Unmarshal(c.Balance, &text)
					n, _ = nativeNumber(text)
				}
				out.Credits.Balance = &n
			}
		}
		if _, exists := root["credits"]; !exists && string(rate["allowed"]) == "false" {
			out.Credits = nil
			out.CreditsObservedAt = now
		}
		if control, exists := root["spend_control"]; exists && string(control) != "null" {
			var c struct {
				Reached *bool `json:"reached"`
			}
			_ = json.Unmarshal(control, &c)
			allowed := c.Reached != nil && !*c.Reached
			if out.Credits != nil {
				out.Credits.Allowed = &allowed
			}
			if !allowed {
				out.Credits = nil
				out.CreditsObservedAt = now
			}
		}
	} else if provider == "anthropic" || provider == "anthropic2" {
		for _, pair := range [][2]string{{"5h", "five_hour"}, {"weekly", "seven_day"}, {"fable", "seven_day_fable"}, {"opus", "seven_day_opus"}, {"sonnet", "seven_day_sonnet"}} {
			var w struct {
				Percent *float64        `json:"utilization"`
				Reset   json.RawMessage `json:"resets_at"`
			}
			if json.Unmarshal(root[pair[1]], &w) != nil || w.Percent == nil || *w.Percent < 0 || *w.Percent > 100 {
				continue
			}
			window := nativeQuotaWindow{Window: pair[0], Percent: *w.Percent, ObservedAt: now}
			if len(w.Reset) > 0 && string(w.Reset) != "null" {
				var raw string
				if json.Unmarshal(w.Reset, &raw) == nil {
					if at, e := time.Parse(time.RFC3339Nano, raw); e == nil {
						window.ResetAt = &at
					} else {
						if raw == "" {
							raw = "invalid"
						}
						window.ResetAt = nativeReset(raw)
					}
				} else {
					window.ResetAt = nativeReset(string(w.Reset))
				}
			}
			out.Windows = append(out.Windows, window)
		}
		var limits []struct {
			Kind     string          `json:"kind"`
			Percent  *float64        `json:"percent"`
			ResetsAt json.RawMessage `json:"resets_at"`
			Scope    struct {
				Model struct {
					DisplayName string `json:"display_name"`
				} `json:"model"`
			} `json:"scope"`
		}
		_ = json.Unmarshal(root["limits"], &limits)
		for _, limit := range limits {
			if limit.Kind != "weekly_scoped" || limit.Percent == nil || *limit.Percent < 0 || *limit.Percent > 100 {
				continue
			}
			family := ""
			for _, name := range []string{"fable", "opus", "sonnet"} {
				if strings.Contains(strings.ToLower(limit.Scope.Model.DisplayName), name) {
					family = name
					break
				}
			}
			if family == "" {
				continue
			}
			var reset *time.Time
			if len(limit.ResetsAt) > 0 && string(limit.ResetsAt) != "null" {
				var text string
				if json.Unmarshal(limit.ResetsAt, &text) == nil {
					if at, err := time.Parse(time.RFC3339Nano, text); err == nil {
						reset = &at
					} else {
						if text == "" {
							text = "invalid"
						}
						reset = nativeReset(text)
					}
				} else {
					reset = nativeReset(string(limit.ResetsAt))
				}
			}
			out.Windows = append(out.Windows, nativeQuotaWindow{Window: family, Percent: *limit.Percent, ObservedAt: now, ResetAt: reset})
		}
	}
	return out, nil
}
