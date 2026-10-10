// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// This profile is original-session admission, not a general MCP client. Native
// Apps remain disabled by default. A selected App receives a user prompt for
// every effect, preserving the original native call identity through the pinned
// requestUserInput fallback. Never/Guardian approval profiles are not callable.
func configureNativeApps(config *Config) error {
	if config.NativeApps == nil {
		return nil
	}
	if config.NativeApps.Validate() != nil || config.Mode != ThreadProtocol || !config.ManagedAuthentication || config.API != nil || config.Sidechat != "" || config.Version != "0.162.0" {
		return domain.NativeAppsUnavailable()
	}
	config.Process.Args = append(config.Process.Args,
		"-c", "features.apps=true", "-c", "features.tool_call_mcp_elicitation=false",
		"-c", "apps._default.enabled=false", "-c", `apps._default.default_tools_approval_mode="prompt"`,
		"-c", `apps._default.approvals_reviewer="user"`)
	for _, id := range config.NativeApps.AppIDs {
		quoted, _ := json.Marshal(id)
		key := "apps." + string(quoted)
		config.Process.Args = append(config.Process.Args, "-c", key+".enabled=true", "-c", key+`.default_tools_approval_mode="prompt"`, "-c", key+`.approvals_reviewer="user"`)
	}
	return nil
}

func cloneNativeApps(selection *domain.SessionNativeAppSelection) *domain.SessionNativeAppSelection {
	if selection == nil {
		return nil
	}
	result := *selection
	result.AppIDs = slices.Clone(selection.AppIDs)
	return &result
}

type nativeAppsPolicy struct {
	Enabled             bool     `json:"enabled"`
	Reviewer            *string  `json:"approvals_reviewer"`
	Destructive         *bool    `json:"destructive_enabled"`
	OpenWorld           *bool    `json:"open_world_enabled"`
	Approval            *string  `json:"default_tools_approval_mode"`
	DefaultToolsEnabled *bool    `json:"default_tools_enabled"`
	OmitToolsFrom       []string `json:"omit_tools_from"`
	Tools               map[string]struct {
		Enabled  *bool   `json:"enabled"`
		Approval *string `json:"approval_mode"`
	} `json:"tools"`
	Links map[string]struct {
		Reviewer *string `json:"approvals_reviewer"`
		Approval *string `json:"default_tools_approval_mode"`
	} `json:"links"`
}

// Requested CLI defaults cannot override a lower-layer per-tool or connected
// account approval rule. Check the resolved native configuration before any
// model turn. Unsafe inherited overrides make the profile unavailable instead
// of rewriting the original policy or lending its installed state authority.
func validateNativeAppsPolicies(raw json.RawMessage, selection domain.SessionNativeAppSelection) error {
	var entries map[string]json.RawMessage
	if selection.Validate() != nil || domain.Decode(raw, &entries) != nil || entries == nil {
		return domain.NativeAppsUnavailable()
	}
	selected := map[string]bool{}
	for _, id := range selection.AppIDs {
		selected[id] = true
	}
	seen := map[string]bool{}
	for id, value := range entries {
		var p nativeAppsPolicy
		if domain.Decode(value, &p) != nil {
			return domain.NativeAppsUnavailable()
		}
		if id == "_default" {
			if p.Enabled || p.Approval == nil || *p.Approval != "prompt" || p.Reviewer == nil || *p.Reviewer != "user" || p.Tools != nil || p.Links != nil || p.DefaultToolsEnabled != nil || p.OmitToolsFrom != nil {
				return domain.NativeAppsUnavailable()
			}
			seen[id] = true
			continue
		}
		if !domain.NativeAppIDValid(id) || (p.Enabled && !selected[id]) {
			return domain.NativeAppsUnavailable()
		}
		if selected[id] {
			if !p.Enabled || p.Approval == nil || *p.Approval != "prompt" || p.Reviewer == nil || *p.Reviewer != "user" {
				return domain.NativeAppsUnavailable()
			}
			seen[id] = true
		}
		for _, tool := range p.Tools {
			if tool.Approval != nil && *tool.Approval != "prompt" {
				return domain.NativeAppsUnavailable()
			}
		}
		for _, link := range p.Links {
			if (link.Approval != nil && *link.Approval != "prompt") || (link.Reviewer != nil && *link.Reviewer != "user") {
				return domain.NativeAppsUnavailable()
			}
		}
	}
	if !seen["_default"] || len(seen) != len(selected)+1 {
		return domain.NativeAppsUnavailable()
	}
	return nil
}

func (c *Client) nativeAppsSettingsAllowed(settings EffectiveSettings) bool {
	return c.nativeApps == nil || len(c.nativeApps.AppIDs) == 0 || (settings.ApprovalPolicy != ApprovalNever && settings.ApprovalsReviewer == "user")
}
func (c *Client) verifyNativeAppsSettings(ctx context.Context, settings EffectiveSettings) error {
	if c.nativeApps == nil {
		return nil
	}
	if !c.nativeAppsSettingsAllowed(settings) {
		return domain.NativeAppsUnavailable()
	}
	return c.verifyManagedConfig(ctx, settings.Cwd)
}
