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

func TestManagedForkPlainAssistantAndReasoningPreserveOriginalHistory(t *testing.T) {
	for _, tc := range []struct {
		kind, raw string
	}{
		{"agentMessage", `{"type":"agentMessage","id":"original-assistant","text":"Original assistant text"}`},
		{"agentMessage", `{"type":"agentMessage","id":"original-assistant","text":"","phase":null,"delivery":null,"memoryCitation":null,"questions":null}`},
		{"agentMessage", `{"type":"agentMessage","id":"original-assistant","text":"Original assistant text","phase":"commentary","questions":[]}`},
		{"agentMessage", `{"type":"agentMessage","id":"original-assistant","text":"Original assistant text","phase":"final_answer"}`},
		{"reasoning", `{"type":"reasoning","id":"original-reasoning"}`},
		{"reasoning", `{"type":"reasoning","id":"original-reasoning","summary":[],"content":[]}`},
		{"reasoning", `{"type":"reasoning","id":"original-reasoning","summary":["first","","third"],"content":["original content"]}`},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			raw := json.RawMessage(tc.raw)
			before := string(raw)
			if !managedForkItem(raw, tc.kind) || string(raw) != before {
				t.Fatal("supported native history rejected or rewritten")
			}
			var value any
			if json.Unmarshal(raw, &value) != nil {
				t.Fatal("invalid fixture")
			}
			normalized, err := json.Marshal(value)
			if err != nil || !equivalentForkJSON(raw, normalized) {
				t.Fatal("normalization changed original native history", err)
			}
		})
	}
}

func TestManagedForkRejectsRichAndMalformedPlainItems(t *testing.T) {
	for _, tc := range []struct {
		kind, raw string
	}{
		{"agentMessage", `{"type":"agentMessage","id":"assistant","text":"text","questions":[{"id":"question"}]}`},
		{"agentMessage", `{"type":"agentMessage","id":"assistant","text":"text","delivery":{"type":"async"}}`},
		{"agentMessage", `{"type":"agentMessage","id":"assistant","text":"text","memoryCitation":{}}`},
		{"agentMessage", `{"type":"agentMessage","id":"assistant"}`},
		{"agentMessage", `{"type":"agentMessage","id":"assistant","text":null}`},
		{"agentMessage", `{"type":"agentMessage","id":"assistant","text":42}`},
		{"agentMessage", `{"type":"agentMessage","id":"assistant","text":"text","phase":"unknown"}`},
		{"agentMessage", `{"type":"agentMessage","id":"assistant","text":"text","questions":{}}`},
		{"agentMessage", `{"type":"agentMessage","id":"assistant","text":"text","extra":true}`},
		{"agentMessage", `{"type":"agentMessage","id":"assistant","text":"text","questions":null,"questions":[]}`},
		{"agentMessage", `{"type":"agentMessage","id":"","text":"text"}`},
		{"agentMessage", `{"type":"reasoning","id":"assistant","text":"text"}`},
		{"reasoning", `{"type":"reasoning","id":"reasoning","encryptedContent":"opaque"}`},
		{"reasoning", `{"type":"reasoning","id":"reasoning","extra":true}`},
		{"reasoning", `{"type":"reasoning","id":"reasoning","summary":null}`},
		{"reasoning", `{"type":"reasoning","id":"reasoning","content":null}`},
		{"reasoning", `{"type":"reasoning","id":"reasoning","summary":[null]}`},
		{"reasoning", `{"type":"reasoning","id":"reasoning","content":[42]}`},
		{"reasoning", `{"type":"reasoning","id":"reasoning","content":{}}`},
		{"reasoning", `{"type":"reasoning","id":"reasoning","summary":[],"summary":[]}`},
		{"reasoning", `{"type":"reasoning","id":""}`},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			if managedForkItem(json.RawMessage(tc.raw), tc.kind) {
				t.Fatal("rich or malformed native history admitted")
			}
		})
	}
}

func TestManagedForkPlainHistoryKeepsItemOrderIdentitiesAndContent(t *testing.T) {
	turns := []json.RawMessage{json.RawMessage(`{"id":"original-turn","items":[{"type":"agentMessage","id":"original-assistant","text":"original text"},{"type":"reasoning","id":"original-reasoning","summary":["original summary"],"content":["original content"]}]}`)}
	proof := (&ForkSource{managedToolHistory: true, turns: turns}).Checkpoint().ForkHistory
	if proof == nil || !proof.matches(turns) {
		t.Fatal("plain history lost original prefix")
	}
	for _, original := range []string{"original-turn", "original-assistant", "original-reasoning", "original text", "original summary", "original content"} {
		changed := []json.RawMessage{json.RawMessage(strings.ReplaceAll(string(turns[0]), original, "changed"))}
		if proof.matches(changed) {
			t.Fatal("native history proof ignored changed identity or content", original)
		}
	}
	reordered := []json.RawMessage{json.RawMessage(`{"id":"original-turn","items":[{"type":"reasoning","id":"original-reasoning","summary":["original summary"],"content":["original content"]},{"type":"agentMessage","id":"original-assistant","text":"original text"}]}`)}
	if proof.matches(reordered) {
		t.Fatal("native item order changed")
	}
}
