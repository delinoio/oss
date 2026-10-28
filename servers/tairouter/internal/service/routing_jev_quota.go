// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/providers"
	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
)

// Advisory values follow pinned jev-quota.ts. They never grant account/model
// eligibility, choose a serving account, or trigger a quota probe.
type jevQuotaTier string

const (
	jevQuotaUnknown         jevQuotaTier = "unknown"
	jevQuotaHealthy         jevQuotaTier = "healthy"
	jevQuotaModerate        jevQuotaTier = "moderate"
	jevQuotaLimited         jevQuotaTier = "limited"
	jevQuotaNearlyExhausted jevQuotaTier = "nearly_exhausted"
)
const jevQuotaInstruction = "Remaining subscription quota is advisory: among adequate options prefer healthier quota; unknown is not exhausted. Never change the target or effort allowlist."
const jevQuotaMaxAge = 30 * time.Minute

type jevQuotaTiers struct {
	Moderate                 *float64
	Limited, NearlyExhausted float64
}
type jevQuotaSignal struct {
	Tier            jevQuotaTier `json:"tier"`
	UsedPercent     *float64     `json:"used_percent,omitempty"`
	Window          string       `json:"window,omitempty"`
	ResetsInSeconds *int64       `json:"resets_in_seconds,omitempty"`
}
type jevQuotaSummary struct {
	Unknown         int          `json:"unknown"`
	Healthy         int          `json:"healthy"`
	Moderate        int          `json:"moderate"`
	Limited         int          `json:"limited"`
	NearlyExhausted int          `json:"nearly_exhausted"`
	Selected        jevQuotaTier `json:"selected,omitempty"`
}

func resolveJEVQuotaTiers(raw *v1.JevQuotaTiers) (jevQuotaTiers, error) {
	out := jevQuotaTiers{Limited: 70, NearlyExhausted: 90}
	if raw == nil {
		return out, nil
	}
	if raw.Moderate != nil {
		v := *raw.Moderate
		out.Moderate = &v
	}
	if raw.Limited != nil {
		out.Limited = *raw.Limited
	}
	if raw.NearlyExhausted != nil {
		out.NearlyExhausted = *raw.NearlyExhausted
	}
	values := []float64{out.Limited, out.NearlyExhausted}
	if out.Moderate != nil {
		values = append(values, *out.Moderate)
	}
	for _, n := range values {
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 100 {
			return out, jevConfigError()
		}
	}
	if out.Limited >= out.NearlyExhausted || out.Moderate != nil && *out.Moderate >= out.Limited {
		return out, jevConfigError()
	}
	return out, nil
}
func jevTier(percent float64, tiers jevQuotaTiers) jevQuotaTier {
	if math.IsNaN(percent) || math.IsInf(percent, 0) || percent < 0 || percent > 100 {
		return jevQuotaUnknown
	}
	if percent >= tiers.NearlyExhausted {
		return jevQuotaNearlyExhausted
	}
	if percent >= tiers.Limited {
		return jevQuotaLimited
	}
	if tiers.Moderate != nil && percent >= *tiers.Moderate {
		return jevQuotaModerate
	}
	return jevQuotaHealthy
}
func jevQuotaRank(tier jevQuotaTier) int {
	switch tier {
	case jevQuotaModerate:
		return 1
	case jevQuotaLimited:
		return 2
	case jevQuotaNearlyExhausted:
		return 3
	}
	return 0
}

var jevAnthropicFamily = regexp.MustCompile(`(?i)(?:^|/)claude-(fable-5(?:-|$)|opus-|sonnet-)`)
var jevArgon = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])argon(?:[^a-z0-9]|$)`)

func jevWindowApplicable(window nativeQuotaWindow, model string) bool {
	switch window.Window {
	case "5h", "weekly", "monthly", "fable", "opus", "sonnet":
	default:
		return false
	}
	id := strings.ToLower(model)
	if window.Family != "" {
		if jevArgon.MatchString(id) {
			return false
		}
		return window.Family == "gem" && strings.HasPrefix(id, "gemini-") || window.Family == "cla" && strings.HasPrefix(id, "claude-")
	}
	switch window.Window {
	case "5h", "weekly", "monthly":
		return true
	case "fable", "opus", "sonnet":
		match := jevAnthropicFamily.FindStringSubmatch(model)
		return len(match) > 1 && strings.HasPrefix(strings.ToLower(match[1]), window.Window)
	}
	return false
}
func jevQuotaFromWindows(windows []nativeQuotaWindow, model string, now time.Time, tiers jevQuotaTiers) jevQuotaSignal {
	unknown := jevQuotaSignal{Tier: jevQuotaUnknown}
	if now.IsZero() || now.UnixMilli() < 0 || len(windows) > 64 {
		return unknown
	}
	var worst *nativeQuotaWindow
	for i := range windows {
		row := &windows[i]
		if !jevWindowApplicable(*row, model) || row.ObservedAt.IsZero() || row.ObservedAt.UnixMilli() < 0 || row.ObservedAt.After(now) || now.Sub(row.ObservedAt) > jevQuotaMaxAge || jevTier(row.Percent, tiers) == jevQuotaUnknown {
			continue
		}
		if row.ResetAt != nil && (row.ResetAt.IsZero() || row.ResetAt.UnixMilli() < 0 || !row.ResetAt.After(now)) {
			continue
		}
		if worst == nil || row.Percent > worst.Percent {
			worst = row
		}
	}
	if worst == nil {
		return unknown
	}
	percent := worst.Percent
	out := jevQuotaSignal{Tier: jevTier(percent, tiers), UsedPercent: &percent, Window: worst.Window}
	if worst.ResetAt != nil {
		seconds := int64(math.Ceil(worst.ResetAt.Sub(now).Seconds()))
		if seconds > 31536000 {
			seconds = 31536000
		}
		out.ResetsInSeconds = &seconds
	}
	return out
}
func jevQuotaPool(signals []jevQuotaSignal) jevQuotaSignal {
	best := jevQuotaSignal{Tier: jevQuotaUnknown}
	for i, s := range signals {
		if i == 0 || jevQuotaRank(s.Tier) < jevQuotaRank(best.Tier) {
			best = s
		}
	}
	return best
}
func jevQuotaClause(signal *jevQuotaSignal) string {
	if signal == nil {
		return ""
	}
	used := ""
	if signal.UsedPercent != nil {
		used = fmt.Sprintf(" (%g%% of %s used)", *signal.UsedPercent, signal.Window)
	}
	return " Quota " + string(signal.Tier) + used + "."
}
func withJEVQuotaSummary(d jevDecision, candidates []jevCandidate) jevDecision {
	var summary *jevQuotaSummary
	for _, c := range candidates {
		if c.Quota != nil {
			summary = &jevQuotaSummary{}
			break
		}
	}
	if summary == nil {
		d.Quota = nil
		return d
	}
	for _, c := range candidates {
		tier := jevQuotaUnknown
		if c.Quota != nil {
			tier = c.Quota.Tier
		}
		switch tier {
		case jevQuotaHealthy:
			summary.Healthy++
		case jevQuotaModerate:
			summary.Moderate++
		case jevQuotaLimited:
			summary.Limited++
		case jevQuotaNearlyExhausted:
			summary.NearlyExhausted++
		default:
			summary.Unknown++
		}
		if c.Key == d.TargetKey {
			summary.Selected = tier
		}
	}
	d.Quota = summary
	return d
}
func jevHasQuota(choices []jevOption) bool {
	for _, o := range choices {
		if o.candidate.Quota != nil {
			return true
		}
	}
	return false
}
func jevWithoutQuota(choices []jevOption) []jevOption {
	out := append([]jevOption(nil), choices...)
	for i := range out {
		out[i].candidate.Quota = nil
	}
	return out
}
func jevDropCandidateQuota(candidates []jevCandidate) []jevCandidate {
	out := append([]jevCandidate(nil), candidates...)
	for i := range out {
		out[i].Quota = nil
	}
	return out
}

// Current route membership comes from the main resolver. Missing observations
// remain explicit unknown rows, so they cannot make a pool look exhausted.
func (a *inferenceAdapter) jevQuotaForSelection(ctx context.Context, p inference.Principal, selection routeSelection, tiers jevQuotaTiers) (jevQuotaSignal, error) {
	unknown := jevQuotaSignal{Tier: jevQuotaUnknown}
	if selection.target.Auth == inference.OAuth && selection.provider == "openai" && (strings.TrimRight(selection.target.BaseURL, "/") != "https://chatgpt.com/backend-api/codex" || selection.target.Wire != inference.Responses) {
		return unknown, nil
	}
	if selection.target.Auth == inference.OAuth && selection.provider == "google-antigravity" {
		preset, ok := providers.Lookup(selection.provider)
		if !ok || strings.TrimRight(selection.target.BaseURL, "/") != strings.TrimRight(preset.BaseURL, "/") {
			return unknown, nil
		}
	}
	nativePool := selection.target.Auth == inference.OAuth && (selection.provider == "openai" || selection.provider == "anthropic")
	parts := strings.Split(strings.ToLower(selection.target.Model), "/")
	if selection.provider == "openai" && parts[len(parts)-1] == "gpt-reserve" {
		return unknown, nil
	}
	if !nativePool && len(selection.request.Candidates) != 1 {
		return unknown, nil
	}
	if !nativePool && selection.target.Auth != inference.APIKey && !(selection.provider == "google-antigravity" && selection.target.Auth == inference.OAuth) {
		return unknown, nil
	}
	now := time.Now()
	var signals []jevQuotaSignal
	for _, candidate := range selection.request.Candidates {
		excluded := false
		for _, id := range selection.request.Exclude {
			if id == candidate.ID {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		until, err := a.s.AccountCooldown(ctx, scopeOf(p), candidate.ID, selection.request.Model)
		if err != nil {
			return unknown, inferenceError(err)
		}
		if until != nil && until.After(now) {
			continue
		}
		// The durable reader fences current member/key/credential generation. No
		// credential refresh or account-selection receipt is created by this read.
		snapshot, found, err := a.readNativeQuota(ctx, p, candidate.ID, candidate.Version)
		if errors.Is(err, store.ErrNotFound) {
			found = false
			err = nil
		}
		if err != nil {
			return unknown, inferenceError(err)
		}
		if !found {
			signals = append(signals, unknown)
			continue
		}
		signals = append(signals, jevQuotaFromWindows(snapshot.Windows, selection.target.Model, now, tiers))
	}
	return jevQuotaPool(signals), nil
}

// Quota is the only optional evidence removed at the byte cap. Never truncate
// targets, efforts, user state or operator notes to make a question fit.
func jevBoundedQuotaPayload(choices []jevOption, build func([]jevOption) (json.RawMessage, error)) (json.RawMessage, []jevOption, error) {
	raw, err := build(choices)
	if (err != nil || len(raw) > jevMaxBytes) && jevHasQuota(choices) {
		choices = jevWithoutQuota(choices)
		raw, err = build(choices)
	}
	if err != nil || len(raw) > jevMaxBytes {
		return nil, choices, jevConfigError()
	}
	return raw, choices, nil
}
