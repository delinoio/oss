// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/core"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func readHostedSettings(ctx context.Context, s *store.Store, sc core.Scope) (*v1.HostedSettings, error) {
	out := &v1.HostedSettings{}
	b, e := s.GetPrivate(ctx, sc, "hosted-helpers", "settings")
	if errors.Is(e, store.ErrNotFound) {
		return out, nil
	}
	if e != nil {
		return nil, e
	}
	if e = protojson.Unmarshal(b, out); e != nil {
		return nil, e
	}
	return out, nil
}
func hostedMembers(ctx context.Context, s *store.Store, sc core.Scope, t *v1.HostedHelperTarget) ([]*v1.PoolMember, *v1.Pool, error) {
	if t == nil {
		return nil, nil, invalid("helper target required")
	}
	if id := t.GetConnectionId(); id != "" {
		return []*v1.PoolMember{{ConnectionId: id, Weight: 1}}, nil, nil
	}
	if id := t.GetPoolId(); id != "" {
		b, e := s.GetPrivate(ctx, sc, string(poolKind), id)
		if e != nil {
			return nil, nil, e
		}
		p := &v1.Pool{}
		if e = json.Unmarshal(b, p); e != nil {
			return nil, nil, e
		}
		if len(p.Members) == 0 {
			return nil, nil, invalid("empty helper pool")
		}
		return p.Members, p, nil
	}
	return nil, nil, invalid("helper selection required")
}
func hostedEligible(d providerData, backend string) bool {
	if d.Metadata == nil {
		return false
	}
	p := d.Metadata.ProviderId
	a := d.Metadata.AuthKind
	switch backend {
	case "openai":
		return p == "openai" && a == v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH
	case "anthropic":
		return (p == "anthropic" || p == "anthropic2") && a == v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH
	case "xai":
		return p == "xai" && a == v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH
	case "gemini":
		return p == "google-antigravity" && a == v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH && d.Credential != nil && d.Credential.ProjectID != ""
	case "exa":
		return p == "exa" && a == v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY
	case "devin":
		return p == "devin"
	case "media":
		return p == "xai" && a == v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY
	case "routed":
		return true
	}
	return false
}
func hostedSearchBackend(s *v1.HostedSearchSettings) string {
	if s == nil || s.Backend == nil {
		return "openai"
	}
	return strings.ToLower(strings.TrimPrefix(s.GetBackend().String(), "HOSTED_SEARCH_BACKEND_"))
}
func hostedVisionBackend(s *v1.HostedVisionSettings) string {
	if s == nil || s.Backend == nil {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(s.GetBackend().String(), "HOSTED_VISION_BACKEND_"))
}
func validateHostedSettings(ctx context.Context, s *store.Store, sc core.Scope, c *v1.HostedSettings) error {
	if c == nil {
		return invalid("hosted settings required")
	}
	check := func(t *v1.HostedHelperTarget, backend string) error {
		members, _, e := hostedMembers(ctx, s, sc, t)
		if e != nil {
			return e
		}
		if len(t.ModelId) > 512 || strings.TrimSpace(t.ModelId) != t.ModelId {
			return invalid("invalid helper model")
		}
		a := &inferenceAdapter{s: s}
		for _, m := range members {
			d, _, e := a.providerVersion(ctx, sc, m.ConnectionId)
			if e != nil {
				return e
			}
			if !hostedEligible(d, backend) {
				return invalid("helper credential is incompatible with backend")
			}
		}
		return nil
	}
	bounded := func(v *uint32, max uint32, zero bool) bool { return v == nil || *v <= max && (zero || *v > 0) }
	if x := c.Search; x != nil {
		backend := hostedSearchBackend(x)
		switch backend {
		case "openai", "anthropic", "xai", "gemini", "exa", "devin":
		default:
			return invalid("invalid search backend")
		}
		if !bounded(x.TimeoutMs, 600000, false) || !bounded(x.MaxSearchesPerTurn, 32, true) {
			return invalid("invalid search limits")
		}
		if x.Reasoning != nil && (x.GetReasoning() == v1.ReasoningEffort_REASONING_EFFORT_UNSPECIFIED || v1.ReasoningEffort_name[int32(x.GetReasoning())] == "") {
			return invalid("invalid helper reasoning")
		}
		if x.Target != nil {
			if e := check(x.Target, backend); e != nil {
				return e
			}
		} else if x.GetEnabled() {
			return invalid("search target required")
		}
		if o := x.XSearch; o != nil {
			if backend != "xai" || len(o.AllowedXHandles) > 20 || len(o.ExcludedXHandles) > 20 || len(o.AllowedXHandles) > 0 && len(o.ExcludedXHandles) > 0 {
				return invalid("invalid X search options")
			}
			for _, handles := range [][]string{o.AllowedXHandles, o.ExcludedXHandles} {
				for _, h := range handles {
					if strings.TrimSpace(h) != h || h == "" || len(h) > 256 {
						return invalid("invalid X handle")
					}
				}
			}
			for _, d := range []*string{o.FromDate, o.ToDate} {
				if d != nil {
					if _, e := time.Parse("2006-01-02", *d); e != nil {
						return invalid("invalid X search date")
					}
				}
			}
		}
	}
	if x := c.Vision; x != nil {
		backend := hostedVisionBackend(x)
		if !bounded(x.TimeoutMs, 2147483647, false) || !bounded(x.MaxDescriptionsPerTurn, 16, true) {
			return invalid("invalid vision limits")
		}
		if backend == "routed" {
			if x.GetRouteModel() == "" || x.GetTarget() != nil {
				return invalid("routed vision requires route model")
			}
			a := &inferenceAdapter{s: s}
			routes, e := a.routes(ctx, sc)
			if e != nil {
				return e
			}
			found := false
			for _, r := range routes {
				if routeAliasMatches(r.ModelId, x.GetRouteModel()) {
					found = true
				}
			}
			if !found {
				return invalid("vision route unavailable")
			}
		} else {
			if backend != "" && backend != "openai" && backend != "anthropic" || x.GetRouteModel() != "" {
				return invalid("invalid vision backend")
			}
			if x.GetTarget() != nil {
				if backend == "" {
					members, _, e := hostedMembers(ctx, s, sc, x.GetTarget())
					if e != nil {
						return e
					}
					a := &inferenceAdapter{s: s}
					d, _, e := a.providerVersion(ctx, sc, members[0].ConnectionId)
					if e != nil {
						return e
					}
					backend = "openai"
					if hostedEligible(d, "anthropic") {
						backend = "anthropic"
					}
				}
				if e := check(x.GetTarget(), backend); e != nil {
					return e
				}
			} else if x.GetEnabled() {
				return invalid("vision target required")
			}
		}
	}
	if x := c.Images; x != nil {
		if !bounded(x.TimeoutMs, 300000, false) || !bounded(x.VideoTimeoutMs, 300000, false) {
			return invalid("invalid media timeout")
		}
		for _, m := range []*string{x.BridgeModel, x.VideoBridgeModel} {
			if m != nil && (*m == "" || len(*m) > 512 || strings.TrimSpace(*m) != *m) {
				return invalid("invalid media model")
			}
		}
		if x.Target != nil {
			if e := check(x.Target, "media"); e != nil {
				return e
			}
		} else if x.BridgeEnabled || x.VideoBridgeEnabled {
			return invalid("media target required")
		}
	}
	return nil
}
