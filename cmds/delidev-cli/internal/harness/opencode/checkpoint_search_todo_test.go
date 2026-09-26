package opencode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckpointSearchPreservesInlineTruncationWithoutArtifacts(t *testing.T) {
	for _, name := range []checkpointToolName{checkpointGlobTool, checkpointGrepTool} {
		for _, fault := range []string{"valid", "truncated", "count", "missing", "null", "artifact", "field", "attachment", "unfinished"} {
			t.Run(string(name)+"/"+fault, func(t *testing.T) {
				tool := checkpointInlineToolFixture(name)
				var metadata map[string]any
				_ = json.Unmarshal(tool.Metadata, &metadata)
				field := "count"
				if name == checkpointGrepTool {
					field = "matches"
				}
				switch fault {
				case "truncated":
					metadata[field], metadata["truncated"] = 100, true
				case "count":
					metadata[field] = -1
				case "missing":
					delete(metadata, "truncated")
				case "null":
					metadata[field] = nil
				case "artifact":
					metadata["outputPath"] = nil
				case "field":
					metadata["unknown"] = false
				case "attachment":
					tool.Attachments = []NativePart{{}}
				case "unfinished":
					tool.State = ToolRunning
				}
				tool.Metadata, _ = json.Marshal(metadata)
				if checkpointInlineTool(tool) != (fault == "valid" || fault == "truncated") {
					t.Fatal("search restoration changed original inline evidence")
				}
			})
		}
	}
}

func TestCheckpointTodoRequiresExactAppliedListAndExplicitInlineResult(t *testing.T) {
	for _, raw := range []string{`{"todos":[],"truncated":false}`, `{"todos":null,"truncated":false}`, `{"truncated":false}`, `{"todos":[{"content":"changed","status":"waiting","priority":"urgent"}],"truncated":false}`, `{"todos":[{"content":"private original result","status":"waiting","priority":"urgent"}],"truncated":true}`} {
		tool := checkpointInlineToolFixture(checkpointTodoTool)
		tool.Metadata = []byte(raw)
		if checkpointInlineTool(tool) {
			t.Fatal("changed or missing original Todo result accepted")
		}
	}
	tool := checkpointInlineToolFixture(checkpointTodoTool)
	if !checkpointInlineTool(tool) {
		t.Fatal("native extension statuses were rewritten")
	}
	tool.Input, tool.Metadata = []byte(`{"todos":[]}`), []byte(`{"todos":[],"truncated":false}`)
	if !checkpointInlineTool(tool) {
		t.Fatal("explicit original Todo clear was lost")
	}
	tool.Input = []byte(`{}`)
	if checkpointInlineTool(tool) {
		t.Fatal("missing Todo input became an explicit clear")
	}
}

func TestCheckpointSearchTodoProofPreservesOriginalHistory(t *testing.T) {
	for _, name := range []checkpointToolName{checkpointGlobTool, checkpointGrepTool, checkpointTodoTool} {
		t.Run(string(name), func(t *testing.T) {
			value, s := checkpointToolsFixture()
			part := &value.History.Messages[0].Parts[0]
			s.observer.parts[part.ID].value.Tool = checkpointInlineToolFixture(name)
			if name == checkpointTodoTool {
				value.History.Todo = &TodoHistoryObservation{EventID: "evt_01960dcbe299ABCDEFGHIJKLMN", Digest: mutationDigest([]byte(`[]`))}
				observed := *value.History.Todo
				s.observer.todo = &observed
			}
			value.Tools = s.checkpointToolHistory(value)
			if value.Tools == nil || value.Tools.Version != 6 || !value.Tools.InteractionFree || checkpointReplacementProfile(value) != nil {
				t.Fatal("original search/Todo state not retained")
			}
			s.predecessor, s.observer = &value, &inputObserver{}
			next := nativeCheckpoint{Project: "global", NativeRoot: "/", Previous: []HistoryObservation{copyHistoryObservation(value.History)}}
			next.Tools = s.checkpointToolHistory(next)
			if next.Tools == nil || next.Tools.Version != 6 || checkpointReplacementProfile(next) != nil {
				t.Fatal("text successor lost prior search/Todo proof")
			}
			if name == checkpointTodoTool {
				next.Previous[0].Todo.Digest = "changed"
				if value.History.Todo.Digest == "changed" {
					t.Fatal("successor changed original Todo proof")
				}
				value.History.Todo = nil
				if validCheckpointTools(value) {
					t.Fatal("Todo result invented an unobserved session update")
				}
			}
			value.Tools.Version = 1
			if validCheckpointTools(value) {
				t.Fatal("legacy proof acquired search/Todo authority")
			}
		})
	}
}

func TestTodoHistoryComparisonUsesOriginalEventAndClosedRead(t *testing.T) {
	for _, mode := range []string{"valid", "clear", "changed", "missing", "event", "nested"} {
		t.Run(mode, func(t *testing.T) {
			original := `[{"content":"private list","status":"waiting","priority":"urgent"}]`
			if mode == "clear" {
				original = `[]`
			}
			expected := &TodoHistoryObservation{EventID: "evt_01960dcbe299ABCDEFGHIJKLMN", Digest: mutationDigest(canonicalNative([]byte(original)))}
			reads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads++
				if r.Method != http.MethodGet || r.URL.Path != "/session/"+fixtureSessionID+"/todo" || r.ContentLength > 0 {
					t.Error("Todo comparison escaped original read authority")
				}
				w.Header().Set("Content-Type", "application/json")
				if mode == "changed" {
					_, _ = io.WriteString(w, `[]`)
				} else if mode == "missing" {
					_, _ = io.WriteString(w, `null`)
				} else {
					_, _ = io.WriteString(w, original)
				}
			}))
			defer server.Close()
			client, transport := probeHTTPClient(strings.TrimPrefix(server.URL, "http://"))
			defer transport.CloseIdleConnections()
			s := &sessionAPI{creation: &sessionCreation{identity: sessionIdentity{id: fixtureSessionID}}, client: client, origin: server.URL, password: "private-fixture", cwd: "/private/workspace", alive: func() error { return nil }}
			if mode == "event" {
				expected.EventID = ""
			}
			if mode == "nested" {
				s.todoRead = true
			}
			err := s.compareTodoHistory(context.Background(), expected)
			if (err == nil) != (mode == "valid" || mode == "clear") || s.todoRead != (mode == "nested") {
				t.Fatal("changed native Todo state accepted or read gate leaked")
			}
			if (reads == 0) != (mode == "event" || mode == "nested") {
				t.Fatal("unowned Todo evidence authorized a read")
			}
			if mode != "nested" {
				if _, _, err := s.request(context.Background(), http.MethodGet, "/session/"+fixtureSessionID+"/todo", nil, http.StatusOK); err == nil {
					t.Fatal("temporary Todo read authority survived comparison")
				}
			}
		})
	}
}

func TestCheckpointTodoCannotInventOrReplaceOriginalListObservation(t *testing.T) {
	value, s := checkpointToolsFixture()
	value.History.Todo = &TodoHistoryObservation{EventID: "evt_01960dcbe299ABCDEFGHIJKLMN", Digest: mutationDigest([]byte(`[]`))}
	if s.checkpointToolHistory(value) != nil {
		t.Fatal("unobserved list gained checkpoint authority")
	}
	observed := *value.History.Todo
	s.observer.todo = &observed
	if proof := s.checkpointToolHistory(value); proof == nil || proof.Version != 6 {
		t.Fatal("independent observed session list was dropped")
	}
	observed.EventID = "evt_01960dcbe298ABCDEFGHIJKLMN"
	if s.checkpointToolHistory(value) != nil {
		t.Fatal("new event silently replaced original closed history")
	}
	value.History.Todo = nil
	if s.checkpointToolHistory(value) != nil {
		t.Fatal("original list observation was silently discarded")
	}
	prior := HistoryObservation{Todo: &observed}
	checkpoint := nativeCheckpoint{Previous: []HistoryObservation{prior}}
	if latestCheckpointTodo(checkpoint) != prior.Todo {
		t.Fatal("later text input lost original Todo state")
	}
	clear := TodoHistoryObservation{EventID: "evt_01960dcbe299ABCDEFGHIJKLMN", Digest: mutationDigest([]byte(`[]`))}
	checkpoint.History.Todo = &clear
	if latestCheckpointTodo(checkpoint) != &clear {
		t.Fatal("explicit later clear did not replace prior list state")
	}
}
