package apiproxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestTitleResponseAllowsBoundedReasoningOnly(t *testing.T) {
	frames := []struct {
		kind  string
		value map[string]any
	}{
		{"response.reasoning_summary_text.delta", map[string]any{"delta": "summary"}},
		{"response.reasoning_summary_text.done", map[string]any{"text": "summary"}},
		{"response.reasoning_text.delta", map[string]any{"delta": "private reasoning"}},
		{"response.reasoning_text.done", map[string]any{"text": "private reasoning"}},
		{"response.reasoning_summary_part.added", map[string]any{"part": map[string]any{"type": "summary_text", "text": "summary"}}},
		{"response.reasoning_summary_part.done", map[string]any{"part": map[string]any{"type": "summary_text", "text": "summary"}}},
		{"response.output_item.added", map[string]any{"item": map[string]any{"type": "reasoning", "id": "rs_fixture"}}},
		{"response.output_item.done", map[string]any{"item": map[string]any{"type": "reasoning", "id": "rs_fixture", "summary": []any{map[string]any{"type": "summary_text", "text": "summary"}}}}},
		{"response.completed", map[string]any{"response": map[string]any{"status": "completed", "output": []any{
			map[string]any{"type": "reasoning", "id": "rs_fixture", "summary": []any{map[string]any{"type": "summary_text", "text": "summary"}}},
			map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "short title"}}},
		}}}},
	}
	textBytes, reasoningBytes := 0, 0
	for _, test := range frames {
		t.Run(test.kind, func(t *testing.T) {
			object := map[string]any{"type": test.kind}
			for key, value := range test.value {
				object[key] = value
			}
			frame, err := json.Marshal(object)
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]json.RawMessage
			if json.Unmarshal(frame, &decoded) != nil {
				t.Fatal("test frame was not valid JSON")
			}
			if err := validateTitleResponseFrame(test.kind, decoded, frame, &textBytes, &reasoningBytes); err != nil {
				t.Fatalf("bounded reasoning frame was rejected: %v", err)
			}
		})
	}
	if textBytes != 0 || reasoningBytes == 0 || reasoningBytes > domain.MaxAutomaticTitleReasoningBytes {
		t.Fatalf("reasoning changed title output accounting or escaped its bound: text=%d reasoning=%d", textBytes, reasoningBytes)
	}
}

func TestTitleResponseRejectsExcessReasoningAndToolAuthority(t *testing.T) {
	textBytes, reasoningBytes := 0, 0
	kind := "response.reasoning_summary_text.delta"
	data, err := json.Marshal(map[string]any{"type": kind, "delta": strings.Repeat("r", domain.MaxAutomaticTitleReasoningBytes)})
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil {
		t.Fatal("oversized reasoning test frame was not valid JSON")
	}
	if err := validateTitleResponseFrame(kind, object, data, &textBytes, &reasoningBytes); err == nil {
		t.Fatal("reasoning bytes were not bounded")
	}
	tool := []byte(`{"type":"response.output_item.added","item":{"type":"function_call","id":"fc_fixture"}}`)
	if json.Unmarshal(tool, &object) != nil {
		t.Fatal("tool test frame was not valid JSON")
	}
	if err := validateTitleResponseFrame("response.output_item.added", object, tool, &textBytes, &reasoningBytes); err == nil {
		t.Fatal("title relay admitted tool authority with a reasoning item")
	}
}
