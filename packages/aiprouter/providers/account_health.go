// SPDX-License-Identifier: Apache-2.0
// Pinned protocols: snapshot/NOTICE; Devin derivation: snapshot/LICENSE-DEVIN-MIT.
package providers

import (
	"context"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"google.golang.org/protobuf/encoding/protowire"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

func first(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v := m[k]; v != nil {
			return v
		}
	}
	return nil
}
func unwrapped(m map[string]any) map[string]any {
	if d := obj(m, "data"); d != nil {
		return d
	}
	return m
}
func ratioWindow(kind string, r map[string]any, limitKey, usedKey string) (QuotaWindow, bool) {
	limit, ok := finite(r[limitKey])
	used, uok := finite(r[usedKey])
	if !ok || !uok || limit <= 0 {
		return QuotaWindow{}, false
	}
	return QuotaWindow{Kind: kind, UsedPercent: math.Min(100, 100*used/limit), ResetAt: resetTime(first(r, "resetAt", "reset_at", "resetTime", "reset_time"))}, true
}
func (a *Auth) accountHealth(ctx context.Context, s core.Scope, c Credential) (Health, error) {
	out := Health{State: HealthUnknown, ObservedAt: a.now()}
	h := bearer(c.AccessToken)
	// All optional fallbacks have finite attempt counts. Each send re-enters Executor.
	read := func(method, endpoint string, body any, headers http.Header) (map[string]any, error) {
		m, status, e := a.request(ctx, s, method, endpoint, body, headers)
		if e != nil {
			return nil, e
		}
		if status != 200 {
			return nil, authFailure(status, m)
		}
		return m, nil
	}
	var err error
	switch c.Provider {
	case "cursor":
		h.Set("Connect-Protocol-Version", "1")
		var m map[string]any
		m, err = read("POST", "https://api2.cursor.sh/aiserver.v1.DashboardService/GetCurrentPeriodUsage", map[string]any{}, h)
		if err == nil {
			r := obj(m, "planUsage")
			out.Windows = cursorUsage(r, first(m, "billingCycleEnd", "periodEnd"))
		}
		if len(out.Windows) == 0 && !terminalHealth(err) && ctx.Err() == nil {
			m, err = read("GET", "https://api2.cursor.sh/api/usage/summary", nil, h)
			if err == nil {
				out.Windows = cursorUsage(obj(obj(m, "individualUsage"), "plan"), m["billingCycleEnd"])
			}
		}
		if len(out.Windows) == 0 && !terminalHealth(err) && ctx.Err() == nil {
			m, err = read("GET", "https://api2.cursor.sh/auth/usage", nil, h)
			if err == nil {
				keys := make([]string, 0, len(m))
				for k := range m {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				keys = append([]string{"gpt-4"}, keys...)
				for _, k := range keys {
					r := obj(m, k)
					used, uok := finite(first(r, "numRequests", "used"))
					limit, lok := finite(first(r, "maxRequestUsage", "limit", "maxRequests"))
					if uok && lok && limit > 0 {
						w := QuotaWindow{Kind: "monthly", UsedPercent: math.Min(100, 100*used/limit)}
						start := resetTime(first(m, "startOfMonth", "billingCycleStart"))
						if !start.IsZero() {
							w.ResetAt = start.UTC().AddDate(0, 1, 0)
						}
						out.Windows = []QuotaWindow{w}
						break
					}
				}
			}
		}
	case "xai":
		id := c.AccountID
		if id == "" {
			id = str(jwtClaims(c.AccessToken), "sub")
		}
		if safeString(id) {
			h.Set("X-Xai-Token-Auth", "xai-grok-cli")
			h.Set("X-Authenticateresponse", "authenticate-response")
			h.Set("X-Grok-Client-Version", "1.0.46")
			h.Set("X-Userid", id)
			var m map[string]any
			m, err = read("GET", "https://cli-chat-proxy.grok.com/v1/billing?format=credits", nil, h)
			if err == nil {
				r := obj(m, "config")
				period := obj(r, "currentPeriod")
				if str(period, "type") == "USAGE_PERIOD_TYPE_WEEKLY" {
					percent := 0.0
					ok := true
					if r["creditUsagePercent"] != nil {
						percent, ok = finite(r["creditUsagePercent"])
					}
					if ok && percent <= 100 {
						out.Windows = []QuotaWindow{{Kind: "weekly", UsedPercent: percent, ResetAt: resetTime(period["end"])}}
					}
				}
			}
		}
		if len(out.Windows) == 0 && !terminalHealth(err) && ctx.Err() == nil {
			var m map[string]any
			m, err = read("GET", "https://cli-chat-proxy.grok.com/v1/billing", nil, bearer(c.AccessToken))
			if err == nil {
				r := obj(m, "config")
				used, uok := cents(r["used"])
				limit, lok := cents(r["monthlyLimit"])
				if uok && lok && limit > 0 {
					out.Windows = []QuotaWindow{{Kind: "monthly", UsedPercent: math.Min(100, 100*used/limit), ResetAt: resetTime(r["billingPeriodEnd"])}}
				}
			}
		}
	case "meta-muse":
		token := c.Metadata["oauthAccessToken"]
		if !safeString(token) {
			return out, nil
		}
		h = bearer(token)
		h.Set("X-Api-Version", "1.0.0")
		var m map[string]any
		m, err = read("POST", "https://api.meta.ai/muse-code/key", map[string]any{}, h)
		// This account token is distinct from the inference key. Its rejection must
		// never retire or overwrite a still-valid inference key.
		if terminalHealth(err) {
			return out, nil
		}
		if err == nil && m["is_subs_active"] != false {
			out.Windows = museWindows(obj(m, "subs_usage"))
		}
	case "kimi", "kimi-responses":
		var m map[string]any
		m, err = read("GET", "https://api.kimi.com/coding/v1/usages", nil, h)
		if err == nil {
			out.Windows = kimiWindows(m)
		}
	case "command-code":
		var m map[string]any
		m, err = read("GET", "https://api.commandcode.ai/alpha/whoami", nil, h)
		if err != nil {
			return healthFailure(out, err)
		}
		org := str(obj(unwrapped(m), "org"), "id")
		q := ""
		if org != "" {
			if !safeString(org) {
				return out, ErrResponse
			}
			q = "?orgId=" + url.QueryEscape(org)
		}
		m, err = read("GET", "https://api.commandcode.ai/alpha/billing/credits"+q, nil, h)
		if err == nil {
			m = unwrapped(m)
			credits := obj(m, "credits")
			if credits != nil {
				sub, subErr := read("GET", "https://api.commandcode.ai/alpha/billing/subscriptions"+q, nil, h)
				if subErr == nil {
					start := str(unwrapped(sub), "currentPeriodStart")
					if safeString(start) {
						sep := "?"
						if q != "" {
							sep = "&"
						}
						usage, usageErr := read("GET", "https://api.commandcode.ai/alpha/usage/summary"+q+sep+"since="+url.QueryEscape(start), nil, h)
						if usageErr == nil {
							used, uok := finite(first(unwrapped(usage), "totalCost", "totalMonthlyCredits"))
							remain := 0.0
							present := false
							for _, key := range []string{"monthlyCredits", "purchasedCredits", "freeCredits"} {
								if n, ok := finite(credits[key]); ok {
									remain += n
									present = true
								}
							}
							if uok && present {
								out.Balances = []Balance{{Unit: "USD", Used: used, Remaining: remain, Total: used + remain}}
							}
						}
					}
				}
				if ctx.Err() != nil {
					return out, ctx.Err()
				}
			}
			limits := obj(m, "windowLimits")
			for _, pair := range [][2]string{{"fiveHour", "five-hour"}, {"weekly", "weekly"}} {
				if w, ok := ratioWindow(pair[1], obj(limits, pair[0]), "cap", "used"); ok {
					out.Windows = append(out.Windows, w)
				}
			}
		}
	case "devin":
		return a.devinHealth(ctx, s, c)
	}
	if err != nil {
		return healthFailure(out, err)
	}
	if len(out.Windows) > 0 || len(out.Balances) > 0 {
		out.State = HealthAvailable
	}
	return out, nil
}
func terminalHealth(err error) bool {
	var ae *AuthError
	return errors.As(err, &ae) && (ae.Status == 401 || ae.Status == 403)
}
func healthFailure(out Health, err error) (Health, error) {
	if terminalHealth(err) {
		out.State = HealthRejected
		return out, nil
	}
	return out, err
}
func cents(v any) (float64, bool) {
	if m, ok := v.(map[string]any); ok {
		return finite(m["cents"])
	}
	return finite(v)
}
func cursorUsage(r map[string]any, reset any) []QuotaWindow {
	out := []QuotaWindow{}
	percent, ok := finite(first(r, "totalPercentUsed", "percentUsed"))
	if !ok {
		limit, lok := finite(first(r, "limit", "limitCents", "totalLimitCents"))
		used, uok := finite(first(r, "includedSpend", "usedCents", "used"))
		if !uok {
			if remain, rok := finite(first(r, "remaining", "remainingCents")); lok && rok {
				used = math.Max(0, limit-remain)
				uok = true
			} else {
				used, uok = finite(r["totalSpend"])
			}
		}
		if lok && uok && limit > 0 {
			percent = math.Min(100, 100*used/limit)
			ok = true
		}
	}
	if ok && percent <= 100 {
		out = append(out, QuotaWindow{Kind: "monthly", UsedPercent: percent, ResetAt: resetTime(reset)})
	}
	for _, pair := range [][2]string{{"autoPercentUsed", "first-party"}, {"apiPercentUsed", "api"}} {
		if n, ok := finite(r[pair[0]]); ok && n <= 100 {
			out = append(out, QuotaWindow{Kind: "monthly", Model: pair[1], UsedPercent: n, ResetAt: resetTime(reset)})
		}
	}
	return out
}
func museWindows(m map[string]any) []QuotaWindow {
	out := []QuotaWindow{}
	for _, pair := range [][2]string{{"window", "subscription"}, {"weekly", "weekly"}} {
		r := obj(m, pair[0])
		w, ok := window(pair[1], "", r, "used_percent", "resets_at")
		if !ok {
			continue
		}
		if pair[0] == "window" {
			if n, ok := finite(r["window_duration_mins"]); ok && n > 0 && n < 1e7 {
				w.Duration = time.Duration(n) * time.Minute
				if n == 300 {
					w.Kind = "five-hour"
				}
			}
		}
		out = append(out, w)
	}
	return out
}
func kimiWindows(m map[string]any) []QuotaWindow {
	if m["usage"] == nil && m["limits"] == nil && m["totalQuota"] == nil {
		m = unwrapped(m)
	}
	out := []QuotaWindow{}
	seen := map[string]bool{}
	add := func(kind string, r map[string]any) {
		if seen[kind] {
			return
		}
		if r == nil {
			return
		}
		copy := map[string]any{}
		for k, v := range r {
			copy[k] = v
		}
		if copy["used"] == nil {
			limit, lok := finite(copy["limit"])
			remain, rok := finite(copy["remaining"])
			if lok && rok && remain <= limit {
				copy["used"] = limit - remain
			}
		}
		w, ok := ratioWindow(kind, copy, "limit", "used")
		if !ok {
			copy["percent"] = first(copy, "utilization", "percent", "usedPercent", "used_percent")
			w, ok = window(kind, "", copy, "percent", "resetTime")
		}
		if ok {
			out = append(out, w)
			seen[kind] = true
		}
	}
	add("weekly", obj(m, "usage"))
	add("total", obj(m, "totalQuota"))
	rows, _ := m["limits"].([]any)
	for _, raw := range rows {
		r, _ := raw.(map[string]any)
		detail := obj(r, "detail")
		if detail == nil {
			detail = r
		}
		win := obj(r, "window")
		duration, _ := finite(first(win, "duration"))
		if duration == 0 {
			duration, _ = finite(first(r, "duration"))
		}
		unit := strings.ToUpper(str(win, "timeUnit") + str(r, "timeUnit") + str(detail, "timeUnit"))
		label := strings.ToLower(str(r, "name") + str(r, "title") + str(r, "scope") + str(detail, "name") + str(detail, "title"))
		kind := ""
		if (duration == 300 && strings.Contains(unit, "MINUTE")) || (duration == 5 && strings.Contains(unit, "HOUR")) || strings.Contains(label, "5 hour") || strings.Contains(label, "5h") {
			kind = "five-hour"
		}
		if (duration == 7 && strings.Contains(unit, "DAY")) || (duration == 168 && strings.Contains(unit, "HOUR")) || strings.Contains(label, "weekly") {
			kind = "weekly"
		}
		if kind != "" {
			add(kind, detail)
		}
	}
	return out
}
func (a *Auth) devinHealth(ctx context.Context, s core.Scope, c Credential) (Health, error) {
	out := Health{State: HealthUnknown, ObservedAt: a.now()}
	fields, err := a.devinRPC(ctx, s, c, "/exa.seat_management_pb.SeatManagementService/GetUserStatus", "")
	if err != nil {
		var ae *AuthError
		if errors.As(err, &ae) && ae.Status == 403 {
			return out, nil
		}
		return healthFailure(out, err)
	}
	sub := func(f []protoField, n protowire.Number) []byte {
		var b []byte
		for _, v := range f {
			if v.n == n && v.t == protowire.BytesType {
				b = v.b
			}
		}
		return b
	}
	user, err := protoFields(sub(fields, 1))
	if err != nil {
		return out, err
	}
	raw := sub(user, 13)
	if raw == nil {
		return out, ErrResponse
	}
	status, err := protoFields(raw)
	if err != nil {
		return out, err
	}
	planRaw := sub(fields, 2)
	if planRaw == nil {
		planRaw = sub(status, 1)
	}
	if planRaw == nil {
		return out, ErrResponse
	}
	plan, err := protoFields(planRaw)
	if err != nil {
		return out, err
	}
	values := map[protowire.Number]int64{}
	for _, f := range status {
		if f.n >= 4 && f.n <= 18 {
			if f.n == 13 {
				continue
			}
			if f.t != protowire.VarintType {
				return out, ErrResponse
			}
			values[f.n] = int64(f.v)
		}
	}
	pv := map[protowire.Number]int64{}
	for _, f := range plan {
		if f.t == protowire.VarintType {
			pv[f.n] = int64(f.v)
		}
	}
	for _, w := range []struct {
		k          string
		p, r, hide protowire.Number
	}{{"daily", 14, 17, 36}, {"weekly", 15, 18, 37}} {
		reset := resetTime(float64(values[w.r]))
		if pv[w.hide] != 1 && reset.After(a.now()) {
			out.Windows = append(out.Windows, QuotaWindow{Kind: w.k, UsedPercent: 100 - math.Max(0, math.Min(100, float64(values[w.p]))), ResetAt: reset})
		}
	}
	evidence := false
	for _, n := range []protowire.Number{4, 6, 7, 8} {
		_, ok := values[n]
		evidence = evidence || ok
	}
	if evidence && (pv[35] == 1 || (pv[35] == 0 && values[17] == 0 && values[18] == 0)) && values[4] >= 0 && values[6] >= 0 && values[7] >= 0 && values[8] >= 0 {
		used := float64(values[6]) + float64(values[7])
		remaining := float64(values[4]) + float64(values[8])
		percent := 100.0
		if remaining > 0 {
			percent = 100 * used / (used + remaining)
		}
		out.Windows = append(out.Windows, QuotaWindow{Kind: "monthly", UsedPercent: percent})
	}
	out.State = HealthAvailable
	return out, nil
}
