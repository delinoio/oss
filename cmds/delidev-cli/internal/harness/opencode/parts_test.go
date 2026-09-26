package opencode

import (
	"encoding/json"
	"strings"
	"testing"
)

func fixturePart(kind PartKind, fields map[string]any) map[string]any {
	value := map[string]any{"id": fixturePartID, "sessionID": fixtureSessionID, "messageID": fixtureMessageID, "type": kind}
	for key, field := range fields {
		value[key] = field
	}
	return value
}

func fixtureCompletedTool() map[string]any {
	return fixturePart(ToolPartKind, map[string]any{
		"callID": "call_private", "tool": "read", "metadata": map[string]any{"private": "private-part-sentinel"},
		"state": map[string]any{"status": "completed", "input": map[string]any{"filePath": "private-part-sentinel"}, "output": "private-part-sentinel", "title": "private-part-sentinel", "metadata": map[string]any{"private": "private-part-sentinel"}, "time": map[string]any{"start": 10, "end": 20}},
	})
}

func TestNativePartsKeepPrivatePayloadsAndDistinctFamilies(t *testing.T) {
	for kind, fields := range map[PartKind]map[string]any{
		TextPartKind:       {"text": "private-part-sentinel", "synthetic": false, "ignored": false, "time": map[string]any{"start": 10, "end": 20}, "metadata": map[string]any{"private": "private-part-sentinel"}},
		ReasoningPartKind:  {"text": "private-part-sentinel", "time": map[string]any{"start": 10}, "metadata": map[string]any{"signature": "private-part-sentinel"}},
		FilePartKind:       {"mime": "text/plain", "filename": "private-part-sentinel", "url": "https://private.invalid/private-part-sentinel"},
		StepStartPartKind:  {"snapshot": "private-part-sentinel"},
		StepFinishPartKind: {"reason": "stop", "snapshot": "private-part-sentinel", "cost": json.RawMessage(`0.000000000123456789`), "tokens": fixtureAssistant()["tokens"]},
		SnapshotPartKind:   {"snapshot": "private-part-sentinel"},
		PatchPartKind:      {"hash": "private-part-sentinel", "files": []string{"private-part-sentinel"}},
		AgentPartKind:      {"name": "explore", "source": map[string]any{"value": "private-part-sentinel", "start": 1, "end": 3}},
		RetryPartKind:      {"attempt": 2, "error": map[string]any{"name": "APIError", "data": map[string]any{"message": "private-part-sentinel", "isRetryable": true}}, "time": map[string]any{"created": 20}},
		CompactionPartKind: {"auto": false, "overflow": false, "tail_start_id": fixtureMessageID},
		SubtaskPartKind:    {"prompt": "private-part-sentinel", "description": "private-part-sentinel", "agent": "explore", "model": inputModel{Provider: "private-provider", Model: "private-model"}, "command": "private-part-sentinel"},
	} {
		t.Run(string(kind), func(t *testing.T) {
			raw, _ := json.Marshal(fixturePart(kind, fields))
			part, err := decodeNativePart(raw)
			if err != nil || part.Kind != kind || part.ID != fixturePartID || part.SessionID != fixtureSessionID || part.MessageID != fixtureMessageID {
				t.Fatalf("native part: %v", err)
			}
			encoded, _ := json.Marshal(part)
			if strings.Contains(string(encoded), "private-part-sentinel") {
				t.Fatal("native private part escaped ordinary serialization")
			}
			if kind == StepFinishPartKind && (part.Step.Cost == nil || part.Step.Cost.String() != "0.000000000123456789" || part.Step.Usage.Total != nil) {
				t.Fatal("step accounting spelling/absence changed")
			}
			if kind == CompactionPartKind && (part.Compaction.Auto || part.Compaction.Overflow == nil || *part.Compaction.Overflow) {
				t.Fatal("native compaction false flags lost")
			}
			if kind == TextPartKind && (part.Text.Synthetic == nil || *part.Text.Synthetic || part.Text.Ignored == nil || *part.Text.Ignored) {
				t.Fatal("native text false flags lost")
			}
		})
	}
}

func TestNativeToolStatesAndResultPayloads(t *testing.T) {
	for _, state := range []map[string]any{
		{"status": "pending", "input": map[string]any{}, "raw": "{\"filePath\":"},
		{"status": "running", "input": map[string]any{"filePath": "private-part-sentinel"}, "time": map[string]any{"start": 10}},
		{"status": "completed", "input": map[string]any{"filePath": "private-part-sentinel"}, "time": map[string]any{"start": 10, "end": 20, "compacted": 30}, "output": "private-part-sentinel", "title": "private-part-sentinel", "metadata": map[string]any{}},
		{"status": "error", "input": map[string]any{"filePath": "private-part-sentinel"}, "time": map[string]any{"start": 10, "end": 20}, "error": "private-part-sentinel"},
	} {
		raw, _ := json.Marshal(fixturePart(ToolPartKind, map[string]any{"callID": "call_private", "tool": "read", "state": state}))
		part, err := decodeNativePart(raw)
		if err != nil || part.Tool == nil || string(part.Tool.State) != state["status"].(string) || part.Tool.CallID != "call_private" {
			t.Fatalf("native tool state: %v", err)
		}
		encoded, _ := json.Marshal(part.Tool)
		if strings.Contains(string(encoded), "private-part-sentinel") {
			t.Fatal("native tool result escaped private body")
		}
	}
}

func TestNativeToolAttachmentsKeepOriginalOwnership(t *testing.T) {
	attachment := fixturePart(FilePartKind, map[string]any{"id": "prt_01960dcbe1fbABCDEFGHIJKLMN", "mime": "image/png", "url": "data:image/png;base64,cHJpdmF0ZQ=="})
	tool := fixtureCompletedTool()
	tool["state"].(map[string]any)["attachments"] = []any{attachment}
	raw, _ := json.Marshal(tool)
	part, err := decodeNativePart(raw)
	if err != nil || len(part.Tool.Attachments) != 1 || part.Tool.Attachments[0].File == nil {
		t.Fatalf("native attachment: %v", err)
	}
	for _, mutate := range []func(map[string]any){
		func(v map[string]any) { v["sessionID"] = "ses_01960dcbe1fbABCDEFGHIJKLMN" },
		func(v map[string]any) { v["messageID"] = "msg_01960dcbe1fbABCDEFGHIJKLMN" },
		func(v map[string]any) { v["id"] = fixturePartID },
		func(v map[string]any) { v["type"] = "tool" },
		func(v map[string]any) { v["url"] = nil },
	} {
		changed := fixtureObject(t, attachment)
		mutate(changed)
		tool["state"].(map[string]any)["attachments"] = []any{changed}
		raw, _ := json.Marshal(tool)
		if _, err := decodeNativePart(raw); err == nil {
			t.Fatal("foreign/recursive attachment accepted")
		}
	}
	tool["state"].(map[string]any)["attachments"] = []any{attachment, attachment}
	raw, _ = json.Marshal(tool)
	if _, err := decodeNativePart(raw); err == nil {
		t.Fatal("duplicate attachment ID accepted")
	}
}

func TestNativePartRejectsMalformedToolStateAndCoordinates(t *testing.T) {
	for _, mutate := range []func(map[string]any){
		func(v map[string]any) { v["ID"] = v["id"]; delete(v, "id") },
		func(v map[string]any) { v["sessionID"] = fixtureMessageID },
		func(v map[string]any) { v["callID"] = "" },
		func(v map[string]any) { v["state"].(map[string]any)["status"] = "success" },
		func(v map[string]any) { v["state"].(map[string]any)["input"] = []any{} },
		func(v map[string]any) { v["state"].(map[string]any)["Input"] = map[string]any{} },
		func(v map[string]any) { v["state"].(map[string]any)["time"].(map[string]any)["end"] = 9 },
		func(v map[string]any) { v["state"].(map[string]any)["time"].(map[string]any)["compacted"] = 19 },
		func(v map[string]any) { v["state"].(map[string]any)["time"].(map[string]any)["end"] = nil },
		func(v map[string]any) { delete(v["state"].(map[string]any), "metadata") },
		func(v map[string]any) { v["state"].(map[string]any)["metadata"] = nil },
		func(v map[string]any) { v["state"].(map[string]any)["attachments"] = nil },
		func(v map[string]any) { v["state"].(map[string]any)["raw"] = "foreign pending field" },
	} {
		value := fixtureCompletedTool()
		mutate(value)
		raw, _ := json.Marshal(value)
		if _, err := decodeNativePart(raw); err == nil {
			t.Fatal("malformed native tool acquired a typed result")
		}
	}
	for _, raw := range []string{
		`{"value":"private","start":3,"end":2}`,
		`{"value":"private","start":0.5,"end":2}`,
		`{"value":"private","start":0,"end":null}`,
		`{"value":"private","Start":0,"end":2}`,
	} {
		if validSourceText([]byte(raw)) {
			t.Fatal("malformed native source coordinates accepted")
		}
	}
}

func TestNativeFileSourceVariantsRemainPrivateReferences(t *testing.T) {
	text := map[string]any{"value": "private-source-sentinel", "start": 2, "end": 4}
	for _, source := range []map[string]any{
		{"type": "file", "text": text, "path": "private-source-sentinel"},
		{"type": "resource", "text": text, "clientName": "private-source-sentinel", "uri": "private-source-sentinel"},
		{"type": "symbol", "text": text, "path": "private-source-sentinel", "name": "private-source-sentinel", "kind": 1, "range": map[string]any{"start": map[string]any{"line": 1, "character": 2}, "end": map[string]any{"line": 1, "character": 4}}},
	} {
		raw, _ := json.Marshal(fixturePart(FilePartKind, map[string]any{"mime": "text/plain", "url": "private-source-sentinel", "source": source}))
		part, err := decodeNativePart(raw)
		if err != nil || part.File.Source == nil {
			t.Fatalf("native file source: %v", err)
		}
		encoded, _ := json.Marshal(part.File)
		if strings.Contains(string(encoded), "private-source-sentinel") {
			t.Fatal("private native source locator escaped ordinary serialization")
		}
		source["unexpected"] = true
		raw, _ = json.Marshal(source)
		if validFileSource(raw) {
			t.Fatal("unknown native source field accepted")
		}
	}
}
