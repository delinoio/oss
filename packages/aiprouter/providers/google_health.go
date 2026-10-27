// SPDX-License-Identifier: Apache-2.0
// Pinned quota protocols: snapshot/NOTICE.
package providers

import (
	"context"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"regexp"
	"sort"
	"strings"
)

var argonToken = regexp.MustCompile(`(?i)(^|[^a-z0-9])argon([^a-z0-9]|$)`)

func googleRemaining(r map[string]any) (float64, bool) {
	if sub := obj(r, "remaining"); sub != nil {
		r = sub
	}
	n, ok := finite(first(r, "remainingFraction", "remainingPercentage"))
	return n, ok && n <= 1
}
func (a *Auth) googleHealth(ctx context.Context, s core.Scope, c Credential) (Health, error) {
	out := Health{State: HealthUnknown, ObservedAt: a.now()}
	if !safeString(c.ProjectID) {
		return out, ErrReauthenticate
	}
	h := bearer(c.AccessToken)
	h.Set("User-Agent", "antigravity/ide/2.5.5 (aidev_client)")
	body := map[string]string{"project": c.ProjectID}
	base := "https://daily-cloudcode-pa.googleapis.com/v1internal:"
	m, status, err := a.request(ctx, s, "POST", base+"retrieveUserQuotaSummary", body, h)
	if err == nil && status == 403 {
		h.Set("User-Agent", "antigravity/1.0")
		m, status, err = a.request(ctx, s, "POST", base+"retrieveUserQuotaSummary", body, h)
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if err == nil && (status == 401 || status == 403) {
		return out, nil
	}
	if err == nil && status == 200 {
		out.Windows = antigravityWindows(m)
		if len(out.Windows) > 0 {
			out.State = HealthAvailable
			return out, nil
		}
	}
	// A support quota denial is not evidence that the inference token is revoked.
	// Fallback reads the account catalog with the same project, never an inference turn.
	h.Set("User-Agent", "antigravity/ide/2.5.5 (aidev_client)")
	m, status, err = a.request(ctx, s, "POST", base+"fetchAvailableModels", body, h)
	if err != nil {
		return out, err
	}
	if status == 401 || status == 403 {
		return out, nil
	}
	if status != 200 {
		return out, authFailure(status, m)
	}
	models := obj(m, "models")
	ids := make([]string, 0, len(models))
	for id := range models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	families := map[string]bool{}
	for _, id := range ids {
		r, _ := models[id].(map[string]any)
		rows := []map[string]any{}
		add := func(v any) {
			if q, ok := v.(map[string]any); ok {
				rows = append(rows, q)
			} else if a, ok := v.([]any); ok {
				for _, x := range a {
					if q, ok := x.(map[string]any); ok {
						rows = append(rows, q)
					}
				}
			}
		}
		add(r["quotaInfo"])
		add(r["quotaInfos"])
		for _, v := range obj(r, "quotaInfoByTier") {
			add(v)
		}
		for _, q := range rows {
			n, ok := googleRemaining(q)
			if !ok {
				continue
			}
			identity := strings.ToLower(id + " " + str(r, "displayName"))
			family := ""
			if !argonToken.MatchString(identity) {
				if strings.Contains(identity, "gemini") {
					family = "gemini"
				} else if strings.Contains(identity, "claude") || strings.Contains(identity, "opus") || strings.Contains(identity, "sonnet") || strings.Contains(identity, "gpt-oss") {
					family = "claude"
				}
			}
			if family != "" && !families[family] {
				families[family] = true
				out.Windows = append(out.Windows, QuotaWindow{Kind: "account-family", Model: family, UsedPercent: 100 * (1 - n), ResetAt: resetTime(q["resetTime"])})
			}
			if modelID(id) && len(out.Windows) < 258 {
				out.Windows = append(out.Windows, QuotaWindow{Kind: "model-observation", Model: id, UsedPercent: 100 * (1 - n), ResetAt: resetTime(q["resetTime"])})
			}
		}
	}
	if len(out.Windows) > 0 {
		out.State = HealthAvailable
	}
	return out, nil
}
