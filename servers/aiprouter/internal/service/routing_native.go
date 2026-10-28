// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"errors"
	"google.golang.org/protobuf/proto"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

// This is an in-flight receipt, not an account-selection cache. PostgreSQL owns
// the affinity epoch and credential generation checked at every completion.
type nativeSelectionState struct {
	Group, Version, Session, Provider, Model, TargetProvider string
	ConfigGroup                                              string
	Health                                                   *nativeHealthReceipt
	Family                                                   *nativeFamilyReceipt
	HealthProbeAccount                                       string
	HealthDetour                                             bool
	HealthHoldSince                                          time.Time
	Epoch, ActiveEpoch                                       int64
	ActiveSession                                            string
	PreserveActive                                           bool
	Preserve, Reused                                         bool
	Candidates                                               map[string]string
}

func nativeProvider(provider string, auth inference.AuthMode) bool {
	return auth == inference.OAuth && (provider == "openai" || provider == "anthropic" || provider == "anthropic2")
}
func nativeQuotaScope(provider, model string) string {
	if provider == "openai" && strings.EqualFold(strings.TrimSpace(model), "gpt-reserve") {
		return "reserve"
	}
	return "shared"
}

func (a *inferenceAdapter) preselectNative(ctx context.Context, p inference.Principal, t inference.Target, provider string, req store.SelectionRequest) (store.SelectionRequest, error) {
	if !nativeProvider(provider, t.Auth) {
		return req, nil
	}
	lease, err := a.current(p)
	if err != nil {
		return req, err
	}
	a.mu.Lock()
	selection := a.selections[selectionKey(t)]
	a.mu.Unlock()
	options := nativePoolOptions(selection.pool)
	now := time.Now()
	quotas := map[string]nativeQuotaSnapshot{}
	deniedIDs := map[string]bool{}
	familyBlocked := map[string]bool{}
	eligible := make([]store.SelectionCandidate, 0, len(req.Candidates))
	preferred := []store.SelectionCandidate{}
	for _, c := range req.Candidates {
		skip := false
		for _, id := range req.Exclude {
			if id == c.ID {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		q, _, e := a.readNativeQuota(ctx, p, c.ID, c.Version)
		if e != nil {
			return req, e
		}
		recovered, e := a.recoverNativeQuotaIfDue(ctx, p, t, provider, c)
		if e != nil {
			return req, e
		}
		if !recovered {
			continue
		}
		// A recovery read may have replaced the windows used for this very pick.
		q, _, e = a.readNativeQuota(ctx, p, c.ID, c.Version)
		if e != nil {
			return req, e
		}
		until, e := a.s.AccountCooldown(ctx, scopeOf(p), c.ID, req.Model)
		if e != nil {
			return req, inferenceError(e)
		}
		if until != nil && until.After(now) {
			continue
		}
		if provider == "openai" && req.PinnedID == "" {
			if options.planExcluded(q.Plan) {
				continue
			}
			credits := options.members[c.ID] != nil && options.members[c.ID].AllowCredits && q.spendableCredits(now)
			held := false
			for _, w := range q.Windows {
				if w.Window == "weekly" && (strings.EqualFold(q.Plan, "free") || strings.EqualFold(q.Plan, "go")) {
					continue
				}
				if nativeWindowCurrent(w, now) && w.Percent >= 100 && (w.Window == "5h" && (w.ResetAt != nil || now.Sub(w.ObservedAt) <= 5*time.Minute) || w.Window != "5h" && w.ResetAt != nil) {
					held = true
				}
			}
			if held && !credits {
				continue
			}
		}
		if provider != "openai" {
			eligibleFamily, e := a.nativeFamilyEligible(ctx, p, c, t.Model)
			if e != nil {
				return req, e
			}
			if !eligibleFamily {
				familyBlocked[c.ID] = true
				continue
			}
		}

		quotas[c.ID] = q
		eligible = append(eligible, c)
		denied, e := a.s.AccountSoftDenied(ctx, lease, c.ID, c.Version, t.Model)
		if e != nil {
			return req, inferenceError(e)
		}
		deniedIDs[c.ID] = denied
		if !denied || c.ID == req.PinnedID {
			preferred = append(preferred, c)
		}
	}
	if len(preferred) > 0 {
		eligible = preferred
	}
	if len(eligible) == 0 {
		return req, inference.ErrNoCredential
	}
	req.AffinityKey = ""
	if !options.enabled || req.PinnedID != "" {
		// Failure-window steering applies to fresh placement even when the
		// proactive pool selector is disabled. Existing conversation bindings
		// must not be mistaken for new threads by the manual-pin policy.
		bound := false
		if provider == "openai" && inference.AffinitySession(ctx) != "" {
			group := "native/" + provider + "/" + req.GroupID + "/" + nativeQuotaScope(provider, t.Model)
			session := selectionVersion([]string{p.KeyID, provider, "session", inference.AffinitySession(ctx)})
			affinity, e := a.s.GetNativeAffinity(ctx, lease, group, req.Version, session)
			if e != nil {
				return req, inferenceError(e)
			}
			bound = affinity.AccountID != ""
		}
		if !bound {
			filtered, e := a.filterNativeHealthNew(ctx, p, t, provider, eligible, req.PinnedID)
			if e != nil {
				return req, e
			}
			if req.PinnedID != "" {
				found := false
				for _, c := range filtered {
					found = found || c.ID == req.PinnedID
				}
				if !found {
					if !a.nativeHealthPolicy(t).DetourPinned {
						return req, inference.ErrNoCredential
					}
					req.PinnedID = ""
				}
			}
			eligible = filtered
		}
		if len(eligible) == 0 {
			return req, inference.ErrNoCredential
		}
		req.Candidates = eligible
		return req, nil
	}
	beforeModelRoute := append([]store.SelectionCandidate(nil), eligible...)
	if provider != "openai" {
		eligible = options.route(eligible, t.Model)
	}
	if len(eligible) == 0 {
		return req, inference.ErrNoCredential
	}
	state := nativeSelectionState{Group: "native/" + provider + "/" + req.GroupID + "/" + nativeQuotaScope(provider, t.Model), Version: req.Version, ConfigGroup: req.GroupID, Provider: provider, Model: t.Model, TargetProvider: t.Provider, Candidates: map[string]string{}}
	for _, c := range eligible {
		state.Candidates[c.ID] = c.Version
	}
	state.ActiveSession = selectionVersion([]string{p.KeyID, provider, "active"})
	active, e := a.s.GetNativeAffinity(ctx, lease, state.Group, state.Version, state.ActiveSession)
	if e != nil {
		return req, inferenceError(e)
	}
	state.ActiveEpoch = active.Epoch
	state.PreserveActive = deniedIDs[active.AccountID]
	save := func(cs []store.SelectionCandidate) (store.SelectionRequest, error) {
		req.Candidates = cs
		a.mu.Lock()
		if a.native == nil {
			a.native = map[string]nativeSelectionState{}
		}
		a.native[selectionKey(t)] = state
		a.mu.Unlock()
		return req, nil
	}
	session := inference.AffinitySession(ctx)
	if session != "" {
		state.Session = selectionVersion([]string{p.KeyID, provider, "session", session})
		affinity, e := a.s.GetNativeAffinity(ctx, lease, state.Group, state.Version, state.Session)
		if e != nil {
			return req, inferenceError(e)
		}
		state.Epoch = affinity.Epoch
		if cs, handled, e := a.resolveNativeHealthAffinity(ctx, p, t, provider, &state, affinity, eligible); e != nil {
			return req, e
		} else if handled {
			if state.HealthDetour {
				eligible = cs
				goto nativeFreshSelection
			}
			return save(cs)
		}
		for _, c := range eligible {
			if c.ID != affinity.AccountID || c.Version != affinity.CredentialVersion {
				continue
			}
			retains := !nativeExhausted(provider, t.Model, quotas[c.ID], now)
			if provider == "openai" {
				score, known := options.score(provider, t.Model, c.ID, quotas[c.ID], now)
				retains = !known || score < 100
			}
			if provider == "openai" && !options.cacheAffinity && !nativeUploadedFiles(inference.RoutingRequestBody(ctx)) {
				retains = options.headroom(provider, t.Model, c.ID, quotas[c.ID], now)
			}
			if retains {
				state.Reused = true
				return save([]store.SelectionCandidate{c})
			}
		}
		if affinity.AccountID != "" {
			// A model-specific exclusion preserves the account's shared conversation pin.
			present := false
			for _, c := range req.Candidates {
				if c.ID == affinity.AccountID && c.Version == affinity.CredentialVersion {
					present = true
				}
			}
			state.Preserve = !present || deniedIDs[affinity.AccountID] || familyBlocked[affinity.AccountID]
			if provider != "openai" {
				allowed := false
				for _, c := range eligible {
					if c.ID == affinity.AccountID {
						allowed = true
					}
				}
				for _, c := range beforeModelRoute {
					if c.ID == affinity.AccountID && !allowed {
						state.Preserve = true
					}
				}
			}
			if q, exists := quotas[affinity.AccountID]; exists && provider != "openai" && nativeExhausted(provider, t.Model, q, now) {
				state.Preserve = true
			}
		}
	}
	if len(eligible) == 0 {
		return req, inference.ErrNoCredential
	}
nativeFreshSelection:
	eligible, e = a.filterNativeHealthNew(ctx, p, t, provider, eligible, req.PinnedID)
	if e != nil {
		return req, e
	}
	if len(eligible) == 0 {
		return req, inference.ErrNoCredential
	}
	if provider != "openai" {
		healthy := []store.SelectionCandidate{}
		for _, c := range eligible {
			if !nativeExhausted(provider, t.Model, quotas[c.ID], now) {
				healthy = append(healthy, c)
			}
		}
		if len(healthy) > 0 {
			eligible = healthy
		}
	}
	eligible = options.tier(eligible, quotas, provider, t.Model, now)
	strategy := options.strategy
	if strategy == v1.NativePoolStrategy_NATIVE_POOL_STRATEGY_RESET_FIRST && nativeQuotaScope(provider, t.Model) != "shared" {
		strategy = v1.NativePoolStrategy_NATIVE_POOL_STRATEGY_QUOTA
	}
	var current *store.SelectionCandidate
	for i := range eligible {
		if eligible[i].ID == active.AccountID && eligible[i].Version == active.CredentialVersion {
			current = &eligible[i]
			break
		}
	}
	if current != nil {
		hold := false
		switch strategy {
		case v1.NativePoolStrategy_NATIVE_POOL_STRATEGY_ROUND_ROBIN:
			hold = active.Successes < options.sticky || provider != "openai" && session == ""
		case v1.NativePoolStrategy_NATIVE_POOL_STRATEGY_FILL_FIRST, v1.NativePoolStrategy_NATIVE_POOL_STRATEGY_QUOTA:
			hold = options.headroom(provider, t.Model, current.ID, quotas[current.ID], now)
		}
		if hold {
			return save([]store.SelectionCandidate{*current})
		}
	}
	req.Strategy = store.SelectionPriority
	switch strategy {
	case v1.NativePoolStrategy_NATIVE_POOL_STRATEGY_ROUND_ROBIN:
		req.Strategy = store.SelectionRoundRobin
	case v1.NativePoolStrategy_NATIVE_POOL_STRATEGY_FILL_FIRST:
		// Stable successor order wraps after the shared active account.
		declared := false
		for _, r := range options.routes {
			if nativeGlob(r.Match, t.Model) {
				for _, id := range r.ConnectionIds {
					for _, c := range eligible {
						if c.ID == id {
							declared = true
						}
					}
				}
				break
			}
		}
		if !declared {
			sort.SliceStable(eligible, func(i, j int) bool { return eligible[i].ID < eligible[j].ID })
		}
		start := 0
		foundActive := false
		for i, c := range eligible {
			if c.ID == active.AccountID {
				start = (i + 1) % len(eligible)
				foundActive = true
			}
		}
		if !foundActive && active.AccountID != "" {
			if !declared {
				for i, c := range eligible {
					if c.ID > active.AccountID {
						start = i
						break
					}
				}
			} else {
				for _, rule := range options.routes {
					if !nativeGlob(rule.Match, t.Model) {
						continue
					}
					at := -1
					for i, id := range rule.ConnectionIds {
						if id == active.AccountID {
							at = i
							break
						}
					}
					if at >= 0 {
						matched := false
						for step := 1; step <= len(rule.ConnectionIds) && !matched; step++ {
							id := rule.ConnectionIds[(at+step)%len(rule.ConnectionIds)]
							for i, c := range eligible {
								if c.ID == id {
									start = i
									matched = true
									break
								}
							}
						}
					}
					break
				}
			}
		}
		eligible = append(append([]store.SelectionCandidate{}, eligible[start:]...), eligible[:start]...)
		for i, c := range eligible {
			if options.headroom(provider, t.Model, c.ID, quotas[c.ID], now) {
				eligible = append([]store.SelectionCandidate{c}, append(eligible[:i], eligible[i+1:]...)...)
				break
			}
		}
	default:
		sort.SliceStable(eligible, func(i, j int) bool {
			l, lk := options.score(provider, t.Model, eligible[i].ID, quotas[eligible[i].ID], now)
			r, rk := options.score(provider, t.Model, eligible[j].ID, quotas[eligible[j].ID], now)
			if strategy == v1.NativePoolStrategy_NATIVE_POOL_STRATEGY_RESET_FIRST {
				reset := func(id string) time.Time {
					best := time.Unix(1<<60, 0)
					if !options.headroom(provider, t.Model, id, quotas[id], now) {
						return best
					}
					for _, w := range quotas[id].Windows {
						if (w.Window == "5h" || w.Window == "weekly") && w.ResetAt != nil && w.ResetAt.After(now) && w.ResetAt.Before(best) {
							best = *w.ResetAt
						}
					}
					return best
				}
				lt, rt := reset(eligible[i].ID), reset(eligible[j].ID)
				if !lt.Equal(rt) {
					return lt.Before(rt)
				}
			}
			if provider != "openai" && options.window != v1.NativeQuotaWindow_NATIVE_QUOTA_WINDOW_FIVE_HOUR && lk != rk {
				return lk
			}
			return l < r
		})
	}
	for i := range eligible {
		eligible[i].Priority = 0
	}
	return save(eligible)
}
func (a *inferenceAdapter) finishNative(ctx context.Context, p inference.Principal, r inference.UsageRecord) error {
	if r.Canceled || r.Outcome == inference.OutcomeCanceled || r.Outcome != inference.OutcomeSuccess {
		return nil
	}
	a.mu.Lock()
	states := make([]nativeSelectionState, 0, len(a.native))
	for _, s := range a.native {
		if s.Model == r.Model && s.TargetProvider == r.Provider && s.Candidates[r.AccountID] == a.versions[r.AccountID] {
			states = append(states, s)
		}
	}
	version := a.versions[r.AccountID]
	a.mu.Unlock()
	if version == "" {
		return nil
	}
	l, err := a.current(p)
	if err != nil {
		return err
	}
	for _, s := range states {
		if s.ConfigGroup != "" {
			current, e := a.nativeConfigurationCurrent(ctx, p, s)
			if e != nil {
				return e
			}
			if !current {
				continue
			}
		}
		if s.ActiveSession != "" && !s.Reused && !s.PreserveActive {
			err = a.s.FinishNativeAffinity(ctx, l, s.Group, s.Version, s.ActiveSession, r.AccountID, version, s.ActiveEpoch, true)
			if err != nil && !errors.Is(err, store.ErrConflict) {
				return inferenceError(err)
			}
		}
		if s.Session == "" || s.Preserve {
			continue
		}
		err = a.s.FinishNativeAffinity(ctx, l, s.Group, s.Version, s.Session, r.AccountID, version, s.Epoch, true)
		// A newer refusal/eviction wins over this request's successful completion.
		if err != nil && !errors.Is(err, store.ErrConflict) {
			return inferenceError(err)
		}
	}
	return nil
}
func (a *inferenceAdapter) failNative(ctx context.Context, in inference.AdapterInput, f inference.HTTPFailure) error {
	a.mu.Lock()
	s, ok := a.native[selectionKey(in.Target)]
	a.mu.Unlock()
	if !ok {
		return nil
	}
	clear := false
	if s.Provider == "openai" {
		clear = f.Status == http.StatusUnauthorized || f.Status == http.StatusForbidden && !nativeHealthWorkspaceDenied(f) || f.Status == http.StatusTooManyRequests || f.Status == http.StatusPaymentRequired
	} else {
		clear = anthropicRevoked(f) || anthropicAccountRefusal(f)
		if f.Status == 429 {
			family := strings.TrimSpace(f.Header.Get("anthropic-ratelimit-unified-7d_oi-status")) == "rejected"
			clear = clear || strings.TrimSpace(f.Header.Get("anthropic-ratelimit-unified-5h-status")) == "rejected" || strings.TrimSpace(f.Header.Get("anthropic-ratelimit-unified-7d-status")) == "rejected" || !family && strings.TrimSpace(f.Header.Get("anthropic-ratelimit-unified-status")) == "rejected"
		}
	}
	if !clear {
		return nil
	}
	l, err := a.current(in.Principal)
	if err != nil {
		return err
	}
	if s.ActiveSession != "" {
		err = a.s.FinishNativeAffinity(ctx, l, s.Group, s.Version, s.ActiveSession, in.Credential.AccountID, in.Credential.CredentialVersion, s.ActiveEpoch, false)
		if err != nil && !errors.Is(err, store.ErrConflict) {
			return inferenceError(err)
		}
	}
	if s.Session == "" {
		return nil
	}
	err = a.s.FinishNativeAffinity(ctx, l, s.Group, s.Version, s.Session, in.Credential.AccountID, in.Credential.CredentialVersion, s.Epoch, false)
	if errors.Is(err, store.ErrConflict) {
		return nil
	}
	return inferenceError(err)
}

// A fingerprint namespaces affinity but does not authorize a late completion.
// Read current route and pool configuration before publishing its result.
func (a *inferenceAdapter) nativeConfigurationCurrent(ctx context.Context, p inference.Principal, s nativeSelectionState) (bool, error) {
	parts := strings.Split(s.ConfigGroup, "/")
	if len(parts) != 2 && len(parts) != 4 || len(parts) < 2 || parts[0] != "route" {
		return false, nil
	}
	raw, _, err := a.s.GetPrivateVersion(ctx, scopeOf(p), string(routeKind), parts[1])
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, inferenceError(err)
	}
	var route v1.Route
	if proto.Unmarshal(raw, &route) != nil {
		return false, inference.ErrUnavailable
	}
	sort.SliceStable(route.Targets, func(i, j int) bool { return route.Targets[i].Priority < route.Targets[j].Priority })
	if !route.Enabled {
		return false, nil
	}
	if len(parts) == 2 {
		return selectionVersion(&route) == s.Version, nil
	}
	if parts[2] != "pool" {
		return false, nil
	}
	raw, err = a.s.GetPrivate(ctx, scopeOf(p), string(poolKind), parts[3])
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, inferenceError(err)
	}
	var pool v1.Pool
	if json.Unmarshal(raw, &pool) != nil {
		return false, inference.ErrUnavailable
	}
	return selectionVersion(struct {
		Route *v1.Route
		Pool  *v1.Pool
	}{&route, &pool}) == s.Version, nil
}

// Uploaded references remain scoped to their issuing account even when the user
// disables prompt-cache affinity. Tool schemas/arguments are caller data, not
// evidence that an uploaded object participates in conversation history.
func nativeUploadedFiles(raw []byte) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	var visit func(any) bool
	visit = func(value any) bool {
		switch node := value.(type) {
		case []any:
			for _, v := range node {
				if visit(v) {
					return true
				}
			}
		case map[string]any:
			for _, k := range []string{"file_id", "upload_id"} {
				if v, ok := node[k].(string); ok && v != "" {
					return true
				}
			}
			for k, v := range node {
				if k == "arguments" || k == "input_schema" || k == "parameters" || k == "tools" || k == "input" && node["type"] == "tool_use" {
					continue
				}
				if visit(v) {
					return true
				}
			}
		}
		return false
	}
	return visit(value)
}
