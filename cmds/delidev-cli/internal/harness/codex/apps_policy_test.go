// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestNativeAppsPolicyDeniesUnselectedAndRequiresEachApproval(t *testing.T) {
	selection := domain.CodexAppConfiguration{Version: 1, SessionID: domain.NewID(), AccountID: domain.NewID(), Generation: domain.NewID(), AppIDs: []string{"connector.a", "connector.b"}}
	raw, err := nativeAppsPolicy(selection)
	if err != nil {
		t.Fatal(err)
	}
	var policy map[string]json.RawMessage
	if json.Unmarshal(raw, &policy) != nil || len(policy) != 3 {
		t.Fatal("unexpected app policy")
	}
	var defaults nativeAppsDefault
	if json.Unmarshal(policy["_default"], &defaults) != nil || defaults.Enabled || defaults.ApprovalMode != "prompt" || defaults.Reviewer != "user" {
		t.Fatal("unselected apps or automatic approvals enabled")
	}
	for _, id := range selection.AppIDs {
		var app nativeAppSelection
		if json.Unmarshal(policy[id], &app) != nil || !app.Enabled || app.ApprovalMode != "prompt" || app.Reviewer != "user" {
			t.Fatal("selection did not retain one-call user approval")
		}
	}
	selection.AppIDs = []string{"_default"}
	if _, err := nativeAppsPolicy(selection); err == nil {
		t.Fatal("reserved policy key accepted as an app")
	}
}

func TestNativeAppsWriteDoesNotAcceptOverridesOrForeignHome(t *testing.T) {
	path := "/private/original/config.toml"
	valid := `{"status":"ok","version":"original-version","filePath":"/private/original/config.toml","overriddenMetadata":null}`
	if _, err := decodeNativeAppsWrite(json.RawMessage(valid), path); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"status":"okOverridden","version":"v","filePath":"/private/original/config.toml","overriddenMetadata":null}`,
		`{"status":"ok","version":"v","filePath":"/foreign/config.toml","overriddenMetadata":null}`,
		`{"status":"ok","version":"v","filePath":"/private/original/config.toml"}`,
		`{"status":"ok","version":"v","filePath":"/private/original/config.toml","overriddenMetadata":{}}`,
		`{"status":"ok","version":"v","filePath":"/private/original/config.toml","overriddenMetadata":null,"authority":true}`,
	} {
		if _, err := decodeNativeAppsWrite(json.RawMessage(raw), path); err == nil {
			t.Fatalf("invalid write receipt accepted: %s", raw)
		}
	}
}
