// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestEffectiveAppsRejectsOverridesAndPersistentToolGrants(t *testing.T) {
	selection := domain.CodexAppConfiguration{Version: 1, SessionID: domain.NewID(), AccountID: domain.NewID(), Generation: domain.NewID(), AppIDs: []string{"original"}}
	valid, err := nativeAppsPolicy(selection)
	if err != nil || verifyEffectiveApps(valid, selection) != nil {
		t.Fatalf("original policy rejected: %v", err)
	}
	for _, mutation := range []func(map[string]any){
		func(m map[string]any) { m["_default"].(map[string]any)["enabled"] = true },
		func(m map[string]any) { m["original"].(map[string]any)["enabled"] = false },
		func(m map[string]any) { m["original"].(map[string]any)["default_tools_approval_mode"] = "approve" },
		func(m map[string]any) { m["original"].(map[string]any)["approvals_reviewer"] = "auto_review" },
		func(m map[string]any) { m["original"].(map[string]any)["links"] = map[string]any{} },
		func(m map[string]any) { m["original"].(map[string]any)["tools"] = map[string]any{} },
		func(m map[string]any) { m["original"].(map[string]any)["default_tools_enabled"] = true },
		func(m map[string]any) { m["foreign"] = map[string]any{"enabled": true} },
		func(m map[string]any) { delete(m, "original") },
	} {
		var m map[string]any
		if json.Unmarshal(valid, &m) != nil {
			t.Fatal("invalid fixture")
		}
		mutation(m)
		raw, _ := json.Marshal(m)
		if verifyEffectiveApps(raw, selection) == nil {
			t.Fatalf("effective app override accepted: %s", raw)
		}
	}
}
