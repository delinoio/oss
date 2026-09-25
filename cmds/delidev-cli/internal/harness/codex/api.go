package codex

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
)

const APIProvider = "delidev_api"
const executionTokenEnv = "DELIDEV_EXECUTION_TOKEN"

// APIConfig carries only a paired server origin and a short-lived execution
// token. It is never a provider key, user-editable native config or persisted
// document. The owning Worker registers its digest before calling Open.
type APIConfig struct {
	ServerOrigin string `json:"-"`
	Token        string `json:"-"`
}

// Retain only non-secret configuration after launch. The process owner receives
// the token through its private environment, never argv or a config file.
type apiBinding struct {
	endpoint string
}

func configureAPI(config *Config) (*apiBinding, error) {
	if config.API == nil {
		return nil, nil
	}
	if config.Mode != ThreadProtocol || !apiproxy.ValidToken(config.API.Token) {
		return nil, domain.Fail(domain.InvalidArgument, "A private execution credential and thread profile are required.", "Use only the owning Worker's registered execution authority.")
	}
	if err := rpc.ValidateEndpoint(config.API.ServerOrigin); err != nil {
		return nil, err
	}
	origin, err := url.Parse(config.API.ServerOrigin)
	if err != nil || origin.ForceQuery || origin.RawPath != "" {
		return nil, incompatible()
	}
	origin.Path = apiproxy.Prefix
	binding := &apiBinding{endpoint: origin.String()}
	for _, entry := range config.Process.Env {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, executionTokenEnv) {
			return nil, domain.Fail(domain.InvalidArgument, "The native runtime already contains an execution credential.", "Build a fresh explicit environment for this assignment.")
		}
	}
	config.Process.Env = append(slices.Clone(config.Process.Env), executionTokenEnv+"="+config.API.Token)
	// Native session flags override project configuration. Legacy managed
	// policy can outrank them, so verifyAPI also checks the effective merged
	// authority before thread creation/resume, without changing that policy.
	provider := `{name="DeliDev",base_url=` + strconv.Quote(binding.endpoint) + `,env_key="` + executionTokenEnv + `",wire_api="responses",requires_openai_auth=false,supports_websockets=false,supports_standalone_web_search=false}`
	config.Process.Args = append(config.Process.Args,
		"-c", `model_provider="`+APIProvider+`"`, "-c", "model_providers."+APIProvider+"="+provider,
		"-c", `shell_environment_policy.exclude=["`+executionTokenEnv+`"]`)
	return binding, nil
}

func (c *Client) verifyAPI(ctx context.Context, cwd string) error {
	if c.api == nil {
		return nil
	}
	response, err := c.wire.Call(ctx, domain.NewID(), "config/read", struct {
		Cwd           string `json:"cwd"`
		IncludeLayers bool   `json:"includeLayers"`
	}{Cwd: cwd})
	if err != nil {
		return err
	}
	if response.ErrorCode != nil {
		return incompatible()
	}
	var result struct {
		Config  map[string]json.RawMessage `json:"config"`
		Origins json.RawMessage            `json:"origins"`
		Layers  json.RawMessage            `json:"layers"`
	}
	if domain.Decode(response.Result, &result) != nil || result.Config == nil {
		return incompatible()
	}
	if err := c.api.validateConfig(result.Config); err != nil {
		if c.logger != nil {
			c.logger.WarnContext(ctx, "Codex native API authority mismatch", "owner_id", c.ownerID, "code", domain.Unsupported)
		}
		return err
	}
	return nil
}

func apiConfigMismatch(field string) *domain.Error {
	return domain.Fail(domain.Unsupported, "Codex execution authority failed its "+field+" check.", "Inspect managed native configuration and revalidate the exact execution profile; no prompt was sent.")
}

func (a *apiBinding) validateConfig(config map[string]json.RawMessage) error {
	var selected, store string
	if json.Unmarshal(config["model_provider"], &selected) != nil || selected != APIProvider || json.Unmarshal(config["cli_auth_credentials_store"], &store) != nil || store != "ephemeral" {
		return apiConfigMismatch("provider-selection")
	}
	var providers map[string]json.RawMessage
	if domain.Decode(config["model_providers"], &providers) != nil {
		return apiConfigMismatch("provider-map")
	}
	// A closed provider shape prevents inherited command auth, alternate
	// headers/query parameters or literal bearer credentials from changing the
	// endpoint or reading another account, even after recursive config merging.
	var provider struct {
		Name               string          `json:"name"`
		BaseURL            string          `json:"base_url"`
		EnvKey             string          `json:"env_key"`
		WireAPI            string          `json:"wire_api"`
		RequiresOpenAIAuth *bool           `json:"requires_openai_auth"`
		SupportsWebsockets *bool           `json:"supports_websockets"`
		SupportsWebSearch  *bool           `json:"supports_standalone_web_search"`
		RequestRetries     *uint64         `json:"request_max_retries"`
		StreamRetries      *uint64         `json:"stream_max_retries"`
		EnvKeyInstructions json.RawMessage `json:"env_key_instructions"`
		BearerToken        json.RawMessage `json:"experimental_bearer_token"`
		Auth               json.RawMessage `json:"auth"`
		AWS                json.RawMessage `json:"aws"`
		Query              json.RawMessage `json:"query_params"`
		Headers            json.RawMessage `json:"http_headers"`
		EnvHeaders         json.RawMessage `json:"env_http_headers"`
		StreamIdleTimeout  json.RawMessage `json:"stream_idle_timeout_ms"`
		WebsocketTimeout   json.RawMessage `json:"websocket_connect_timeout_ms"`
	}
	if domain.Decode(providers[APIProvider], &provider) != nil {
		return apiConfigMismatch("provider-shape")
	}
	for _, field := range []struct{ name, got, expected string }{
		{"provider-name", provider.Name, "DeliDev"}, {"provider-endpoint", provider.BaseURL, a.endpoint}, {"provider-environment", provider.EnvKey, executionTokenEnv}, {"provider-protocol", provider.WireAPI, "responses"},
	} {
		if field.got != field.expected {
			return apiConfigMismatch(field.name)
		}
	}
	for _, flag := range []*bool{provider.RequiresOpenAIAuth, provider.SupportsWebsockets, provider.SupportsWebSearch} {
		if flag == nil || *flag {
			return apiConfigMismatch("transport")
		}
	}
	// config/read serializes all optional native provider fields as null. Only
	// those exact null observations are equivalent to absent configuration;
	// populated extra authority cannot be silently discarded by decoding.
	for _, optional := range []json.RawMessage{provider.EnvKeyInstructions, provider.BearerToken, provider.Auth, provider.AWS, provider.Query, provider.Headers, provider.EnvHeaders, provider.StreamIdleTimeout, provider.WebsocketTimeout} {
		if len(optional) != 0 && string(optional) != "null" {
			return apiConfigMismatch("additional-provider-authority")
		}
	}
	// Retry limits are native provider behavior. Preserve their default or
	// explicitly configured values; the DeliDev relay never retries requests.
	var shell struct {
		Exclude []string          `json:"exclude"`
		Set     map[string]string `json:"set"`
	}
	// Other native shell policy fields retain their native behavior. Only the
	// execution credential's exclusion and absence from explicit setters are
	// ownership requirements, not a replacement for native permissions.
	if json.Unmarshal(config["shell_environment_policy"], &shell) != nil || !slices.Contains(shell.Exclude, executionTokenEnv) {
		return apiConfigMismatch("shell-exclusion")
	}
	for key := range shell.Set {
		if strings.EqualFold(key, executionTokenEnv) {
			return apiConfigMismatch("shell-setter")
		}
	}
	return nil
}
