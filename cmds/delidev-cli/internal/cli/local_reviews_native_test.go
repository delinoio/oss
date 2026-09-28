package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

type nativeReviewExpectation struct{ prompt, command string }
type nativeReviewScenario struct {
	ready    chan struct{}
	expected atomic.Pointer[nativeReviewExpectation]
}

func newNativeReviewScenario() *nativeReviewScenario {
	return &nativeReviewScenario{ready: make(chan struct{})}
}

// This loopback provider supplies deterministic Responses events, while the
// installed Codex process owns history, tool execution and its real file writes.
// It never uses hosted inference or credentials from the user's environment.
func (s *nativeReviewScenario) respond(t *testing.T, w http.ResponseWriter, r *http.Request, body []byte, call int64) {
	t.Helper()
	var request struct {
		Input []struct {
			Type, Role string
			Content    json.RawMessage
			Output     json.RawMessage
		} `json:"input"`
		Tools []struct{ Type, Name string } `json:"tools"`
	}
	if json.Unmarshal(body, &request) != nil {
		t.Error("invalid native review request")
		http.Error(w, "invalid", 400)
		return
	}
	var expected *nativeReviewExpectation
	if call > 1 {
		select {
		case <-s.ready:
			expected = s.expected.Load()
		case <-r.Context().Done():
			return
		}
	}
	first, review := 0, 0
	for _, item := range request.Input {
		if item.Role != "user" {
			continue
		}
		var content []struct{ Type, Text string }
		if json.Unmarshal(item.Content, &content) != nil {
			t.Error("native user content unavailable")
			continue
		}
		for _, part := range content {
			if part.Text == "Public first prompt" {
				first++
			}
			if expected != nil && part.Text == expected.prompt {
				review++
			}
		}
	}
	if first != 1 || call > 1 && review != 1 || call < 1 || call > 3 {
		t.Errorf("native review history changed: call=%d original=%d review=%d", call, first, review)
		http.Error(w, "invalid history", 400)
		return
	}
	var item any = map[string]any{"type": "message", "id": fmt.Sprintf("msg_review_%d", call), "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Local review processing complete."}}}
	if call == 2 {
		found := false
		for _, tool := range request.Tools {
			found = found || tool.Type == "function" && tool.Name == "exec_command"
		}
		if !found {
			t.Error("verified native command tool absent")
			http.Error(w, "unsupported", 400)
			return
		}
		arguments, _ := json.Marshal(map[string]any{"cmd": expected.command, "login": false, "max_output_tokens": 1000, "yield_time_ms": 1000})
		item = map[string]any{"type": "function_call", "id": "fc_local_review", "call_id": "call_local_review", "name": "exec_command", "arguments": string(arguments)}
	}
	if call == 3 {
		var output struct {
			Input []struct {
				Type   string
				CallID string `json:"call_id"`
				Output string
			} `json:"input"`
		}
		if json.Unmarshal(body, &output) != nil {
			t.Error("native command output unavailable")
		}
		found := false
		for _, value := range output.Input {
			found = found || value.Type == "function_call_output" && value.CallID == "call_local_review" && strings.Contains(value.Output, "native-review-written")
		}
		if !found {
			t.Error("original native write result absent")
			http.Error(w, "missing result", 400)
			return
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range []any{
		map[string]any{"type": "response.created", "response": map[string]any{"id": fmt.Sprintf("resp_review_%d", call), "status": "in_progress"}},
		map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item},
		map[string]any{"type": "response.completed", "response": map[string]any{"id": fmt.Sprintf("resp_review_%d", call), "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}},
	} {
		raw, _ := json.Marshal(event)
		fmt.Fprintf(w, "data: %s\n\n", raw)
	}
}

func verifyNativeLocalReview(t *testing.T, scenario *nativeReviewScenario, run func([]string, any) map[string]any, waitTurn func(int, string), session string, job domain.Job) {
	t.Helper()
	var input domain.ExecutionJobInput
	var manifest workspace.Manifest
	if domain.Decode(job.Input, &input) != nil || domain.Decode(input.Manifest, &manifest) != nil || len(manifest.Repositories) != 2 {
		t.Fatal("original native review workspace missing")
	}
	references := []domain.ReviewCommentRef{}
	comments := []domain.ReviewComment{}
	var commands []string
	quote := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\"'\"'") + "'" }
	for index, repo := range manifest.Repositories {
		path := filepath.Join(repo.Path, "tracked.txt")
		if err := os.WriteFile(path, []byte("Review this original fixture line\n"), 0600); err != nil {
			t.Fatal(err)
		}
		observed := run([]string{"session", "diff", "--id", session, "--repository-id", string(repo.ID), "--comparison", "creation"}, nil)
		raw, _ := json.Marshal(observed["diff"])
		var diff domain.WorkspaceDiff
		if domain.Decode(raw, &diff) != nil || diff.Revision == "" {
			t.Fatal("original Worker diff missing")
		}
		create := domain.CreateReviewComment{Query: domain.WorkspaceReadQuery{Operation: domain.WorkspaceGitDiff, RepositoryID: repo.ID, Comparison: domain.DiffCreation, Path: "."}, DiffRevision: diff.Revision, Selection: domain.ReviewSelection{Kind: domain.ReviewLineAnchor, Path: "tracked.txt", Side: domain.ReviewNewSide, Start: 1, End: 1}, Body: fmt.Sprintf("Revise selected repository %d using its original context.", index)}
		resource := run([]string{"session", "review", "create", "--id", session}, create)["comment"].(map[string]any)
		raw, _ = json.Marshal(resource["data"])
		var value domain.LocalReview
		if domain.Decode(raw, &value) != nil || value.Validate() != nil || value.Comment.Anchor.Context != "Review this original fixture line\n" {
			t.Fatal("review anchor changed")
		}
		references = append(references, domain.ReviewCommentRef{ID: domain.ID(resource["id"].(string)), Revision: uint64(resource["revision"].(float64))})
		comments = append(comments, *value.Comment)
		commands = append(commands, "printf 'Reviewed by native agent\\n' > "+quote(path))
	}
	commands = append(commands, "printf 'native-review-written\\n'")
	request := domain.NewID()
	args := []string{"session", "review", "submit", "--id", session, "--request-id", string(request)}
	selected := domain.SubmitReviewComments{Comments: references, Mode: domain.ExecuteMode}
	accepted := run(args, selected)
	queued := accepted["change"].(map[string]any)["input"].(map[string]any)
	prompt := queued["data"].(map[string]any)["prompt"].(string)
	scenario.expected.Store(&nativeReviewExpectation{prompt: prompt, command: strings.Join(commands, " && ")})
	close(scenario.ready)
	waitTurn(2, queued["id"].(string))
	replay := run(args, selected)
	if replay["replayed"] != true || replay["change"].(map[string]any)["input"].(map[string]any)["id"] != queued["id"] {
		t.Fatal("review receipt dispatched another native input")
	}
	for index, repo := range manifest.Repositories {
		content, err := os.ReadFile(filepath.Join(repo.Path, "tracked.txt"))
		if err != nil || string(content) != "Reviewed by native agent\n" {
			t.Fatal("native agent did not update the original selected file", err)
		}
		observed := run([]string{"session", "diff", "--id", session, "--repository-id", string(repo.ID), "--comparison", "creation"}, nil)["diff"].(map[string]any)
		if observed["revision"] == comments[index].Anchor.DiffRevision || !strings.Contains(observed["patch"].(string), "+Reviewed by native agent") {
			t.Fatal("updated diff missing original native change")
		}
		retained := run([]string{"session", "review", "get", "--id", session, "--review-id", string(references[index].ID)}, nil)["data"].(map[string]any)
		raw, _ := json.Marshal(retained)
		var value domain.LocalReview
		if domain.Decode(raw, &value) != nil || value.Comment.Anchor != comments[index].Anchor || value.Comment.Body != comments[index].Body || value.Comment.LastSubmissionID != request {
			t.Fatal("agent response silently resolved or reanchored feedback")
		}
	}
	queue := run([]string{"queue", "list", "--session-id", session}, nil)["inputs"].([]any)
	if len(queue) != 2 || queue[1].(map[string]any)["data"].(map[string]any)["delivery"] != string(domain.InputAccepted) {
		t.Fatal("grouped review lost ordinary input ownership")
	}
	records := run([]string{"message", "list", "--session-id", session}, nil)["resources"].([]any)
	user := 0
	for _, record := range records {
		raw, _ := json.Marshal(record.(map[string]any)["data"])
		var message domain.ExecutionMessage
		if domain.Decode(raw, &message) != nil {
			t.Fatal("invalid original native transcript")
		}
		if message.Role == domain.UserMessage && message.Text == prompt {
			user++
		}
	}
	if user != 1 {
		t.Fatal("review prompt duplicated or missing from original native transcript", user)
	}
	t.Log("Actual Codex: two-repository line comments -> grouped ordinary input -> native history continuation -> owned command writes -> updated Worker diff; exact receipt replay produced no second input or automatic resolution. Scripted local provider only.")
}
