// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func sidechatConfigFixture() map[string]any {
	features := map[string]any{"skip_host_skill_discovery": true}
	for _, key := range sidechatDisabledFeatures {
		features[key] = false
	}
	return map[string]any{
		"features": features, "mcp_servers": map[string]any{}, "plugins": map[string]any{}, "hooks": map[string]any{}, "notify": []string{},
		"sandbox_mode": "read-only", "approval_policy": "never", "approvals_reviewer": "user", "allow_login_shell": false, "web_search": "disabled",
	}
}

func TestSidechatRejectsMergedExternalAuthority(t *testing.T) {
	for _, change := range []string{"valid", "mcp", "plugins", "hooks", "notify", "network", "approval", "login-shell", "managed-feature", "missing-feature", "null-feature", "discover-skills"} {
		t.Run(change, func(t *testing.T) {
			config := sidechatConfigFixture()
			features := config["features"].(map[string]any)
			switch change {
			case "mcp", "plugins", "hooks":
				field := change
				if change == "mcp" {
					field = "mcp_servers"
				}
				config[field] = map[string]any{"inherited": map[string]any{"enabled": false}}
			case "notify":
				config["notify"] = []string{"external-command"}
			case "network":
				config["web_search"] = "live"
			case "approval":
				config["approval_policy"] = "on-request"
			case "login-shell":
				config["allow_login_shell"] = true
			case "managed-feature":
				features["hooks"] = true
			case "missing-feature":
				delete(features, "request_permissions_tool")
			case "null-feature":
				features["plugins"] = nil
			case "discover-skills":
				features["skip_host_skill_discovery"] = false
			}
			raw, _ := json.Marshal(config)
			var merged map[string]json.RawMessage
			if json.Unmarshal(raw, &merged) != nil {
				t.Fatal("invalid fixture")
			}
			if err := validateSidechatConfig(merged); (err == nil) != (change == "valid" || change == "subscription") {
				t.Fatal("merged authority classification", err)
			}
		})
	}
}

func TestSidechatProfileBeforeLaunchAndPermissionOverlay(t *testing.T) {
	for _, change := range []string{"valid", "unknown", "probe", "subscription", "mixed-auth", "model-observation", "title"} {
		t.Run(change, func(t *testing.T) {
			cfg := Config{Sidechat: ReadOnlySidechatV1, Mode: ThreadProtocol}
			switch change {
			case "unknown":
				cfg.Sidechat = "future"
			case "probe":
				cfg.Mode = ProbeProtocol
			case "subscription":
				cfg.ManagedAuthentication = true
			case "mixed-auth":
				cfg.ManagedAuthentication = true
				cfg.API = &APIConfig{}
			case "model-observation":
				cfg.ModelObservation = true
			case "title":
				cfg.API = &APIConfig{TitleProfile: true}
			}
			if err := configureSidechat(&cfg); (err == nil) != (change == "valid" || change == "subscription") {
				t.Fatal("unverified profile admitted", err)
			}
			if change == "valid" {
				for _, flag := range []string{`features.hooks=false`, `features.plugins=false`, `approval_policy="never"`, `sandbox_mode="read-only"`, `notify=[]`} {
					if !slices.Contains(cfg.Process.Args, flag) {
						t.Fatal("missing pre-launch restriction")
					}
				}
			}
		})
	}
	source := EffectiveSettings{Model: "m", Provider: "p", ApprovalPolicy: ApprovalOnRequest, ApprovalsReviewer: "user", Sandbox: Sandbox{Type: WorkspaceWrite, NetworkAccess: true}}
	child := source
	child.ApprovalPolicy, child.Sandbox = ApprovalNever, Sandbox{Type: ReadOnly}
	c := &Client{sidechat: ReadOnlySidechatV1}
	if !c.forkDefaults(source, child) || sameForkDefaults(source, child) {
		t.Fatal("Sidechat overlay changed ordinary Fork policy")
	}
	for _, change := range []string{"model", "provider", "network", "write-root", "approval", "reviewer"} {
		bad := child
		switch change {
		case "model":
			bad.Model = "other"
		case "provider":
			bad.Provider = "other"
		case "network":
			bad.Sandbox.NetworkAccess = true
		case "write-root":
			bad.Sandbox.WritableRoots = []string{"foreign"}
		case "approval":
			bad.ApprovalPolicy = ApprovalOnRequest
		case "reviewer":
			bad.ApprovalsReviewer = "guardian"
		}
		if c.forkDefaults(source, bad) {
			t.Fatal("expanded Sidechat permission")
		}
	}
	for _, decision := range []ApprovalDecisionKind{ApprovalAccept, ApprovalAcceptSession, ApprovalExecpolicy, ApprovalNetworkPolicy} {
		_, err := c.RespondApproval(context.Background(), domain.NewID(), domain.NewID(), domain.NewID(), ApprovalDecision{Kind: decision})
		assertCode(t, err, domain.Unsupported)
	}
	_, err := c.GrantPermissions(context.Background(), domain.NewID(), domain.NewID(), domain.NewID(), PermissionGrant{})
	assertCode(t, err, domain.Unsupported)
	if strings.Contains(strings.Join(sidechatDisabledFeatures, " "), "respect_system_proxy") {
		t.Fatal("Sidechat rewrote original account route")
	}
}
