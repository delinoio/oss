// SPDX-License-Identifier: Apache-2.0
// Provider protocols adapted from pinned upstream; see snapshot/NOTICE.
package providers

import (
	"context"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"math"
	"strconv"
	"strings"
	"time"
)

var ErrProfileUnavailable = errors.New("provider profile unavailable")

type HealthState string

const (
	HealthAvailable HealthState = "available"
	HealthUnknown   HealthState = "unknown"
	HealthRejected  HealthState = "rejected"
)

type QuotaWindow struct {
	Kind        string
	Model       string
	UsedPercent float64
	ResetAt     time.Time
	Duration    time.Duration
}

// Monetary/credit balances never imply a quota percentage or routing entitlement.
type Balance struct {
	Kind                           string
	ExpiringAmount                 float64
	ExpiresAt                      time.Time
	Unit                           string
	Total, Used, Remaining, Frozen float64
}
type Health struct {
	State      HealthState
	ObservedAt time.Time
	Windows    []QuotaWindow
	Balances   []Balance
	Exhausted  *bool
}

func finite(v any) (float64, bool) {
	var n float64
	switch x := v.(type) {
	case float64:
		n = x
	case string:
		var e error
		n, e = strconv.ParseFloat(x, 64)
		if e != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return n, !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0
}
func resetTime(v any) time.Time {
	if s, ok := v.(string); ok {
		if t, e := time.Parse(time.RFC3339Nano, s); e == nil {
			return t
		}
	}
	if n, ok := finite(v); ok && n > 0 && n < 1e15 {
		if n > 1e12 {
			return time.UnixMilli(int64(n))
		}
		return time.Unix(int64(n), 0)
	}
	return time.Time{}
}
func window(kind, model string, row map[string]any, percent, reset string) (QuotaWindow, bool) {
	n, ok := finite(row[percent])
	if !ok || n > 100 {
		return QuotaWindow{}, false
	}
	return QuotaWindow{Kind: kind, Model: model, UsedPercent: n, ResetAt: resetTime(row[reset])}, true
}
func precise(row map[string]any, a, b string) (float64, bool) {
	if n, ok := finite(row[a]); ok {
		return n, true
	}
	return finite(row[b])
}

// ProbeHealth performs a bounded support request, never an inference turn. A
// rejection applies only to this credential generation; caller owns retirement,
// retry spacing and freshness. Unknown does not erase previously valid evidence.
func (a *Auth) ProbeHealth(ctx context.Context, s core.Scope, c Credential) (Health, error) {
	out := Health{State: HealthUnknown, ObservedAt: a.now()}
	if !validScope(s) || c.Scope != s {
		return out, ErrScope
	}
	if !safeString(c.AccessToken) {
		return out, ErrReauthenticate
	}
	switch c.Provider {
	case "google-antigravity":
		return a.googleHealth(ctx, s, c)
	case "cursor", "xai", "meta-muse", "devin", "kimi", "kimi-responses", "command-code":
		return a.accountHealth(ctx, s, c)
	}
	endpoint, method := "", "GET"
	h := bearer(c.AccessToken)
	var body any
	switch c.Provider {
	case "anthropic", "anthropic2":
		endpoint = "https://api.anthropic.com/api/oauth/usage"
		h.Set("User-Agent", "claude-cli/2.1.280 (external, cli)")
		h.Set("Anthropic-Beta", "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27,prompt-caching-scope-2026-01-05")
	case "openai":
		endpoint = "https://chatgpt.com/backend-api/wham/usage"
		if c.AccountID != "" {
			if !safeString(c.AccountID) {
				return out, ErrResponse
			}
			h.Set("ChatGPT-Account-Id", c.AccountID)
		}
	case "kiro":
		var err error
		endpoint, body, h, err = kiroManagement(c, true)
		if err != nil {
			return out, err
		}
		method = "POST"
	case "dsh-account":
		endpoint = "https://platform.deepseek.com/api/v0/users/get_user_summary"
		h.Del("Authorization")
		h.Set("X-Dsh-Auth-Token", c.AccessToken)
	case "workbuddy-ai":
		var err error
		h, err = workBuddyHeaders(c)
		if err != nil {
			return out, err
		}
		method = "POST"
		endpoint = "https://www.workbuddy.ai/billing/meter/get-user-resource-summary"
		body = map[string]any{}
	case "google-antigravity":
		if !safeString(c.ProjectID) {
			return out, ErrReauthenticate
		}
		endpoint = "https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary"
		method = "POST"
		body = map[string]string{"project": c.ProjectID}
		h.Set("User-Agent", "antigravity/1.0")
	default:
		return out, nil
	}
	m, status, err := a.request(ctx, s, method, endpoint, body, h)
	if err != nil {
		return out, err
	}
	if status == 401 || status == 403 {
		out.State = HealthRejected
		return out, nil
	}
	if status != 200 {
		return out, authFailure(status, m)
	}
	add := func(k, model string, r map[string]any, p, t string) {
		if w, ok := window(k, model, r, p, t); ok {
			out.Windows = append(out.Windows, w)
		}
	}
	switch c.Provider {
	case "anthropic", "anthropic2":
		add("five-hour", "", obj(m, "five_hour"), "utilization", "resets_at")
		add("weekly", "", obj(m, "seven_day"), "utilization", "resets_at")
		for _, family := range []string{"fable", "opus", "sonnet"} {
			add("weekly", family, obj(m, "seven_day_"+family), "utilization", "resets_at")
		}
		limits, _ := m["limits"].([]any)
		for _, v := range limits {
			r, ok := v.(map[string]any)
			if !ok || str(r, "kind") != "weekly_scoped" {
				continue
			}
			name := strings.ToLower(str(obj(obj(r, "scope"), "model"), "display_name"))
			for _, family := range []string{"fable", "opus", "sonnet"} {
				if strings.Contains(name, family) {
					found := false
					for _, w := range out.Windows {
						found = found || w.Model == family
					}
					if !found {
						add("weekly", family, r, "utilization", "resets_at")
					}
					break
				}
			}
		}
	case "openai":
		rate := obj(m, "rate_limit")
		for _, key := range []string{"primary_window", "secondary_window", "tertiary_window"} {
			r := obj(rate, key)
			w, ok := window("rate-limit", "", r, "used_percent", "reset_at")
			if !ok {
				continue
			}
			if n, ok := finite(r["limit_window_seconds"]); ok && n > 0 && n < 1e9 {
				w.Duration = time.Duration(n) * time.Second
				if n == 18000 {
					w.Kind = "five-hour"
				}
				if n == 604800 {
					w.Kind = "weekly"
				}
			}
			out.Windows = append(out.Windows, w)
		}
	case "kiro":
		rows, _ := m["usageBreakdownList"].([]any)
		var row map[string]any
		for _, kind := range []string{"AGENTIC_REQUEST", "CREDIT"} {
			for _, raw := range rows {
				r, ok := raw.(map[string]any)
				if ok && str(r, "resourceType") == kind {
					row = r
					break
				}
			}
			if row != nil {
				break
			}
		}
		used, uok := precise(row, "currentUsageWithPrecision", "currentUsage")
		limit, lok := precise(row, "usageLimitWithPrecision", "usageLimit")
		if uok && lok && limit > 0 {
			out.Windows = append(out.Windows, QuotaWindow{Kind: "monthly", UsedPercent: math.Min(100, 100*used/limit), ResetAt: resetTime(m["nextDateReset"])})
			v := used >= limit && str(obj(m, "overageConfiguration"), "overageStatus") == "DISABLED"
			out.Exhausted = &v
		}
		trial := obj(row, "freeTrialInfo")
		used, uok = precise(trial, "currentUsageWithPrecision", "currentUsage")
		limit, lok = precise(trial, "usageLimitWithPrecision", "usageLimit")
		if uok && lok && limit > 0 {
			out.Windows = append(out.Windows, QuotaWindow{Kind: "trial", UsedPercent: math.Min(100, 100*used/limit)})
		}
	case "dsh-account":
		d := obj(m, "data")
		if m["code"] == float64(0) && d["biz_code"] == float64(0) && obj(d, "biz_data") != nil {
			out.State = HealthAvailable
			return out, nil
		}
	case "workbuddy-ai":
		if m["code"] != float64(0) && m["code"] != "0" {
			return out, ErrResponse
		}
		d := obj(m, "data")
		rows, ok := d["Packages"].([]any)
		if !ok || len(rows) > 512 {
			return out, ErrResponse
		}
		b := Balance{Unit: "credits"}
		for _, raw := range rows {
			r, _ := raw.(map[string]any)
			total, tok := finite(r["CycleTotalCapacity"])
			used, uok := finite(r["CycleUsedCapacity"])
			frozen := 0.0
			if r["CycleFrozenCapacity"] != nil {
				var ok bool
				frozen, ok = finite(r["CycleFrozenCapacity"])
				if !ok {
					return out, ErrResponse
				}
			}
			if !tok || !uok {
				return out, ErrResponse
			}
			remaining := math.Max(0, total-used-frozen)
			if r["CycleRemainCapacity"] != nil {
				var ok bool
				remaining, ok = finite(r["CycleRemainCapacity"])
				if !ok {
					return out, ErrResponse
				}
			}
			b.Total += total
			b.Used += used
			b.Remaining += remaining
			b.Frozen += frozen
		}
		if b.Total == 0 && c.Metadata["enterpriseId"] != "" {
			team, e := a.workBuddy(ctx, s, "POST", "/billing/meter/get-enterprise-user-usage", map[string]any{}, h)
			if e != nil {
				return out, e
			}
			total, tok := finite(team["limitNum"])
			used, uok := finite(team["credit"])
			if !tok || !uok {
				return out, ErrResponse
			}
			b = Balance{Unit: "credits", Total: total, Used: used, Remaining: math.Max(0, total-used)}
		}
		groups, _ := d["CreditGroups"].([]any)
		for _, raw := range groups {
			r, _ := raw.(map[string]any)
			kind := str(r, "Kind")
			if !contains([]string{"plan_base", "plan_bonus", "addon", "benefit"}, kind) {
				kind = "other"
			}
			if g, ok := workBuddyAmount(r); ok {
				g.Kind = kind
				out.Balances = append(out.Balances, g)
			}
		}
		if b.Remaining > 0 {
			amount, at, e := a.workBuddyExpiry(ctx, s, d, h)
			if e == nil {
				b.ExpiringAmount = amount
				b.ExpiresAt = at
			} else if ctx.Err() != nil {
				return out, ctx.Err()
			}
		}
		out.Balances = append([]Balance{b}, out.Balances...)
	case "google-antigravity":
		out.Windows = antigravityWindows(m)
	}
	if len(out.Windows) > 0 || len(out.Balances) > 0 {
		out.State = HealthAvailable
	}
	return out, nil
}
func antigravityWindows(m map[string]any) []QuotaWindow {
	out := []QuotaWindow{}
	groups, _ := m["groups"].([]any)
	for _, raw := range groups {
		g, _ := raw.(map[string]any)
		name := strings.ToLower(str(g, "displayName") + " " + str(g, "description"))
		family := ""
		if argonToken.MatchString(str(g, "displayName")) {
			family = "gemini-4-argon"
		} else if strings.Contains(name, "gemini") {
			family = "gemini"
		} else if strings.Contains(name, "claude") || strings.Contains(name, "3p") || strings.Contains(name, "gpt") {
			family = "claude"
		}
		if family == "" {
			continue
		}
		buckets, _ := g["buckets"].([]any)
		for _, raw := range buckets {
			b, _ := raw.(map[string]any)
			label := strings.ToLower(str(b, "window") + " " + str(b, "bucketId") + " " + str(b, "displayName"))
			kind := ""
			if strings.Contains(label, "week") {
				kind = "weekly"
			} else if strings.Contains(label, "5h") || strings.Contains(label, "five") {
				kind = "five-hour"
			}
			if kind == "" {
				continue
			}
			bucketIdentity := str(b, "bucketId") + " " + str(b, "displayName")
			bucketFamily := family
			if argonToken.MatchString(bucketIdentity) {
				bucketFamily = "gemini-4-argon"
			} else if strings.Contains(strings.ToLower(bucketIdentity), "gemini") {
				bucketFamily = "gemini"
			} else if strings.Contains(strings.ToLower(bucketIdentity), "claude") {
				bucketFamily = "claude"
			}
			remaining := obj(b, "remaining")
			if remaining == nil {
				remaining = b
			}
			n, ok := finite(remaining["remainingFraction"])
			if !ok {
				n, ok = finite(remaining["remainingPercentage"])
			}
			if !ok || n > 1 {
				continue
			}
			out = append(out, QuotaWindow{Kind: kind, Model: bucketFamily, UsedPercent: (1 - n) * 100, ResetAt: resetTime(b["resetTime"])})
		}
	}
	return out
}
