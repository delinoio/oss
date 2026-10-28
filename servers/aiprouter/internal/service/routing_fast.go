// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/inference/policy"
	"github.com/delinoio/oss/packages/aiprouter/providers"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

const fastRowSuffix = "--fast"

func fastTargetRequested(t inference.Target) bool { return t.Fast }

// Fast uses the final model wire and the selected account's resolved overrides.
// Metadata is a detached policy snapshot; no credentials enter this authority.
func resolvedFastPolicy(resolved *policy.Resolved) (inference.FastPolicy, *bool) {
	var fields struct {
		Capability *bool           `json:"supportsServiceTier"`
		Models     map[string]bool `json:"modelSupportsServiceTier"`
		Wire       json.RawMessage `json:"fastWire"`
	}
	if resolved == nil || json.Unmarshal(resolved.Metadata(), &fields) != nil {
		return inference.FastPolicy{}, nil
	}
	capability := fields.Capability
	if capability == nil || *capability {
		if value, ok := fields.Models[resolved.Model]; ok {
			capability = &value
		} else {
			// Exact spellings win. Sorting makes malformed duplicate folded keys
			// deterministic rather than dependent on Go map iteration.
			keys := make([]string, 0, len(fields.Models))
			for key := range fields.Models {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if strings.EqualFold(key, resolved.Model) {
					value := fields.Models[key]
					capability = &value
					break
				}
			}
		}
	}
	var declaration struct {
		Kind      inference.FastWireKind `json:"kind"`
		Canonical map[string]string      `json:"canonicalToWire"`
		Betas     []string               `json:"betas"`
	}
	if len(fields.Wire) == 0 {
		if resolved.Wire == providers.Chat || resolved.Wire == providers.Responses {
			declaration.Kind = inference.FastServiceTier
			declaration.Canonical = map[string]string{"priority": "priority"}
		}
	} else if json.Unmarshal(fields.Wire, &declaration) != nil {
		return inference.FastPolicy{}, capability
	}
	fast := inference.FastPolicy{Kind: declaration.Kind, Priority: declaration.Canonical["priority"], Betas: append([]string(nil), declaration.Betas...)}
	wireAvailable := false
	switch fast.Kind {
	case inference.FastServiceTier, inference.FastModelVariant:
		wireAvailable = resolved.Wire == providers.Chat || resolved.Wire == providers.Responses
	case inference.FastAnthropicSpeed:
		wireAvailable = resolved.Wire == providers.Anthropic
	case inference.FastCursorVariant:
		wireAvailable = resolved.Wire == providers.Wire("cursor")
	}
	fast.Eligible = capability != nil && *capability && wireAvailable && fast.Priority != ""
	if fast.Kind == inference.FastModelVariant {
		fast.Model = fast.Priority
	}
	// Pinned xai-fast-model.ts changes only serialization, never logical model
	// identity. A declared operator wire, including explicit null, defeats it.
	if fast.Eligible && len(fields.Wire) == 0 && resolved.ProviderID == "xai" && resolved.Auth == providers.OAuth && resolved.Model == "grok-4.7" {
		fast.Kind = inference.FastModelVariant
		fast.Model = "grok-4.7-build-fast"
	}
	return fast, capability
}

func resolvedFastProviderRoute(ctx context.Context, d providerData, model string) (providers.Route, *policy.Resolved, error) {
	route, resolved, err := resolvedProviderRoute(ctx, d, model)
	if err != nil {
		return route, resolved, err
	}
	fast, _ := resolvedFastPolicy(resolved)
	if !fast.Eligible {
		return route, resolved, &inference.Error{Status: 400, Code: "fast_not_supported"}
	}
	return route, resolved, nil
}

func applyFastTarget(ctx context.Context, d providerData, model string, target *inference.Target) error {
	route, resolved, err := resolvedFastProviderRoute(ctx, d, model)
	if err != nil {
		return err
	}
	fast, _ := resolvedFastPolicy(resolved)
	target.Fast, target.FastPolicy = true, &fast
	target.Model, target.BaseURL, target.Wire = route.Model, route.BaseURL, routeWire(route)
	target.Policy, target.Endpoint = resolved, resolved.Endpoint
	return nil
}

// Unknown custom capabilities stay absent. Native Codex discovery additionally
// requires fresh exact-account catalog tier evidence, as distinct from routing.
func fastModelEvidence(d providerData, model string, now time.Time) *bool {
	_, resolved, err := resolvedProviderRoute(context.Background(), d, model)
	if err != nil {
		return nil
	}
	fast, capability := resolvedFastPolicy(resolved)
	if capability == nil {
		return nil
	}
	available := fast.Eligible
	if available && d.Metadata.ProviderId == "openai" && d.Credential != nil {
		available = false
		fresh := !d.ModelsDiscoveredAt.IsZero() && !d.ModelsDiscoveredAt.After(now) && now.Sub(d.ModelsDiscoveredAt) <= 6*time.Hour
		if fresh {
			for _, raw := range d.CodexCatalog {
				var row struct {
					Slug  string   `json:"slug"`
					Tiers []string `json:"additional_speed_tiers"`
				}
				if json.Unmarshal(raw, &row) != nil || row.Slug != model {
					continue
				}
				for _, tier := range row.Tiers {
					if tier == "fast" {
						available = true
					}
				}
				break
			}
		}
	}
	return &available
}

// Reserve the entire explicit roster before adding siblings. Synthetic metadata
// cannot recursively advertise another sibling, including after client export.
func expandFastModelViews(views []routeModelView) []routeModelView {
	exact := make(map[string]bool, len(views))
	for _, view := range views {
		exact[view.ID] = true
	}
	out := make([]routeModelView, 0, len(views)*2)
	for _, view := range views {
		if exact[view.ID+fastRowSuffix] {
			view.Metadata = cloneFastMetadata(view.Metadata)
			view.Metadata["fast_row_available"] = false
		}
		out = append(out, view)
	}
	for _, view := range out[:len(views)] {
		if view.Metadata["fast_row_available"] != true {
			continue
		}
		view.ID += fastRowSuffix
		view.Name += " Fast"
		view.Metadata = cloneFastMetadata(view.Metadata)
		view.Metadata["fast_row_available"] = false
		// Native catalogs describe the base model. Do not fabricate a catalog row
		// by assigning an upstream base catalog to a synthetic service selector.
		view.Catalog = nil
		out = append(out, view)
	}
	return out
}

func cloneFastMetadata(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for key, value := range in {
		out[key] = value
	}
	return out
}

// Explicit routes, including every supported alias, reserve their selectors before
// synthetic rows. Matching the complete synthetic ID also supports desktop hashes
// without decoding or trusting a client-supplied provider namespace.
func chooseFastRoute(routes []*v1.Route, requested string) (*v1.Route, bool, error) {
	for _, synthetic := range []bool{false, true} {
		var chosen *v1.Route
		for _, route := range routes {
			if route == nil || !route.Enabled {
				continue
			}
			id := route.ModelId
			if synthetic {
				id += fastRowSuffix
			}
			if !routeAliasMatches(id, requested) {
				continue
			}
			if chosen != nil {
				return nil, false, &inference.Error{Status: 409, Code: "ambiguous_model_route"}
			}
			chosen = route
		}
		if chosen != nil {
			return chosen, synthetic, nil
		}
	}
	return nil, false, &inference.Error{Status: 404, Code: "model_not_found"}
}
