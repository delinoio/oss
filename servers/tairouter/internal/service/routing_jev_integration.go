// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
	"google.golang.org/protobuf/proto"
)

const jevPinNamespace = "jev-route-pin"

type jevStoredPin struct {
	Epoch, Config string
	Decision      *jevDecision `json:",omitempty"`
	IdleExpires   time.Time
}
type jevRequestPin struct {
	principal         inference.Principal
	id, epoch, config string
	routeVersion      string
	ttl               time.Duration
	decision          jevDecision
	target            inference.Target
	eligible          func(context.Context, jevCandidate) (bool, error)
	candidate         jevCandidate
	route             *v1.Route
}

func newJEVEpoch() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func (a *inferenceAdapter) beginJEVPin(ctx context.Context, p inference.Principal, route *v1.Route, candidates []jevCandidate, config string, eligible func(context.Context, jevCandidate) (bool, error)) (*jevRequestPin, *jevDecision, error) {
	ttl := time.Duration(route.GetJevDecision().GetSessionPinTtlSeconds()) * time.Second
	lane := inference.AffinitySession(ctx)
	if ttl == 0 || lane == "" {
		return nil, nil, nil
	}
	if ttl < time.Minute || ttl > 24*time.Hour {
		return nil, nil, jevConfigError()
	}
	rawRoute, routeVersion, err := a.s.GetPrivateVersion(ctx, scopeOf(p), string(routeKind), route.Id)
	if err != nil {
		return nil, nil, inferenceError(err)
	}
	var currentRoute v1.Route
	if proto.Unmarshal(rawRoute, &currentRoute) != nil || !proto.Equal(route, &currentRoute) {
		return nil, nil, inference.ErrUnavailable
	}
	config = selectionVersion([]string{config, routeVersion})
	scope := &jevRequestPin{principal: p, id: selectionVersion([]string{p.OrganizationID, p.UserID, p.KeyID, route.Id, lane}), config: config, routeVersion: routeVersion, ttl: ttl, eligible: eligible, route: proto.Clone(route).(*v1.Route)}
	var current jevStoredPin
	err = a.UpdateProviderState(ctx, p, jevPinNamespace, scope.id, time.Now().Add(24*time.Hour), func(raw []byte) ([]byte, error) {
		if len(raw) > 0 && json.Unmarshal(raw, &current) != nil {
			return nil, inference.ErrContinuation
		}
		if current.Epoch == "" || current.Config != config {
			current = jevStoredPin{Epoch: newJEVEpoch(), Config: config}
		}
		if current.Decision != nil && !current.IdleExpires.After(time.Now()) {
			current.Decision = nil
		}
		scope.epoch = current.Epoch
		return json.Marshal(current)
	})
	if err != nil {
		return nil, nil, err
	}
	if current.Decision == nil {
		return scope, nil, nil
	}
	for _, c := range candidates {
		if c.Key != current.Decision.TargetKey {
			continue
		}
		effortAllowed := current.Decision.Effort == "" && len(c.Efforts) == 0
		for _, e := range c.Efforts {
			if e == current.Decision.Effort {
				effortAllowed = true
			}
		}
		ok, err := eligible(ctx, c)
		if err != nil {
			return nil, nil, err
		}
		if ok && effortAllowed && (c.Quota == nil || c.Quota.Tier != jevQuotaNearlyExhausted) {
			return scope, current.Decision, nil
		}
	}
	// A no-longer-eligible target releases the pin and fences earlier completions.
	err = a.UpdateProviderState(ctx, p, jevPinNamespace, scope.id, time.Now().Add(24*time.Hour), func(raw []byte) ([]byte, error) {
		var latest jevStoredPin
		if json.Unmarshal(raw, &latest) != nil {
			return nil, inference.ErrContinuation
		}
		if latest.Epoch == scope.epoch {
			latest.Epoch = newJEVEpoch()
			latest.Decision = nil
		}
		scope.epoch = latest.Epoch
		return json.Marshal(latest)
	})
	return scope, nil, err
}

// finishJEV is called once by the request's usage recorder. A failed target
// invalidates concurrent writers; cancellation neither publishes nor releases.
func (a *inferenceAdapter) finishJEV(ctx context.Context, r inference.UsageRecord) error {
	a.mu.Lock()
	scope := a.jev
	a.jev = nil
	a.mu.Unlock()
	if scope == nil || r.Canceled || r.Outcome == inference.OutcomeCanceled {
		return nil
	}
	if r.Outcome != inference.OutcomeSuccess && r.Sends == 0 {
		return nil
	}
	success := r.Outcome == inference.OutcomeSuccess && scope.decision.Gate == jevApply && r.Model == scope.target.Model && r.Provider == scope.target.Provider
	if success {
		ok, err := scope.eligible(ctx, scope.candidate)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		raw, version, err := a.s.GetPrivateVersion(ctx, scopeOf(scope.principal), string(routeKind), scope.route.Id)
		if err != nil {
			return inferenceError(err)
		}
		var current v1.Route
		if proto.Unmarshal(raw, &current) != nil {
			return inference.ErrUnavailable
		}
		if !proto.Equal(&current, scope.route) || scope.routeVersion != "" && version != scope.routeVersion {
			return nil
		}
	}
	return a.UpdateProviderState(ctx, scope.principal, jevPinNamespace, scope.id, time.Now().Add(24*time.Hour), func(raw []byte) ([]byte, error) {
		// An expired/evicted epoch is unknown. Never restore it from an old request.
		if len(raw) == 0 {
			return nil, store.ErrNotFound
		}
		var current jevStoredPin
		if json.Unmarshal(raw, &current) != nil {
			return nil, inference.ErrContinuation
		}
		if current.Config != scope.config {
			return raw, nil
		}
		if success {
			if current.Epoch == scope.epoch {
				decision := scope.decision
				current.Decision = &decision
				current.IdleExpires = time.Now().Add(scope.ttl)
			}
		} else {
			current.Epoch = newJEVEpoch()
			current.Decision = nil
		}
		return json.Marshal(current)
	})
}

// withJEVModelInvocation isolates nested ordinary routing state. The same lease
// and core physical-send counter remain in force; no second admission is taken.
func (a *inferenceAdapter) withJEVModelInvocation(invoke func(context.Context, string, json.RawMessage) (string, error)) func(context.Context, string, json.RawMessage) (string, error) {
	return func(ctx context.Context, model string, raw json.RawMessage) (string, error) {
		a.mu.Lock()
		selections := maps.Clone(a.selections)
		connections := maps.Clone(a.connections)
		versions := maps.Clone(a.versions)
		attempted := make(map[string]map[string]bool, len(a.attempted))
		for k, v := range a.attempted {
			attempted[k] = maps.Clone(v)
		}
		account, version, target, pin := a.activeAccount, a.activeVersion, a.activeTarget, a.jev
		native := maps.Clone(a.native)
		handoff := a.compactionHandoff
		a.compactionHandoff = nil
		a.native = nil
		a.jev = nil
		a.mu.Unlock()
		defer func() {
			a.mu.Lock()
			a.selections = selections
			a.connections = connections
			a.versions = versions
			a.attempted = attempted
			a.activeAccount = account
			a.activeVersion = version
			a.activeTarget = target
			a.jev = pin
			a.native = native
			a.compactionHandoff = handoff
			a.mu.Unlock()
		}()
		return invoke(ctx, model, raw)
	}
}

func jevRouteTarget(chosen *v1.Route, selection routeSelection) *v1.RouteTarget {
	for _, rt := range chosen.Targets {
		model := rt.ModelId
		if model == "" {
			model = chosen.ModelId
		}
		if model != selection.request.Model {
			continue
		}
		if id := rt.GetPoolId(); id != "" {
			if strings.HasSuffix(selection.request.GroupID, "/pool/"+id) {
				return rt
			}
			continue
		}
		for _, candidate := range selection.request.Candidates {
			if candidate.ID == rt.GetConnectionId() {
				return rt
			}
		}
	}
	return nil
}

// selectJEVRouteInput is the service composition seam. Core supplies the original
// body, destination policy and a budget-bound context before it serializes a send.
func (a *inferenceAdapter) selectJEVRouteInput(ctx context.Context, p inference.Principal, chosen *v1.Route, out inference.Route, selections map[string]routeSelection, body json.RawMessage, policy inference.DestinationPolicy, invoke func(context.Context, string, json.RawMessage) (string, error)) (inference.Route, error) {
	if chosen.GetStrategy() != v1.RoutingStrategy_ROUTING_STRATEGY_JEV {
		return out, nil
	}
	cfg := chosen.GetJevDecision()
	if cfg == nil {
		return out, jevConfigError()
	}
	if err := validateJEVRoute(ctx, a.s, scopeOf(p), chosen); err != nil {
		return out, err
	}
	var candidates []jevCandidate
	targets := map[string]inference.Target{}
	for _, target := range out.Targets {
		key := selectionKey(target)
		group, ok := selections[key]
		if !ok {
			return out, jevConfigError()
		}
		rt := jevRouteTarget(chosen, group)
		if rt == nil {
			return out, jevConfigError()
		}
		c := jevCandidate{Key: key, Provider: group.provider, Model: target.Model, Profile: rt.ModelProfile}
		for _, effort := range rt.ReasoningEfforts {
			c.Efforts = append(c.Efforts, jevEffort(effort))
		}
		candidates = append(candidates, c)
		targets[key] = target
	}
	eligible := func(ctx context.Context, c jevCandidate) (bool, error) {
		raw, err := a.s.GetPrivate(ctx, scopeOf(p), string(routeKind), chosen.Id)
		if err != nil {
			return false, inferenceError(err)
		}
		var currentRoute v1.Route
		if proto.Unmarshal(raw, &currentRoute) != nil {
			return false, inference.ErrUnavailable
		}
		if !proto.Equal(&currentRoute, chosen) {
			return false, nil
		}
		group, ok := selections[c.Key]
		if !ok {
			return false, nil
		}
		if rt := jevRouteTarget(chosen, group); rt != nil && rt.GetPoolId() != "" {
			raw, err := a.s.GetPrivate(ctx, scopeOf(p), string(poolKind), rt.GetPoolId())
			if err != nil {
				return false, inferenceError(err)
			}
			var pool v1.Pool
			if json.Unmarshal(raw, &pool) != nil {
				return false, inference.ErrUnavailable
			}
			version := selectionVersion(struct {
				Route *v1.Route
				Pool  *v1.Pool
			}{chosen, &pool})
			if version != group.request.Version {
				return false, nil
			}
		}
		for _, candidate := range group.request.Candidates {
			d, version, err := a.providerVersion(ctx, scopeOf(p), candidate.ID)
			if errors.Is(err, inference.ErrNoCredential) || errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return false, inferenceError(err)
			}
			if candidate.Version != "" && version != candidate.Version {
				continue
			}
			evidence, ok := modelEvidence(d, group.target.Model, time.Now())
			if !ok {
				continue
			}
			target, err := selectedTargetForContext(ctx, d, group.target)
			if err != nil || !sameCredentialLane(group.target, target) {
				continue
			}
			supported := true
			for _, e := range c.Efforts {
				found := false
				for _, allowed := range evidence.Efforts {
					if string(e) == allowed {
						found = true
					}
				}
				if !found {
					supported = false
				}
			}
			if supported {
				return true, nil
			}
		}
		return false, nil
	}
	requests := make(map[string]store.SelectionRequest, len(selections))
	for key, selection := range selections {
		requests[key] = selection.request
	}
	config := selectionVersion(struct {
		Route      *v1.Route
		Selections map[string]store.SelectionRequest
	}{chosen, requests})
	if cfg.GetDecisionQuotaSignals() {
		tiers, err := resolveJEVQuotaTiers(cfg.GetDecisionQuotaTiers())
		if err != nil {
			return out, err
		}
		for i := range candidates {
			signal, err := a.jevQuotaForSelection(ctx, p, selections[candidates[i].Key], tiers)
			if err != nil {
				return out, err
			}
			candidates[i].Quota = &signal
		}
	}
	scope, pinned, err := a.beginJEVPin(ctx, p, chosen, candidates, config, eligible)
	if err != nil {
		return out, err
	}
	var decision jevDecision
	if pinned != nil {
		decision = withJEVQuotaSummary(*pinned, candidates)
	} else {
		options := jevOptions{ConnectionID: cfg.DecisionConnectionId, Model: cfg.DecisionServiceModel, DecisionModel: cfg.DecisionModel, Timeout: time.Duration(cfg.DecisionTimeoutMs) * time.Millisecond, Body: body, Candidates: candidates, Eligible: eligible}
		if invoke != nil {
			options.InvokeModel = a.withJEVModelInvocation(invoke)
		}
		decision, err = a.resolveJEV(ctx, p, options, policy)
		if err != nil {
			return out, err
		}
	}
	target, ok := targets[decision.TargetKey]
	if !ok {
		return out, inference.ErrNoCredential
	}
	// Limit the later account selection to accounts supporting the chosen effort.
	group := selections[decision.TargetKey]
	filtered := group.request.Candidates[:0:0]
	for _, candidate := range group.request.Candidates {
		d, version, err := a.providerVersion(ctx, scopeOf(p), candidate.ID)
		if errors.Is(err, inference.ErrNoCredential) || errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return out, inferenceError(err)
		}
		evidence, ok := modelEvidence(d, target.Model, time.Now())
		if !ok {
			continue
		}
		supports := decision.Effort == ""
		for _, effort := range evidence.Efforts {
			if effort == string(decision.Effort) {
				supports = true
			}
		}
		if supports {
			candidate.Version = version
			filtered = append(filtered, candidate)
		}
	}
	if len(filtered) == 0 {
		return out, inference.ErrNoCredential
	}
	group.request.Candidates = filtered
	selections[decision.TargetKey] = group
	target.ReasoningEffort = string(decision.Effort)
	out.Targets = []inference.Target{target}
	if scope != nil {
		scope.decision = decision
		scope.target = target
		for _, candidate := range candidates {
			if candidate.Key == decision.TargetKey {
				scope.candidate = candidate
				scope.candidate.Efforts = nil
				if decision.Effort != "" {
					scope.candidate.Efforts = []jevEffort{decision.Effort}
				}
				break
			}
		}
	}
	a.mu.Lock()
	a.jev = scope
	a.mu.Unlock()
	return out, nil
}

// selectJEVRoute runs after Resolve has built all candidate lanes, before its
// ordinary strategy truncation. A nested decision model may never enter JEV.
func (a *inferenceAdapter) selectJEVRoute(ctx context.Context, p inference.Principal, chosen *v1.Route, out inference.Route, selections map[string]routeSelection) (inference.Route, error) {
	if chosen.GetStrategy() != v1.RoutingStrategy_ROUTING_STRATEGY_JEV {
		return out, nil
	}
	if inference.RoutingModelInvocation(ctx) {
		return out, &inference.Error{Status: 400, Code: "recursive_routing_model"}
	}
	body := inference.RoutingRequestBody(ctx)
	if len(body) == 0 {
		return out, &inference.Error{Status: 503, Code: "routing_request_context_required"}
	}
	// A service needs one physical exchange. The model backend receives at most
	// four extra exchanges; all decision and inference sends share the same ledger.
	if chosen.GetJevDecision().GetDecisionModel() != "" {
		out.MaxSends += 4
	} else {
		out.MaxSends++
	}
	ctx = inference.RoutingDecisionContext(ctx, out.MaxSends)
	return a.selectJEVRouteInput(ctx, p, chosen, out, selections, body, inference.RoutingDestinationPolicy(ctx), inference.InvokeRoutingModel)
}

// failJEV fences a real upstream failure even if a later retry succeeds. The
// request retains its old epoch, so that success cannot republish the failed pin.
func (a *inferenceAdapter) failJEV(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	a.mu.Lock()
	scope := a.jev
	a.mu.Unlock()
	if scope == nil {
		return nil
	}
	return a.UpdateProviderState(ctx, scope.principal, jevPinNamespace, scope.id, time.Now().Add(24*time.Hour), func(raw []byte) ([]byte, error) {
		if len(raw) == 0 {
			return nil, store.ErrNotFound
		}
		var current jevStoredPin
		if json.Unmarshal(raw, &current) != nil {
			return nil, inference.ErrContinuation
		}
		if current.Config == scope.config {
			current.Epoch = newJEVEpoch()
			current.Decision = nil
		}
		return json.Marshal(current)
	})
}
