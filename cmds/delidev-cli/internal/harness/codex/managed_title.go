// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// ManagedTitleProvider is the private provider alias used by the managed
// automatic-title profile. Codex rejects overrides of its built-in `openai`
// provider, so retry limits can only be pinned on an alias that reproduces the
// built-in ChatGPT authority: the OpenAI display name, Responses wire protocol,
// ChatGPT-authenticated requests, the native default base URL and no extra
// provider authority. Normal managed execution keeps the built-in provider.
const ManagedTitleProvider = "delidev_chatgpt_title"

// managedTitleProviderDefinition is the exact alias table. It names no base URL,
// credential, header or query authority, disables websockets so the pinned HTTP
// stream is the only transport, and sets both native retry limits to zero.
const managedTitleProviderDefinition = `{name="OpenAI",wire_api="responses",requires_openai_auth=true,supports_websockets=false,request_max_retries=0,stream_max_retries=0}`

// The pinned native profile reports unified_exec as enabled even when it is
// configured off, but it is only a transport of shell_tool, which is disabled.
// managedTitleDisabledFeatures extends the read-only Sidechat denial list with
// every native execution, image and discovery tool the pinned profile can turn
// off. A title never reads files, runs commands, searches skills or sleeps.
var managedTitleDisabledFeatures = append(slices.Clone(sidechatDisabledFeatures), "shell_tool", "unified_exec_tty", "view_image", "sleep_tool", "write_stdin_approval", "skill_search")

func managedTitleUnavailable() *domain.Error {
	return domain.Fail(domain.Unsupported, "The native managed title profile could not be verified.", "Use the pinned Codex profile without inherited tools, hooks, plugins, skills, ambient instructions or retries; no prompt was sent.")
}

func managedTitleMismatch(field string) *domain.Error {
	e := managedTitleUnavailable()
	e.Guidance = "Reconcile the native managed title " + field + " observation without changing managed policy; no prompt was sent."
	return e
}

// readOnlyProfile reports the profiles that must prove an effective read-only,
// approval-free thread before any input is accepted.
func (c *Client) readOnlyProfile() bool { return c.sidechat != "" || c.managedTitle }

// managedProviderKey is the provider key the managed thread must select.
func (c *Client) managedProviderKey() string {
	if c.managedTitle {
		return ManagedTitleProvider
	}
	return managedOpenAIProviderKey
}

func configureManagedTitle(config *Config) error {
	if !config.ManagedTitle {
		return nil
	}
	config.Process.Args = append(config.Process.Args, "-c", `model_provider="`+ManagedTitleProvider+`"`, "-c", "model_providers."+ManagedTitleProvider+"="+managedTitleProviderDefinition)
	for _, key := range managedTitleDisabledFeatures {
		config.Process.Args = append(config.Process.Args, "-c", "features."+key+"=false")
	}
	for _, option := range []string{
		`features.skip_host_skill_discovery=true`,
		`approval_policy="never"`, `approvals_reviewer="user"`, `allow_login_shell=false`,
		`web_search="disabled"`, `notify=[]`, `mcp_servers={}`, `plugins={}`, `hooks={}`,
		`tools.experimental_request_user_input.enabled=false`,
		`sandbox_mode="read-only"`, `project_doc_max_bytes=0`,
	} {
		config.Process.Args = append(config.Process.Args, "-c", option)
	}
	return nil
}

// managedTitleInertProviderKeys are the provider fields native config/read may
// report as null defaults. Any value would add endpoint, credential, header or
// timeout authority to the alias.
var managedTitleInertProviderKeys = []string{"base_url", "env_key", "experimental_bearer_token", "auth", "aws", "http_headers", "env_http_headers", "query_params", "model_catalog_url", "gateway_oauth", "env_key_instructions", "stream_idle_timeout_ms", "websocket_connect_timeout_ms"}

// validateManagedTitleProvider proves the alias is exactly the retry-free
// built-in ChatGPT authority and carries no additional provider authority.
func validateManagedTitleProvider(provider map[string]json.RawMessage) error {
	for key, want := range map[string]string{
		"name": `"OpenAI"`, "wire_api": `"responses"`, "requires_openai_auth": "true", "supports_websockets": "false",
		"request_max_retries": "0", "stream_max_retries": "0",
	} {
		if strings.TrimSpace(string(provider[key])) != want {
			return managedTitleMismatch("provider-" + key)
		}
	}
	// The shape is closed: every observed key must be one of the six required
	// fields above, a known inert default that is null or absent, or the
	// standalone web-search flag in its disabled state. An inherited or newly
	// supported field is refused rather than assumed harmless.
	for key, raw := range provider {
		switch key {
		case "name", "wire_api", "requires_openai_auth", "supports_websockets", "request_max_retries", "stream_max_retries":
		case "supports_standalone_web_search":
			if value := strings.TrimSpace(string(raw)); value != "false" && value != "null" {
				return managedTitleMismatch("provider-authority")
			}
		default:
			if !slices.Contains(managedTitleInertProviderKeys, key) || (len(raw) != 0 && strings.TrimSpace(string(raw)) != "null") {
				return managedTitleMismatch("provider-authority")
			}
		}
	}
	return nil
}

// validateManagedTitleConfig checks the merged native configuration. Requested
// flags are never accepted as observed enforcement.
func validateManagedTitleConfig(config map[string]json.RawMessage) error {
	if err := validateSidechatCommonConfig(config); err != nil {
		return managedTitleMismatch("read-only-profile")
	}
	if string(config["sandbox_mode"]) != `"read-only"` || strings.TrimSpace(string(config["project_doc_max_bytes"])) != "0" {
		return managedTitleMismatch("ambient-instructions")
	}
	var features map[string]json.RawMessage
	if json.Unmarshal(config["features"], &features) != nil {
		return managedTitleUnavailable()
	}
	for _, key := range managedTitleDisabledFeatures {
		if string(features[key]) != "false" {
			return managedTitleMismatch("feature-" + key)
		}
	}
	for _, key := range []string{"instructions", "developer_instructions", "model_instructions_file", "compact_prompt", "experimental_compact_prompt_file", "user_instructions"} {
		if raw := config[key]; len(raw) != 0 && string(raw) != "null" {
			return managedTitleMismatch("ambient-instructions")
		}
	}
	return nil
}

// verifyManagedTitleFeatures reads the normalized effective feature flags. The
// disabled tools must be present and off; an absent flag means the installed
// native profile cannot prove the isolation and is refused before input.
func verifyManagedTitleFeatures(enabled map[string]bool) error {
	for _, key := range append(slices.Clone(managedTitleDisabledFeatures), "skip_host_skill_discovery") {
		value, present := enabled[key]
		if !present {
			return managedTitleMismatch("missing-feature-" + key)
		}
		if key == "skip_host_skill_discovery" && !value || key != "skip_host_skill_discovery" && value {
			return managedTitleMismatch("effective-feature-" + key)
		}
	}
	return nil
}

func (c *Client) logManagedTitleRejection(ctx context.Context, returned error) {
	if returned != nil && c.logger != nil {
		c.logger.WarnContext(ctx, "Codex managed title profile rejected", "owner_id", c.ownerID, "code", domain.SafeError(returned).Code)
	}
}
