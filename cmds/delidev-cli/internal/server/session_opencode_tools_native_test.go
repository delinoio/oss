package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
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
	if tool == "glob" || tool == "grep" {
		path = filepath.Join(manifest.PrimaryPath, "original-inline-tool-sentinel.txt")
	}
	if tool == "read" || tool == "glob" || tool == "grep" || tool == "edit" || tool == "apply_patch" {
		if err := os.WriteFile(path, []byte("original-inline-tool-sentinel\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if tool == "apply_patch" {
		for _, suffix := range []string{".move", ".delete"} {
			if err := os.WriteFile(path+suffix, []byte("original-inline-tool-sentinel\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return path
}

func openCodeContinuationToolArguments(tool, path string) string {
	args := map[string]any{"filePath": path}
	if tool == "write" {
		args = map[string]any{"filePath": path, "content": "original-inline-tool-sentinel\n"}
	}
	if tool == "edit" {
		args = map[string]any{"filePath": path, "oldString": "original-inline-tool-sentinel", "newString": "original-inline-tool-sentinel edited"}
	}
	if tool == "apply_patch" {
		args = map[string]any{"patchText": "*** Begin Patch\n*** Update File: " + path + "\n@@\n-original-inline-tool-sentinel\n+original-inline-tool-sentinel edited\n*** Add File: " + path + ".added\n+original added file\n*** Update File: " + path + ".move\n*** Move to: " + path + ".moved\n@@\n-original-inline-tool-sentinel\n+original moved file\n*** Delete File: " + path + ".delete\n*** End Patch"}
	}
	if tool == "glob" {
		args = map[string]any{"pattern": "*.txt", "path": filepath.Dir(path)}
	}
	if tool == "grep" {
		args = map[string]any{"pattern": "original-inline-tool-sentinel", "include": "*.txt", "path": filepath.Dir(path)}
	}
	if tool == "todowrite" {
		args = map[string]any{"todos": []domain.OpenCodeTodo{{Content: "original-inline-tool-sentinel", Status: domain.OpenCodeTodoRunning, Priority: domain.OpenCodeTodoHigh}, {Content: "Original second task", Status: "waiting", Priority: "urgent"}}}
	}
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
	serveOpenCodeContinuationArguments(t, w, tool, openCodeContinuationToolArguments(tool, path), ids...)
}

func serveOpenCodeContinuationArguments(t *testing.T, w http.ResponseWriter, tool, arguments string, ids ...string) {
	t.Helper()
	callID := continuationToolCall
	if len(ids) == 0 {
		ids = []string{callID}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	calls := []any{}
	for index, id := range ids {
		calls = append(calls, map[string]any{"index": index, "id": id, "type": "function", "function": map[string]any{"name": tool, "arguments": arguments}})
	}
	delta := map[string]any{"role": "assistant", "tool_calls": calls}
	for _, chunk := range []map[string]any{
		{"id": "chatcmpl-original-tool", "object": "chat.completion.chunk", "created": 1, "model": openCodeContinuationModel(tool), "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}},
		{"id": "chatcmpl-original-tool", "object": "chat.completion.chunk", "created": 1, "model": openCodeContinuationModel(tool), "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}}, "usage": map[string]any{"prompt_tokens": 20, "completion_tokens": 4, "total_tokens": 24}},
	} {
		raw, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

func verifyOpenCodeContinuationTool(t *testing.T, raw []byte, tool, path string, dismissed ...bool) string {
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
	marker := openCodeContinuationResultMarker(tool)
	if len(dismissed) == 1 && dismissed[0] {
		marker = "The user dismissed this question"
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
			if proposals != 1 || message.CallID != continuationToolCall || json.Unmarshal(message.Content, &result) != nil || !strings.Contains(result, marker) || strings.Contains(result, "changed-source-after-original-tool") {
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

func openCodeTodoArguments(turn int) string {
	todos := openCodeTodoList(turn)
	raw, _ := json.Marshal(domain.OpenCodeTodoInput{Todos: todos})
	return string(raw)
}

func openCodeTodoList(turn int) []domain.OpenCodeTodo {
	if turn == 1 {
		return []domain.OpenCodeTodo{}
	}
	state := domain.OpenCodeTodoRunning
	if turn > 1 {
		state = domain.OpenCodeTodoCompleted
	}
	return []domain.OpenCodeTodo{{Content: "original-inline-tool-sentinel", Status: state, Priority: domain.OpenCodeTodoHigh}, {Content: "Original second task", Status: "waiting", Priority: "urgent"}}
}

func verifyOpenCodeRepeatedTodo(t *testing.T, raw []byte, expected int) map[string]string {
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
		t.Error("invalid Todo conversation")
		return nil
	}
	proposals := map[string]int{}
	results := map[string]string{}
	for _, message := range body.Messages {
		for _, call := range message.Calls {
			index := len(proposals)
			var input domain.OpenCodeTodoInput
			if message.Role != "assistant" || call.ID != fmt.Sprintf("%s_%d", continuationToolCall, index) || call.Function.Name != "todowrite" || domain.Decode([]byte(call.Function.Arguments), &input) != nil || domain.ValidateOpenCodeTodos(input.Todos) != nil || !slices.Equal(input.Todos, openCodeTodoList(index)) {
				t.Error("changed original Todo proposal or explicit clear")
			}
			proposals[call.ID] = index
		}
		if message.Role == "tool" {
			index, found := proposals[message.CallID]
			var result string
			var todos []domain.OpenCodeTodo
			if !found || results[message.CallID] != "" || json.Unmarshal(message.Content, &result) != nil || domain.Decode([]byte(result), &todos) != nil || domain.ValidateOpenCodeTodos(todos) != nil || !slices.Equal(todos, openCodeTodoList(index)) {
				t.Error("changed original Todo result or explicit clear")
			}
			results[message.CallID] = result
		}
	}
	if len(proposals) != expected || len(results) != expected {
		t.Error("repeated or omitted original Todo history", len(proposals), len(results), expected)
	}
	return results
}

func openCodeContinuationFileTool(tool string) bool {
	return tool == "write" || tool == "edit" || tool == "apply_patch"
}

func openCodeContinuationResultMarker(tool string) string {
	switch tool {
	case "write":
		return "Wrote file successfully."
	case "edit":
		return "Edit applied successfully."
	case "apply_patch":
		return "Success. Updated the following files:"
	default:
		return "original-inline-tool-sentinel"
	}
}

func openCodeContinuationModel(tool string) string {
	// The pinned native registry selects Apply Patch instead of Write/Edit for
	// this model family. Use the original registry; do not expose hidden tools.
	if tool == "apply_patch" {
		return "gpt-5-fixture"
	}
	return "fixture-model"
}

func verifyOpenCodeContinuationPatchFiles(t *testing.T, path string, turn int) {
	t.Helper()
	for suffix, expected := range map[string]string{".added": "original added file\n", ".moved": "original moved file\n"} {
		value, err := os.ReadFile(path + suffix)
		if err != nil || string(value) != expected {
			t.Fatal("original patch add/move lost", suffix, err)
		}
	}
	if _, err := os.Stat(path + ".move"); !os.IsNotExist(err) {
		t.Fatal("original moved file was recreated", err)
	}
	if turn == 0 {
		if _, err := os.Stat(path + ".delete"); !os.IsNotExist(err) {
			t.Fatal("native patch did not delete original file", err)
		}
		if err := os.WriteFile(path+".delete", []byte("later recreated file\n"), 0600); err != nil {
			t.Fatal(err)
		}
	} else if value, err := os.ReadFile(path + ".delete"); err != nil || string(value) != "later recreated file\n" {
		t.Fatal("replacement replayed original deletion", err)
	}
}

func openCodeContinuationQuestionResponse(dismissed bool) domain.QuestionResponseInput {
	response := &domain.OpenCodeQuestionResponse{Answers: openCodeContinuationAnswers()}
	if dismissed {
		response = &domain.OpenCodeQuestionResponse{Reject: true}
	}
	return domain.QuestionResponseInput{OpenCode: response}
}

const openCodeContinuationCorrection = "Private original correction: leave the file unread and continue."

func verifyOpenCodeRejectedReads(t *testing.T, raw []byte, path string, count int, correction bool) string {
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
		t.Error("invalid original rejected Read conversation")
		return ""
	}
	proposals := map[string]bool{}
	results := map[string]string{}
	corrections := 0
	for _, message := range body.Messages {
		for _, call := range message.Calls {
			var input struct {
				FilePath string `json:"filePath"`
			}
			id := continuationToolCall
			if count > 1 {
				id = fmt.Sprintf("%s_%d", continuationToolCall, len(proposals))
			}
			if message.Role != "assistant" || call.ID != id || proposals[call.ID] || call.Function.Name != "read" || domain.Decode([]byte(call.Function.Arguments), &input) != nil || input.FilePath != path {
				t.Error("changed rejected Read proposal")
			}
			proposals[call.ID] = true
		}
		if message.Role == "tool" {
			var result string
			if !proposals[message.CallID] || results[message.CallID] != "" || json.Unmarshal(message.Content, &result) != nil || result == "" || strings.Contains(result, "original-inline-tool-sentinel") || strings.Contains(result, "changed-source-after-original-tool") {
				t.Error("rejected Read acquired output or lost original error")
			}
			if strings.Contains(result, openCodeContinuationCorrection) {
				corrections++
			}
			results[message.CallID] = result
		}
	}
	if len(proposals) != count || len(results) != count || (!correction && corrections != 0) || (correction && corrections != 1) {
		t.Error("repeated, omitted or changed original rejection/correction", len(proposals), len(results), corrections)
	}
	encoded, _ := json.Marshal(results)
	return string(encoded)
}
