// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func nativeAppsSelectionFixture() domain.SessionNativeAppSelection {
	return domain.SessionNativeAppSelection{Scope: nativeAppsScopeFixture(), InventoryID: domain.NewID(), Revision: 1, AppIDs: []string{"original"}}
}
func TestSessionNativeAppsProfileRequiresOriginalExclusiveNativeOwner(t *testing.T) {
	selection := nativeAppsSelectionFixture()
	for _, scenario := range []string{"original", "foreign-account-runtime", "API", "sidechat", "unknown-prestart-version", "non-thread"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := Config{NativeApps: &selection, ManagedAuthentication: true, Mode: ThreadProtocol, Version: "0.162.0"}
			switch scenario {
			case "foreign-account-runtime":
				cfg.ManagedAuthentication = false
			case "API":
				cfg.API = &APIConfig{}
			case "sidechat":
				cfg.Sidechat = "invalid"
			case "unknown-prestart-version":
				cfg.Version = ""
			case "non-thread":
				cfg.Mode = SubscriptionProtocol
			}
			err := configureNativeApps(&cfg)
			if (err == nil) != (scenario == "original" || scenario == "unknown-prestart-version") {
				t.Fatal("profile borrowed unsupported native authority")
			}
			if scenario == "original" {
				args := strings.Join(cfg.Process.Args, " ")
				for _, expected := range []string{"apps._default.enabled=false", "features.tool_call_mcp_elicitation=false", `apps."original".enabled=true`, `default_tools_approval_mode="prompt"`, `approvals_reviewer="user"`} {
					if !strings.Contains(args, expected) {
						t.Fatal("original per-call policy lost", expected)
					}
				}
				copy := cloneNativeApps(cfg.NativeApps)
				copy.AppIDs[0] = "foreign"
				if selection.AppIDs[0] != "original" {
					t.Fatal("frozen selection aliased caller state")
				}
			}
		})
	}
}
func TestSessionNativeAppsPolicyRejectsInheritedEffectAndReviewerBypasses(t *testing.T) {
	baseline := `{"_default":{"enabled":false,"default_tools_approval_mode":"prompt","approvals_reviewer":"user"},"original":{"enabled":true,"default_tools_approval_mode":"prompt","approvals_reviewer":"user"}}`
	for _, scenario := range []string{"original", "unselected-enabled", "tool-auto", "tool-approve", "link-approve", "link-guardian", "default-enabled", "partial", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			raw := baseline
			switch scenario {
			case "unselected-enabled":
				raw = strings.TrimSuffix(raw, "}") + `,"foreign":{"enabled":true}}`
			case "tool-auto":
				raw = strings.Replace(raw, `"original":{"enabled":true`, `"original":{"tools":{"call":{"approval_mode":"auto"}},"enabled":true`, 1)
			case "tool-approve":
				raw = strings.Replace(raw, `"original":{"enabled":true`, `"original":{"tools":{"call":{"approval_mode":"approve"}},"enabled":true`, 1)
			case "link-approve":
				raw = strings.Replace(raw, `"original":{"enabled":true`, `"original":{"links":{"account":{"default_tools_approval_mode":"approve"}},"enabled":true`, 1)
			case "link-guardian":
				raw = strings.Replace(raw, `"original":{"enabled":true`, `"original":{"links":{"account":{"approvals_reviewer":"guardian"}},"enabled":true`, 1)
			case "default-enabled":
				raw = strings.Replace(raw, `"enabled":false`, `"enabled":true`, 1)
			case "partial":
				raw = `{"_default":{"enabled":false,"default_tools_approval_mode":"prompt","approvals_reviewer":"user"}}`
			case "unknown":
				raw = strings.Replace(raw, `"original":{"enabled":true`, `"original":{"grant_all":true,"enabled":true`, 1)
			}
			err := validateNativeAppsPolicies(json.RawMessage(raw), nativeAppsSelectionFixture())
			if (err == nil) != (scenario == "original") {
				t.Fatal("unsafe native policy accepted", scenario, err)
			}
		})
	}
}

func TestSessionNativeAppsInitializedUserAgentOwnsProfileWithoutProbe(t *testing.T) {
	selected := nativeAppsSelectionFixture()
	empty := selected
	empty.AppIDs = []string{}
	for _, scenario := range []struct {
		agent           string
		selectedAllowed bool
		version         string
	}{
		{"delidev/0.162.0 (fixture)", true, "0.162.0"},
		{"delidev/0.161.0 (fixture)", false, "0.161.0"},
		{"delidev/unknown (fixture)", false, "unknown"},
		{"delidev/ (fixture)", false, ""},
	} {
		t.Run(scenario.agent, func(t *testing.T) {
			observed := nativeInitializedVersion(scenario.agent)
			if observed != scenario.version {
				t.Fatal("actual version was guessed", observed)
			}
			if (nativeAppsInitialized(&selected, observed) == nil) != scenario.selectedAllowed {
				t.Fatal("selected Apps borrowed unsupported original profile")
			}
			if nativeAppsInitialized(&empty, observed) != nil || nativeAppsInitialized(nil, observed) != nil {
				t.Fatal("optional unavailable inventory blocked ordinary initialization")
			}
		})
	}
}
