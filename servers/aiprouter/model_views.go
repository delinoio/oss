// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/core"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/providers"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	"google.golang.org/protobuf/proto"
)

type routeModelView struct {
	ID, Name            string
	Context, Output     int
	Input               int
	Metadata            map[string]any
	Modalities, Efforts []string
	Catalog             json.RawMessage
}

func (a *inferenceAdapter) routes(ctx context.Context, sc core.Scope) ([]*v1.Route, error) {
	ids, err := a.s.ListPrivate(ctx, sc, string(routeKind))
	if err != nil {
		return nil, inferenceError(err)
	}
	out := make([]*v1.Route, 0, len(ids))
	for _, id := range ids {
		b, err := a.s.GetPrivate(ctx, sc, string(routeKind), id)
		if err != nil {
			return nil, inferenceError(err)
		}
		r := new(v1.Route)
		if proto.Unmarshal(b, r) != nil {
			return nil, inference.ErrUnavailable
		}
		if r.Enabled {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModelId < out[j].ModelId })
	return out, nil
}
func (a *inferenceAdapter) targetMembers(ctx context.Context, sc core.Scope, t *v1.RouteTarget) ([]*v1.PoolMember, *v1.Pool, error) {
	if t.GetPoolId() == "" {
		return []*v1.PoolMember{{ConnectionId: t.GetConnectionId(), Weight: 1}}, nil, nil
	}
	b, e := a.s.GetPrivate(ctx, sc, string(poolKind), t.GetPoolId())
	if e != nil {
		return nil, nil, inferenceError(e)
	}
	pool := new(v1.Pool)
	if json.Unmarshal(b, pool) != nil {
		return nil, nil, inference.ErrUnavailable
	}
	return pool.Members, pool, nil
}

// Discovery is evidence for this account, not a global grant for its provider.
// The six-hour bound matches the pinned account roster lifetime.
func modelEvidence(d providerData, model string, now time.Time) (routeModelView, bool) {
	out := routeModelView{Name: model}
	_, known := providers.Lookup(d.Metadata.ProviderId)
	gated := d.Metadata.ProviderId == "openai" && d.Credential != nil && (model == "gpt-daybreak-blue-latest" || model == "gpt-6-astra-minor")
	fresh := d.Models != nil && !d.ModelsDiscoveredAt.IsZero() && !d.ModelsDiscoveredAt.After(now) && now.Sub(d.ModelsDiscoveredAt) <= 6*time.Hour
	found := false
	if fresh && d.Models.State == providers.ValidationAccepted {
		for _, m := range d.Models.Models {
			if m.ID == model {
				out.Name = m.Name
				out.Context = m.ContextWindow
				out.Output = m.MaxOutputTokens
				out.Modalities = m.InputModalities
				out.Efforts = m.ReasoningEfforts
				found = true
				break
			}
		}
	}
	if gated && !found {
		return out, false
	}
	// Subscription rosters are authoritative when discovery is supported. Flagship
	// Codex rows deliberately remain routable without a positive entitlement roster.
	requiresRoster := d.Metadata.ProviderId == "cursor" || d.Metadata.ProviderId == "google-antigravity" || d.Metadata.ProviderId == "zed"
	if d.Credential != nil && d.Metadata.ProviderId != "openai" && (requiresRoster && (!fresh || !found) || fresh && d.Models.State == providers.ValidationAccepted && !found) {
		return out, false
	}
	if known {
		if _, resolved, err := resolvedProviderRoute(context.Background(), d, model); err == nil {
			m := resolved.ModelPolicy()
			if out.Context == 0 && m.ContextWindow != nil {
				out.Context = int(*m.ContextWindow)
			}
			if out.Output == 0 && m.MaxOutputTokens != nil {
				out.Output = int(*m.MaxOutputTokens)
			}
			if m.MaxInputTokens != nil {
				out.Input = int(*m.MaxInputTokens)
			}
			if out.Modalities == nil {
				out.Modalities = m.InputModalities
			}
			if out.Efforts == nil {
				out.Efforts = m.ReasoningEfforts
			}
			out.Metadata = map[string]any{}
			for key, value := range map[string]*bool{"supports_reasoning": m.SupportsReasoning, "supports_thinking_toggle": m.SupportsThinkingToggle, "supports_reasoning_effort": m.SupportsReasoningEffort, "default_thinking_enabled": m.DefaultThinkingEnabled, "supports_reasoning_summaries": m.SupportsReasoningSummaries, "supports_tools": m.SupportsTools} {
				if value != nil {
					out.Metadata[key] = *value
				}
			}
			if m.ReasoningInterleaving != nil {
				out.Metadata["reasoning_interleaving"] = string(*m.ReasoningInterleaving)
			}
			if m.SupportsReasoningEffort != nil && !*m.SupportsReasoningEffort || m.SupportsReasoning != nil && !*m.SupportsReasoning {
				if out.Efforts != nil {
					out.Efforts = []string{}
				}
			}
			for _, effort := range out.Efforts {
				if effort == m.DefaultReasoningEffort && effort != "" {
					out.Metadata["default_reasoning_effort"] = effort
				}
			}
		}
	}
	if fast := fastModelEvidence(d, model, now); fast != nil {
		if out.Metadata == nil {
			out.Metadata = map[string]any{}
		}
		out.Metadata["fast_row_available"] = *fast
	}
	if out.Name == "" {
		out.Name = model
	}
	if fresh {
		for _, raw := range d.CodexCatalog {
			var row struct {
				Slug string `json:"slug"`
			}
			if json.Unmarshal(raw, &row) == nil && row.Slug == model {
				out.Catalog = append(json.RawMessage(nil), raw...)
				break
			}
		}
	}
	return out, true
}

func (a *inferenceAdapter) modelViews(ctx context.Context, sc core.Scope) ([]routeModelView, error) {
	routes, e := a.routes(ctx, sc)
	if e != nil {
		return nil, e
	}
	out := []routeModelView{}
	seen := map[string]bool{}
	for _, r := range routes {
		if seen[r.ModelId] {
			return nil, &inference.Error{Status: 409, Code: "ambiguous_model_route"}
		}
		seen[r.ModelId] = true
		var view *routeModelView
		for _, target := range r.Targets {
			members, _, e := a.targetMembers(ctx, sc, target)
			if e != nil {
				return nil, e
			}
			model := target.ModelId
			if model == "" {
				model = r.ModelId
			}
			for _, member := range members {
				d, e := a.provider(ctx, sc, member.ConnectionId)
				if errors.Is(e, inference.ErrNoCredential) || errors.Is(e, store.ErrNotFound) {
					continue
				}
				if e != nil {
					return nil, inferenceError(e)
				}
				if _, e = providers.Resolve(providerConnection(d), model, providers.InboundChat); e != nil {
					continue
				}
				row, eligible := modelEvidence(d, model, time.Now())
				if !eligible {
					continue
				}
				until, e := a.s.AccountCooldown(ctx, sc, member.ConnectionId, model)
				if e != nil {
					return nil, inferenceError(e)
				}
				if until != nil && until.After(time.Now()) {
					continue
				}
				row.ID = r.ModelId
				if view == nil {
					view = &row
				} else {
					mergeModelEvidence(view, row)
				}
			}
		}
		if view != nil {
			out = append(out, *view)
		}
	}
	return expandFastModelViews(out), nil
}

// A route can use multiple accounts or models. Publish only their shared facts;
// metadata from the first account must not overstate another target's ability.
func mergeModelEvidence(a *routeModelView, b routeModelView) {
	if a.Input == 0 || b.Input == 0 {
		a.Input = 0
	} else if b.Input < a.Input {
		a.Input = b.Input
	}
	for k, v := range a.Metadata {
		if other, ok := b.Metadata[k]; !ok || other != v {
			delete(a.Metadata, k)
		}
	}

	if a.Context == 0 || b.Context == 0 {
		a.Context = 0
	} else if b.Context < a.Context {
		a.Context = b.Context
	}
	if a.Output == 0 || b.Output == 0 {
		a.Output = 0
	} else if b.Output < a.Output {
		a.Output = b.Output
	}
	a.Modalities = intersectEvidence(a.Modalities, b.Modalities)
	a.Efforts = intersectEvidence(a.Efforts, b.Efforts)
	if string(a.Catalog) != string(b.Catalog) {
		a.Catalog = nil
	}
	if a.Name != b.Name {
		a.Name = a.ID
	}
}
func intersectEvidence(a, b []string) []string {
	if a == nil || b == nil {
		return nil
	}
	out := []string{}
	for _, x := range a {
		for _, y := range b {
			if x == y {
				out = append(out, x)
				break
			}
		}
	}
	return out
}
func (a *inferenceAdapter) models(ctx context.Context, sc core.Scope) ([]inference.Model, error) {
	views, e := a.modelViews(ctx, sc)
	if e != nil {
		return nil, e
	}
	out := make([]inference.Model, 0, len(views))
	for _, v := range views {
		out = append(out, inference.Model{ID: v.ID, Object: "model", OwnedBy: "aiprouter"})
	}
	return out, nil
}
func (a *inferenceAdapter) Models(ctx context.Context, p inference.Principal) ([]inference.Model, error) {
	if e := a.BeforeSend(ctx, p); e != nil {
		return nil, e
	}
	out, e := a.models(ctx, scopeOf(p))
	if e != nil {
		return nil, e
	}
	if e = a.BeforeSend(ctx, p); e != nil {
		return nil, e
	}
	return out, nil
}

// Use the pinned reversible alias codec with the service's explicit route
// namespace. The route remains the only authority; aliases create no grants.
func cliRouteAlias(id string) string {
	if strings.ContainsAny(id, "/~") {
		return "ocx-claude2-aiprouter--" + strings.ReplaceAll(strings.ReplaceAll(id, "~", "~t"), "/", "~s")
	}
	return "ocx-claude-aiprouter--" + id
}
func desktopRouteAlias(id string) string {
	sum := sha256.Sum256([]byte("aiprouter/" + id))
	n := binary.BigEndian.Uint32(sum[:4]) % 33696
	tail := strconv.FormatUint(uint64(n%1296), 36)
	if len(tail) < 2 {
		tail = "0" + tail
	}
	return "claude-opus-4-8-" + string(rune('a'+n/1296)) + tail
}
func routeAliasMatches(id, requested string) bool {
	return id == requested || cliRouteAlias(id) == requested || desktopRouteAlias(id) == requested
}

func (a *inferenceAdapter) Read(ctx context.Context, p inference.Principal, op inference.Operation, q url.Values) (json.RawMessage, error) {
	if e := a.BeforeSend(ctx, p); e != nil {
		return nil, e
	}
	if op != inference.Catalog && op != inference.Operation("models") {
		return nil, inference.ErrUnsupported
	}
	for key, values := range q {
		if len(values) != 1 || (key != "ids" && key != "format" && key != "client_version") {
			return nil, &inference.Error{Status: 400, Code: "invalid_model_query"}
		}
	}
	ids, format := q.Get("ids"), q.Get("format")
	if ids != "" && ids != "cli" && ids != "desktop" || format != "" && format != "desktop-config" || format == "desktop-config" && (ids == "cli" || q.Has("client_version")) {
		return nil, &inference.Error{Status: 400, Code: "invalid_model_query"}
	}
	if q.Has("client_version") && !validClientVersion(q.Get("client_version")) {
		return nil, &inference.Error{Status: 400, Code: "invalid_model_query"}
	}
	views, e := a.modelViews(ctx, scopeOf(p))
	if e != nil {
		return nil, e
	}
	var result any
	switch {
	case op == inference.Catalog || q.Has("client_version"):
		rows := []json.RawMessage{}
		for _, v := range views {
			if v.Catalog == nil {
				continue
			}
			var row map[string]json.RawMessage
			if json.Unmarshal(v.Catalog, &row) != nil {
				return nil, inference.ErrUnavailable
			}
			if q.Has("client_version") {
				var minimum string
				_ = json.Unmarshal(row["minimal_client_version"], &minimum)
				if minimum != "" && !clientVersionAtLeast(q.Get("client_version"), minimum) {
					continue
				}
			}
			for field := range row {
				if !codexPublicFields[field] {
					delete(row, field)
				}
			}
			row["slug"], _ = json.Marshal(v.ID)
			raw, e := json.Marshal(row)
			if e != nil {
				return nil, inference.ErrUnavailable
			}
			rows = append(rows, raw)
		}
		if len(rows) == 0 {
			return nil, &inference.Error{Status: 404, Code: "catalog_not_found"}
		}
		result = struct {
			Models []json.RawMessage `json:"models"`
		}{rows}
	case format == "desktop-config":
		rows := []map[string]any{}
		aliases := map[string]bool{}
		for _, v := range views {
			alias := desktopRouteAlias(v.ID)
			if aliases[alias] {
				return nil, &inference.Error{Status: 409, Code: "ambiguous_model_alias"}
			}
			aliases[alias] = true
			row := map[string]any{"name": alias, "labelOverride": v.Name, "anthropicFamilyTier": "opus"}
			if v.Context >= 1_000_000 {
				row["supports1m"] = true
			}
			rows = append(rows, row)
		}
		result = map[string]any{"version": 1, "models": rows}
	default:
		rows := []map[string]any{}
		aliases := map[string]bool{}
		for _, v := range views {
			id := v.ID
			if ids == "cli" {
				id = cliRouteAlias(id)
			} else if ids == "desktop" {
				id = desktopRouteAlias(id)
			}
			if aliases[id] {
				return nil, &inference.Error{Status: 409, Code: "ambiguous_model_alias"}
			}
			aliases[id] = true
			row := map[string]any{"id": id, "display_name": v.Name, "object": "model", "owned_by": "aiprouter"}
			if ids != "" {
				row["capabilities"] = anthropicViewCapabilities(v)
				row["type"] = "model"
				row["created_at"] = "2026-01-01T00:00:00Z"
				row["max_input_tokens"] = nil
				row["max_tokens"] = nil
				if v.Input > 0 {
					row["max_input_tokens"] = v.Input
				}
			}
			for k, value := range v.Metadata {
				row[k] = value
			}
			if v.Input > 0 {
				row["max_input_tokens"] = v.Input
			}
			if v.Efforts != nil {
				row["reasoning_efforts_known"] = true
			}
			if v.Context > 0 {
				row["context_window"] = v.Context
			}
			if v.Output > 0 {
				row["max_output_tokens"] = v.Output
			}
			if v.Modalities != nil {
				row["input_modalities"] = v.Modalities
			}
			if v.Efforts != nil {
				row["reasoning_efforts"] = v.Efforts
			}
			rows = append(rows, row)
		}
		result = map[string]any{"object": "list", "data": rows}
	}
	b, e := json.Marshal(result)
	if e != nil {
		return nil, inference.ErrUnavailable
	}
	if len(b) > 256<<20 {
		return nil, &inference.Error{Status: 507, Code: "catalog_too_large"}
	}
	if e = a.BeforeSend(ctx, p); e != nil {
		return nil, e
	}
	return b, nil
}

var _ inference.ClientViews = (*inferenceAdapter)(nil)

// Keep the pinned Codex model schema, including instructions and tool metadata,
// while withholding unexpected account/private fields from discovery envelopes.
var codexPublicFields = func() map[string]bool {
	out := map[string]bool{}
	for _, key := range strings.Fields(`slug display_name description priority visibility base_instructions model_messages context_window max_context_window auto_compact_token_limit comp_hash default_reasoning_level supported_reasoning_levels default_reasoning_summary supports_reasoning_summaries supports_reasoning_summary_parameter supports_reasoning_effort_updates support_verbosity default_verbosity input_modalities supports_image_detail_original supports_parallel_tool_calls supports_search_tool web_search_tool_type tool_mode experimental_supported_tools apply_patch_tool_type shell_type truncation_policy use_responses_lite supported_in_api prefer_websockets supports_websockets minimal_client_version available_in_plans available_access_programs additional_speed_tiers default_service_tier service_tiers upgrade availability_nux auto_review_model_override guardian model_specialty multi_agent_reasoning_effort multi_agent_version node_repl_auto_review_required node_repl_disabled requires_sandboxed_review include_apps_usage_instructions include_plugin_usage_instructions include_skills_usage_instructions supports_experimental_context`) {
		out[key] = true
	}
	return out
}()

func anthropicViewCapabilities(v routeModelView) map[string]any {
	out := map[string]any{}
	if v.Modalities != nil {
		image := false
		for _, m := range v.Modalities {
			if m == "image" {
				image = true
			}
		}
		out["image_input"] = map[string]bool{"supported": image}
	}
	if v.Efforts != nil {
		rungs := map[string]bool{}
		for _, r := range v.Efforts {
			switch r {
			case "low", "medium", "high", "xhigh", "max":
				rungs[r] = true
			}
		}
		effort := map[string]any{"supported": len(rungs) > 0}
		for _, r := range []string{"low", "medium", "high", "max", "xhigh"} {
			effort[r] = map[string]bool{"supported": rungs[r]}
		}
		if len(rungs) == 0 {
			effort["xhigh"] = nil
		}
		out["effort"] = effort
	}
	return out
}

var clientVersionPattern = regexp.MustCompile(`^\d+(?:\.\d+)*(?:[-+][0-9A-Za-z.-]+)?$`)

func validClientVersion(v string) bool {
	if len(v) == 0 || len(v) > 64 || !clientVersionPattern.MatchString(v) {
		return false
	}
	core := strings.FieldsFunc(v, func(r rune) bool { return r == '-' || r == '+' })[0]
	nonzero := false
	for _, part := range strings.Split(core, ".") {
		n, e := strconv.ParseUint(part, 10, 32)
		if e != nil {
			return false
		}
		nonzero = nonzero || n > 0
	}
	return nonzero
}
func clientVersionAtLeast(value, minimum string) bool {
	if !validClientVersion(value) || !validClientVersion(minimum) {
		return false
	}
	parts := func(v string) []string {
		return strings.Split(strings.FieldsFunc(v, func(r rune) bool { return r == '-' || r == '+' })[0], ".")
	}
	a, b := parts(value), parts(minimum)
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y uint64
		if i < len(a) {
			x, _ = strconv.ParseUint(a[i], 10, 32)
		}
		if i < len(b) {
			y, _ = strconv.ParseUint(b[i], 10, 32)
		}
		if x != y {
			return x > y
		}
	}
	return true
}

// clientModels shares the same current-account evidence as the HTTP model views.
func (a *inferenceAdapter) clientModels(ctx context.Context, sc core.Scope) ([]*v1.Model, error) {
	views, err := a.modelViews(ctx, sc)
	if err != nil {
		return nil, err
	}
	out := make([]*v1.Model, 0, len(views))
	for _, view := range views {
		m := &v1.Model{Id: view.ID, DisplayName: view.Name}
		if view.Context > 0 {
			n := uint64(view.Context)
			m.ContextLimit = &n
		}
		if view.Output > 0 {
			n := uint64(view.Output)
			m.OutputLimit = &n
		}
		if view.Input > 0 {
			n := uint64(view.Input)
			m.MaxInputTokens = &n
		}
		for _, effort := range view.Efforts {
			if n, ok := v1.ReasoningEffort_value["REASONING_EFFORT_"+strings.ToUpper(effort)]; ok && n != 0 {
				m.ReasoningEfforts = append(m.ReasoningEfforts, v1.ReasoningEffort(n))
			}
		}
		if view.Efforts != nil {
			known := true
			m.ReasoningEffortsKnown = &known
		}

		for _, modality := range view.Modalities {
			switch modality {
			case "text":
				m.Capabilities = append(m.Capabilities, v1.ModelCapability_MODEL_CAPABILITY_TEXT)
			case "image":
				m.Capabilities = append(m.Capabilities, v1.ModelCapability_MODEL_CAPABILITY_IMAGE_INPUT)
			case "audio":
				m.Capabilities = append(m.Capabilities, v1.ModelCapability_MODEL_CAPABILITY_AUDIO_INPUT)
			case "video":
				m.Capabilities = append(m.Capabilities, v1.ModelCapability_MODEL_CAPABILITY_VIDEO_INPUT)
			}
		}

		for key, dest := range map[string]**bool{"supports_reasoning": &m.SupportsReasoning, "supports_reasoning_effort": &m.SupportsReasoningEffort, "supports_thinking_toggle": &m.SupportsThinkingToggle, "default_thinking_enabled": &m.DefaultThinkingEnabled, "supports_reasoning_summaries": &m.SupportsReasoningSummaries, "supports_tools": &m.SupportsTools, "fast_row_available": &m.FastRowAvailable} {
			if value, ok := view.Metadata[key].(bool); ok {
				v := value
				*dest = &v
			}
		}
		if value, ok := view.Metadata["reasoning_interleaving"].(string); ok {
			var interleaving v1.ReasoningInterleaving
			switch value {
			case "none":
				interleaving = v1.ReasoningInterleaving_REASONING_INTERLEAVING_NONE
			case "reasoning_content":
				interleaving = v1.ReasoningInterleaving_REASONING_INTERLEAVING_REASONING_CONTENT
			}
			if interleaving != v1.ReasoningInterleaving_REASONING_INTERLEAVING_UNSPECIFIED {
				m.ReasoningInterleaving = &interleaving
			}
		}

		if effort, ok := view.Metadata["default_reasoning_effort"].(string); ok {
			if n, ok := v1.ReasoningEffort_value["REASONING_EFFORT_"+strings.ToUpper(effort)]; ok && n != 0 {
				v := v1.ReasoningEffort(n)
				m.DefaultReasoningEffort = &v
			}
		}
		out = append(out, m)
	}
	return out, nil
}
