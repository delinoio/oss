package opencode

import (
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
)

func fixtureAssistant() map[string]any {
	return map[string]any{
		"id": "msg_01960dcbe1fbABCDEFGHIJKLMN", "sessionID": fixtureSessionID, "role": "assistant", "time": map[string]any{"created": 1235},
		"parentID": fixtureMessageID, "modelID": "private-model", "providerID": "private-provider", "mode": "build", "agent": "build",
		"path": map[string]any{"cwd": "/private/workspace", "root": "/private/root"}, "cost": 0,
		"tokens": map[string]any{"input": 20, "output": 4, "reasoning": 0, "cache": map[string]any{"read": 0, "write": 0}},
	}
}

func TestNativeMessageCompletionAndErrorAreIndependent(t *testing.T) {
	value := fixtureAssistant()
	for _, state := range []string{"partial", "finish", "completed", "error"} {
		switch state {
		case "finish":
			value["finish"] = "stop"
		case "completed":
			value["time"].(map[string]any)["completed"] = 1236
		case "error":
			value["error"] = map[string]any{"name": "APIError", "data": map[string]any{"message": "private-diagnostic", "isRetryable": false, "statusCode": 401}}
		}
		raw, _ := json.Marshal(value)
		message, err := decodeNativeMessage(raw)
		if err != nil || message.Role != AssistantMessageRole || message.User != nil || message.Assistant == nil || message.Assistant.ParentID != fixtureMessageID {
			t.Fatalf("native assistant: %v", err)
		}
		assistant := message.Assistant
		if (assistant.Finish != nil) != (state != "partial") || (assistant.Completed != nil) != (state == "completed" || state == "error") || (assistant.Error != nil) != (state == "error") {
			t.Fatal("finish, completion timestamp or native error collapsed")
		}
		encoded, _ := json.Marshal(message)
		for _, secret := range []string{"private-diagnostic", "/private/workspace", "/private/root", "private-model"} {
			if strings.Contains(string(encoded), secret) {
				t.Fatal("private native message escaped ordinary serialization")
			}
		}
	}
}

func TestNativeUsagePreservesUnknownAndExactCostSpelling(t *testing.T) {
	value := fixtureAssistant()
	value["cost"] = json.RawMessage(`0.000000123456789123456789`)
	for _, total := range []any{nil, 0, 24} {
		if total != nil {
			value["tokens"].(map[string]any)["total"] = total
		}
		raw, _ := json.Marshal(value)
		message, err := decodeNativeMessage(raw)
		if err != nil || message.Assistant.Cost.String() != "0.000000123456789123456789" || (message.Assistant.Usage.Total == nil) != (total == nil) {
			t.Fatalf("native accounting spelling/absence: %v", err)
		}
		if total != nil && *message.Assistant.Usage.Total != uint64(total.(int)) {
			t.Fatal("reported total replaced by derived counters")
		}
	}
	for _, invalid := range []any{nil, -1, 0.5, "20", 9007199254740992.0} {
		value := fixtureAssistant()
		value["tokens"].(map[string]any)["input"] = invalid
		raw, _ := json.Marshal(value)
		if _, err := decodeNativeMessage(raw); err == nil {
			t.Fatal("invalid native usage acquired accounting meaning")
		}
	}
}

func TestNativeErrorClassificationExcludesDiagnostics(t *testing.T) {
	sentinel := "private-error-reflection-sentinel"
	for _, test := range []struct {
		kind NativeErrorKind
		data map[string]any
	}{
		{ProviderAuthErrorKind, map[string]any{"providerID": sentinel, "message": sentinel}},
		{UnknownErrorKind, map[string]any{"message": sentinel, "ref": sentinel}},
		{OutputLengthErrorKind, map[string]any{}},
		{AbortedErrorKind, map[string]any{"message": sentinel}},
		{StructuredErrorKind, map[string]any{"message": sentinel, "retries": 3}},
		{ContextErrorKind, map[string]any{"message": sentinel, "responseBody": sentinel}},
		{ContentErrorKind, map[string]any{"message": sentinel}},
		{APIErrorKind, map[string]any{"message": sentinel, "isRetryable": false, "statusCode": 429, "responseHeaders": map[string]any{sentinel: sentinel}, "responseBody": sentinel, "metadata": map[string]any{sentinel: sentinel}}},
	} {
		raw, _ := json.Marshal(map[string]any{"name": test.kind, "data": test.data})
		value, err := decodeNativeError(raw)
		if err != nil || value.Kind != test.kind {
			t.Fatalf("native error %s: %v", test.kind, err)
		}
		encoded, _ := json.Marshal(value)
		if strings.Contains(string(encoded), sentinel) {
			t.Fatal("native diagnostic reflection escaped classification")
		}
		if test.kind == APIErrorKind && (value.Retryable == nil || *value.Retryable || value.StatusCode == nil || *value.StatusCode != 429) {
			t.Fatal("native retry/status observation was inferred or lost")
		}
		test.data["unexpected"] = sentinel
		raw, _ = json.Marshal(map[string]any{"name": test.kind, "data": test.data})
		if _, err := decodeNativeError(raw); err == nil || strings.Contains(err.Error(), sentinel) {
			t.Fatal("unknown native diagnostic fields accepted or exposed")
		}
	}
}

func TestNativeMessageRejectsAliasesAndContradictions(t *testing.T) {
	for _, mutate := range []func(map[string]any){
		func(v map[string]any) { v["ID"] = v["id"]; delete(v, "id") },
		func(v map[string]any) { v["parentID"] = v["id"] },
		func(v map[string]any) { v["sessionID"] = fixtureMessageID },
		func(v map[string]any) { v["time"].(map[string]any)["completed"] = 1234 },
		func(v map[string]any) { v["time"].(map[string]any)["created"] = nil },
		func(v map[string]any) { v["time"].(map[string]any)["Created"] = 1235 },
		func(v map[string]any) { v["path"].(map[string]any)["Cwd"] = "/private/workspace" },
		func(v map[string]any) { v["tokens"].(map[string]any)["cache"] = nil },
		func(v map[string]any) { v["tokens"].(map[string]any)["Total"] = 24 },
		func(v map[string]any) { v["finish"] = "success" },
		func(v map[string]any) { v["finish"] = "other" },
		func(v map[string]any) { v["finish"] = nil },
		func(v map[string]any) { v["variant"] = nil },
		func(v map[string]any) { v["summary"] = nil },
		func(v map[string]any) { v["cost"] = "0.25" },
		func(v map[string]any) { v["cost"] = -1 },
		func(v map[string]any) {
			v["error"] = map[string]any{"name": "APIError", "data": map[string]any{"message": "private", "isRetryable": nil}}
		},
	} {
		value := fixtureAssistant()
		mutate(value)
		raw, _ := json.Marshal(value)
		if _, err := decodeNativeMessage(raw); err == nil {
			t.Fatal("malformed native assistant accepted")
		}
	}
}

func TestNativeStructuredAndUserContextStayPrivateAndCannotAcknowledgeChangedInput(t *testing.T) {
	value := fixtureAssistant()
	value["structured"] = nil
	value["summary"] = false
	raw, _ := json.Marshal(value)
	message, err := decodeNativeMessage(raw)
	if err != nil || string(message.Assistant.Structured) != "null" || message.Assistant.Summary == nil || *message.Assistant.Summary {
		t.Fatal("explicit structured null or summary false lost")
	}
	input := sessionInput{receipt: InputReceipt{SessionID: fixtureSessionID, MessageID: fixtureMessageID, PartID: fixturePartID}, digest: sha256.Sum256([]byte("private input"))}
	for _, mutate := range []func(map[string]any){
		func(v map[string]any) { v["system"] = "private-context-sentinel" },
		func(v map[string]any) { v["tools"] = map[string]any{"private-context-sentinel": true} },
		func(v map[string]any) {
			v["format"] = map[string]any{"type": "json_schema", "schema": map[string]any{"description": "private-context-sentinel"}, "retryCount": 2}
		},
		func(v map[string]any) { v["model"].(map[string]any)["variant"] = "private-context-sentinel" },
	} {
		root := fixtureObject(t, fixtureInput(input.receipt, fixtureSettings(), "private input"))
		info := root["info"].(map[string]any)
		mutate(info)
		raw, _ := json.Marshal(info)
		message, err := decodeNativeMessage(raw)
		if err != nil || message.User == nil {
			t.Fatalf("valid native user context: %v", err)
		}
		serialized, _ := json.Marshal(message)
		if strings.Contains(string(serialized), "private-context-sentinel") {
			t.Fatal("native context escaped serialization")
		}
		raw, _ = json.Marshal(root)
		if _, err := validateStoredInput(raw, fixtureSettings(), input); err == nil {
			t.Fatal("changed original user context acknowledged submitted input")
		}
	}
}
