// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Source: openai/codex c1382380de69521303b416720a52f42d51af6248,
// AppsConfig and AppsDefaultConfig. This object belongs in the private user
// layer, not session flags: a session flag would survive an explicit removal.
// Each selected connector requires a fresh, one-call native approval. Replacing
// the complete apps object also removes inherited per-tool and link approvals.
type nativeAppApprovalMode string

const nativeAppPrompt nativeAppApprovalMode = "prompt"

type nativeAppsDefault struct {
	Enabled      bool                     `json:"enabled"`
	Reviewer     domain.ApprovalsReviewer `json:"approvals_reviewer"`
	Destructive  bool                     `json:"destructive_enabled"`
	OpenWorld    bool                     `json:"open_world_enabled"`
	ApprovalMode nativeAppApprovalMode    `json:"default_tools_approval_mode"`
}

type nativeAppSelection struct {
	Enabled      bool                     `json:"enabled"`
	Reviewer     domain.ApprovalsReviewer `json:"approvals_reviewer"`
	ApprovalMode nativeAppApprovalMode    `json:"default_tools_approval_mode"`
}

func nativeAppsPolicy(selection domain.CodexAppConfiguration) (json.RawMessage, error) {
	if selection.Validate() != nil {
		return nil, incompatible()
	}
	apps := map[string]any{
		"_default": nativeAppsDefault{Reviewer: domain.CodexReviewerUser, Destructive: true, OpenWorld: true, ApprovalMode: nativeAppPrompt},
	}
	for _, id := range selection.AppIDs {
		// _default is a native configuration key, never a connector identity.
		if id == "_default" {
			return nil, incompatible()
		}
		apps[id] = nativeAppSelection{Enabled: true, Reviewer: domain.CodexReviewerUser, ApprovalMode: nativeAppPrompt}
	}
	encoded, err := json.Marshal(apps)
	if err != nil {
		return nil, incompatible()
	}
	return encoded, nil
}

// A configuration write is not a runtime refresh or a revocation receipt.
// Only an unoverridden write in the retained original private home is eligible
// for the separate original-thread catalog barrier.
type nativeAppsWriteResult struct {
	Status     string          `json:"status"`
	Version    string          `json:"version"`
	FilePath   string          `json:"filePath"`
	Overridden json.RawMessage `json:"overriddenMetadata"`
}

func decodeNativeAppsWrite(raw json.RawMessage, originalPath string) (nativeAppsWriteResult, error) {
	var result nativeAppsWriteResult
	if domain.DecodeBounded(raw, &result, 16<<10) != nil || result.Status != "ok" || result.FilePath != originalPath || domain.Text(result.Version, "private native configuration version", 1024, true) != nil || string(result.Overridden) != "null" {
		return nativeAppsWriteResult{}, incompatible()
	}
	return result, nil
}
