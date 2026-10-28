// SPDX-License-Identifier: Apache-2.0
// Pinned support endpoints and field mappings: snapshot/NOTICE.
package providers

import (
	"context"
	"github.com/delinoio/oss/packages/tairouter/core"
	"math"
	"net/url"
	"strings"
)

type keyProbe struct {
	kind, endpoint string
	bare           bool
}

// Exact destinations, including their paths, bind support requests to the selected
// key. An edited base URL never inherits the original provider's support endpoint.
func keyHealthProbe(base string) keyProbe {
	b := strings.TrimRight(base, "/")
	kind, endpoint := "", ""
	switch b {
	case "https://api.kimi.com/coding/v1":
		kind = "kimi"
	case "https://api.commandcode.ai", "https://api.commandcode.ai/provider/v1":
		kind = "command-code"
	case "https://api.a6api.com", "https://api.a6api.com/v1":
		kind = "a6api"
		endpoint = "https://api.a6api.com/dashboard/billing/subscription"
	case "https://openrouter.ai/api/v1":
		kind = "openrouter"
		endpoint = b + "/key"
	case "https://opencode.ai/zen/go/v1":
		kind = "opencode-go"
		endpoint = b + "/usage"
	case "https://api.deepseek.com", "https://api.deepseek.com/v1":
		kind = "deepseek"
		endpoint = "https://api.deepseek.com/user/balance"
	case "https://api.cline.bot", "https://api.cline.bot/api/v1":
		kind = "cline"
		endpoint = "https://api.cline.bot/api/v1/users/me/plan/usage-limits"
	case "https://ollama.com", "https://ollama.com/v1", "https://ollama.com/api":
		kind = "ollama"
		endpoint = "https://ollama.com/api/balance"
	case "https://api.z.ai", "https://api.z.ai/api/coding/paas/v4", "https://api.z.ai/api/anthropic", "https://api.z.ai/api/v1":
		kind = "zai"
		endpoint = "https://api.z.ai/api/monitor/usage/quota/limit"
	case "https://open.bigmodel.cn", "https://open.bigmodel.cn/api/coding/paas/v4", "https://open.bigmodel.cn/api/v1":
		return keyProbe{"zai", "https://open.bigmodel.cn/api/monitor/usage/quota/limit", true}
	case "https://api.minimax.io/v1", "https://api.minimaxi.com/v1":
		kind = "minimax"
		endpoint = strings.TrimSuffix(b, "/v1") + "/v1/api/openplatform/coding_plan/remains"
	case "https://api.moonshot.ai/v1", "https://api.moonshot.cn/v1":
		kind = "moonshot"
		endpoint = b + "/users/me/balance"
	case "https://api.venice.ai/api/v1":
		kind = "venice"
		endpoint = b + "/billing/balance"
	case "https://api.synthetic.new/v2", "https://api.synthetic.new/openai/v1":
		kind = "synthetic"
		endpoint = "https://api.synthetic.new/v2/quotas"
	case "https://api.deepinfra.com", "https://api.deepinfra.com/v1/openai":
		kind = "deepinfra"
		endpoint = "https://api.deepinfra.com/payment/checklist?compute_owed=true"
	case "https://api.neuralwatt.com/v1":
		kind = "neuralwatt"
		endpoint = b + "/quota"
	case "https://api.abliteration.ai", "https://api.abliteration.ai/v1":
		kind = "abliteration"
		endpoint = "https://api.abliteration.ai/v1/credits"
	}
	return keyProbe{kind, endpoint, false}
}

// ProbeConnectionHealth handles a selected explicit API key or account credential.
// No key-pool walking, environment references, home lookup or destination fallback.
func (a *Auth) ProbeConnectionHealth(ctx context.Context, s core.Scope, c Connection) (Health, error) {
	out := Health{State: HealthUnknown, ObservedAt: a.now()}
	if !validScope(s) || (c.Credential != nil && c.Credential.Scope != s) {
		return out, ErrScope
	}
	r, err := Resolve(c, "__health__", InboundResponses)
	if err != nil {
		return out, err
	}
	if c.Credential != nil {
		return a.ProbeHealth(ctx, s, *c.Credential)
	}
	p := keyHealthProbe(r.BaseURL)
	if p.kind == "" || c.APIKey == "" {
		return out, nil
	}
	if p.kind == "kimi" || p.kind == "command-code" {
		return a.ProbeHealth(ctx, s, Credential{Scope: s, Provider: p.kind, AccessToken: c.APIKey})
	}
	h := bearer(c.APIKey)
	if p.bare {
		h.Set("Authorization", c.APIKey)
	}
	m, status, err := a.request(ctx, s, "GET", p.endpoint, nil, h)
	if err != nil {
		return out, err
	}
	if status == 401 || status == 403 {
		out.State = HealthRejected
		return out, nil
	}
	if p.kind == "cline" && status == 404 {
		return out, nil
	}
	if status != 200 {
		return out, authFailure(status, m)
	}
	if p.kind == "a6api" {
		subscription := unwrapped(m)
		m, status, err = a.request(ctx, s, "GET", "https://api.a6api.com/api/usage/token/", nil, h)
		if err != nil {
			return out, err
		}
		if status != 200 {
			return healthFailure(out, authFailure(status, m))
		}
		return a6Health(out, subscription, unwrapped(m))
	}
	out = parseKeyHealth(out, p.kind, m, p.endpoint)
	if p.kind == "ollama" && out.State == HealthUnknown {
		m, status, err = a.request(ctx, s, "GET", "https://ollama.com/api/usage", nil, h)
		if err != nil {
			return out, err
		}
		if status != 200 {
			return healthFailure(out, authFailure(status, m))
		}
		out = parseKeyHealth(out, "ollama-usage", m, p.endpoint)
	}
	return out, nil
}
func parseKeyHealth(out Health, kind string, m map[string]any, endpoint string) Health {
	d := unwrapped(m)
	add := func(k string, r map[string]any, p, t string) {
		if w, ok := window(k, "", r, p, t); ok {
			out.Windows = append(out.Windows, w)
		}
	}
	ratio := func(k string, r map[string]any, l, u string) {
		if w, ok := ratioWindow(k, r, l, u); ok {
			out.Windows = append(out.Windows, w)
		}
	}
	balance := func(unit string, v any) {
		if n, ok := finite(v); ok {
			out.Balances = append(out.Balances, Balance{Unit: unit, Remaining: n})
		}
	}
	switch kind {
	case "openrouter":
		limit, lok := finite(d["limit"])
		if !lok || limit == 0 {
			out.State = HealthAvailable
			break
		}
		used, uok := finite(d["usage"])
		if remaining, rok := finite(d["limit_remaining"]); rok {
			used = math.Max(0, limit-remaining)
			uok = true
		}
		if uok {
			out.Windows = append(out.Windows, QuotaWindow{Kind: "spending-cap", UsedPercent: math.Min(100, 100*used/limit)})
			out.Balances = append(out.Balances, Balance{Unit: "USD", Total: limit, Used: used, Remaining: math.Max(0, limit-used)})
		}
	case "opencode-go":
		r := obj(m, "usage")
		for _, pair := range [][2]string{{"rolling", "five-hour"}, {"weekly", "weekly"}, {"monthly", "monthly"}} {
			add(pair[1], obj(r, pair[0]), "percent", "resetsAt")
		}
	case "deepseek":
		rows, _ := m["balance_infos"].([]any)
		for _, raw := range rows {
			r, _ := raw.(map[string]any)
			unit := str(r, "currency")
			if unit == "USD" || unit == "CNY" {
				balance(unit, first(r, "total_balance", "granted_balance", "topped_up_balance"))
			}
		}
	case "cline":
		rows, _ := d["limits"].([]any)
		for _, raw := range rows {
			r, _ := raw.(map[string]any)
			k := str(r, "type")
			if k == "five_hour" {
				k = "five-hour"
			}
			if k == "five-hour" || k == "weekly" || k == "monthly" {
				add(k, r, "percentUsed", "resetsAt")
			}
		}
	case "ollama", "ollama-usage":
		r := obj(m, "included")
		if kind == "ollama-usage" {
			r = obj(m, "limits")
		}
		for _, pair := range [][2]string{{"session", "five-hour"}, {"weekly", "weekly"}, {"monthly", "monthly"}} {
			b := obj(r, pair[0])
			n, ok := finite(b["remaining_percent"])
			if ok {
				n = 100 - n
			} else if n, ok = finite(b["usage"]); ok {
				n *= 100
			}
			if ok && n >= 0 && n <= 100 {
				out.Windows = append(out.Windows, QuotaWindow{Kind: pair[1], UsedPercent: n, ResetAt: resetTime(b["resets_at"])})
			}
		}
		total, tok := finite(r["allowance_usd"])
		remain, rok := finite(r["balance_usd"])
		if tok && rok {
			out.Balances = append(out.Balances, Balance{Unit: "USD", Total: total, Used: math.Max(0, total-remain), Remaining: remain})
		}
	case "zai":
		if m["success"] == false {
			break
		}
		rows, ok := d["limits"].([]any)
		if ok {
			out.State = HealthAvailable
			for _, raw := range rows {
				r, _ := raw.(map[string]any)
				if str(r, "type") != "TOKENS_LIMIT" && str(r, "type") != "CREDIT_LIMIT" {
					continue
				}
				k := ""
				if number(r, "unit") == 3 && number(r, "number") == 5 {
					k = "five-hour"
				}
				if number(r, "unit") == 6 && number(r, "number") == 1 {
					k = "weekly"
				}
				if k == "" {
					continue
				}
				if w, ok := window(k, "", r, "percentage", "nextResetTime"); ok {
					out.Windows = append(out.Windows, w)
				} else if w, ok := ratioWindow(k, r, "usage", "currentValue"); ok {
					w.ResetAt = resetTime(r["nextResetTime"])
					out.Windows = append(out.Windows, w)
				}
			}
		} else {
			r := obj(d, "quota")
			if r == nil {
				r = d
			}
			for _, pair := range [][2]string{{"fiveHour", "five-hour"}, {"weekly", "weekly"}} {
				v := first(r, pair[0]+"Percent", pair[0]+"Usage", pair[0]+"Used")
				add(pair[1], map[string]any{"percent": v}, "percent", "")
			}
		}
	case "minimax":
		if obj(m, "base_resp")["status_code"] != float64(0) {
			break
		}
		rows, _ := m["model_remains"].([]any)
		for _, raw := range rows {
			r, _ := raw.(map[string]any)
			if str(r, "model_name") != "general" {
				continue
			}
			for _, pair := range [][3]string{{"current_interval_remaining_percent", "five-hour", "end_time"}, {"current_weekly_remaining_percent", "weekly", "weekly_end_time"}} {
				if pair[1] == "weekly" && number(r, "current_weekly_status") != 1 {
					continue
				}
				if n, ok := finite(r[pair[0]]); ok && n <= 100 {
					out.Windows = append(out.Windows, QuotaWindow{Kind: pair[1], UsedPercent: 100 - n, ResetAt: resetTime(r[pair[2]])})
				}
			}
		}
	case "moonshot":
		unit := "USD"
		u, _ := url.Parse(endpoint)
		if u.Host == "api.moonshot.cn" {
			unit = "CNY"
		}
		balance(unit, d["available_balance"])
	case "venice":
		balance("DIEM", d["balance"])
		balance("USD", d["balance_usd"])
		ratio("epoch", d, "diem_epoch_allocated", "diem_epoch_used")
	case "synthetic":
		r := obj(d, "quota")
		if r == nil {
			r = obj(d, "quotas")
		}
		if r == nil {
			r = d
		}
		add("five-hour", r, "rollingFiveHourLimit", "")
		add("weekly", r, "weeklyTokenLimit", "")
	case "deepinfra":
		n, ok := signedFinite(d["stripe_balance"])
		if ok {
			balance("USD", math.Max(0, -n))
			limit, lok := finite(d["spending_limit"])
			if lok && limit > 0 {
				spent, sok := finite(d["total_amount_due"])
				if !sok || spent == 0 {
					spent = math.Max(0, limit-math.Max(0, -n))
				}
				ratio("billing-cycle", map[string]any{"limit": limit, "spent": spent}, "limit", "spent")
			}
		}
	case "neuralwatt":
		r := obj(d, "subscription")
		ratio("subscription", r, "kwh_included", "kwh_used")
		if len(out.Windows) > 0 {
			out.Windows[len(out.Windows)-1].ResetAt = resetTime(r["current_period_end"])
		}
		b := obj(d, "balance")
		total, tok := finite(b["total_credits_usd"])
		remain, rok := finite(b["credits_remaining_usd"])
		if tok && rok {
			out.Balances = append(out.Balances, Balance{Unit: "USD", Total: total, Used: math.Max(0, total-remain), Remaining: remain})
		}
	case "abliteration":
		total, tok := finite(d["total_credits"])
		used, uok := finite(d["total_usage"])
		if tok && uok {
			out.State = HealthAvailable
			out.Balances = append(out.Balances, Balance{Unit: "USD", Total: total, Used: used, Remaining: math.Max(0, total-used)})
			if total > 0 {
				ratio("credits", d, "total_credits", "total_usage")
			}
		}
	}
	if len(out.Windows) > 0 || len(out.Balances) > 0 {
		out.State = HealthAvailable
	}
	return out
}
func signedFinite(v any) (float64, bool) {
	if n, ok := finite(v); ok {
		return n, true
	}
	switch x := v.(type) {
	case float64:
		if !math.IsNaN(x) && !math.IsInf(x, 0) {
			return x, true
		}
	case string:
		if strings.HasPrefix(x, "-") {
			n, ok := finite(strings.TrimPrefix(x, "-"))
			return -n, ok
		}
	}
	return 0, false
}
func a6Health(out Health, subscription, token map[string]any) (Health, error) {
	if token["unlimited_quota"] == true || token["unlimited_quota"] == float64(1) || token["unlimited_quota"] == "true" {
		out.State = HealthAvailable
		return out, nil
	}
	limit, lok := finite(subscription["hard_limit_usd"])
	grant, gok := finite(token["total_granted"])
	used, uok := finite(token["total_used"])
	remain, rok := finite(token["total_available"])
	if !lok || !gok || !uok || !rok || limit <= 0 || grant <= 0 || math.Abs(used+remain-grant) > math.Abs(grant)*1e-9 {
		return out, ErrResponse
	}
	out.State = HealthAvailable
	out.Balances = []Balance{{Unit: "USD", Total: limit, Used: used * limit / grant, Remaining: remain * limit / grant}}
	out.Windows = []QuotaWindow{{Kind: "credits", UsedPercent: math.Min(100, 100*used/grant), ResetAt: resetTime(token["expires_at"])}}
	return out, nil
}
