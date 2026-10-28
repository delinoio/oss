// SPDX-License-Identifier: Apache-2.0
package cursor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

func TestStructuredPatchCases(t *testing.T) {
	for _, tc := range []struct{ name, args, want string }{
		{"edit_file", `{"file_path":"./src/a.go","old_string":"  old","new_string":"new"}`, "*** Begin Patch\n*** Update File: src/a.go\n@@\n-  old\n+  new\n*** End Patch"},
		{"edit_file", `{"path":"a","old_string":"  same","new_string":"same"}`, "*** Begin Patch\n*** Update File: a\n@@\n-  same\n+same\n*** End Patch"},
		{"edit_file", `{"file_path":"a","old_string":"","new_string":"one\ntwo\n"}`, "*** Begin Patch\n*** Add File: a\n+one\n+two\n*** End Patch"},
		{"edit_file", `{"file_path":"a","delete_file":true}`, "*** Begin Patch\n*** Delete File: a\n*** End Patch"},
		{"multi_edit", `{"file_path":"a","edits":[{"old_string":"a","new_string":"b"},{"old_string":"b","new_string":"c"}]}`, "*** Begin Patch\n*** Update File: a\n@@\n-a\n+c\n*** End Patch"},
		{"multi_edit", `{"file_path":"a","edits":[{"old_string":"a","new_string":"b"},{"old_string":"x b","new_string":"x c"}]}`, "*** Begin Patch\n*** Update File: a\n@@\n-a\n+b\n@@\n-x b\n+x c\n*** End Patch"},
	} {
		t.Run(tc.name+"/"+tc.args, func(t *testing.T) {
			var args map[string]any
			json.Unmarshal([]byte(tc.args), &args)
			patch, err := structuredPatch(tc.name, args)
			if err != nil || patch != tc.want {
				t.Fatalf("got %q %v want %q", patch, err, tc.want)
			}
		})
	}
	for _, raw := range []string{
		`{"file_path":"a\n*** Delete File: b","old_string":"a","new_string":"b"}`,
		`{"file_path":"a","replace_all":true,"old_string":"a","new_string":"b"}`,
		`{"file_path":"a","edits":[{"old_string":"a","new_string":"a\n"}]}`,
		`{"file_path":"a","edits":[{"old_string":"a\nb","new_string":"c"},{"old_string":"b","new_string":"d"}]}`,
		`{"file_path":"a","edits":[{"old_string":"","new_string":"b"},{"old_string":"c","new_string":"d"}]}`,
	} {
		var args map[string]any
		json.Unmarshal([]byte(raw), &args)
		if patch, err := structuredPatch("multi_edit", args); err == nil {
			t.Fatalf("unsafe edit became %q", patch)
		}
	}
}
func TestSyntheticEditCatalogAndEvent(t *testing.T) {
	var body map[string]any
	json.Unmarshal([]byte(`{"tools":[{"type":"custom","name":"apply_patch"}]}`), &body)
	tools, _, err := readTools(body, inference.Responses)
	if err != nil || len(tools) != 3 {
		t.Fatalf("catalog %+v %v", tools, err)
	}
	s := newSession("composer-1", tools)
	s.open["call"] = &openCall{Name: "edit_file"}
	events, err := s.commit("call", `{"file_path":"a","old_string":"old","new_string":"new"}`)
	if err != nil || len(events) != 1 || events[0].Name != "apply_patch" || !strings.Contains(events[0].Arguments, "*** Begin Patch") {
		t.Fatalf("converted event %+v %v", events, err)
	}
	body["tool_choice"] = map[string]any{"type": "custom", "name": "apply_patch"}
	tools, _, err = readTools(body, inference.Responses)
	if err != nil || len(tools) != 1 {
		t.Fatal("forced selection gained siblings", err)
	}
	delete(body, "tool_choice")
	body["tools"] = append(body["tools"].([]any), map[string]any{"type": "function", "name": "edit_file"})
	tools, _, err = readTools(body, inference.Responses)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		if tool.WireName == "edit_file" && tool.Synthetic {
			t.Fatal("caller tool was shadowed")
		}
	}
}

func TestSchemaGatedArgumentAliases(t *testing.T) {
	schema, _ := encodeValue(map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "command": map[string]any{"type": "string"}}}, 0)
	tool := tool{Definition: pb(3, schema)}
	for _, tc := range []struct{ raw, want string }{
		{`{"filepath":"alias","path":"explicit","cmd":"pwd","body":"untouched"}`, `{"path":"explicit","command":"pwd","body":"untouched"}`},
		{`{"file":"first","filepath":"second"}`, `{"path":"first"}`},
	} {
		var args, expected map[string]any
		json.Unmarshal([]byte(tc.raw), &args)
		json.Unmarshal([]byte(tc.want), &expected)
		_, got := normalizeArguments(tc.raw, args, tool)
		actual, _ := json.Marshal(got)
		want, _ := json.Marshal(expected)
		if string(actual) != string(want) {
			t.Fatalf("got %s want %s", actual, want)
		}
	}
}
