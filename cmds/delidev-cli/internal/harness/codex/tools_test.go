package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func commandFixture() map[string]any {
	return map[string]any{"type": "commandExecution", "id": "native-command", "status": "inProgress", "command": "cat example.txt", "cwd": "/fixture", "commandActions": []any{map[string]any{"type": "read", "command": "cat example.txt", "name": "example.txt", "path": "/fixture/example.txt"}}, "processId": nil, "aggregatedOutput": nil, "exitCode": nil, "durationMs": nil, "pluginId": nil, "scriptPath": nil}
}

func TestNativeToolLifecyclePreservesObservationsWithoutAuthorizingWork(t *testing.T) {
	c, turn := observationClient()
	item := commandFixture()
	params := map[string]any{"threadId": c.thread, "turnId": turn, "item": item, "startedAtMs": 1}
	e, err := observeFixture(c, "item/started", params)
	if err != nil || e.Kind != ToolStartedEvent || !e.Correlated || e.Late || e.Tool == nil || e.Tool.Command == nil || e.Tool.Kind != CommandTool || e.Tool.Command.Source != AgentCommand || e.Tool.Command.ExitCode != nil || e.Tool.Command.AggregatedOutput != nil || e.Tool.Command.DurationMS != nil || len(e.Tool.Command.Actions) != 1 || *e.Tool.Command.Actions[0].Path != "/fixture/example.txt" {
		t.Fatalf("native command provenance/default/unknown fields changed: %v", err)
	}
	item["status"], item["exitCode"], item["durationMs"], item["aggregatedOutput"] = "failed", 17, 8, "native final output"
	delete(params, "startedAtMs")
	params["completedAtMs"] = 9
	e, err = observeFixture(c, "item/completed", params)
	if err != nil || e.Kind != ToolCompletedEvent || e.Tool.Status != ToolFailed || *e.Tool.Command.ExitCode != 17 || *e.Tool.Command.DurationMS != 8 || *e.Tool.Command.AggregatedOutput != "native final output" || c.execution.turns[turn].Turn.Status != TurnRunning {
		t.Fatal("native tool failure was lost or confused with turn completion")
	}
	item = map[string]any{"type": "fileChange", "id": "native-patch", "status": "completed", "changes": []any{map[string]any{"path": "before.txt", "diff": "@@\n-before\n+after\n", "kind": map[string]any{"type": "update", "move_path": "after.txt"}}}}
	params["item"] = item
	e, err = observeFixture(c, "item/completed", params)
	if err != nil || e.Tool.Kind != PatchTool || e.Tool.Command != nil || len(e.Tool.Changes) != 1 || e.Tool.Changes[0].Kind != UpdatedFile || *e.Tool.Changes[0].MovePath != "after.txt" {
		t.Fatal("native patch operation or move provenance changed")
	}
}

func TestNativeToolUpdatesKeepDeltaPatchAndInputDistinct(t *testing.T) {
	c, turn := observationClient()
	params := map[string]any{"threadId": c.thread, "turnId": turn, "itemId": "native-command", "delta": "partial 한글\n"}
	e, err := observeFixture(c, "item/commandExecution/outputDelta", params)
	if err != nil || e.Kind != ToolOutputEvent || e.TextDelta != "partial 한글\n" || e.Tool != nil || !e.Correlated {
		t.Fatal("command output delta lost its separate native meaning")
	}
	params["stdin"], params["processId"] = "", "native-process"
	delete(params, "delta")
	e, err = observeFixture(c, "item/commandExecution/terminalInteraction", params)
	if err != nil || e.Kind != ToolInputEvent || e.ToolInput == nil || e.ToolInput.Text != "" || e.ToolInput.ProcessID != "native-process" || e.TextDelta != "" {
		t.Fatal("native terminal input was conflated with output or execution authority")
	}
	delete(params, "stdin")
	delete(params, "processId")
	params["changes"] = []any{map[string]any{"path": "new.txt", "diff": "+text", "kind": map[string]any{"type": "add"}}}
	e, err = observeFixture(c, "item/fileChange/patchUpdated", params)
	if err != nil || e.Kind != ToolPatchEvent || e.Tool == nil || e.Tool.Status != "" || e.Tool.Changes[0].Kind != AddedFile {
		t.Fatal("patch update invented a terminal tool status")
	}
	params["delta"] = nil
	if _, err := observeFixture(c, "item/fileChange/patchUpdated", params); err == nil {
		t.Fatal("another event kind's null payload was accepted")
	}
	delete(params, "delta")
	params["threadId"] = domain.NewID()
	e, err = observeFixture(c, "item/fileChange/patchUpdated", params)
	raw, _ := json.Marshal(e)
	if err != nil || e.Kind != NativeExtensionEvent || e.Tool != nil || strings.Contains(string(raw), "new.txt") {
		t.Fatal("subagent patch leaked as a root tool observation")
	}
}

func TestNativeToolProfilesRejectMalformedOrMismatchedObservations(t *testing.T) {
	for _, bad := range []string{"missing-command", "missing-actions", "null-source", "unknown-source", "unknown-field", "missing-cwd", "negative-duration", "exit-overflow", "wrong-phase", "cross-action-null", "unknown-turn"} {
		t.Run(bad, func(t *testing.T) {
			c, turn := observationClient()
			item := commandFixture()
			switch bad {
			case "missing-command":
				delete(item, "command")
			case "missing-actions":
				delete(item, "commandActions")
			case "null-source":
				item["source"] = nil
			case "unknown-source":
				item["source"] = "replacement-source"
			case "unknown-field":
				item["private"] = "unvalidated-value"
			case "missing-cwd":
				delete(item, "cwd")
			case "negative-duration":
				item["durationMs"] = -1
			case "exit-overflow":
				item["exitCode"] = json.Number("2147483648")
			case "wrong-phase":
				item["status"] = "completed"
			case "cross-action-null":
				item["commandActions"] = []any{map[string]any{"type": "unknown", "command": "printf fixture", "path": nil}}
			case "unknown-turn":
				turn = domain.NewID()
			}
			if _, err := observeFixture(c, "item/started", map[string]any{"threadId": c.thread, "turnId": turn, "item": item, "startedAtMs": 1}); err == nil {
				t.Fatal("malformed native tool was normalized")
			}
		})
	}
	for _, kind := range []map[string]any{{"type": "delete", "move_path": nil}, {"type": "rename"}, {"type": "update", "move_path": ""}} {
		raw, _ := json.Marshal(map[string]any{"path": "fixture", "diff": "", "kind": kind})
		if _, err := decodeFileChanges([]json.RawMessage{raw}); err == nil {
			t.Fatal("unsupported patch operation metadata was accepted")
		}
	}
}
