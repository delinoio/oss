// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

const mcpStartedFixture = `{"type":"mcpToolCall","id":"mcp-call","server":"native","tool":"list_mcp_resources","arguments":{"private":"argument-sentinel"},"status":"inProgress","result":null,"error":null,"durationMs":null}`
const mcpCompletedFixture = `{"type":"mcpToolCall","id":"mcp-call","server":"native","tool":"list_mcp_resources","arguments":{"private":"argument-sentinel"},"status":"completed","result":{"content":[{"type":"text","text":"Original result"},{"type":"image","data":"private-image-sentinel","mimeType":"image/png"},{"type":"resource_link","uri":"private-resource-sentinel","name":"resource"}],"structuredContent":{"private":"structured-sentinel"}},"error":null,"durationMs":9007199254740993}`

func TestNativeMCPToolLifecycleUsesNegotiatedInertProjection(t *testing.T) {
	for _, provider := range []string{"subscription", "api"} {
		t.Run(provider, func(t *testing.T) {
			c, turn := observationClient()
			c.dynamicToolsEnabled = true
			if provider == "api" {
				c.api = &apiBinding{}
			}
			start, err := observeFixture(c, "item/started", dynamicItemEnvelopeFixture(c, turn, false, mcpStartedFixture))
			if err != nil || start.Kind != DynamicToolEvent || start.DynamicTool.Stage != domain.DynamicStarted {
				t.Fatal("native MCP start rejected", err)
			}
			done, err := observeFixture(c, "item/completed", dynamicItemEnvelopeFixture(c, turn, true, mcpCompletedFixture))
			if err != nil || done.Kind != DynamicToolEvent || done.DynamicTool.Stage != domain.DynamicCompleted {
				t.Fatal("native MCP completion rejected", err)
			}
			snapshot := done.DynamicTool.Snapshot
			if snapshot.Success == nil || !*snapshot.Success || snapshot.DurationMS == nil || *snapshot.DurationMS != 9007199254740993 || snapshot.Content == nil || len(*snapshot.Content) != 4 || *(*snapshot.Content)[0].Text != "Original result" {
				t.Fatal("native outcome, duration or ordered output lost")
			}
			raw, _ := json.Marshal(done.DynamicTool)
			for _, secret := range []string{"argument-sentinel", "private-image-sentinel", "private-resource-sentinel", "structured-sentinel"} {
				if bytes.Contains(raw, []byte(secret)) {
					t.Fatal("private native bytes escaped")
				}
			}
			if !c.dynamicItems["mcp-call"].Completed || c.dynamicItems["mcp-call"].Origin != mcpToolOrigin || len(c.dynamicArrivals) != 0 {
				t.Fatal("MCP observation gained a dynamic response owner")
			}
			if _, err := observeFixture(c, "item/completed", dynamicItemEnvelopeFixture(c, turn, true, mcpCompletedFixture)); err == nil {
				t.Fatal("duplicate completion accepted")
			}
		})
	}
}

func TestNativeMCPRejectsInvalidAndChangedOriginalCalls(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(mcpStartedFixture, `"inProgress"`, `"completed"`, 1),
		strings.Replace(mcpStartedFixture, `"arguments":{`, `"arguments":null,"extra":{`, 1),
		strings.Replace(mcpStartedFixture, `"result":null`, `"result":{"content":[]}`, 1),
		strings.Replace(mcpStartedFixture, `"durationMs":null`, `"durationMs":1`, 1),
		strings.Replace(mcpStartedFixture, `"id":"mcp-call"`, `"id":"mcp-call","id":"other"`, 1),
		strings.Replace(mcpStartedFixture, `"native"`, `"\ud800"`, 1),
	} {
		c, turn := observationClient()
		c.dynamicToolsEnabled = true
		if _, err := observeFixture(c, "item/started", dynamicItemEnvelopeFixture(c, turn, false, raw)); err == nil {
			t.Fatal("malformed MCP start accepted")
		}
	}
	for _, change := range []string{"server", "tool", "arguments"} {
		c, turn := observationClient()
		c.dynamicToolsEnabled = true
		if _, err := observeFixture(c, "item/started", dynamicItemEnvelopeFixture(c, turn, false, mcpStartedFixture)); err != nil {
			t.Fatal(err)
		}
		raw := strings.Replace(mcpCompletedFixture, map[string]string{"server": "native", "tool": "list_mcp_resources", "arguments": "argument-sentinel"}[change], "changed", 1)
		if _, err := observeFixture(c, "item/completed", dynamicItemEnvelopeFixture(c, turn, true, raw)); err == nil {
			t.Fatal("changed MCP call accepted", change)
		}
	}
}

func TestNativeMCPFailureAndPrivateProfiles(t *testing.T) {
	c, turn := observationClient()
	c.dynamicToolsEnabled = true
	raw := strings.Replace(strings.Replace(mcpStartedFixture, `"inProgress"`, `"failed"`, 1), `"error":null`, `"error":{"message":"private-native-error"}`, 1)
	event, err := observeFixture(c, "item/completed", dynamicItemEnvelopeFixture(c, turn, true, raw))
	if err != nil || event.DynamicTool.Snapshot.Success == nil || *event.DynamicTool.Snapshot.Success {
		t.Fatal("native failure lost", err)
	}
	for _, profile := range []string{"legacy", "sidechat", "foreign-thread"} {
		c, turn := observationClient()
		c.dynamicToolsEnabled = profile != "legacy"
		if profile == "sidechat" {
			c.sidechat = "restricted"
		}
		params := dynamicItemEnvelopeFixture(c, turn, false, mcpStartedFixture)
		if profile == "foreign-thread" {
			params["threadId"] = domain.NewID()
		}
		event, err := observeFixture(c, "item/started", params)
		if err != nil || event.Kind != NativeExtensionEvent || len(c.dynamicItems) != 0 {
			t.Fatal("private profile adopted an MCP tool", profile, err)
		}
	}
}

func TestNativeToolOriginsCannotAdoptEachOthersCalls(t *testing.T) {
	c, turn := observationClient()
	c.dynamicToolsEnabled = true
	if _, err := observeFixture(c, "item/started", dynamicItemEnvelopeFixture(c, turn, false, mcpStartedFixture)); err != nil {
		t.Fatal(err)
	}
	prior := c.dynamicItems["mcp-call"]
	snapshot := &domain.DynamicToolSnapshot{Identity: prior.Identity, Status: domain.DynamicDone}
	if _, err := c.observeExternalToolSnapshotLocked(nativewire.Event{Method: "item/completed"}, turn, "mcp-call", snapshot, dynamicToolOrigin); err == nil {
		t.Fatal("dynamic lifecycle adopted an MCP item")
	}
}

func TestNativeWebToolCompletionDoesNotInventSuccess(t *testing.T) {
	c, turn := observationClient()
	c.dynamicToolsEnabled = true
	start := `{"type":"webSearch","id":"web-call","query":"","action":null}`
	done := `{"type":"webSearch","id":"web-call","query":"private-query","action":{"type":"search","query":"private-query"},"results":[{"url":"private-result-locator"}]}`
	if _, err := observeFixture(c, "item/started", dynamicItemEnvelopeFixture(c, turn, false, start)); err != nil {
		t.Fatal(err)
	}
	event, err := observeFixture(c, "item/completed", dynamicItemEnvelopeFixture(c, turn, true, done))
	if err != nil || event.DynamicTool.Snapshot.Success != nil || event.DynamicTool.Snapshot.Status != domain.DynamicDone {
		t.Fatal("web completion invented success", err)
	}
	raw, _ := json.Marshal(event.DynamicTool)
	if bytes.Contains(raw, []byte("private-query")) || bytes.Contains(raw, []byte("private-result-locator")) {
		t.Fatal("web private content escaped")
	}
}
