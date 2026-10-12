// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"strings"
	"testing"
)

func (f *threadFixture) handleSidechatCompaction(id json.RawMessage, method string, raw json.RawMessage, write func(json.RawMessage, any)) bool {
	if f.mode != "thread-continuation-sidechat" && !strings.HasPrefix(f.mode, "thread-native-review-") {
		return false
	}
	if method == "fixture/sidechat-drift" {
		var input struct {
			Kind string `json:"kind"`
		}
		if domain.Decode(raw, &input) != nil {
			os.Exit(66)
		}
		f.sidechatDrift = input.Kind
		write(id, map[string]any{})
		return true
	}
	if method == "thread/queue/list" {
		write(id, map[string]any{"data": []any{}, "nextCursor": nil})
		return true
	}
	if method != "config/read" && method != "experimentalFeature/list" && method != "thread/compact/start" {
		return false
	}
	out, err := os.OpenFile(os.Getenv("DELIDEV_CODEX_CAPTURE"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(67)
	}
	_ = json.NewEncoder(out).Encode(map[string]any{"method": method, "params": raw})
	out.Close()
	if method == "config/read" {
		features := map[string]bool{"skip_host_skill_discovery": true}
		for _, key := range sidechatDisabledFeatures {
			features[key] = false
		}
		config := map[string]any{"features": features, "sandbox_mode": "read-only", "approval_policy": "never", "approvals_reviewer": "user", "allow_login_shell": false, "web_search": "disabled"}
		if strings.HasPrefix(f.mode, "thread-native-review-") {
			config["review_model"] = "fixture-model"
			delete(config, "sandbox_mode")
			config["default_permissions"] = nativeReviewPermissionProfile
			config["permissions"] = map[string]any{
				nativeReviewPermissionProfile: map[string]any{
					"filesystem": map[string]any{
						":root": "deny", ":minimal": "read", ":workspace_roots": map[string]any{".": "read"},
					},
					"network": map[string]any{"enabled": false},
				},
			}
		}
		if os.Getenv("DELIDEV_CODEX_MANAGED_SIDECHAT") == "1" {
			config["cli_auth_credentials_store"], config["model_provider"], config["forced_login_method"] = "file", "openai", "chatgpt"
			config["model_providers"] = map[string]any{}
			if f.sidechatDrift == "provider" {
				config["model_provider"] = "foreign"
			}
			if f.sidechatDrift == "authentication" {
				config["cli_auth_credentials_store"] = "keyring"
			}
		}
		if f.sidechatDrift == "mcp" {
			config["mcp_servers"] = map[string]any{"foreign": map[string]any{"command": "foreign"}}
		}
		if f.sidechatDrift == "permission" {
			config["sandbox_mode"] = "workspace-write"
		}
		write(id, map[string]any{"config": config, "origins": map[string]any{}, "layers": nil})
		return true
	}
	if method == "experimentalFeature/list" {
		data := []any{}
		for _, name := range append(sidechatDisabledFeatures[:len(sidechatDisabledFeatures):len(sidechatDisabledFeatures)], "skip_host_skill_discovery") {
			enabled := name == "skip_host_skill_discovery" || f.sidechatDrift == "feature" && name == "hooks"
			data = append(data, map[string]any{"name": name, "stage": "stable", "displayName": nil, "description": nil, "announcement": nil, "enabled": enabled, "defaultEnabled": false})
		}
		if f.sidechatDrift == "duplicate-inventory" {
			data = append(data, data[0])
		}
		if f.sidechatDrift == "malformed-inventory" {
			data[0].(map[string]any)["enabled"] = nil
		}
		write(id, map[string]any{"data": data, "nextCursor": nil})
		return true
	}
	write(id, map[string]any{})
	return true
}

func TestSidechatCompactionRechecksChangedNativeAuthorityBeforeOnceOnlySend(t *testing.T) {
	for _, kind := range []string{"mcp", "permission", "feature", "duplicate-inventory", "malformed-inventory", "unchanged"} {
		t.Run(kind, func(t *testing.T) {
			c, capture, source, _ := continuationFixture(t, "sidechat")
			if _, err := c.VerifyContinuation(context.Background(), domain.NewID(), source, ContinueAfterSuccess); err != nil {
				t.Fatal(err)
			}
			fixtureSignal(t, c, "sidechat-drift", map[string]any{"kind": kind})
			action := domain.NewID()
			err := c.StartCompaction(context.Background(), action, source, nil)
			if kind == "unchanged" {
				if err != nil || len(requestsOf(t, capture, "thread/compact/start")) != 1 {
					t.Fatal("original safe compaction was not sent", err)
				}
				if err := c.StartCompaction(context.Background(), action, source, nil); err == nil || len(requestsOf(t, capture, "thread/compact/start")) != 1 {
					t.Fatal("compaction sent twice", err)
				}
			} else if err == nil || c.execution.compaction != nil || len(requestsOf(t, capture, "thread/compact/start")) != 0 {
				t.Fatal("changed Sidechat authority claimed or sent compaction", err)
			}
		})
	}
}

func TestNativeReviewPermissionProfileRejectsBroaderOrInheritedFilesystemAccess(t *testing.T) {
	valid := func() map[string]json.RawMessage {
		features := map[string]bool{"skip_host_skill_discovery": true}
		for _, key := range sidechatDisabledFeatures {
			features[key] = false
		}
		var config map[string]json.RawMessage
		raw, err := json.Marshal(map[string]any{
			"features":        features,
			"approval_policy": "never", "approvals_reviewer": "user", "allow_login_shell": false, "web_search": "disabled",
			"default_permissions": nativeReviewPermissionProfile,
			"permissions": map[string]any{nativeReviewPermissionProfile: map[string]any{
				"filesystem": map[string]any{":root": "deny", ":minimal": "read", ":workspace_roots": map[string]any{".": "read"}},
				"network":    map[string]any{"enabled": false},
			}},
		})
		if err != nil || json.Unmarshal(raw, &config) != nil {
			t.Fatal("could not construct permission profile fixture", err)
		}
		return config
	}
	for _, kind := range []string{"broader-root", "network", "extra-workspace", "legacy-sandbox", "wrong-default"} {
		t.Run(kind, func(t *testing.T) {
			config := valid()
			var permissions map[string]json.RawMessage
			if err := json.Unmarshal(config["permissions"], &permissions); err != nil {
				t.Fatal(err)
			}
			var profile map[string]json.RawMessage
			if err := json.Unmarshal(permissions[nativeReviewPermissionProfile], &profile); err != nil {
				t.Fatal(err)
			}
			var filesystem map[string]json.RawMessage
			if err := json.Unmarshal(profile["filesystem"], &filesystem); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "broader-root":
				filesystem[":root"] = json.RawMessage(`"read"`)
			case "network":
				profile["network"] = json.RawMessage(`{"enabled":true}`)
			case "extra-workspace":
				filesystem[":workspace_roots"] = json.RawMessage(`{".":"read","/outside":"read"}`)
			case "legacy-sandbox":
				config["sandbox_mode"] = json.RawMessage(`"read-only"`)
			case "wrong-default":
				config["default_permissions"] = json.RawMessage(`"read-only"`)
			}
			profile["filesystem"], _ = json.Marshal(filesystem)
			permissions[nativeReviewPermissionProfile], _ = json.Marshal(profile)
			config["permissions"], _ = json.Marshal(permissions)
			if err := validateNativeReviewPermissionProfile(config); err == nil {
				t.Fatal("broader or inherited native read authority accepted")
			}
		})
	}
	config := valid()
	var permissions map[string]json.RawMessage
	if err := json.Unmarshal(config["permissions"], &permissions); err != nil {
		t.Fatal(err)
	}
	permissions["unused-user-profile"] = json.RawMessage(`{"filesystem":{"/other":"read"}}`)
	config["permissions"], _ = json.Marshal(permissions)
	if err := validateNativeReviewPermissionProfile(config); err != nil {
		t.Fatal("unused user profile prevented the selected restricted profile", err)
	}
}

func TestManagedSidechatRechecksCombinedNativeProfileBeforeInputAndCompaction(t *testing.T) {
	for _, operation := range []string{"input", "steer", "compaction"} {
		for _, drift := range []string{"mcp", "permission", "feature", "provider", "authentication", "duplicate-inventory", "malformed-inventory", "unchanged"} {
			t.Run(operation+"/"+drift, func(t *testing.T) {
				t.Setenv("DELIDEV_CODEX_MANAGED_SIDECHAT", "1")
				c, capture, source, _ := continuationFixture(t, "sidechat")
				if _, err := c.VerifyContinuation(context.Background(), domain.NewID(), source, ContinueAfterSuccess); err != nil {
					t.Fatal(err)
				}
				var active TurnResult
				if operation == "steer" {
					var err error
					active, err = c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
					if err != nil {
						t.Fatal(err)
					}
				}
				fixtureSignal(t, c, "sidechat-drift", map[string]any{"kind": drift})
				var err error
				method := "thread/compact/start"
				switch operation {
				case "input":
					method = "turn/start"
					_, err = c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
				case "steer":
					method = "turn/steer"
					_, err = c.Steer(context.Background(), domain.NewID(), domain.NewID(), active.TurnID, input(domain.ExecuteMode))
				default:
					err = c.StartCompaction(context.Background(), domain.NewID(), source, nil)
				}
				if drift == "unchanged" {
					if err != nil || len(requestsOf(t, capture, method)) != 1 {
						t.Fatal("verified managed operation refused", err)
					}
				} else if err == nil || len(requestsOf(t, capture, method)) != 0 {
					t.Fatal("changed managed Sidechat profile sent native input", err)
				}
			})
		}
	}
}
