// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"errors"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

// A native usage read and a due quota recovery probe share one fetch per
// candidate. The normal collector owns a separate durable cadence claim. These
// are allowances, never replenishments or promises that a probe will run.
func routingObservationAllowance(connections map[string]providerData) int {
	native, normal := 0, 0
	for _, d := range connections {
		if d.Metadata == nil {
			continue
		}
		if d.Metadata.AuthKind == v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH && (d.Metadata.ProviderId == "openai" || d.Metadata.ProviderId == "anthropic" || d.Metadata.ProviderId == "anthropic2") {
			native++
		}
		normal += normalQuotaCollectionBudget(d)
		if vertexAutoRequested(d) {
			normal++
		}
	}
	return min(native, 512) + normal
}

// These allowances cover bounded support exchanges, not extra retry attempts.
// All exchanges still consume the original ledger and current dispatch guard.
func (a *inferenceAdapter) hostedExchangeAllowance(ctx context.Context, p inference.Principal, mainBudget int) (int, error) {
	settings, err := readHostedSettings(ctx, a.s, scopeOf(p))
	if err != nil {
		return 0, inferenceError(err)
	}
	extra := 0
	if s := settings.Search; s != nil && s.Target != nil && (s.Enabled == nil || s.GetEnabled()) {
		n := 3
		if s.MaxSearchesPerTurn != nil {
			n = int(s.GetMaxSearchesPerTurn())
		}
		// One operation, plus at most one credential refresh and discovery.
		refresh, err := a.helperRefreshAllowance(ctx, p, s.Target)
		if err != nil {
			return 0, err
		}
		extra += n * (1 + refresh)
	}
	if s := settings.Images; s != nil && s.Target != nil {
		refresh, err := a.helperRefreshAllowance(ctx, p, s.Target)
		if err != nil {
			return 0, err
		}
		if s.BridgeEnabled {
			extra += 10 * (1 + 4 + refresh)
		} // submit, four downloads, refresh/discovery
		if s.VideoBridgeEnabled {
			timeout := 300 * time.Second
			if s.VideoTimeoutMs != nil {
				timeout = time.Duration(*s.VideoTimeoutMs) * time.Millisecond
			}
			polls := 0
			for elapsed, delay := time.Duration(0), 5*time.Second; elapsed+delay < timeout; {
				elapsed += delay
				polls++
				delay = delay * 3 / 2
				if delay > 15*time.Second {
					delay = 15 * time.Second
				}
			}
			extra += 3 * (1 + polls + 1 + refresh)
		}
	}
	if s := settings.Vision; s != nil && (s.GetTarget() != nil || s.GetRouteModel() != "") && (s.Enabled == nil || s.GetEnabled()) {
		n := 8
		if s.MaxDescriptionsPerTurn != nil {
			n = int(s.GetMaxDescriptionsPerTurn())
		}
		refresh, err := a.helperRefreshAllowance(ctx, p, s.GetTarget())
		if err != nil {
			return 0, err
		}
		sends := 1
		if s.GetRouteModel() != "" {
			refresh = 0
			sends, err = a.routedHelperBudget(ctx, p, s.GetRouteModel())
			if err != nil {
				return 0, err
			}
		}
		extra += n * (sends + refresh)
	}
	if extra > 0 {
		extra += 3 * mainBudget
	} // Each helper round repeats the original adapter preflights and bounded exchanges.
	return extra, nil
}

// Inspect candidates without selecting: sizing must not advance RR/weighted
// receipts or recurse through hosted settings on a routed vision helper.
func (a *inferenceAdapter) routedHelperBudget(ctx context.Context, p inference.Principal, model string) (int, error) {
	routes, err := a.routes(ctx, scopeOf(p))
	if err != nil {
		return 0, err
	}
	var chosen *v1.Route
	for _, r := range routes {
		if routeAliasMatches(r.ModelId, model) {
			if chosen != nil {
				return 0, inference.ErrUnsupported
			}
			chosen = r
		}
	}
	if chosen == nil || chosen.Strategy == v1.RoutingStrategy_ROUTING_STRATEGY_JEV {
		return 0, inference.ErrUnsupported
	}
	maxSends := 4
	observedConnections := map[string]providerData{}
	for _, rt := range chosen.Targets {
		members, _, err := a.targetMembers(ctx, scopeOf(p), rt)
		if err != nil {
			return 0, err
		}
		n := len(members)
		if n > 6 {
			n = 6
		}
		for _, member := range members {
			d, err := a.provider(ctx, scopeOf(p), member.ConnectionId)
			if errors.Is(err, inference.ErrNoCredential) || errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return 0, inferenceError(err)
			}
			auth := inference.APIKey
			if d.Metadata.AuthKind == v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH {
				auth = inference.OAuth
			}
			observedConnections[member.ConnectionId] = d
			sends := baseLaneBudget(d.Metadata.ProviderId, auth, n)
			if sends > maxSends {
				maxSends = sends
			}
		}
	}
	return maxSends + routingObservationAllowance(observedConnections), nil
}

// Bounds audited against refreshProviderCredential and its discovery branches.
func providerRefreshAllowance(provider string) int {
	switch provider {
	case "google-antigravity", "zed":
		return 8
	case "github-copilot", "google-vertex":
		return 4
	case "xai":
		return 3
	default:
		return 2
	}
}
func baseLaneBudget(provider string, auth inference.AuthMode, accounts int) int {
	if provider == "cursor" {
		return cursorPhysicalExchangeBudget
	}
	if auth != inference.OAuth {
		return 4
	}
	limit := 6
	if provider == "openai" {
		limit = 2
	}
	if provider == "anthropic" || provider == "anthropic2" {
		limit = 4
	}
	if accounts > limit {
		accounts = limit
	}
	attempts := 2
	if provider == "github-copilot" {
		attempts = 8
	}
	budget := accounts * (attempts + providerRefreshAllowance(provider))
	if budget < 4 {
		return 4
	}
	return budget
}
func (a *inferenceAdapter) helperRefreshAllowance(ctx context.Context, p inference.Principal, target *v1.HostedHelperTarget) (int, error) {
	if target == nil {
		return 0, nil
	}
	members, _, err := hostedMembers(ctx, a.s, scopeOf(p), target)
	if err != nil {
		return 0, inferenceError(err)
	}
	max := 0
	for _, member := range members {
		d, err := a.provider(ctx, scopeOf(p), member.ConnectionId)
		if errors.Is(err, inference.ErrNoCredential) || errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return 0, inferenceError(err)
		}
		if d.Metadata.AuthKind == v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH {
			if n := providerRefreshAllowance(d.Metadata.ProviderId); n > max {
				max = n
			}
		}
	}
	return max, nil
}
