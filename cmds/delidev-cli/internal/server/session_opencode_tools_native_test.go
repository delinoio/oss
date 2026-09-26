package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

const continuationToolCall = "call_original_continuation_tool"

func prepareOpenCodeContinuationTool(t *testing.T, f *firstDispatchFixture, tool string, permission ...bool) string {
	t.Helper()
	record, err := f.service.Store.Get(context.Background(), domain.JobKind, domain.ID(f.change.WorkspaceJob.Id))
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Decode[domain.Job](record)
	var manifest workspace.Manifest
	if err != nil || domain.Decode(job.Output, &manifest) != nil {
		t.Fatal("missing prepared fixture workspace")
	}
	name := "original-tool-result.txt"
	if len(permission) == 1 && permission[0] {
		name = ".env.original-tool-result"
	}
	path := filepath.Join(manifest.PrimaryPath, name)
	if tool == "read" {
		if err := os.WriteFile(path, []byte("original-inline-tool-sentinel\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func openCodeContinuationToolArguments(tool, path string) string {
	args := map[string]any{"filePath": path}
	if tool == "question" {
		args = map[string]any{"questions": []any{
			map[string]any{"question": "original-inline-tool-sentinel", "header": "Order", "multiple": true, "custom": false, "options": []any{map[string]any{"label": "First", "description": "First option"}, map[string]any{"label": "Second", "description": "Second option"}}},
			map[string]any{"question": "Original unanswered row", "header": "Optional", "custom": true, "options": []any{map[string]any{"label": "First", "description": "First option"}}},
			map[string]any{"question": "Original empty answer", "header": "Empty", "custom": true, "options": []any{map[string]any{"label": "First", "description": "First option"}}},
		}}
	}
	if tool == "bash" {
		// The path is a generated fixture path, but quote it as shell data.
		quoted := "'" + strings.ReplaceAll(path, "'", "'\"'\"'") + "'"
		args = map[string]any{"command": "printf 'original-inline-tool-sentinel\\n' >> " + quoted + "; cat " + quoted}
	}
	raw, _ := json.Marshal(args)
	return string(raw)
}

func openCodeContinuationAnswers() [][]string { return [][]string{{"Second", "First"}, {}, {""}} }

func serveOpenCodeContinuationTool(t *testing.T, w http.ResponseWriter, tool, path string, ids ...string) {
	t.Helper()
	callID := continuationToolCall
	if len(ids) == 0 {
		ids = []string{callID}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	calls := []any{}
	for index, id := range ids {
		calls = append(calls, map[string]any{"index": index, "id": id, "type": "function", "function": map[string]any{"name": tool, "arguments": openCodeContinuationToolArguments(tool, path)}})
	}
	delta := map[string]any{"role": "assistant", "tool_calls": calls}
	for _, chunk := range []map[string]any{
		{"id": "chatcmpl-original-tool", "object": "chat.completion.chunk", "created": 1, "model": "fixture-model", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}},
		{"id": "chatcmpl-original-tool", "object": "chat.completion.chunk", "created": 1, "model": "fixture-model", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}}, "usage": map[string]any{"prompt_tokens": 20, "completion_tokens": 4, "total_tokens": 24}},
	} {
		raw, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

func verifyOpenCodeContinuationTool(t *testing.T, raw []byte, tool, path string) string {
	t.Helper()
	var body struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
			CallID  string          `json:"tool_call_id"`
			Calls   []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if json.Unmarshal(raw, &body) != nil {
		t.Error("invalid provider conversation")
		return ""
	}
	proposals, results := 0, 0
	var result string
	for _, message := range body.Messages {
		for _, call := range message.Calls {
			proposals++
			var original, observed map[string]any
			_ = json.Unmarshal([]byte(openCodeContinuationToolArguments(tool, path)), &original)
			if json.Unmarshal([]byte(call.Function.Arguments), &observed) != nil {
				t.Error("invalid original native tool arguments")
			}
			want, _ := json.Marshal(original)
			got, _ := json.Marshal(observed)
			if message.Role != "assistant" || call.ID != continuationToolCall || call.Type != "function" || call.Function.Name != tool || string(want) != string(got) {
				t.Error("replacement altered native tool call")
			}
		}
		if message.Role == "tool" {
			results++
			if proposals != 1 || message.CallID != continuationToolCall || json.Unmarshal(message.Content, &result) != nil || !strings.Contains(result, "original-inline-tool-sentinel") || strings.Contains(result, "changed-source-after-original-tool") {
				t.Error("replacement altered original native tool output or ordering")
			}
		}
	}
	if proposals != 1 || results != 1 {
		t.Error("replacement repeated or omitted original tool conversation", proposals, results)
	}
	return result
}

func verifyOpenCodeRepeatedRead(t *testing.T, raw []byte, path string, expected int, originalCounts ...int) map[string]string {
	t.Helper()
	var body struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
			CallID  string          `json:"tool_call_id"`
			Calls   []struct {
				ID       string                           `json:"id"`
				Function struct{ Name, Arguments string } `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if json.Unmarshal(raw, &body) != nil {
		t.Error("invalid native remembered Read conversation")
		return nil
	}
	results := map[string]string{}
	proposalIDs := map[string]int{}
	originalCount := 1
	if len(originalCounts) == 1 {
		originalCount = originalCounts[0]
	}
	proposals := 0
	for _, message := range body.Messages {
		for _, call := range message.Calls {
			var args struct {
				FilePath string `json:"filePath"`
			}
			if message.Role != "assistant" || call.ID != fmt.Sprintf("%s_%d", continuationToolCall, proposals) || call.Function.Name != "read" || json.Unmarshal([]byte(call.Function.Arguments), &args) != nil || args.FilePath != path {
				t.Error("changed remembered Read proposal")
			}
			proposalIDs[call.ID] = proposals
			proposals++
		}
		if message.Role == "tool" {
			index, found := proposalIDs[message.CallID]
			var result string
			want := "original-inline-tool-sentinel"
			if index >= originalCount {
				want = "changed-source-after-original-tool"
			}
			if !found || index >= proposals || results[message.CallID] != "" || json.Unmarshal(message.Content, &result) != nil || !strings.Contains(result, want) {
				t.Error("lost or replayed remembered Read result")
			}
			results[message.CallID] = result
		}
	}
	if proposals != expected || len(results) != expected {
		t.Error("repeated or omitted remembered Read conversation", proposals, len(results), expected)
	}
	return results
}
