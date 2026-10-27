// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/inference/policy"
	"github.com/delinoio/oss/packages/aiprouter/inference/providers/azure"
	"github.com/delinoio/oss/packages/aiprouter/inference/providers/dsh"
	"github.com/delinoio/oss/packages/aiprouter/inference/providers/google"
	"github.com/delinoio/oss/packages/aiprouter/inference/providers/kiro"
	"github.com/delinoio/oss/packages/aiprouter/inference/providers/zed"
	"github.com/delinoio/oss/packages/aiprouter/providers"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	"google.golang.org/protobuf/proto"
)

type routeSelection struct {
	pool     *v1.Pool
	request  store.SelectionRequest
	target   inference.Target
	provider string
}

func selectionVersion(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func routeWire(r providers.Route) inference.Wire {
	if r.Preset.ID == "google-vertex" {
		return google.WireVertex
	}
	w := inference.Wire(r.Wire)
	switch w {
	case "google":
		return "google-gemini"
	case "cursor":
		return "cursor-agent"
	case "zed":
		switch zed.NormalizeProvider("", r.Model) {
		case zed.OpenAI:
			return zed.WireOpenAI
		case zed.Google:
			return zed.WireGoogle
		case zed.XAI:
			return zed.WireXAI
		default:
			return zed.WireAnthropic
		}
	}
	return w
}
func targetFor(d providerData, model string) (inference.Target, error) {
	return targetForContext(context.Background(), d, model)
}
func targetForContext(ctx context.Context, d providerData, model string) (inference.Target, error) {
	r, resolved, e := resolvedProviderRoute(ctx, d, model)
	if e != nil {
		return inference.Target{}, inference.ErrUnsupported
	}
	auth := inference.APIKey
	if d.Metadata.AuthKind == v1.ProviderAuthKind_PROVIDER_AUTH_KIND_NO_AUTH {
		auth = inference.Anonymous
	} else if d.Metadata.AuthKind == v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH {
		auth = inference.OAuth
	}
	// Every header, including private custom values, comes from the selected account.
	t := inference.Target{Provider: d.Metadata.Id, BaseURL: r.BaseURL, Model: r.Model, Wire: routeWire(r), Auth: auth, Policy: resolved, Endpoint: resolved.Endpoint}
	if d.Metadata.StallTimeoutSeconds != nil {
		v := time.Duration(*d.Metadata.StallTimeoutSeconds) * time.Second
		t.StallTimeout = &v
	}
	t.Retry = providerRetryPolicy(r, auth)
	return t, nil
}

// Resolve the selected configuration before synthesizing final-wire authentication.
func resolvedProviderRoute(ctx context.Context, d providerData, model string) (providers.Route, *policy.Resolved, error) {
	c := providerConnection(d)
	if d.Metadata.ProviderId == "dsh" {
		h := make(http.Header)
		for k, v := range d.CustomHeaders {
			h.Set(k, v)
		}
		if err := dsh.ValidateHeaders(h); err != nil {
			return providers.Route{}, nil, err
		}
	}
	r, err := providers.Resolve(c, model, providerInbound(ctx))
	if err != nil {
		return r, nil, err
	}
	overrides, err := providerPolicyOverrides(d)
	if err != nil {
		return r, nil, err
	}
	resolved, err := policy.Resolve(policy.Config{ProviderID: r.Preset.ID, Model: r.Model, BaseURL: r.BaseURL, Wire: policyDefaultWire(c, r), Inbound: providerInbound(ctx), Auth: c.Auth, Registry: r.Preset.Metadata, Overrides: overrides})
	if err != nil {
		return r, nil, err
	}
	if resolved.Wire != r.Wire {
		r, err = providers.ResolvePolicyWire(c, r.Model, providerInbound(ctx), resolved.Wire)
		if err != nil {
			return r, nil, err
		}
	}
	r.BaseURL, r.Model, r.Wire = resolved.BaseURL, resolved.Model, resolved.Wire
	if d.Metadata.ProviderId == "google-vertex" {
		_, _, base, e := selectedVertexMetadata(d)
		if e != nil {
			return r, nil, e
		}
		r.BaseURL, resolved.BaseURL, resolved.Endpoint = base, base, ""
	}
	return r, &resolved, nil
}
func sameCredentialLane(a, b inference.Target) bool {
	return a.BaseURL == b.BaseURL && a.Model == b.Model && a.Wire == b.Wire && a.Auth == b.Auth && a.Endpoint == b.Endpoint && policyIdentity(a) == policyIdentity(b) && selectionVersion(a.ReasoningRetention) == selectionVersion(b.ReasoningRetention) && a.ForceMessagesTranslation == b.ForceMessagesTranslation && a.Fast == b.Fast && selectionVersion(a.FastPolicy) == selectionVersion(b.FastPolicy)
}

func (a *inferenceAdapter) Resolve(ctx context.Context, p inference.Principal, model string, op inference.Operation) (inference.Route, error) {
	if e := a.BeforeSend(ctx, p); e != nil {
		return inference.Route{}, e
	}
	sc := scopeOf(p)
	routes, e := a.routes(ctx, sc)
	if e != nil {
		return inference.Route{}, e
	}
	chosen, syntheticFast, e := chooseFastRoute(routes, model)
	if e != nil {
		return inference.Route{}, e
	}
	routeBytes, routeVersion, e := a.s.GetPrivateVersion(ctx, sc, string(routeKind), chosen.Id)
	if e != nil {
		return inference.Route{}, inferenceError(e)
	}
	var latest v1.Route
	if proto.Unmarshal(routeBytes, &latest) != nil || !proto.Equal(chosen, &latest) {
		return inference.Route{}, inference.ErrUnavailable
	}
	sort.SliceStable(chosen.Targets, func(i, j int) bool { return chosen.Targets[i].Priority < chosen.Targets[j].Priority })
	selections := map[string]routeSelection{}
	connections := map[string]providerData{}
	targetWeights := map[string]int{}
	out := inference.Route{MaxSends: 4}
	for _, rt := range chosen.Targets {
		members, pool, e := a.targetMembers(ctx, sc, rt)
		if e != nil {
			return out, e
		}
		upstream := rt.ModelId
		if upstream == "" {
			upstream = chosen.ModelId
		}
		req := store.SelectionRequest{GroupID: "route/" + chosen.Id, Version: selectionVersion(chosen), Model: upstream, Strategy: store.SelectionPriority}
		if pool != nil {
			req.GroupID += "/pool/" + pool.Id
			req.Version = selectionVersion(struct {
				Route *v1.Route
				Pool  *v1.Pool
			}{chosen, pool})
			switch pool.Strategy {
			case v1.PoolStrategy_POOL_STRATEGY_ROUND_ROBIN:
				req.Strategy = store.SelectionRoundRobin
			case v1.PoolStrategy_POOL_STRATEGY_LEAST_RECENTLY_USED:
				req.Strategy = store.SelectionLRU
			}
		}
		var target inference.Target
		provider := ""
		for _, member := range members {
			d, version, e := a.providerVersion(ctx, sc, member.ConnectionId)
			if errors.Is(e, inference.ErrNoCredential) || errors.Is(e, store.ErrNotFound) {
				continue
			}
			if e != nil {
				return out, inferenceError(e)
			}
			if _, eligible := modelEvidence(d, upstream, time.Now()); !eligible {
				continue
			}
			t, e := targetForContext(ctx, d, upstream)
			if e != nil {
				return out, e
			}
			if err := applyReasoningRetention(chosen.ReasoningRetention, &t); err != nil {
				return out, err
			}
			if syntheticFast {
				if e = applyFastTarget(ctx, d, upstream, &t); e != nil {
					return out, e
				}
			}
			t.ForceMessagesTranslation = poolForcesMessagesTranslation(pool, d.Metadata.ProviderId, t.Auth)
			if target.Provider == "" {
				target = t
				provider = d.Metadata.ProviderId
			} else if !sameCredentialLane(target, t) || provider != d.Metadata.ProviderId {
				return out, &inference.Error{Status: 409, Code: "incompatible_pool_transport"}
			}
			connections[member.ConnectionId] = d
			weight := int(member.Weight)
			if weight == 0 {
				weight = 1
			}
			req.Candidates = append(req.Candidates, store.SelectionCandidate{ID: member.ConnectionId, Version: version, Priority: int(member.Priority), Weight: weight})
		}
		if len(req.Candidates) == 0 {
			continue
		}
		target.Retry.RotateOn429 = len(req.Candidates) > 1
		if chosen.Strategy == v1.RoutingStrategy_ROUTING_STRATEGY_FALLBACK {
			target.Retry.FallbackOn429 = true
			target.Retry.FallbackOn5xx = true
		}
		if budget := baseLaneBudget(provider, target.Auth, len(req.Candidates)); budget > out.MaxSends {
			out.MaxSends = budget
		}
		if genericAffinityEnabled(pool, target.Auth, provider) {
			if session := inference.AffinitySession(ctx); session != "" {
				req.AffinityKey = selectionVersion([]string{p.KeyID, provider, session})
				req.AffinityTTL = 24 * time.Hour
			}
		}
		selections[selectionKey(target)] = routeSelection{request: req, target: target, provider: provider, pool: pool}
		targetWeights[selectionKey(target)] = int(rt.Weight)
		out.Targets = append(out.Targets, target)
	}
	if len(out.Targets) == 0 {
		return out, inference.ErrNoCredential
	}
	extra, err := a.hostedExchangeAllowance(ctx, p, out.MaxSends)
	if err != nil {
		return out, err
	}
	out.MaxSends += extra
	// Reserve support exchanges once; every later operation consumes this same ledger.
	out.MaxSends += routingObservationAllowance(connections)
	a.mu.Lock()
	a.selections = selections
	a.connections = connections
	a.attempted = map[string]map[string]bool{}
	a.native = map[string]nativeSelectionState{}
	a.compactionHandoff = nil
	a.mu.Unlock()
	out, e = a.selectJEVRoute(ctx, p, chosen, out, selections)
	if e != nil {
		return out, e
	}
	if chosen.Strategy == v1.RoutingStrategy_ROUTING_STRATEGY_WEIGHTED || chosen.Strategy == v1.RoutingStrategy_ROUTING_STRATEGY_ROUND_ROBIN {
		req := store.SelectionRequest{GroupID: chosen.Id, Version: routeVersion, Model: chosen.ModelId, Strategy: store.SelectionWeighted}
		if chosen.Strategy == v1.RoutingStrategy_ROUTING_STRATEGY_ROUND_ROBIN {
			req.Strategy = store.SelectionRoundRobin
		}
		for _, t := range out.Targets {
			weight := targetWeights[selectionKey(t)]
			if weight < 1 {
				weight = 1
			}
			req.Candidates = append(req.Candidates, store.SelectionCandidate{ID: selectionKey(t), Weight: weight})
		}

		lease, e := a.current(p)
		if e != nil {
			return out, e
		}
		receipt, e := a.s.SelectRoute(ctx, lease, req)
		if e != nil {
			return out, inferenceError(e)
		}
		for _, t := range out.Targets {
			if selectionKey(t) == receipt.CandidateID {
				out.Targets = []inference.Target{t}
				break
			}
		}
	} else if chosen.Strategy != v1.RoutingStrategy_ROUTING_STRATEGY_FALLBACK {
		out.Targets = out.Targets[:1]
	} else if len(out.Targets) > 2 {
		// The guarded request ledger allows one provider/model transition. Account
		// rotation within a target is tracked separately and does not expand this.
		out.Targets = out.Targets[:2]
	}
	a.mu.Lock()
	a.selections = selections
	a.attempted = map[string]map[string]bool{}
	a.connections = connections
	a.mu.Unlock()
	if e = a.BeforeSend(ctx, p); e != nil {
		return out, e
	}
	return out, nil
}

func credentialFor(ctx context.Context, d providerData, t inference.Target) (inference.Credential, error) {
	current, e := selectedTargetForContext(ctx, d, t)
	if e != nil || !sameCredentialLane(t, current) {
		return inference.Credential{}, inference.ErrNoCredential
	}
	r, _, e := resolvedProviderRoute(ctx, d, t.Model)
	if fastTargetRequested(t) {
		r, _, e = resolvedFastProviderRoute(ctx, d, t.Model)
	}
	if e != nil {
		return inference.Credential{}, inference.ErrNoCredential
	}
	c := inference.Credential{AccountID: d.Metadata.Id, Secret: d.Secret, Headers: r.Headers.Clone()}
	if d.Credential != nil {
		c.Secret = d.Credential.AccessToken
		if d.Metadata.ProviderId == "workbuddy-ai" {
			for field, header := range map[string]string{"uid": "X-User-Id", "domain": "X-Domain", "enterpriseId": "X-Enterprise-Id"} {
				if value := d.Credential.Metadata[field]; value != "" {
					c.Headers.Set(header, value)
				}
			}
			if value := d.Credential.Metadata["enterpriseId"]; value != "" {
				c.Headers.Set("X-Tenant-Id", value)
			}
		}
		if d.Credential.ProjectID != "" {
			c.Headers.Set("X-Aiprouter-Google-Project", d.Credential.ProjectID)
		}
	}
	if d.Metadata.ProviderId == "google-vertex" {
		project, location, _, err := selectedVertexMetadata(d)
		if err != nil {
			return inference.Credential{}, err
		}
		c.Headers.Set(google.ProjectHeader, project)
		c.Headers.Set(google.LocationHeader, location)
	}
	if r.Wire == providers.Wire(azure.Wire) && d.Metadata.Configuration != nil && d.Metadata.Configuration.MinMaxOutputTokens != nil {
		raw, err := json.Marshal(azure.Options{MinMaxOutputTokens: d.Metadata.Configuration.GetMinMaxOutputTokens()})
		if err != nil {
			return inference.Credential{}, inference.ErrUnavailable
		}
		c.Headers.Set(azure.OptionsHeader, string(raw))
	}
	return c, nil
}
func (a *inferenceAdapter) Select(ctx context.Context, p inference.Principal, t inference.Target, previous string) (inference.Credential, error) {
	if e := a.BeforeSend(ctx, p); e != nil {
		return inference.Credential{}, e
	}
	lease, e := a.current(p)
	if e != nil {
		return inference.Credential{}, e
	}
	a.mu.Lock()
	selection, ok := a.selections[selectionKey(t)]
	a.mu.Unlock()
	if !ok {
		if previous != "" {
			return inference.Credential{}, inference.ErrNoCredential
		}
		d, e := a.provider(ctx, scopeOf(p), t.Provider)
		if e != nil {
			return inference.Credential{}, inferenceError(e)
		}
		return a.captureCredential(ctx, p, t, d.Metadata.Id)
	}
	req := selection.request
	req.Candidates = nil
	if previous != "" {
		a.mu.Lock()
		limit := 6
		if selection.provider == "openai" && t.Auth == inference.OAuth {
			limit = 2
		} else if (selection.provider == "anthropic" || selection.provider == "anthropic2") && t.Auth == inference.OAuth {
			limit = 4
		}
		if t.Auth == inference.OAuth && len(a.attempted[selectionKey(t)]) >= limit {
			a.mu.Unlock()
			return inference.Credential{}, inference.ErrNoCredential
		}
		for id := range a.attempted[selectionKey(t)] {
			req.Exclude = append(req.Exclude, id)
		}
		a.mu.Unlock()
	}

	for _, candidate := range selection.request.Candidates {
		if candidate.ID == previous {
			continue
		}
		d, version, e := a.providerVersion(ctx, scopeOf(p), candidate.ID)
		if errors.Is(e, inference.ErrNoCredential) || errors.Is(e, store.ErrNotFound) {
			continue
		}
		if e != nil {
			return inference.Credential{}, inferenceError(e)
		}
		if _, eligible := modelEvidence(d, t.Model, time.Now()); !eligible {
			continue
		}
		current, e := selectedTargetForContext(ctx, d, t)
		if e != nil || !sameCredentialLane(t, current) {
			continue
		}
		candidate.Version = version
		req.Candidates = append(req.Candidates, candidate)
	}
	if len(req.Candidates) == 0 {
		return inference.Credential{}, inference.ErrNoCredential
	}
	req, e = a.preselectNative(ctx, p, t, selection.provider, req)
	if e != nil {
		return inference.Credential{}, e
	}
	receipt, e := a.s.SelectAccount(ctx, lease, req)
	if errors.Is(e, store.ErrNotFound) {
		return inference.Credential{}, inference.ErrNoCredential
	}
	if e != nil {
		return inference.Credential{}, inferenceError(e)
	}
	d, e := a.provider(ctx, scopeOf(p), receipt.CandidateID)
	if e != nil {
		return inference.Credential{}, inferenceError(e)
	}
	a.mu.Lock()
	if a.attempted == nil {
		a.attempted = map[string]map[string]bool{}
	}
	if a.attempted[selectionKey(t)] == nil {
		a.attempted[selectionKey(t)] = map[string]bool{}
	}
	a.attempted[selectionKey(t)][d.Metadata.Id] = true
	a.mu.Unlock()
	return a.captureCredential(ctx, p, t, d.Metadata.Id)
}
func (a *inferenceAdapter) Refresh(ctx context.Context, p inference.Principal, t inference.Target, c inference.Credential) (inference.Credential, error) {
	if e := a.BeforeSend(ctx, p); e != nil {
		return c, e
	}
	lease, e := a.current(p)
	if e != nil {
		return c, e
	}
	a.mu.Lock()
	group, ok := a.selections[selectionKey(t)]
	a.mu.Unlock()
	allowed := c.AccountID == t.Provider
	if ok {
		for _, candidate := range group.request.Candidates {
			if candidate.ID == c.AccountID {
				allowed = true
			}
		}
	}
	if !allowed {
		return c, inference.ErrNoCredential
	}
	d, e := refreshProviderCredential(ctx, a.s, lease, c.AccountID)
	if e != nil {
		return c, inferenceError(e)
	}
	return a.captureCredential(ctx, p, t, d.Metadata.Id)
}
func (a *inferenceAdapter) Failed(ctx context.Context, p inference.Principal, t inference.Target, c inference.Credential, status int, delay time.Duration) error {
	if e := a.BeforeSend(ctx, p); e != nil {
		return e
	}
	lease, e := a.current(p)
	if e != nil {
		return e
	}
	if e = a.failJEV(ctx); e != nil {
		return e
	}
	if canonicalMistral(t) {
		return nil
	}
	if status != http.StatusTooManyRequests {
		return nil
	}
	if managed, err := a.nativeHealthOwnsQuota(ctx, p, t, c, status); err != nil {
		return err
	} else if managed {
		return nil
	}
	if delay <= 0 {
		delay = time.Minute
	}
	cap := 10 * time.Minute
	a.mu.Lock()
	d := a.connections[c.AccountID]
	version := a.versions[c.AccountID]
	a.mu.Unlock()
	if d.Metadata != nil && d.Metadata.ProviderId == "openai" && t.Auth == inference.OAuth {
		cap = 24 * time.Hour
	}
	if delay > cap {
		delay = cap
	}
	return inferenceError(a.s.SetAccountCooldownVersion(ctx, lease, c.AccountID, version, t.Model, time.Now().Add(delay)))
}

func providerRetryPolicy(r providers.Route, auth inference.AuthMode) inference.RetryPolicy {
	policy := inference.RetryPolicy{MaxWait: 60 * time.Second}
	if auth == inference.OAuth {
		policy.RefreshOn401 = r.Preset.ID != "zed" && r.Preset.ID != "meta-muse" && r.Preset.ID != "dsh" && r.Preset.ID != "mimo-free"
		return policy
	}
	if auth != inference.APIKey {
		return policy
	}
	var metadata struct {
		Rate *struct {
			Enabled  *bool `json:"enabled"`
			Attempts *int  `json:"attempts"`
			Interval *int  `json:"intervalMs"`
			Max      *int  `json:"maxIntervalMs"`
		} `json:"retryOn429"`
		Transient *struct {
			Enabled  *bool `json:"enabled"`
			Attempts *int  `json:"attempts"`
		} `json:"transientRetryOn5xx"`
	}
	_ = json.Unmarshal(r.Preset.Metadata, &metadata)
	if metadata.Rate != nil {
		if metadata.Rate.Enabled == nil || *metadata.Rate.Enabled {
			policy.RateLimitRetries = 3
			policy.Interval = 5 * time.Second
			if metadata.Rate.Attempts != nil {
				policy.RateLimitRetries = *metadata.Rate.Attempts
			}
			if metadata.Rate.Interval != nil {
				policy.Interval = time.Duration(*metadata.Rate.Interval) * time.Millisecond
			}
			if metadata.Rate.Max != nil {
				policy.MaxWait = time.Duration(*metadata.Rate.Max) * time.Millisecond
			}
		}
	} else if canonicalPatientKey(r) {
		policy.RateLimitRetries = 6
		policy.Interval = 10 * time.Second
	}
	if metadata.Transient != nil && (r.Wire == providers.Chat || r.Wire == providers.Responses) && (metadata.Transient.Enabled == nil || *metadata.Transient.Enabled) {
		policy.TransientSends = 3
		if metadata.Transient.Attempts != nil {
			policy.TransientSends = *metadata.Transient.Attempts
		}
	}
	return policy
}
func canonicalPatientKey(r providers.Route) bool {
	base := strings.TrimRight(r.BaseURL, "/")
	return r.Wire == providers.Chat && base == "https://api.mistral.ai/v1" || base == "https://opencode.ai/zen/go/v1" || (base == "https://api.commandcode.ai" || base == "https://api.commandcode.ai/provider/v1")
}

// captureCredential binds the exact encrypted version to all physical writes.
// A metadata edit, pause, replacement or uncertain refresh invalidates this proof.
func (a *inferenceAdapter) captureCredential(ctx context.Context, p inference.Principal, t inference.Target, id string) (inference.Credential, error) {
	d, version, e := a.providerVersion(ctx, scopeOf(p), id)
	if e != nil {
		return inference.Credential{}, inferenceError(e)
	}
	c, e := credentialFor(ctx, d, t)
	if e != nil {
		return c, e
	}
	a.mu.Lock()
	if a.versions == nil {
		a.versions = map[string]string{}
	}
	if a.connections == nil {
		a.connections = map[string]providerData{}
	}
	c.CredentialVersion = version
	c.StateBinding = selectionVersion([]string{id, version})
	a.versions[id] = version
	a.connections[id] = d
	a.activeTarget = t
	a.compactionHandoff = nil
	a.activeAccount = id
	a.activeVersion = version
	a.mu.Unlock()
	if err := a.captureNativeHealthDetour(ctx, p, t, c); err != nil {
		return inference.Credential{}, err
	}
	return c, nil
}
func (a *inferenceAdapter) SelectBound(ctx context.Context, p inference.Principal, t inference.Target, id string) (inference.Credential, error) {
	if e := a.BeforeSend(ctx, p); e != nil {
		return inference.Credential{}, e
	}
	a.mu.Lock()
	group, ok := a.selections[selectionKey(t)]
	a.mu.Unlock()
	allowed := id == t.Provider
	if ok {
		for _, c := range group.request.Candidates {
			if c.ID == id {
				allowed = true
			}
		}
	}
	if !allowed {
		return inference.Credential{}, inference.ErrContinuation
	}
	return a.captureCredential(ctx, p, t, id)
}
func (a *inferenceAdapter) AuthorizeRecovery(ctx context.Context, p inference.Principal, t inference.Target, endpoint string) error {
	if e := a.BeforeSend(ctx, p); e != nil {
		return e
	}
	a.mu.Lock()
	id := a.activeAccount
	a.mu.Unlock()
	d, e := a.provider(ctx, scopeOf(p), id)
	if e != nil {
		return inferenceError(e)
	}
	if d.Metadata.ProviderId != "kiro" || t.Wire != "kiro" || t.Auth != inference.OAuth {
		return inference.ErrDestination
	}
	original, e := targetForContext(ctx, d, t.Model)
	if e != nil || original.BaseURL != t.BaseURL {
		return inference.ErrDestination
	}
	if alternate := kiro.LegacyEndpoint(original.BaseURL); alternate == "" || alternate != endpoint {
		return inference.ErrDestination
	}
	return nil
}

func providerInbound(ctx context.Context) providers.Inbound {
	switch inference.InboundWire(ctx) {
	case inference.Responses:
		return providers.InboundResponses
	case inference.Anthropic:
		return providers.InboundAnthropic
	default:
		return providers.InboundChat
	}
}

// The pinned Cursor transport is a duplex turn: RunSSE registration and
// BidiAppend replies are distinct physical exchanges, not whole-turn retries.
// Bound the service turn to registration, initial append and 256 protocol
// replies, matching the adapter's bounded reply queue. Upstream does not define
// a total append cap; this explicit service safety bound must not be described
// as its retry count. Every exchange consumes the original request ledger.
const cursorPhysicalExchangeBudget = 2 + 256

func selectionKey(t inference.Target) string {
	return selectionVersion([]string{t.Provider, t.BaseURL, t.Model, string(t.Wire), string(t.Auth), t.Endpoint, policyIdentity(t), selectionVersion(t.Fast), selectionVersion(t.FastPolicy), selectionVersion(t.ForceMessagesTranslation), selectionVersion(t.ReasoningRetention)})
}

func genericAffinityEnabled(pool *v1.Pool, auth inference.AuthMode, provider string) bool {
	return pool != nil && pool.GetAffinityEnabled() && auth == inference.OAuth && provider != "openai" && provider != "anthropic" && provider != "anthropic2"
}

func policyDefaultWire(c providers.Connection, r providers.Route) providers.Wire {
	if c.Wire != "" {
		return c.Wire
	}
	return r.Preset.Wire
}
func policyIdentity(t inference.Target) string {
	if t.Policy == nil {
		return ""
	}
	return string(t.Policy.Metadata())
}

// Reapply semantic selectors against the current account before comparing its lane.
func selectedTargetForContext(ctx context.Context, d providerData, t inference.Target) (inference.Target, error) {
	current, err := targetForContext(ctx, d, t.Model)
	// This flag belongs to the captured pool policy, not the account credential.
	current.ForceMessagesTranslation = t.ForceMessagesTranslation
	current.ReasoningRetention = t.ReasoningRetention
	if err == nil && fastTargetRequested(t) {
		err = applyFastTarget(ctx, d, t.Model, &current)
	}
	return current, err
}

func poolForcesMessagesTranslation(pool *v1.Pool, provider string, auth inference.AuthMode) bool {
	return pool != nil && pool.NativeOptions != nil && pool.NativeOptions.NativeMessages != nil && !*pool.NativeOptions.NativeMessages && auth == inference.OAuth && (provider == "anthropic" || provider == "anthropic2")
}
