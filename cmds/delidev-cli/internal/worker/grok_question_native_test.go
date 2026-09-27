package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const workerQuestionText = "Which original Worker option?"
const workerQuestionAnswer = "Bespoke original answer."

func nativeGrokQuestionProvider(t *testing.T, w http.ResponseWriter, raw []byte, model string, cancelled bool) {
	t.Helper()
	var body struct {
		Tools    []json.RawMessage `json:"tools"`
		Messages []struct {
			Role    string          `json:"role"`
			Tool    string          `json:"tool_call_id"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(raw, &body) != nil {
		t.Error("invalid original question provider input")
		return
	}
	completed := false
	for _, message := range body.Messages {
		if message.Role == "tool" {
			var content string
			if message.Tool != "original-worker-question" || json.Unmarshal(message.Content, &content) != nil || cancelled && !strings.Contains(content, "User declined to answer") || !cancelled && !strings.Contains(content, workerQuestionAnswer) {
				t.Error("native provider lost original question result")
				return
			}
			completed = true
		}
	}
	write := func(delta map[string]any, finish any, usage bool) {
		chunk := map[string]any{"id": "chat-worker-question", "object": "chat.completion.chunk", "created": 1, "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
		if usage {
			chunk["usage"] = map[string]any{"prompt_tokens": 11, "completion_tokens": 5, "total_tokens": 16}
		}
		encoded, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
	}
	finish := "stop"
	if len(body.Tools) > 0 && !completed {
		args, _ := json.Marshal(map[string]any{"questions": []any{map[string]any{"question": workerQuestionText, "options": []any{map[string]any{"label": "One", "description": "First fixture option"}, map[string]any{"label": "Two", "description": "Second fixture option"}}}}})
		write(map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "original-worker-question", "type": "function", "function": map[string]any{"name": "ask_user_question", "arguments": string(args)}}}}, nil, false)
		finish = "tool_calls"
	} else {
		write(map[string]any{"role": "assistant", "content": "Original Worker question completed."}, nil, false)
	}
	write(map[string]any{}, finish, true)
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

func nativeGrokWorkerQuestion(t *testing.T, ctx context.Context, api *grok.OwnedAPI, p *ExecutionPublisher, journal *grokClaimJournal, session domain.ID, cancelled bool) {
	t.Helper()
	var arrival domain.ID
	completed := false
	stages, mode, run := 4, grok.NativeDefaultMode, api.RunQuestions
	if p.input.Input.Mode == domain.PlanMode {
		stages, mode, run = 6, grok.NativePlanMode, api.RunPlanQuestions
	}
	result, err := run(ctx, p.input.TurnRequestID, p.input.Input.Prompt, func(callback context.Context, v grok.InputObservation) error {
		claims, err := readGrokClaims(p.config.Root, journal.state.Reference)
		if err != nil || len(claims) < stages || len(claims) > stages+1 || claims[stages-1].Input.NativePromptID != v.NativePromptID || v.InputID != p.input.TurnRequestID {
			return grokClaimUncertain()
		}
		if v.QuestionOffer != nil {
			if len(claims) != stages || arrival != "" || v.Question == nil || v.Question.Request == nil || v.Question.Request.Mode != mode || len(v.Question.Request.Questions) != 1 || v.Question.Request.Questions[0].Question != workerQuestionText {
				t.Error("native question proposal was not original")
				return grokClaimUncertain()
			}
			arrival = v.QuestionOffer.ArrivalID
			answer := grok.QuestionAnswer{Outcome: grok.QuestionAccepted, Answers: map[string]string{workerQuestionText: workerQuestionAnswer}}
			if cancelled {
				answer = grok.QuestionAnswer{Outcome: grok.QuestionCancelled}
			}
			delivery, err := api.ReplyQuestion(callback, domain.NewID(), arrival, answer)
			if err != nil || !delivery.Claimed || !delivery.Delivered || delivery.Resolved {
				t.Error("original question reply failed", err)
				return grokClaimUncertain()
			}
			retained, err := readGrokClaims(p.config.Root, journal.state.Reference)
			if err != nil || len(retained) != stages+1 || retained[stages].QuestionReply == nil || *retained[stages].QuestionReply != delivery.Claim || delivery.Claim.NativeSessionID != session || delivery.Claim.ProposalDigest != v.QuestionOffer.ProposalDigest {
				t.Error("question lost synchronized Worker ownership")
				return grokClaimUncertain()
			}
		}
		if v.Kind == grok.InputCompleted {
			completed = true
		}
		if v.Kind == grok.StopSettled || v.Kind == grok.InputPermissionRejected {
			t.Error("question cancellation became Stop or permission rejection")
			return grokClaimUncertain()
		}
		return nil
	})
	if err != nil || !completed || result.Reason != grok.EndTurn || result.Meta.Input != 11 || result.Meta.Usage.Input != 22 {
		t.Fatal("native Worker question did not continue", err)
	}
	delivery, err := api.InspectQuestion(arrival)
	if err != nil || !delivery.Resolved || !delivery.Delivered || delivery.ProblemCode != "" {
		t.Fatal("original question resolution was not retained", err)
	}
	if _, err := api.ReplyQuestion(ctx, domain.NewID(), arrival, grok.QuestionAnswer{Outcome: grok.QuestionCancelled}); err == nil {
		t.Fatal("original Worker question replayed")
	}
	if p.state.Pending != nil || p.state.LastSequence != 0 {
		t.Fatal("question claims fabricated public publication")
	}
	raw, err := security.ReadPrivate(journal.path, maxGrokClaimBytes)
	if err != nil || strings.Contains(string(raw), workerQuestionText) || strings.Contains(string(raw), workerQuestionAnswer) {
		t.Fatal("question journal retained private content", err)
	}
	if err := api.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readGrokClaims(p.config.Root, journal.state.Reference); err != nil {
		t.Fatal("cleanup lost original question ownership", err)
	}
}
