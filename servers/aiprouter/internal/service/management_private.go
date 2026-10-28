// SPDX-License-Identifier: Apache-2.0
package service

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"github.com/delinoio/oss/packages/aiprouter/providers"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	"google.golang.org/protobuf/proto"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type privateKind string

const (
	providerKind privateKind = "provider"
	poolKind     privateKind = "pool"
	routeKind    privateKind = "route"
)

// Connection metadata and the secret are encrypted together. Only Metadata is returned by RPCs.
type providerData struct {
	Metadata           *v1.ProviderConnection
	Secret             string
	Credential         *providers.Credential
	Models             *providers.ModelCatalog
	ModelsDiscoveredAt time.Time
	CodexCatalog       []json.RawMessage
	CustomHeaders      map[string]string
}

func (m *management) put(ctx context.Context, sc core.Scope, kind privateKind, id string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return m.store.PutPrivate(ctx, sc, string(kind), id, b)
}
func (m *management) get(ctx context.Context, sc core.Scope, kind privateKind, id string, v any) error {
	b, e := m.store.GetPrivate(ctx, sc, string(kind), id)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, v)
}
func validProviderURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Scheme == "https" || u.Scheme == "http")
}
func (m *management) ListProviderCatalog(ctx context.Context, r *connect.Request[v1.ListProviderCatalogRequest]) (*connect.Response[v1.ListProviderCatalogResponse], error) {
	out := &v1.ListProviderCatalogResponse{}
	for _, p := range providers.Catalog() {
		if p.Excluded || p.Deprecated != "" {
			continue
		}
		out.Providers = append(out.Providers, catalogMessage(p))
	}
	out.Providers = append(out.Providers, &v1.ProviderDefinition{Id: "custom", DisplayName: "Custom HTTP provider", AuthKinds: []v1.ProviderAuthKind{v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY}, ApiShapes: []v1.ApiShape{v1.ApiShape_API_SHAPE_CHAT_COMPLETIONS, v1.ApiShape_API_SHAPE_RESPONSES, v1.ApiShape_API_SHAPE_ANTHROPIC_MESSAGES}})
	return connect.NewResponse(out), nil
}

func (m *management) ListProviderConnections(ctx context.Context, r *connect.Request[v1.ListProviderConnectionsRequest]) (*connect.Response[v1.ListProviderConnectionsResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	ids, e := m.store.ListPrivate(ctx, sc, string(providerKind))
	if e != nil {
		return nil, rpcError(e)
	}
	out := &v1.ListProviderConnectionsResponse{}
	for _, id := range ids {
		var d providerData
		raw, _, readErr := m.store.GetPrivateVersionForManagement(ctx, sc, string(providerKind), id)
		if readErr != nil {
			return nil, rpcError(readErr)
		}
		if e = json.Unmarshal(raw, &d); e != nil {
			return nil, rpcError(e)
		}
		if _, _, usableErr := m.store.GetPrivateVersion(ctx, sc, string(providerKind), id); errors.Is(usableErr, store.ErrReauthentication) || errors.Is(usableErr, store.ErrBusy) {
			if d.Metadata != nil {
				d.Metadata.State = v1.ConnectionState_CONNECTION_STATE_AUTHORIZATION_REQUIRED
			}
		} else if usableErr != nil {
			return nil, rpcError(usableErr)
		}
		out.Connections = append(out.Connections, d.Metadata)
	}
	return connect.NewResponse(out), nil
}
func (m *management) CreateProviderConnection(ctx context.Context, r *connect.Request[v1.CreateProviderConnectionRequest]) (*connect.Response[v1.CreateProviderConnectionResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	q := r.Msg
	if q.BaseUrl == "" {
		if preset, ok := providers.Lookup(q.ProviderId); ok {
			q.BaseUrl = preset.BaseURL
		}
	}
	if strings.TrimSpace(q.Name) == "" || len(q.Name) > 200 || !validProviderURL(q.BaseUrl) {
		return nil, invalid("valid name and provider URL required")
	}
	if q.AuthKind != v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY && q.AuthKind != v1.ProviderAuthKind_PROVIDER_AUTH_KIND_NO_AUTH {
		return nil, invalid("use BeginProviderOAuth for OAuth connections")
	}

	d := providerData{Metadata: &v1.ProviderConnection{Id: core.ID(), OrganizationId: sc.OrganizationID, ProviderId: q.ProviderId, Name: q.Name, BaseUrl: q.BaseUrl, AuthKind: q.AuthKind, State: v1.ConnectionState_CONNECTION_STATE_READY, CreatedAt: timestamp(time.Now()), StallTimeoutSeconds: q.StallTimeoutSeconds}, Secret: q.GetApiKey()}
	if e = applyProviderConfiguration(&d, q.Configuration, false, q.CustomHeaders); e != nil {
		return nil, e
	}
	if e = providers.ValidateConnection(providerConnection(d)); e != nil {
		return nil, invalid("provider connection does not match its catalog requirements")
	}
	e = m.put(ctx, sc, providerKind, d.Metadata.Id, d)
	return connect.NewResponse(&v1.CreateProviderConnectionResponse{Connection: d.Metadata}), rpcError(e)
}
func (m *management) UpdateProviderConnection(ctx context.Context, r *connect.Request[v1.UpdateProviderConnectionRequest]) (*connect.Response[v1.UpdateProviderConnectionResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	q := r.Msg
	var d providerData
	raw, version, e := m.store.GetPrivateVersionForManagement(ctx, sc, string(providerKind), q.ConnectionId)
	if e != nil {
		return nil, rpcError(e)
	}
	if e = json.Unmarshal(raw, &d); e != nil || d.Metadata == nil {
		return nil, invalid("invalid stored connection")
	}
	if q.Name != nil {
		if q.GetName() == "" || len(q.GetName()) > 200 {
			return nil, invalid("invalid name")
		}
		d.Metadata.Name = q.GetName()
	}
	if q.BaseUrl != nil {
		if !validProviderURL(q.GetBaseUrl()) {
			return nil, invalid("invalid provider URL")
		}
		d.Metadata.BaseUrl = q.GetBaseUrl()
	}
	if q.ApiKey != nil {
		if d.Metadata.AuthKind != v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY {
			return nil, invalid("use provider login to replace OAuth credentials")
		}
		d.Secret = q.GetApiKey()
	}
	if q.Enabled != nil {
		d.Metadata.State = v1.ConnectionState_CONNECTION_STATE_READY
		if !q.GetEnabled() {
			d.Metadata.State = v1.ConnectionState_CONNECTION_STATE_DISABLED
		}
	}
	if q.ResetStallTimeout {
		d.Metadata.StallTimeoutSeconds = nil
	} else if q.StallTimeoutSeconds != nil {
		d.Metadata.StallTimeoutSeconds = q.StallTimeoutSeconds
	}
	if e = applyProviderConfiguration(&d, q.Configuration, q.ResetConfiguration, q.CustomHeaders); e != nil {
		return nil, e
	}
	if e = providers.ValidateConnection(providerConnection(d)); e != nil {
		return nil, invalid("provider connection does not match its catalog requirements")
	}
	if q.ApiKey != nil || q.BaseUrl != nil || q.Configuration != nil || q.ResetConfiguration || q.CustomHeaders != nil {
		d.Models = nil
		d.CodexCatalog = nil
		d.ModelsDiscoveredAt = time.Time{}
	}
	raw, e = json.Marshal(d)
	if e != nil {
		return nil, rpcError(e)
	}
	e = m.store.CompareAndSwapScopePrivate(ctx, sc, string(providerKind), q.ConnectionId, version, raw)
	return connect.NewResponse(&v1.UpdateProviderConnectionResponse{Connection: d.Metadata}), rpcError(e)
}
func (m *management) DeleteProviderConnection(ctx context.Context, r *connect.Request[v1.DeleteProviderConnectionRequest]) (*connect.Response[v1.DeleteProviderConnectionResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&v1.DeleteProviderConnectionResponse{}), rpcError(m.store.DeletePrivate(ctx, sc, string(providerKind), r.Msg.ConnectionId))
}
func (m *management) ListProviderModels(ctx context.Context, r *connect.Request[v1.ListProviderModelsRequest]) (*connect.Response[v1.ListProviderModelsResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	d, e := m.discoverProviderModels(ctx, sc, r.Msg.ConnectionId)
	if e != nil {
		return nil, providerRPCError(e)
	}
	out := &v1.ListProviderModelsResponse{}
	p, _ := providers.Lookup(d.Metadata.ProviderId)
	for _, model := range d.Models.Models {
		item := providerModel(p, model.ID)
		if model.Name != "" {
			item.DisplayName = model.Name
		}
		if model.ContextWindow > 0 {
			n := uint64(model.ContextWindow)
			item.ContextLimit = &n
		}
		if model.MaxOutputTokens > 0 {
			n := uint64(model.MaxOutputTokens)
			item.OutputLimit = &n
		}
		out.Models = append(out.Models, item)
	}
	return connect.NewResponse(out), nil
}

func (m *management) validatePool(ctx context.Context, sc core.Scope, p *v1.Pool) error {
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 200 || len(p.Members) == 0 || len(p.Members) > 100 {
		return invalid("pool name and 1 to 100 members required")
	}
	switch p.Strategy {
	case v1.PoolStrategy_POOL_STRATEGY_PRIORITY, v1.PoolStrategy_POOL_STRATEGY_ROUND_ROBIN, v1.PoolStrategy_POOL_STRATEGY_LEAST_RECENTLY_USED:
	default:
		return invalid("invalid pool strategy")
	}
	seen := map[string]bool{}
	nativeProvider := ""
	homogeneousNative := true
	hasNativeOptions := p.NativeOptions != nil
	for _, member := range p.Members {
		if member == nil || member.ConnectionId == "" || seen[member.ConnectionId] {
			return invalid("duplicate or missing pool member")
		}
		seen[member.ConnectionId] = true
		var d providerData
		if e := m.get(ctx, sc, providerKind, member.ConnectionId, &d); e != nil {
			return rpcError(e)
		}
		if d.Metadata == nil {
			return invalid("invalid pool connection")
		}
		id := d.Metadata.ProviderId
		if d.Metadata.AuthKind != v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH || (id != "openai" && id != "anthropic" && id != "anthropic2") {
			homogeneousNative = false
		}
		if nativeProvider == "" {
			nativeProvider = id
		} else if nativeProvider != id {
			homogeneousNative = false
		}
		hasNativeOptions = hasNativeOptions || member.NativeOptions != nil
	}
	if hasNativeOptions && !homogeneousNative {
		return invalid("native options require one native OAuth provider instance")
	}
	if hasNativeOptions {
		return m.validateNativePoolOptions(ctx, sc, p, nativeProvider)
	}
	return nil
}

var nativeRouteName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var nativeRouteMatch = regexp.MustCompile(`^[A-Za-z0-9_./:*?-]+$`)

func (m *management) validateNativePoolOptions(ctx context.Context, sc core.Scope, p *v1.Pool, provider string) error {
	codex := provider == "openai"
	for _, member := range p.Members {
		o := member.NativeOptions
		if o == nil {
			continue
		}
		if o.AutoSwitchThreshold != nil && *o.AutoSwitchThreshold > 100 {
			return invalid("native member threshold must be 0 to 100")
		}
		if o.Priority != nil && (*o.Priority < -100 || *o.Priority > 100) {
			return invalid("native priority must be -100 to 100")
		}
		if o.AllowCredits && !codex {
			return invalid("credits require Codex")
		}
	}
	o := p.NativeOptions
	if o == nil {
		return nil
	}
	switch o.Strategy {
	case v1.NativePoolStrategy_NATIVE_POOL_STRATEGY_UNSPECIFIED, v1.NativePoolStrategy_NATIVE_POOL_STRATEGY_QUOTA, v1.NativePoolStrategy_NATIVE_POOL_STRATEGY_ROUND_ROBIN, v1.NativePoolStrategy_NATIVE_POOL_STRATEGY_FILL_FIRST:
	case v1.NativePoolStrategy_NATIVE_POOL_STRATEGY_RESET_FIRST:
		if !codex {
			return invalid("reset-first requires Codex")
		}
	default:
		return invalid("invalid native pool strategy")
	}
	if o.StickyLimit != nil && (*o.StickyLimit < 1 || *o.StickyLimit > 100) {
		return invalid("native sticky limit must be 1 to 100")
	}
	if o.AutoSwitchThreshold != nil && *o.AutoSwitchThreshold > 100 {
		return invalid("native threshold must be 0 to 100")
	}
	switch o.QuotaWindow {
	case v1.NativeQuotaWindow_NATIVE_QUOTA_WINDOW_UNSPECIFIED:
	case v1.NativeQuotaWindow_NATIVE_QUOTA_WINDOW_FIVE_HOUR, v1.NativeQuotaWindow_NATIVE_QUOTA_WINDOW_WEEKLY, v1.NativeQuotaWindow_NATIVE_QUOTA_WINDOW_MAX_UTILIZATION:
		if codex {
			return invalid("quota window requires Anthropic")
		}
	default:
		return invalid("invalid native quota window")
	}
	if !codex && (o.CacheAffinity != nil || len(o.ExcludedPlans) != 0) {
		return invalid("cache affinity and excluded plans require Codex")
	}
	if codex && (o.NativeMessages != nil || len(o.Routes) != 0) {
		return invalid("native messages and model routes require Anthropic")
	}
	if !codex && (o.UpstreamFailoverThreshold != nil || o.FailureWindow != nil || o.PinnedTransientPolicy != nil) {
		return invalid("native health options require Codex")
	}
	if o.UpstreamFailoverThreshold != nil && *o.UpstreamFailoverThreshold > 20 {
		return invalid("upstream failover threshold must be 0 to 20")
	}
	switch o.GetPinnedTransientPolicy() {
	case v1.NativePinnedTransientPolicy_NATIVE_PINNED_TRANSIENT_POLICY_UNSPECIFIED, v1.NativePinnedTransientPolicy_NATIVE_PINNED_TRANSIENT_POLICY_KEEP, v1.NativePinnedTransientPolicy_NATIVE_PINNED_TRANSIENT_POLICY_DETOUR_NEW_THREADS:
	default:
		return invalid("invalid pinned transient policy")
	}
	if len(o.ExcludedPlans) > 100 {
		return invalid("too many excluded plans")
	}
	plans := map[string]bool{}
	for _, plan := range o.ExcludedPlans {
		key := strings.ToLower(plan)
		if !validConfiguredModel(plan) || len(plan) > 128 || plans[key] {
			return invalid("invalid or duplicate excluded plan")
		}
		plans[key] = true
	}
	if len(o.Routes) > 32 {
		return invalid("at most 32 native model routes allowed")
	}
	names, patterns := map[string]bool{}, map[string]bool{}
	for _, route := range o.Routes {
		if route == nil || !nativeRouteName.MatchString(route.Name) || names[route.Name] || len(route.Match) > 128 || !nativeRouteMatch.MatchString(route.Match) || patterns[route.Match] {
			return invalid("invalid or duplicate native model route")
		}
		names[route.Name], patterns[route.Match] = true, true
		if len(route.ConnectionIds) < 1 || len(route.ConnectionIds) > 32 {
			return invalid("native model route requires 1 to 32 connections")
		}
		seen := map[string]bool{}
		for _, id := range route.ConnectionIds {
			if !validConfiguredModel(id) || len(id) > 128 || seen[id] {
				return invalid("native route connection must be a unique stored ID")
			}
			seen[id] = true
			// A stored owned connection can remain dormant outside the roster;
			// runtime intersects the ordered allowlist with eligible members.
			var d providerData
			if err := m.get(ctx, sc, providerKind, id, &d); err != nil {
				return rpcError(err)
			}
			if d.Metadata == nil || d.Metadata.ProviderId != provider || d.Metadata.AuthKind != v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH {
				return invalid("native route connection must use the same provider instance")
			}
		}
	}
	return nil
}
func (m *management) ListPools(ctx context.Context, r *connect.Request[v1.ListPoolsRequest]) (*connect.Response[v1.ListPoolsResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	ids, e := m.store.ListPrivate(ctx, sc, string(poolKind))
	if e != nil {
		return nil, rpcError(e)
	}
	out := &v1.ListPoolsResponse{}
	for _, id := range ids {
		p := &v1.Pool{}
		if e = m.get(ctx, sc, poolKind, id, p); e != nil {
			return nil, rpcError(e)
		}
		out.Pools = append(out.Pools, p)
	}
	return connect.NewResponse(out), nil
}
func (m *management) CreatePool(ctx context.Context, r *connect.Request[v1.CreatePoolRequest]) (*connect.Response[v1.CreatePoolResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	p := &v1.Pool{Id: core.ID(), OrganizationId: sc.OrganizationID, Name: r.Msg.Name, Strategy: r.Msg.Strategy, Members: r.Msg.Members, AffinityEnabled: r.Msg.GetAffinityEnabled(), NativeOptions: r.Msg.NativeOptions}
	if e = m.validatePool(ctx, sc, p); e != nil {
		return nil, e
	}
	// Creating a personal native pool is the explicit proactive opt-in. Importers
	// preserve their source default separately; updates remain full replacements.
	nativeProvider := ""
	native := true
	for _, member := range p.Members {
		var d providerData
		if e = m.get(ctx, sc, providerKind, member.ConnectionId, &d); e != nil {
			return nil, rpcError(e)
		}
		if d.Metadata == nil {
			native = false
			break
		}
		id := d.Metadata.ProviderId
		if d.Metadata.AuthKind != v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH || (id != "openai" && id != "anthropic" && id != "anthropic2") || (nativeProvider != "" && nativeProvider != id) {
			native = false
			break
		}
		nativeProvider = id
	}
	if native {
		if p.NativeOptions == nil {
			p.NativeOptions = &v1.NativePoolOptions{}
		} else {
			p.NativeOptions = proto.Clone(p.NativeOptions).(*v1.NativePoolOptions)
		}
		if p.NativeOptions.Enabled == nil {
			enabled := true
			p.NativeOptions.Enabled = &enabled
		}
	}
	return connect.NewResponse(&v1.CreatePoolResponse{Pool: p}), rpcError(m.put(ctx, sc, poolKind, p.Id, p))
}
func (m *management) UpdatePool(ctx context.Context, r *connect.Request[v1.UpdatePoolRequest]) (*connect.Response[v1.UpdatePoolResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	p := &v1.Pool{}
	raw, version, e := m.store.GetPrivateVersion(ctx, sc, string(poolKind), r.Msg.PoolId)
	if e != nil {
		return nil, rpcError(e)
	}
	if e = json.Unmarshal(raw, p); e != nil {
		return nil, rpcError(e)
	}
	p.Name = r.Msg.Name
	p.Strategy = r.Msg.Strategy
	p.Members = r.Msg.Members
	p.AffinityEnabled = r.Msg.GetAffinityEnabled()
	p.NativeOptions = r.Msg.NativeOptions
	if e = m.validatePool(ctx, sc, p); e != nil {
		return nil, e
	}
	raw, e = json.Marshal(p)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&v1.UpdatePoolResponse{Pool: p}), rpcError(m.store.CompareAndSwapScopePrivate(ctx, sc, string(poolKind), p.Id, version, raw))
}
func (m *management) DeletePool(ctx context.Context, r *connect.Request[v1.DeletePoolRequest]) (*connect.Response[v1.DeletePoolResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&v1.DeletePoolResponse{}), rpcError(m.store.DeletePrivate(ctx, sc, string(poolKind), r.Msg.PoolId))
}

// Route uses a Protobuf oneof; encode with proto rather than encoding/json.
func (m *management) putRoute(ctx context.Context, sc core.Scope, p *v1.Route) error {
	b, e := proto.Marshal(p)
	if e != nil {
		return e
	}
	return m.store.PutPrivate(ctx, sc, string(routeKind), p.Id, b)
}
func (m *management) getRoute(ctx context.Context, sc core.Scope, id string) (*v1.Route, error) {
	b, e := m.store.GetPrivate(ctx, sc, string(routeKind), id)
	if e != nil {
		return nil, e
	}
	p := &v1.Route{}
	e = proto.Unmarshal(b, p)
	return p, e
}
func (m *management) validateRoute(ctx context.Context, sc core.Scope, p *v1.Route) error {
	if err := validateReasoningRetention(p.ReasoningRetention); err != nil {
		return err
	}
	if strings.TrimSpace(p.ModelId) == "" || len(p.ModelId) > 512 || len(p.Targets) == 0 || len(p.Targets) > 100 {
		return invalid("model and 1 to 100 targets required")
	}
	switch p.Strategy {
	case v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, v1.RoutingStrategy_ROUTING_STRATEGY_FALLBACK, v1.RoutingStrategy_ROUTING_STRATEGY_WEIGHTED, v1.RoutingStrategy_ROUTING_STRATEGY_ROUND_ROBIN, v1.RoutingStrategy_ROUTING_STRATEGY_JEV:
	default:
		return invalid("invalid routing strategy")
	}
	if p.Strategy == v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT && len(p.Targets) != 1 {
		return invalid("direct route requires one target")
	}
	seen := map[string]bool{}
	for _, target := range p.Targets {
		if target == nil {
			return invalid("target required")
		}
		if len([]rune(target.ModelProfile)) > 512 {
			return invalid("model profile exceeds 512 characters")
		}
		seenEfforts := map[string]bool{}
		for _, effort := range target.ReasoningEfforts {
			switch effort {
			case "low", "medium", "high", "xhigh", "max", "ultra":
			default:
				return invalid("invalid reasoning effort")
			}
			if seenEfforts[effort] {
				return invalid("duplicate reasoning effort")
			}
			seenEfforts[effort] = true
		}
		if len(target.ModelId) > 512 {
			return invalid("invalid target model")
		}
		if p.Strategy == v1.RoutingStrategy_ROUTING_STRATEGY_WEIGHTED && (target.Weight == 0 || target.Weight > 1000000) {
			return invalid("weighted targets require weights from 1 to 1000000")
		}
		key := target.GetConnectionId() + "/" + target.GetPoolId() + "/" + target.ModelId
		if seen[key] {
			return invalid("duplicate target")
		}
		seen[key] = true
		if target.GetConnectionId() != "" {
			var d providerData
			if e := m.get(ctx, sc, providerKind, target.GetConnectionId(), &d); e != nil {
				return rpcError(e)
			}
		} else if target.GetPoolId() != "" {
			var pool v1.Pool
			if e := m.get(ctx, sc, poolKind, target.GetPoolId(), &pool); e != nil {
				return rpcError(e)
			}
		} else {
			return invalid("connection or pool required")
		}
	}
	return validateJEVRoute(ctx, m.store, sc, p)
}
func (m *management) ListRoutes(ctx context.Context, r *connect.Request[v1.ListRoutesRequest]) (*connect.Response[v1.ListRoutesResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	ids, e := m.store.ListPrivate(ctx, sc, string(routeKind))
	if e != nil {
		return nil, rpcError(e)
	}
	out := &v1.ListRoutesResponse{}
	for _, id := range ids {
		p, e := m.getRoute(ctx, sc, id)
		if e != nil {
			return nil, rpcError(e)
		}
		out.Routes = append(out.Routes, p)
	}
	return connect.NewResponse(out), nil
}
func (m *management) CreateRoute(ctx context.Context, r *connect.Request[v1.CreateRouteRequest]) (*connect.Response[v1.CreateRouteResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	p := &v1.Route{Id: core.ID(), OrganizationId: sc.OrganizationID, ModelId: r.Msg.ModelId, Strategy: r.Msg.Strategy, Targets: r.Msg.Targets, Enabled: r.Msg.Enabled, JevDecision: r.Msg.JevDecision, ReasoningRetention: r.Msg.ReasoningRetention}
	if e = m.validateRoute(ctx, sc, p); e != nil {
		return nil, e
	}
	return connect.NewResponse(&v1.CreateRouteResponse{Route: p}), rpcError(m.putRoute(ctx, sc, p))
}
func (m *management) UpdateRoute(ctx context.Context, r *connect.Request[v1.UpdateRouteRequest]) (*connect.Response[v1.UpdateRouteResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	raw, version, e := m.store.GetPrivateVersion(ctx, sc, string(routeKind), r.Msg.RouteId)
	if e != nil {
		return nil, rpcError(e)
	}
	p := &v1.Route{}
	if e = proto.Unmarshal(raw, p); e != nil {
		return nil, rpcError(e)
	}
	p.ModelId = r.Msg.ModelId
	p.Strategy = r.Msg.Strategy
	p.Targets = r.Msg.Targets
	p.Enabled = r.Msg.Enabled
	p.JevDecision = r.Msg.JevDecision
	p.ReasoningRetention = r.Msg.ReasoningRetention
	if e = m.validateRoute(ctx, sc, p); e != nil {
		return nil, e
	}
	raw, e = proto.Marshal(p)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&v1.UpdateRouteResponse{Route: p}), rpcError(m.store.CompareAndSwapScopePrivate(ctx, sc, string(routeKind), p.Id, version, raw))
}
func (m *management) DeleteRoute(ctx context.Context, r *connect.Request[v1.DeleteRouteRequest]) (*connect.Response[v1.DeleteRouteResponse], error) {
	sc, e := m.scope(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&v1.DeleteRouteResponse{}), rpcError(m.store.DeletePrivate(ctx, sc, string(routeKind), r.Msg.RouteId))
}
