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
