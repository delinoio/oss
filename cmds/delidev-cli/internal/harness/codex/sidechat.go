// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// This is a private installed-version enforcement profile. A public reservation
// or a saved permission label cannot select it without the owning Worker.
type SidechatProfile string

const ReadOnlySidechatV1 SidechatProfile = "codex-read-only-sidechat-v1"

// Pin native execution features before app-server starts. MCP tables merge
// across layers, so empty CLI tables are insufficient: verification below also
// rejects every inherited server/plugin/hook/notification entry before Fork.
// Keep ordinary shell reads behind the native read-only, network-disabled
// sandbox; do not emulate those permissions with prompt text or approval UI.
var sidechatDisabledFeatures = []string{
	"hooks", "plugins", "remote_plugin", "plugin_sharing", "recommended_plugins",
	"apps", "psp", "enable_mcp_apps", "tool_search", "tool_suggest",
	"executor_capability_discovery", "multi_agent", "multi_agent_v2", "enable_fanout",
	"js_repl", "js_repl_tools_only", "code_mode", "browser_use", "computer_use",
	"image_generation", "standalone_web_search", "memories", "shell_snapshot",
	"exec_permission_approvals", "request_permissions_tool", "skill_mcp_dependency_install",
	"skill_env_var_dependency_prompt", "workspace_dependencies", "remote_control",
	"remote_models", "goals", "external_migration", "auth_elicitation",
	"tool_call_mcp_elicitation", "in_app_local_automation",
}

func sidechatUnavailable() *domain.Error {
	return domain.Fail(domain.Unsupported, "Native read-only Sidechat authority could not be verified.", "Use the pinned supported native profile without inherited external tools, hooks, plugins or notification commands; do not change managed policy to bypass this check.")
}

func sidechatMismatch(field string) *domain.Error {
	e := sidechatUnavailable()
	e.Guidance = fmt.Sprintf("Reconcile the native Sidechat %s observation without changing managed policy.", field)
	return e
}

func configureSidechat(config *Config) error {
	if config.Sidechat == "" {
		return nil
	}
	if config.Sidechat != ReadOnlySidechatV1 || config.Mode != ThreadProtocol || (config.ManagedAuthentication && config.API != nil) || config.ModelObservation || config.API != nil && config.API.TitleProfile {
		return sidechatUnavailable()
	}
	for _, key := range sidechatDisabledFeatures {
		config.Process.Args = append(config.Process.Args, "-c", "features."+key+"=false")
	}
	for _, option := range []string{
		`features.skip_host_skill_discovery=true`, `sandbox_mode="read-only"`,
		`approval_policy="never"`, `approvals_reviewer="user"`, `allow_login_shell=false`,
		`web_search="disabled"`, `notify=[]`, `mcp_servers={}`, `plugins={}`, `hooks={}`,
		`tools.experimental_request_user_input.enabled=false`,
	} {
		config.Process.Args = append(config.Process.Args, "-c", option)
	}
	return nil
}

func validateSidechatConfig(config map[string]json.RawMessage) error {
	var features map[string]json.RawMessage
	if json.Unmarshal(config["features"], &features) != nil || features == nil {
		return sidechatUnavailable()
	}
	for _, key := range sidechatDisabledFeatures {
		if string(features[key]) != "false" {
			return sidechatMismatch("feature-" + key)
		}
	}
	if string(features["skip_host_skill_discovery"]) != "true" {
		return sidechatUnavailable()
	}
	for _, field := range []string{"mcp_servers", "plugins"} {
		if len(config[field]) == 0 || string(config[field]) == "null" {
			continue
		}
		var entries map[string]json.RawMessage
		if json.Unmarshal(config[field], &entries) != nil || entries == nil || len(entries) != 0 {
			return sidechatMismatch(field)
		}
	}
	if raw := config["hooks"]; len(raw) != 0 && string(raw) != "null" {
		// Native ConfigToml serializes default empty lifecycle arrays and state.
		// Accept only that exact inert shape, rejecting any retained hook entry.
		var hooks map[string]json.RawMessage
		if json.Unmarshal(raw, &hooks) != nil || hooks == nil {
			return sidechatMismatch("hooks")
		}
		for key, value := range hooks {
			if key == "state" {
				var state map[string]json.RawMessage
				if json.Unmarshal(value, &state) != nil || state == nil || len(state) != 0 {
					return sidechatMismatch("hooks")
				}
				continue
			}
			if !slices.Contains([]string{"SessionStart", "SessionEnd", "SubagentStart", "SubagentStop", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PreCompact", "PostCompact", "Stop", "Interrupt", "PermissionRequest"}, key) {
				return sidechatMismatch("hooks")
			}
			var entries []json.RawMessage
			if json.Unmarshal(value, &entries) != nil || entries == nil || len(entries) != 0 {
				return sidechatMismatch("hooks")
			}
		}
	}
	if len(config["notify"]) != 0 && string(config["notify"]) != "null" {
		var command []json.RawMessage
		if json.Unmarshal(config["notify"], &command) != nil || command == nil || len(command) != 0 {
			return sidechatUnavailable()
		}
	}
	for key, expected := range map[string]string{
		"sandbox_mode": `"read-only"`, "approval_policy": `"never"`,
		"approvals_reviewer": `"user"`, "allow_login_shell": "false", "web_search": `"disabled"`,
	} {
		if string(config[key]) != expected {
			return sidechatMismatch(key)
		}
	}
	return nil
}

// config/read applies exact managed requirements; experimentalFeature/list
// additionally observes normalized effective flags, including thread-local
// project requirements. Never accept requested flags as observed enforcement.
func (c *Client) verifySidechat(ctx context.Context, cwd string, thread domain.ID) (returned error) {
	if c.sidechat == "" {
		return nil
	}
	defer func() {
		if returned != nil && c.logger != nil {
			c.logger.WarnContext(ctx, "Codex Sidechat authority rejected", "owner_id", c.ownerID, "code", domain.SafeError(returned).Code)
		}
	}()
	if c.managedHome != "" {
		if err := c.verifyManagedConfig(ctx, cwd); err != nil {
			return err
		}
	}
	response, err := c.wire.Call(ctx, domain.NewID(), "config/read", struct {
		Cwd           string `json:"cwd"`
		IncludeLayers bool   `json:"includeLayers"`
	}{Cwd: cwd})
	if err != nil {
		return err
	}
	var config struct {
		Config  map[string]json.RawMessage `json:"config"`
		Origins json.RawMessage            `json:"origins"`
		Layers  json.RawMessage            `json:"layers"`
	}
	if response.ErrorCode != nil || domain.Decode(response.Result, &config) != nil {
		return sidechatMismatch("config-shape")
	}
	if err := validateSidechatConfig(config.Config); err != nil {
		return err
	}
	response, err = c.wire.Call(ctx, domain.NewID(), "experimentalFeature/list", struct {
		Thread domain.ID `json:"threadId,omitempty"`
		Limit  int       `json:"limit"`
	}{thread, 256})
	if err != nil {
		return err
	}
	var observed struct {
		Data []struct {
			Name           string  `json:"name"`
			Stage          string  `json:"stage"`
			DisplayName    *string `json:"displayName"`
			Description    *string `json:"description"`
			Announcement   *string `json:"announcement"`
			Enabled        *bool   `json:"enabled"`
			DefaultEnabled *bool   `json:"defaultEnabled"`
		} `json:"data"`
		Next *string `json:"nextCursor"`
	}
	if response.ErrorCode != nil || domain.Decode(response.Result, &observed) != nil || len(observed.Data) == 0 || len(observed.Data) > 256 || observed.Next != nil {
		return sidechatUnavailable()
	}
	seen := map[string]bool{}
	for _, f := range observed.Data {
		if domain.Text(f.Name, "native feature", 128, true) != nil || seen[f.Name] || f.Enabled == nil || f.DefaultEnabled == nil || !slices.Contains([]string{"stable", "beta", "underDevelopment", "deprecated", "removed"}, f.Stage) {
			return sidechatUnavailable()
		}
		seen[f.Name] = true
		if slices.Contains(sidechatDisabledFeatures, f.Name) && *f.Enabled || f.Name == "skip_host_skill_discovery" && !*f.Enabled {
			return sidechatMismatch("effective-feature-" + f.Name)
		}
	}
	for _, key := range append(slices.Clone(sidechatDisabledFeatures), "skip_host_skill_discovery") {
		if !seen[key] {
			return sidechatMismatch("missing-feature-" + key)
		}
	}
	return nil
}

func sidechatSettings(settings ThreadSettings) bool {
	return settings.Options.Permission == domain.PermissionReadOnly && settings.Options.ApprovalPolicy == string(ApprovalNever) && settings.Options.ApprovalReviewModel == "" && settings.Options.ClaudePermission == ""
}

func sidechatEffective(settings EffectiveSettings) bool {
	return settings.ApprovalPolicy == ApprovalNever && settings.ApprovalsReviewer == "user" && settings.Sandbox.Type == ReadOnly && !settings.Sandbox.NetworkAccess && len(settings.Sandbox.WritableRoots) == 0 && !settings.Sandbox.ExcludeSlashTmp && !settings.Sandbox.ExcludeTmpdirEnvVar
}

func (c *Client) forkDefaults(source, child EffectiveSettings) bool {
	if c.sidechat == "" {
		return sameForkDefaults(source, child)
	}
	return sidechatEffective(child) && source.Model == child.Model && source.Provider == child.Provider && optionalForkString(source.Effort, child.Effort) && optionalForkString(source.ServiceTier, child.ServiceTier)
}
