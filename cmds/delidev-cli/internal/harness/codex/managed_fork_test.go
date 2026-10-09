// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestManagedForkSettledToolEligibilityAndCompletePrefix(t *testing.T) {
	for _, status := range []string{"completed", "failed", "declined"} {
		item := map[string]any{"type": "commandExecution", "id": "original-command", "status": status, "command": "original command", "cwd": "/historical/path", "source": "agent", "commandActions": []any{}, "aggregatedOutput": "original output"}
		if status != "declined" {
			item["exitCode"] = 3
		}
		raw, _ := json.Marshal(item)
		if !settledForkTool(raw, "commandExecution") {
			t.Fatal("settled command rejected", status)
		}
		for _, field := range []string{"id", "command", "cwd", "aggregatedOutput", "exitCode", "source"} {
			changed := map[string]any{}
			for key, value := range item {
				changed[key] = value
			}
			changed[field] = "changed"
			other, _ := json.Marshal(changed)
			if equivalentForkJSON(raw, other) {
				t.Fatal("native tool prefix lost original field", field)
			}
		}
		for _, fault := range []string{"active", "missing-exit", "plugin", "script", "startup", "input", "unknown"} {
			changed := map[string]any{}
			for key, value := range item {
				changed[key] = value
			}
			switch fault {
			case "active":
				changed["status"] = "inProgress"
			case "missing-exit":
				if status == "declined" {
					continue
				}
				delete(changed, "exitCode")
			case "plugin":
				changed["pluginId"] = "plugin"
			case "script":
				changed["scriptPath"] = "script"
			case "startup":
				changed["source"] = "execStartup"
			case "input":
				changed["source"] = "execInput"
			case "unknown":
				changed["extra"] = "foreign"
			}
			bad, _ := json.Marshal(changed)
			if settledForkTool(bad, "commandExecution") {
				t.Fatal("unsupported tool adopted", status, fault)
			}
		}
	}
	for _, status := range []string{"completed", "failed", "declined"} {
		raw := json.RawMessage(`{"type":"fileChange","id":"patch","status":"` + status + `","changes":[]}`)
		if !settledForkTool(raw, "fileChange") {
			t.Fatal("terminal patch refused")
		}
	}
	if settledForkTool(json.RawMessage(`{"type":"fileChange","id":"patch","status":"inProgress","changes":[]}`), "fileChange") {
		t.Fatal("active patch accepted")
	}
}

func TestManagedForkRetainsCompleteHistoryBeforeFirstResume(t *testing.T) {
	turns := []json.RawMessage{json.RawMessage(`{"id":"original-turn","items":[{"id":"original-tool","type":"commandExecution","status":"completed","source":"agent","command":"true","cwd":"/original","aggregatedOutput":"original output","exitCode":0}]}`)}
	source := &ForkSource{managedToolHistory: true, turns: turns}
	checkpoint := source.Checkpoint()
	if checkpoint.ForkHistory == nil || !checkpoint.ForkHistory.matches(turns) {
		t.Fatal("missing complete inherited history proof")
	}
	changed := []json.RawMessage{json.RawMessage(strings.ReplaceAll(string(turns[0]), "original output", "changed output"))}
	if checkpoint.ForkHistory.matches(changed) || checkpoint.ForkHistory.matches(nil) || checkpoint.ForkHistory.matches(append(turns, turns...)) {
		t.Fatal("changed inherited native history authorized continuation")
	}
	if (&ForkSource{turns: turns}).Checkpoint().ForkHistory != nil {
		t.Fatal("API profile gained managed authority")
	}
}
