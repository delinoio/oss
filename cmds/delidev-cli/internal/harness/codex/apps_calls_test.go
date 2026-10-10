// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func originalAppCallFixture(completed bool) map[string]any {
	item := map[string]any{"type": "mcpToolCall", "id": "original-app-call", "server": "codex_apps", "tool": "original-native-tool", "status": "inProgress", "arguments": map[string]any{"original": true}, "appContext": map[string]any{"connectorId": "original-app", "linkId": "original-link", "resourceUri": nil, "appName": "Original", "actionName": "Original action"}, "mcpAppUi": nil, "pluginId": nil, "readOnlyHint": nil, "result": nil, "error": nil, "durationMs": nil}
	if completed {
		item["status"] = "completed"
		item["result"] = map[string]any{"content": []any{map[string]any{"type": "text", "text": "Original result"}}, "structuredContent": nil, "_meta": map[string]any{"private-transport-marker": true}}
	}
	return item
}

func TestNativeAppCallRequiresSelectedOriginalServerAndClosedShape(t *testing.T) {
	selection := domain.CodexAppConfiguration{Version: 1, SessionID: domain.NewID(), AccountID: domain.NewID(), Generation: domain.NewID(), AppIDs: []string{"original-app"}}
	raw, _ := json.Marshal(originalAppCallFixture(true))
	tool, err := decodeCodexAppCall(raw, selection, true)
	if err != nil || tool.CodexApp.Identity.AccountID != selection.AccountID || tool.CodexApp.Identity.Generation != selection.Generation {
		t.Fatalf("original call: %v", err)
	}
	public, _ := json.Marshal(tool.CodexApp)
	if strings.Contains(string(public), "private-transport-marker") || strings.Contains(string(public), "mcpAppUi") {
		t.Fatal("private transport/presentation published")
	}
	for _, change := range []func(map[string]any){
		func(m map[string]any) { m["server"] = "configured-mcp" },
		func(m map[string]any) { m["appContext"] = nil },
		func(m map[string]any) { m["appContext"].(map[string]any)["connectorId"] = "unselected" },
		func(m map[string]any) { delete(m["appContext"].(map[string]any), "linkId") },
		func(m map[string]any) { m["status"] = "declined" },
		func(m map[string]any) { m["result"] = nil },
		func(m map[string]any) { delete(m["result"].(map[string]any), "_meta") },
		func(m map[string]any) { delete(m, "readOnlyHint") },
		func(m map[string]any) { m["mcpAppResourceUri"] = nil },
		func(m map[string]any) { m["error"] = map[string]any{} },
		func(m map[string]any) { m["foreignAuthority"] = true },
	} {
		fixture := originalAppCallFixture(true)
		change(fixture)
		raw, _ := json.Marshal(fixture)
		if _, err := decodeCodexAppCall(raw, selection, true); err == nil {
			t.Fatalf("invalid app item accepted: %s", raw)
		}
	}
}

func TestNativeAppCallRejectsDuplicateAndSubstitutedCompletion(t *testing.T) {
	thread, turn := domain.NewID(), domain.NewID()
	selection := domain.CodexAppConfiguration{Version: 1, SessionID: domain.NewID(), AccountID: domain.NewID(), Generation: domain.NewID(), AppIDs: []string{"original-app"}}
	c := &Client{appsProfile: true, thread: thread, managedHome: t.TempDir(), apps: &appsController{original: selection}, execution: &executionState{turns: map[domain.ID]trackedTurn{turn: {Turn: Turn{ID: turn, Status: TurnRunning}}}}}
	started, _ := json.Marshal(originalAppCallFixture(false))
	completed, _ := json.Marshal(originalAppCallFixture(true))
	if _, err := c.observeCodexAppCallLocked(nativewire.Event{Method: "item/completed"}, turn, completed); err == nil {
		t.Fatal("completion adopted an unobserved call")
	}
	event, err := c.observeCodexAppCallLocked(nativewire.Event{Method: "item/started"}, turn, started)
	if err != nil {
		t.Fatal(err)
	}
	// Public observations cannot rewrite the retained original call identity.
	event.Tool.CodexApp.Identity.Arguments[0] = '['
	*event.Tool.CodexApp.Identity.AppName = "Caller changed display"
	if _, err := c.observeCodexAppCallLocked(nativewire.Event{Method: "item/started"}, turn, started); err == nil {
		t.Fatal("duplicate native invocation adopted")
	}
	foreign := originalAppCallFixture(true)
	foreign["arguments"] = map[string]any{"original": false}
	raw, _ := json.Marshal(foreign)
	if _, err := c.observeCodexAppCallLocked(nativewire.Event{Method: "item/completed"}, turn, raw); err == nil || c.apps.calls["original-app-call"].completed {
		t.Fatal("substituted completion altered original call")
	}
	if _, err := c.observeCodexAppCallLocked(nativewire.Event{Method: "item/completed"}, turn, completed); err != nil {
		t.Fatal(err)
	}
	if _, err := c.observeCodexAppCallLocked(nativewire.Event{Method: "item/completed"}, turn, completed); err == nil {
		t.Fatal("completed native invocation replayed")
	}
}

func TestNativeAppHistoryShapePreservesRemovedIDsWithoutLiveAuthority(t *testing.T) {
	item := originalAppCallFixture(true)
	item["appContext"].(map[string]any)["connectorId"] = "old-removed-original-app"
	raw, _ := json.Marshal(item)
	if _, err := decodeNativeCodexAppCall(raw, true); err != nil {
		t.Fatal(err)
	}
	selection := domain.CodexAppConfiguration{Version: 1, SessionID: domain.NewID(), AccountID: domain.NewID(), Generation: domain.NewID(), AppIDs: []string{}}
	if _, err := decodeCodexAppCall(raw, selection, true); err == nil {
		t.Fatal("historical shape granted removed app live authority")
	}
	for _, mutate := range []func(map[string]any){
		func(v map[string]any) { v["server"] = "external-mcp" },
		func(v map[string]any) { v["status"] = "inProgress" },
		func(v map[string]any) { v["arguments"] = nil; delete(v, "arguments") },
		func(v map[string]any) { v["appContext"] = nil },
		func(v map[string]any) { v["result"].(map[string]any)["content"] = nil },
	} {
		fixture := originalAppCallFixture(true)
		mutate(fixture)
		raw, _ := json.Marshal(fixture)
		if _, err := decodeNativeCodexAppCall(raw, true); err == nil {
			t.Fatalf("invalid private history accepted: %s", raw)
		}
	}
}
