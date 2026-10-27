// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	"google.golang.org/protobuf/proto"
)

// This adapter stores only serving identity. It never replays an alias or runs a
// combo selector, and never stores request content or credential material.
type compactionHandoffs struct{ a *inferenceAdapter }

var _ inference.CompactionHandoffs = compactionHandoffs{}

type compactionHandoffConfig = store.NativeHealthBinding
type compactionHandoffIdentity struct {
	Model, PolicyModel, Physical string
	Fast                         bool
	Config                       []compactionHandoffConfig
}

func compactPhysicalIdentity(t inference.Target) string {
	return selectionVersion(struct {
		BaseURL, Model, Wire, Auth, Endpoint string
		Fast                                 bool
		Policy                               *inference.FastPolicy
	}{t.BaseURL, compactServingModel(t), string(t.Wire), string(t.Auth), t.Endpoint, t.Fast, t.FastPolicy})
}
func compactServingModel(t inference.Target) string {
	if t.Fast && t.FastPolicy != nil && t.FastPolicy.Kind == inference.FastModelVariant {
		return t.FastPolicy.Model
	}
	return t.Model
}
func compactHandoffLane(ctx context.Context, p inference.Principal) string {
	lane := inference.AffinitySession(ctx)
	if lane == "" || p.KeyID == "" || p.OrganizationID == "" || p.UserID == "" {
		return ""
	}
	return selectionVersion([]string{p.OrganizationID, p.UserID, p.KeyID, lane})
}
func compactHandoffModel(model string) bool {
	return strings.TrimSpace(model) != "" && len(utf16.Encode([]rune(model))) <= 512
}

// Match the pinned compact classifier without turning a native 404 into quota
// recovery. Body text is inspected only in this request and is never retained.
func compactHandoffQuota(f inference.HTTPFailure) bool {
	if f.Status < 400 || f.Status == 404 || f.Status == 499 {
		return false
	}
	text := strings.ToLower(string(f.Body))
	if strings.Contains(text, "upstream_reset_replay_refused") || strings.Contains(text, "upstream_no_response") || strings.Contains(text, "upstream_closed_before_response") || strings.Contains(text, "request_send_budget_exhausted") || strings.Contains(text, "non_replayable") || strings.Contains(text, "non-replayable") {
		return false
	}
	if f.Status == 402 || f.Status == 429 {
		return true
	}
	for _, s := range []string{"context_length_exceeded", "context window", "context length", "maximum context", "too many tokens", "cursor resource limit exceeded", "cursor context limit exceeded"} {
		if strings.Contains(text, s) {
			return strings.Contains(text, "usage limit")
		}
	}
	for _, s := range []string{"insufficient_quota", "exceeded your current quota", "quota exhausted", "account quota exceeded", "monthly quota exceeded", "daily quota exceeded", "rate limit", "too many requests", "resource_exhausted", "resource exhausted", "throttling", "usage limit"} {
		if strings.Contains(text, s) {
			return true
		}
	}
	return false
}

// A selected route/pool is a configuration fence, never the physical identity.
// Verify its captured selection fingerprint before retaining its current version.
func (h compactionHandoffs) config(ctx context.Context, p inference.Principal, t inference.Target, c inference.Credential) ([]compactionHandoffConfig, error) {
	h.a.mu.Lock()
	groups := make([]routeSelection, 0, len(h.a.selections))
	for _, g := range h.a.selections {
		groups = append(groups, g)
	}
	h.a.mu.Unlock()
	sort.Slice(groups, func(i, j int) bool { return groups[i].request.GroupID < groups[j].request.GroupID })
	for _, g := range groups {
		member := false
		for _, candidate := range g.request.Candidates {
			if candidate.ID == c.AccountID && candidate.Version == c.CredentialVersion {
				member = true
				break
			}
		}
		if !member || g.target.BaseURL != t.BaseURL || !strings.HasPrefix(g.request.GroupID, "route/") {
			continue
		}
		ids := strings.SplitN(strings.TrimPrefix(g.request.GroupID, "route/"), "/pool/", 2)
		raw, version, err := h.a.s.GetPrivateVersion(ctx, scopeOf(p), string(routeKind), ids[0])
		if err != nil {
			return nil, err
		}
		var route v1.Route
		if proto.Unmarshal(raw, &route) != nil || !route.Enabled {
			return nil, inference.ErrContinuation
		}
		sort.SliceStable(route.Targets, func(i, j int) bool { return route.Targets[i].Priority < route.Targets[j].Priority })
		config := []compactionHandoffConfig{{Kind: string(routeKind), ID: ids[0], Version: version}}
		fingerprint := selectionVersion(&route)
		if len(ids) == 2 {
			raw, version, err = h.a.s.GetPrivateVersion(ctx, scopeOf(p), string(poolKind), ids[1])
			if err != nil {
				return nil, err
			}
			var pool v1.Pool
			if json.Unmarshal(raw, &pool) != nil {
				return nil, inference.ErrContinuation
			}
			fingerprint = selectionVersion(struct {
				Route *v1.Route
				Pool  *v1.Pool
			}{&route, &pool})
			config = append(config, compactionHandoffConfig{Kind: string(poolKind), ID: ids[1], Version: version})
		}
		if fingerprint == g.request.Version {
			return config, nil
		}
	}
	return nil, inference.ErrContinuation
}

func (h compactionHandoffs) Capture(ctx context.Context, p inference.Principal, t inference.Target, c inference.Credential) error {
	lane := compactHandoffLane(ctx, p)
	if lane == "" || !compactHandoffModel(compactServingModel(t)) {
		return nil
	}
	lease, err := h.a.current(p)
	if err != nil {
		return err
	}
	d, version, err := h.a.providerVersion(ctx, scopeOf(p), c.AccountID)
	if err != nil {
		return inferenceError(err)
	}
	if version != c.CredentialVersion {
		return inference.ErrContinuation
	}
	if _, ok := modelEvidence(d, t.Model, time.Now()); !ok {
		return inference.ErrContinuation
	}
	physical, err := targetForContext(ctx, d, t.Model)
	if err != nil {
		return err
	}
	if t.Fast {
		if err = applyFastTarget(ctx, d, t.Model, &physical); err != nil {
			return err
		}
	}
	if compactPhysicalIdentity(physical) != compactPhysicalIdentity(t) {
		return inference.ErrContinuation
	}
	configs, err := h.config(ctx, p, t, c)
	if err != nil {
		return inferenceError(err)
	}
	raw, err := json.Marshal(compactionHandoffIdentity{Model: compactServingModel(t), PolicyModel: t.Model, Fast: t.Fast, Physical: compactPhysicalIdentity(t), Config: configs})
	if err != nil {
		return err
	}
	return inferenceError(h.a.s.PutCompactionHandoff(ctx, lease, lane, store.CompactionHandoff{AccountID: c.AccountID, CredentialVersion: c.CredentialVersion, Data: raw, Bindings: configs}))
}
func (h compactionHandoffs) Lookup(ctx context.Context, p inference.Principal, previous inference.Target, f inference.HTTPFailure) (*inference.CompactionHandoff, error) {
	lane := compactHandoffLane(ctx, p)
	if lane == "" || !compactHandoffQuota(f) {
		return nil, nil
	}
	if err := h.a.BeforeSend(ctx, p); err != nil {
		return nil, err
	}
	lease, err := h.a.current(p)
	if err != nil {
		return nil, err
	}
	row, err := h.a.s.GetCompactionHandoff(ctx, lease, lane)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrBusy) || errors.Is(err, store.ErrReauthentication) {
		// Invalid saved account state is a cache miss only while admission remains
		// current. Key/member revocation must never be downgraded to an ordinary miss.
		if e := h.a.BeforeSend(ctx, p); e != nil {
			return nil, e
		}
		return nil, nil
	}
	if err != nil {
		return nil, inferenceError(err)
	}
	var saved compactionHandoffIdentity
	if json.Unmarshal(row.Data, &saved) != nil || !compactHandoffModel(saved.Model) || !compactHandoffModel(saved.PolicyModel) || len(saved.Config) == 0 || len(saved.Config) > 2 {
		return nil, inference.ErrContinuation
	}
	if selectionVersion(saved.Config) != selectionVersion(row.Bindings) {
		return nil, inference.ErrContinuation
	}
	var rememberedRoute v1.Route
	var rememberedPool *v1.Pool
	groupID := ""
	for _, config := range saved.Config {
		if config.Kind != string(routeKind) && config.Kind != string(poolKind) {
			return nil, inference.ErrContinuation
		}
		raw, version, err := h.a.s.GetPrivateVersion(ctx, scopeOf(p), config.Kind, config.ID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, inferenceError(err)
		}
		if version != config.Version {
			return nil, nil
		}
		if config.Kind == string(routeKind) {
			if proto.Unmarshal(raw, &rememberedRoute) != nil || !rememberedRoute.Enabled {
				return nil, nil
			}
			groupID = "route/" + config.ID
		} else {
			rememberedPool = &v1.Pool{}
			if json.Unmarshal(raw, rememberedPool) != nil {
				return nil, inference.ErrContinuation
			}
		}
	}
	d, version, err := h.a.providerVersion(ctx, scopeOf(p), row.AccountID)
	if errors.Is(err, inference.ErrNoCredential) || errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, inferenceError(err)
	}
	if version != row.CredentialVersion {
		return nil, nil
	}
	if _, ok := modelEvidence(d, saved.PolicyModel, time.Now()); !ok {
		return nil, nil
	}
	until, err := h.a.s.AccountCooldown(ctx, scopeOf(p), row.AccountID, saved.Model)
	if err != nil {
		return nil, inferenceError(err)
	}
	if until != nil && until.After(time.Now()) {
		return nil, nil
	}
	target, err := targetForContext(ctx, d, saved.PolicyModel)
	if err != nil {
		return nil, nil
	}
	if saved.Fast {
		if err = applyFastTarget(ctx, d, saved.PolicyModel, &target); err != nil {
			return nil, nil
		}
	}
	if compactServingModel(target) != saved.Model {
		return nil, nil
	}
	if compactPhysicalIdentity(target) != saved.Physical || compactPhysicalIdentity(target) == compactPhysicalIdentity(previous) {
		return nil, nil
	}
	sort.SliceStable(rememberedRoute.Targets, func(i, j int) bool { return rememberedRoute.Targets[i].Priority < rememberedRoute.Targets[j].Priority })
	configVersion := selectionVersion(&rememberedRoute)
	if rememberedPool != nil {
		groupID += "/pool/" + rememberedPool.Id
		configVersion = selectionVersion(struct {
			Route *v1.Route
			Pool  *v1.Pool
		}{&rememberedRoute, rememberedPool})
	}
	if groupID == "" {
		return nil, inference.ErrContinuation
	}
	h.a.mu.Lock()
	if h.a.selections == nil {
		h.a.selections = map[string]routeSelection{}
	}
	h.a.selections[selectionKey(target)] = routeSelection{provider: d.Metadata.ProviderId, target: target, pool: rememberedPool, request: store.SelectionRequest{GroupID: groupID, Version: configVersion, Model: target.Model, Candidates: []store.SelectionCandidate{{ID: row.AccountID, Version: row.CredentialVersion}}}}
	h.a.mu.Unlock()
	credential, err := h.a.captureCredential(ctx, p, target, row.AccountID)
	if err != nil {
		return nil, err
	}
	if credential.CredentialVersion != row.CredentialVersion {
		return nil, inference.ErrContinuation
	}
	h.a.mu.Lock()
	h.a.compactionHandoff = &compactionHandoffDispatch{AccountID: row.AccountID, CredentialVersion: row.CredentialVersion, Target: target, Bindings: append([]store.NativeHealthBinding(nil), saved.Config...)}
	h.a.mu.Unlock()
	return &inference.CompactionHandoff{Target: target, Credential: credential}, nil
}
func (h compactionHandoffs) Clear(ctx context.Context, p inference.Principal) error {
	lane := compactHandoffLane(ctx, p)
	if lane == "" {
		return nil
	}
	lease, err := h.a.current(p)
	if err != nil {
		return err
	}
	return inferenceError(h.a.s.ClearCompactionHandoff(ctx, lease, lane))
}

// The receipt is request-local; the store revalidates every binding while the
// first physical write holds the owner transaction. Native admission still runs.
type compactionHandoffDispatch struct {
	AccountID, CredentialVersion string
	Target                       inference.Target
	Bindings                     []store.NativeHealthBinding
}

func (a *inferenceAdapter) dispatchCompactionHandoff(ctx context.Context, p inference.Principal, start func(context.Context) error) (bool, error) {
	a.mu.Lock()
	receipt := a.compactionHandoff
	account, version, target := a.activeAccount, a.activeVersion, a.activeTarget
	a.mu.Unlock()
	if receipt == nil {
		return false, nil
	}
	if account != receipt.AccountID || version != receipt.CredentialVersion || compactPhysicalIdentity(target) != compactPhysicalIdentity(receipt.Target) {
		return true, inference.ErrContinuation
	}
	ctx = store.WithDispatchBindings(ctx, receipt.Bindings)
	if handled, err := a.dispatchNativeHealth(ctx, p, start); handled {
		return true, err
	}
	if handled, err := a.dispatchNativeFamily(ctx, p, start); handled {
		return true, err
	}
	lease, err := a.current(p)
	if err != nil {
		return true, err
	}
	return true, inferenceError(a.s.WithAccountDispatch(ctx, lease, account, version, start))
}
