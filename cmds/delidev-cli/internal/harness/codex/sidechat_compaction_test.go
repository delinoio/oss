// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"testing"
)

func (f *threadFixture) handleSidechatCompaction(id json.RawMessage, method string, raw json.RawMessage, write func(json.RawMessage, any)) bool {
	if f.mode != "thread-continuation-sidechat" {
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
		write(id, map[string]any{"data": data, "nextCursor": nil})
		return true
	}
	write(id, map[string]any{})
	return true
}

func TestSidechatCompactionRechecksChangedNativeAuthorityBeforeOnceOnlySend(t *testing.T) {
	for _, kind := range []string{"mcp", "permission", "feature", "unchanged"} {
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

func TestManagedSidechatRechecksCombinedNativeProfileBeforeInputAndCompaction(t *testing.T) {
	for _, operation := range []string{"input", "steer", "compaction"} {
		for _, drift := range []string{"mcp", "permission", "feature", "provider", "authentication", "unchanged"} {
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
