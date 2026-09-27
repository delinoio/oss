package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func nativeGrokMixedProvider(t *testing.T, w http.ResponseWriter, raw []byte, path, model string) {
	t.Helper()
	var body struct {
		Tools    []json.RawMessage `json:"tools"`
		Messages []struct {
			Role    string          `json:"role"`
			ID      string          `json:"tool_call_id"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(raw, &body) != nil {
		t.Error("invalid original mixed provider request")
		return
	}
	step := 0
	for _, m := range body.Messages {
		if m.Role == "tool" {
			var content string
			if m.ID != fmt.Sprintf("mixed-worker-tool-%d", step) || json.Unmarshal(m.Content, &content) != nil || content == "" {
				t.Error("mixed Worker tool lineage changed")
				return
			}
			step++
		}
	}
	delta := map[string]any{"role": "assistant", "content": "Original Worker mixed tools completed."}
	finish := "stop"
	if len(body.Tools) > 0 && step < 4 {
		var name string
		var args any
		switch step {
		case 0, 2:
			name, args = "write", map[string]any{"file_path": path, "content": fmt.Sprintf("Mixed Worker write %d.\n", step)}
		case 1:
			name, args = "ask_user_question", map[string]any{"questions": []any{map[string]any{"question": workerQuestionText, "options": []any{map[string]any{"label": "One", "description": "First"}, map[string]any{"label": "Two", "description": "Second"}}}}}
		case 3:
			name, args = "read_file", map[string]any{"target_file": path}
		}
		encoded, _ := json.Marshal(args)
		delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("mixed-worker-tool-%d", step), "type": "function", "function": map[string]any{"name": name, "arguments": string(encoded)}}}}
		finish = "tool_calls"
	}
	for _, part := range []any{
		map[string]any{"id": "chat-worker-mixed", "object": "chat.completion.chunk", "created": 1, "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}},
		map[string]any{"id": "chat-worker-mixed", "object": "chat.completion.chunk", "created": 1, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}, "usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 5, "total_tokens": 16}},
	} {
		encoded, _ := json.Marshal(part)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

func nativeGrokWorkerMixed(t *testing.T, ctx context.Context, api *grok.OwnedAPI, p *ExecutionPublisher, journal *grokClaimJournal, session domain.ID, path string) {
	t.Helper()
	var fileArrival, questionArrival domain.ID
	completed, inherited := false, false
	result, err := api.RunTools(ctx, p.input.TurnRequestID, p.input.Input.Prompt, func(callback context.Context, v grok.InputObservation) error {
		claims, err := readGrokClaims(p.config.Root, journal.state.Reference)
		if err != nil || len(claims) < 4 || len(claims) > 6 || claims[3].Input.NativePromptID != v.NativePromptID || v.InputID != p.input.TurnRequestID {
			return grokClaimUncertain()
		}
		if v.Permission != nil {
			if fileArrival != "" || len(claims) != 4 || v.Permission.ToolID != "mixed-worker-tool-0" {
				return grokClaimUncertain()
			}
			fileArrival = v.Permission.ArrivalID
			delivery, err := api.ReplyFilePermission(callback, domain.NewID(), fileArrival, grok.AllowFileSession)
			if err != nil || !delivery.Delivered || delivery.Resolved || delivery.Claim.NativeSessionID != session {
				return grokClaimUncertain()
			}
		}
		if v.QuestionOffer != nil {
			if questionArrival != "" || len(claims) != 5 || claims[4].FileReply == nil || v.QuestionOffer.ToolID != "mixed-worker-tool-1" {
				return grokClaimUncertain()
			}
			questionArrival = v.QuestionOffer.ArrivalID
			delivery, err := api.ReplyQuestion(callback, domain.NewID(), questionArrival, grok.QuestionAnswer{Outcome: grok.QuestionAccepted, Answers: map[string]string{workerQuestionText: workerQuestionAnswer}})
			if err != nil || !delivery.Delivered || delivery.Resolved || delivery.Claim.NativeSessionID != session {
				return grokClaimUncertain()
			}
		}
		if v.FileTool != nil && v.FileTool.Observation != nil && v.FileTool.Observation.ID == "mixed-worker-tool-2" && v.FileTool.Observation.Write != nil {
			inherited = v.FileTool.InheritedPermission == fileArrival
		}
		completed = completed || v.Kind == grok.InputCompleted
		return nil
	})
	if err != nil || !completed || !inherited || result.Reason != grok.EndTurn || result.Meta.Usage.Input != 55 || result.Meta.Input != 11 {
		t.Fatal("mixed original Worker input did not settle", err)
	}
	claims, err := readGrokClaims(p.config.Root, journal.state.Reference)
	if err != nil || len(claims) != 6 || claims[4].FileReply == nil || claims[5].QuestionReply == nil || claims[4].FileReply.ArrivalID != fileArrival || claims[5].QuestionReply.ArrivalID != questionArrival {
		t.Fatal("mixed Worker claims lost original order", err)
	}
	file, err := api.InspectFilePermission(fileArrival)
	question, questionErr := api.InspectQuestion(questionArrival)
	if err != nil || questionErr != nil || !file.Resolved || !question.Resolved || file.ProblemCode != "" || question.ProblemCode != "" {
		t.Fatal("mixed response resolution did not settle")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "Mixed Worker write 2.\n" {
		t.Fatal("mixed Worker file effect changed")
	}
	raw, err = security.ReadPrivate(journal.path, maxGrokClaimBytes)
	if err != nil || strings.Contains(string(raw), workerQuestionText) || strings.Contains(string(raw), workerQuestionAnswer) || strings.Contains(string(raw), path) || strings.Contains(string(raw), "Mixed Worker write") {
		t.Fatal("mixed journal retained private native contents", err)
	}
	if p.state.Pending != nil || p.state.LastSequence != 0 {
		t.Fatal("mixed replies fabricated public publication")
	}
	if err := api.Close(); err != nil {
		t.Fatal(err)
	}
}
