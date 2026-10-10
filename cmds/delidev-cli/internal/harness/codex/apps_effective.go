// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type effectiveAppsDefault struct {
	Enabled      *bool                    `json:"enabled"`
	Reviewer     domain.ApprovalsReviewer `json:"approvals_reviewer"`
	Destructive  *bool                    `json:"destructive_enabled"`
	OpenWorld    *bool                    `json:"open_world_enabled"`
	ApprovalMode nativeAppApprovalMode    `json:"default_tools_approval_mode"`
}

type effectiveAppPolicy struct {
	Enabled      *bool                    `json:"enabled"`
	OmitTools    json.RawMessage          `json:"omit_tools_from"`
	Reviewer     domain.ApprovalsReviewer `json:"approvals_reviewer"`
	Destructive  *bool                    `json:"destructive_enabled"`
	OpenWorld    *bool                    `json:"open_world_enabled"`
	ApprovalMode nativeAppApprovalMode    `json:"default_tools_approval_mode"`
	ToolsEnabled *bool                    `json:"default_tools_enabled"`
	Tools        json.RawMessage          `json:"tools"`
	Links        json.RawMessage          `json:"links"`
}

func absentOrNull(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}

// Verify the resolved native configuration, including stronger project or
// managed layers. A successful write to the user file does not prove that its
// app policy took effect. Tool/link exceptions cannot bypass one-call approval.
func verifyEffectiveApps(raw json.RawMessage, selection domain.CodexAppConfiguration) error {
	if selection.Validate() != nil {
		return incompatible()
	}
	var apps map[string]json.RawMessage
	if domain.DecodeBounded(raw, &apps, 1<<20) != nil || apps == nil || len(apps) > domain.MaxCodexAppInventory+1 {
		return incompatible()
	}
	var defaults effectiveAppsDefault
	if domain.Decode(apps["_default"], &defaults) != nil || defaults.Enabled == nil || *defaults.Enabled || defaults.Reviewer != domain.CodexReviewerUser || defaults.ApprovalMode != nativeAppPrompt || defaults.Destructive == nil || !*defaults.Destructive || defaults.OpenWorld == nil || !*defaults.OpenWorld {
		return incompatible()
	}
	seen := map[string]bool{}
	for id, value := range apps {
		if id == "_default" {
			continue
		}
		var app effectiveAppPolicy
		if domain.Text(id, "effective app identity", 1024, true) != nil || domain.Decode(value, &app) != nil || app.Enabled == nil {
			return incompatible()
		}
		selected := slices.Contains(selection.AppIDs, id)
		if *app.Enabled != selected {
			return incompatible()
		}
		if selected {
			if app.Reviewer != domain.CodexReviewerUser || app.ApprovalMode != nativeAppPrompt || !absentOrNull(app.Tools) || !absentOrNull(app.Links) || app.ToolsEnabled != nil {
				return incompatible()
			}
			seen[id] = true
		}
	}
	if len(seen) != len(selection.AppIDs) {
		return incompatible()
	}
	return nil
}
