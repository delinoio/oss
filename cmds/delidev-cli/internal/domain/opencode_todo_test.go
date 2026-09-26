package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOpenCodeTodoInputDistinguishesOmittedAndEmpty(t *testing.T) {
	for _, raw := range []string{`{}`, `{"todos":[]}`, `{"todos":[{"content":"","status":"waiting","priority":"urgent"}]}`} {
		var input OpenCodeTodoInput
		if Decode([]byte(raw), &input) != nil {
			t.Fatal("valid native input rejected")
		}
		encoded, err := json.Marshal(input)
		if err != nil || string(encoded) != raw {
			t.Fatal("native proposal or exact empty list changed")
		}
	}
	for _, raw := range []string{`null`, `{"todos":null}`, `{"Todos":[]}`, `{"todos":[null]}`, `{"todos":[{"content":"x","priority":"high"}]}`} {
		var input OpenCodeTodoInput
		if Decode([]byte(raw), &input) == nil {
			t.Fatalf("incomplete todo accepted: %s", raw)
		}
	}
}

func TestOpenCodeTodoMetadataRetainsAbsentAndEmptyLists(t *testing.T) {
	for _, raw := range []string{`{}`, `{"interrupted":true}`, `{"todos":[],"truncated":false}`} {
		var metadata OpenCodeTodoMetadata
		if Decode([]byte(raw), &metadata) != nil {
			t.Fatal("original metadata rejected")
		}
		out, err := json.Marshal(metadata)
		if err != nil || string(out) != raw {
			t.Fatal("metadata list absence or clearing changed")
		}
	}
	var metadata OpenCodeTodoMetadata
	if Decode([]byte(`{"todos":null}`), &metadata) == nil {
		t.Fatal("null became absent native list")
	}
}

func TestOpenCodeTodoProgressRejectsMixedAndOversizedEvidence(t *testing.T) {
	for _, name := range []string{"plan", "diff", "missing-list", "event", "count", "text", "aggregate"} {
		t.Run(name, func(t *testing.T) {
			u := ExecutionProgressUpdate{ID: NewID(), Progress: NativeProgress{Kind: OpenCodeTodoProgressKind, Todo: &OpenCodeTodoProgress{NativeEventID: "evt_01960dcbe1faABCDEFGHIJKLMN", Todos: []OpenCodeTodo{}}}}
			switch name {
			case "plan":
				u.Progress.Plan = &NativePlan{Steps: []PlanStep{}}
			case "diff":
				value := ""
				u.Progress.Diff = &value
			case "missing-list":
				u.Progress.Todo.Todos = nil
			case "event":
				u.Progress.Todo.NativeEventID = "msg_01960dcbe1faABCDEFGHIJKLMN"
			case "count":
				u.Progress.Todo.Todos = make([]OpenCodeTodo, MaxArtifactParts+1)
			case "text":
				u.Progress.Todo.Todos = []OpenCodeTodo{{Content: strings.Repeat("x", MaxMessageText+1)}}
			case "aggregate":
				u.Progress.Todo.Todos = []OpenCodeTodo{{Content: strings.Repeat("x", MaxMessageText)}, {Content: strings.Repeat("y", MaxMessageText)}}
			}
			if u.Validate() == nil {
				t.Fatal("unsupported progress passed its publication bound")
			}
		})
	}
}
