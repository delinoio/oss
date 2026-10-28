// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/packages/tairouter/core"
	"github.com/delinoio/oss/packages/tairouter/providers"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

func providerConnection(d providerData) providers.Connection {
	c := providers.Connection{Provider: d.Metadata.ProviderId, BaseURL: d.Metadata.BaseUrl, APIKey: d.Secret, Credential: d.Credential, Headers: d.CustomHeaders}
	if c.Provider == "custom" {
		c.Wire = providers.Chat
	}
	if config := d.Metadata.Configuration; config != nil {
		if config.Wire != nil {
			c.Wire = configuredProviderWire(*config.Wire)
		}
		c.ModelWires = make(map[string]providers.Wire, len(config.ModelWires))
		for model, wire := range config.ModelWires {
			c.ModelWires[model] = configuredProviderWire(wire)
		}
	}
	switch d.Metadata.AuthKind {
	case v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY:
		c.Auth = providers.Key
	case v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH:
		if p, ok := providers.Lookup(c.Provider); ok {
			c.Auth = p.Auth
		}
		if c.Provider == "google-vertex" && c.Credential != nil && c.Credential.Google != nil {
			c.Auth = providers.OAuth
		}
	case v1.ProviderAuthKind_PROVIDER_AUTH_KIND_NO_AUTH:
		c.Auth = providers.Local
	}
	return c
}
func catalogMessage(p providers.Preset) *v1.ProviderDefinition {
	auth := v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY
	switch p.Auth {
	case providers.OAuth, providers.Forward:
		auth = v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH
	case providers.Local:
		auth = v1.ProviderAuthKind_PROVIDER_AUTH_KIND_NO_AUTH
	}
	shapes := []v1.ApiShape{v1.ApiShape_API_SHAPE_MODELS}
	switch p.Wire {
	case providers.Responses:
		shapes = append(shapes, v1.ApiShape_API_SHAPE_RESPONSES)
	case providers.Anthropic:
		shapes = append(shapes, v1.ApiShape_API_SHAPE_ANTHROPIC_MESSAGES)
	default:
		shapes = append(shapes, v1.ApiShape_API_SHAPE_CHAT_COMPLETIONS)
	}
	auths := []v1.ProviderAuthKind{auth}
	if p.AllowKeyAuthOverride && auth != v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY {
		auths = append(auths, v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY)
	}
	if len(providers.AuthMethods(p.ID)) > 0 && auth != v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH {
		auths = append(auths, v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH)
	}
	out := &v1.ProviderDefinition{Id: p.ID, DisplayName: p.Label, AuthKinds: auths, ApiShapes: shapes}
	for _, method := range providers.AuthMethods(p.ID) {
		switch method {
		case providers.PKCE:
			out.OauthMethods = append(out.OauthMethods, v1.ProviderOAuthMethod_PROVIDER_O_AUTH_METHOD_PKCE)
		case providers.Device:
			out.OauthMethods = append(out.OauthMethods, v1.ProviderOAuthMethod_PROVIDER_O_AUTH_METHOD_DEVICE)
		case providers.BrowserPoll:
			out.OauthMethods = append(out.OauthMethods, v1.ProviderOAuthMethod_PROVIDER_O_AUTH_METHOD_BROWSER_POLL)
		case providers.ExplicitToken:
			out.OauthMethods = append(out.OauthMethods, v1.ProviderOAuthMethod_PROVIDER_O_AUTH_METHOD_EXPLICIT_TOKEN)
		case providers.NativeCallback:
			out.OauthMethods = append(out.OauthMethods, v1.ProviderOAuthMethod_PROVIDER_O_AUTH_METHOD_NATIVE_CALLBACK)
		}
	}
	return out
}
func providerModel(p providers.Preset, id string) *v1.Model {
	out := &v1.Model{Id: id, DisplayName: id, ApiShapes: catalogMessage(p).ApiShapes}
	if n := p.ModelContextWindows[id]; n > 0 {
		v := uint64(n)
		out.ContextLimit = &v
	} else if p.ContextWindow > 0 {
		v := uint64(p.ContextWindow)
		out.ContextLimit = &v
	}
	if n := p.ModelMaxOutputTokens[id]; n > 0 {
		v := uint64(n)
		out.OutputLimit = &v
	} else if p.DefaultMaxOutputTokens > 0 {
		v := uint64(p.DefaultMaxOutputTokens)
		out.OutputLimit = &v
	}
	return out
}

// discoverProviderModels serializes catalog calls with refresh. Some providers
// renew an account token during discovery; their result must use the original CAS.
func (m *management) discoverProviderModels(ctx context.Context, sc core.Scope, id string) (providerData, error) {
	var d providerData
	raw, version, claim, err := m.store.ClaimScopePrivateRefresh(ctx, sc, string(providerKind), id)
	if err != nil {
		return d, err
	}
	var sent atomic.Bool
	complete := false
	defer func() {
		if complete {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		// Zed discovery can rotate its LLM token. A failed/ambiguous call must not
		// leave its previous token eligible for a later rotating exchange.
		if sent.Load() && d.Metadata != nil && d.Metadata.ProviderId == "zed" {
			_ = m.store.MarkScopePrivateRefreshUncertain(cleanup, sc, string(providerKind), id, claim)
		} else {
			_ = m.store.ReleaseScopePrivateRefresh(cleanup, sc, string(providerKind), id, claim)
		}
	}()
	if err = json.Unmarshal(raw, &d); err != nil {
		return providerData{}, err
	}
	if d.Metadata == nil || d.Metadata.State != v1.ConnectionState_CONNECTION_STATE_READY {
		return providerData{}, providers.ErrReauthenticate
	}
	executor := &providerExecutor{store: m.store, scope: sc, test: m.providerExecutor, onSend: func() { sent.Store(true) }, gate: func(c context.Context, start func(context.Context) error) error {
		return m.store.WithScopePrivateRefreshDispatch(c, sc, string(providerKind), id, claim, start)
	}}
	catalog, fresh, err := providers.NewAuth(executor).Discover(ctx, sc, providerConnection(d))
	if err != nil {
		return providerData{}, err
	}
	if fresh != nil {
		d.Credential = fresh
	}
	d.CodexCatalog = catalog.CodexCatalog
	d.Models = &catalog
	d.ModelsDiscoveredAt = time.Now()
	raw, err = json.Marshal(d)
	if err != nil {
		return providerData{}, err
	}
	if err = m.store.CompleteScopePrivateRefresh(ctx, sc, string(providerKind), id, version, claim, raw); err != nil {
		return providerData{}, err
	}
	complete = true
	return d, nil
}

func configuredProviderWire(w v1.ProviderWire) providers.Wire {
	switch w {
	case v1.ProviderWire_PROVIDER_WIRE_CHAT:
		return providers.Chat
	case v1.ProviderWire_PROVIDER_WIRE_RESPONSES:
		return providers.Responses
	case v1.ProviderWire_PROVIDER_WIRE_ANTHROPIC:
		return providers.Anthropic
	case v1.ProviderWire_PROVIDER_WIRE_GOOGLE:
		return providers.Google
	case v1.ProviderWire_PROVIDER_WIRE_JEV_DECISION:
		return providers.Wire("jev-decision")
	default:
		return providers.Wire("invalid")
	}
}
func genericProviderWire(w providers.Wire) bool {
	return w == providers.Chat || w == providers.Responses || w == providers.Anthropic || w == providers.Google
}
func validConfiguredPath(path string) bool {
	if path == "" || len(path) > 2048 || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\\\r\n\x00?#") {
		return false
	}
	parsed, err := url.ParseRequestURI(path)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	for _, part := range strings.Split(parsed.Path, "/") {
		if part == ".." || part == "." {
			return false
		}
	}
	return true
}
func validConfiguredModel(model string) bool {
	if model == "" || len(model) > 512 || strings.TrimSpace(model) != model || !utf8.ValidString(model) {
		return false
	}
	for _, r := range model {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
func validateProviderConfiguration(provider string, config *v1.ProviderConfiguration) error {
	if config == nil {
		return nil
	}
	if config.VertexProject != nil || config.VertexLocation != nil {
		if provider != "google-vertex" {
			return invalid("Vertex project and location require Google Vertex")
		}
		if config.VertexProject != nil {
			project := *config.VertexProject
			if !validConfiguredModel(project) || project == "." || project == ".." || strings.ContainsAny(project, "/\\?#% \t\n\r") {
				return invalid("invalid Vertex project")
			}
		}
		if config.VertexLocation != nil && !selectedVertexLocation.MatchString(*config.VertexLocation) {
			return invalid("invalid Vertex location")
		}
	}
	configurable := provider == "custom"
	if p, ok := providers.Lookup(provider); ok {
		configurable = genericProviderWire(p.Wire)
	}
	if config.Wire != nil && !(provider == "custom" && *config.Wire == v1.ProviderWire_PROVIDER_WIRE_JEV_DECISION) && (!configurable || !genericProviderWire(configuredProviderWire(*config.Wire))) {
		return invalid("wire override is unsupported for this provider")
	}
	if len(config.ModelWires) > 2000 {
		return invalid("too many model wire overrides")
	}
	for model, wire := range config.ModelWires {
		if !configurable || !validConfiguredModel(model) || (wire != v1.ProviderWire_PROVIDER_WIRE_CHAT && wire != v1.ProviderWire_PROVIDER_WIRE_RESPONSES) {
			return invalid("invalid model wire override")
		}
	}
	for _, path := range []*string{config.ResponsesPath, config.ChatCompletionsPath} {
		if path != nil && (!configurable || !validConfiguredPath(*path)) {
			return invalid("invalid provider endpoint path")
		}
	}
	if repair := config.GetPolicy().GetResponsesItemIdRepair(); repair != nil {
		for _, ids := range [][]string{repair.Message, repair.Reasoning} {
			if len(ids) > 2000 {
				return invalid("too many response item repair identifiers")
			}
			seen := map[string]bool{}
			for _, id := range ids {
				if !validConfiguredModel(id) || seen[id] {
					return invalid("invalid response item repair identifier")
				}
				seen[id] = true
			}
		}
	}
	if config.MinMaxOutputTokens != nil && (provider != "azure-openai" || *config.MinMaxOutputTokens <= 0 || *config.MinMaxOutputTokens > 2147483647) {
		return invalid("output budget floor requires Azure and a positive bounded value")
	}
	return nil
}
func validatedProviderHeaders(values map[string]string) (map[string]string, error) {
	if len(values) > 64 {
		return nil, invalid("too many custom headers")
	}
	out := make(map[string]string, len(values))
	total := 0
	for name, value := range values {
		if name == "" || len(name) > 128 {
			return nil, invalid("invalid custom header name")
		}
		for _, r := range name {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
				return nil, invalid("invalid custom header name")
			}
		}
		lower := strings.ToLower(name)
		switch lower {
		case "authorization", "proxy-authorization", "cookie", "set-cookie", "host", "content-length", "transfer-encoding", "connection", "proxy-connection", "keep-alive", "upgrade", "te", "trailer", "expect", "x-api-key", "api-key", "x-goog-api-key", "x-amz-security-token", "x-dsh-auth-token", "chatgpt-account-id":
			return nil, invalid("custom headers cannot override authentication or transport fields")
		}
		if strings.HasPrefix(lower, "x-tairouter-") {
			return nil, invalid("custom headers cannot override internal fields")
		}
		if len(value) > 8192 || strings.ContainsAny(value, "\r\n\x00") {
			return nil, invalid("invalid custom header value")
		}
		canonical := http.CanonicalHeaderKey(name)
		if _, exists := out[canonical]; exists {
			return nil, invalid("duplicate custom header name")
		}
		total += len(name) + len(value)
		if total > 32768 {
			return nil, invalid("custom headers exceed size limit")
		}
		out[canonical] = value
	}
	return out, nil
}
func applyProviderConfiguration(d *providerData, config *v1.ProviderConfiguration, reset bool, headers *v1.ProviderCustomHeaders) error {
	if d.Metadata == nil {
		return invalid("invalid provider connection")
	}
	if reset && config != nil {
		return invalid("configuration and reset cannot be supplied together")
	}
	if reset {
		d.Metadata.Configuration = nil
	} else if config != nil {
		d.Metadata.Configuration = proto.Clone(config).(*v1.ProviderConfiguration)
	}
	if err := validateProviderConfiguration(d.Metadata.ProviderId, d.Metadata.Configuration); err != nil {
		return err
	}
	if config := d.Metadata.Configuration; config != nil && config.VertexProject != nil && strings.EqualFold(*config.VertexProject, "auto") {
		if d.Metadata.AuthKind != v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY || *config.VertexProject != "auto" {
			return invalid("Vertex auto project requires API key authentication")
		}
	}
	if headers != nil {
		var err error
		d.CustomHeaders, err = validatedProviderHeaders(headers.Values)
		if err != nil {
			return err
		}
	}
	d.Metadata.CustomHeaderNames = nil
	for name := range d.CustomHeaders {
		d.Metadata.CustomHeaderNames = append(d.Metadata.CustomHeaderNames, name)
	}
	sort.Strings(d.Metadata.CustomHeaderNames)
	return nil
}

// providerPolicyOverrides converts only typed management fields. Proto JSON keeps
// optional false values and uses the pinned camelCase policy names. Header values
// deliberately stay outside this policy/capability representation.
func providerPolicyOverrides(d providerData) (json.RawMessage, error) {
	fields := map[string]any{}
	if d.Metadata == nil || d.Metadata.Configuration == nil {
		return json.RawMessage(`{}`), nil
	}
	c := d.Metadata.Configuration
	if c.Policy != nil {
		raw, err := protojson.Marshal(c.Policy)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &fields); err != nil {
			return nil, err
		}
	}
	if c.ResponsesPath != nil {
		fields["responsesPath"] = *c.ResponsesPath
	}
	if c.ChatCompletionsPath != nil {
		fields["chatCompletionsPath"] = *c.ChatCompletionsPath
	}
	if c.MinMaxOutputTokens != nil {
		fields["minMaxOutputTokens"] = *c.MinMaxOutputTokens
	}
	if len(c.ModelWires) > 0 {
		models := map[string]string{}
		for model, wire := range c.ModelWires {
			models[model] = string(configuredProviderWire(wire))
		}
		fields["modelAdapters"] = models
	}
	return json.Marshal(fields)
}
