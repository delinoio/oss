package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const workerPlanOriginal = "# Original Worker plan\n\nUse the isolated fixture.\n"
const workerPlanRevised = "# Revised Worker plan\n\nUse the corrected fixture.\n"

type nativeGrokPlanFixture struct {
	mu       sync.Mutex
	path     string
	scenario string
}

func (f *nativeGrokPlanFixture) provider(t *testing.T, w http.ResponseWriter, raw []byte, model string) {
	t.Helper()
	var body struct {
		Tools    []json.RawMessage `json:"tools"`
		Messages []struct {
			Role string `json:"role"`
			ID   string `json:"tool_call_id"`
		} `json:"messages"`
	}
	if json.Unmarshal(raw, &body) != nil {
		t.Error("invalid original Plan provider body")
		return
	}
	count := 0
	for _, m := range body.Messages {
		if m.Role == "tool" {
			if m.ID != fmt.Sprintf("original-plan-%d", count) {
				t.Error("Worker Plan tool order changed")
				return
			}
			count++
		}
	}
	limit := 4
	if f.scenario == "revised" {
		limit = 6
	}
	delta := map[string]any{"role": "assistant", "content": "Original Worker Plan completed."}
	finish := "stop"
	if len(body.Tools) > 0 && count < limit {
		var name string
		args := map[string]any{}
		switch count {
		case 0:
			name = "enter_plan_mode"
		case 1:
			name = "ask_user_question"
			args = map[string]any{"questions": []any{map[string]any{"question": workerQuestionText, "options": []any{map[string]any{"label": "One", "description": "First"}, map[string]any{"label": "Two", "description": "Second"}}}}}
		case 2, 4:
			f.mu.Lock()
			path := f.path
			f.mu.Unlock()
			if path == "" {
				t.Error("Worker Plan write preceded original path publication")
				return
			}
			content := workerPlanOriginal
			if count == 4 {
				content = workerPlanRevised
			}
			name = "write"
			args = map[string]any{"file_path": path, "content": content}
		case 3, 5:
			name = "exit_plan_mode"
		}
		argsRaw, _ := json.Marshal(args)
		delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("original-plan-%d", count), "type": "function", "function": map[string]any{"name": name, "arguments": string(argsRaw)}}}}
		finish = "tool_calls"
	}
	for _, part := range []any{
		map[string]any{"id": "chat-worker-plan", "object": "chat.completion.chunk", "created": 1, "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}},
		map[string]any{"id": "chat-worker-plan", "object": "chat.completion.chunk", "created": 1, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}, "usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 5, "total_tokens": 16}},
	} {
		encoded, _ := json.Marshal(part)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

func (f *nativeGrokPlanFixture) run(t *testing.T, ctx context.Context, api *grok.OwnedAPI, p *ExecutionPublisher, journal *grokClaimJournal, session domain.ID) {
	t.Helper()
	stages := 4
	if p.input.Input.Mode == domain.PlanMode {
		stages = 6
	}
	var arrivals []domain.ID
	completed := false
	result, err := api.RunPlanning(ctx, p.input.TurnRequestID, p.input.Input.Prompt, func(callback context.Context, v grok.InputObservation) error {
		claims, err := readGrokClaims(p.config.Root, journal.state.Reference)
		if err != nil || len(claims) < stages || claims[stages-1].Input.NativePromptID != v.NativePromptID || v.InputID != p.input.TurnRequestID {
			return grokClaimUncertain()
		}
		if v.Plan != nil && v.Plan.Observation != nil && v.Plan.Observation.Entered != nil {
			f.mu.Lock()
			f.path = v.Plan.Observation.Entered.Path
			f.mu.Unlock()
		}
		if v.QuestionOffer != nil {
			if v.Question == nil || v.Question.Request == nil || v.Question.Request.Mode != grok.NativePlanMode {
				return grokClaimUncertain()
			}
			_, err := api.ReplyQuestion(callback, domain.NewID(), v.QuestionOffer.ArrivalID, grok.QuestionAnswer{Outcome: grok.QuestionAccepted, Answers: map[string]string{workerQuestionText: workerQuestionAnswer}})
			return err
		}
		if v.Permission != nil {
			t.Error("native Plan file fabricated ordinary permission")
			return grokClaimUncertain()
		}
		if v.PlanOffer != nil {
			revision := uint64(1)
			outcome := grok.PlanOutcome(f.scenario)
			content := workerPlanOriginal
			if f.scenario == "revised" {
				outcome = grok.PlanCancelled
				if len(arrivals) == 1 {
					revision, outcome, content = 2, grok.PlanApproved, workerPlanRevised
				}
			}
			if v.Plan == nil || v.Plan.Request == nil || v.Plan.Request.Content != content || v.PlanOffer.Origin.Revision != revision || len(claims) != stages+1+len(arrivals) {
				return grokClaimUncertain()
			}
			arrival := v.PlanOffer.ArrivalID
			delivery, err := api.ReplyPlan(callback, domain.NewID(), arrival, outcome)
			if err != nil || !delivery.Delivered || delivery.Resolved {
				return grokClaimUncertain()
			}
			arrivals = append(arrivals, arrival)
			retained, err := readGrokClaims(p.config.Root, journal.state.Reference)
			if err != nil || len(retained) != stages+1+len(arrivals) || retained[len(retained)-1].PlanReply == nil || *retained[len(retained)-1].PlanReply != delivery.Claim || delivery.Claim.NativeSessionID != session || delivery.Claim.Revision != revision {
				return grokClaimUncertain()
			}
		}
		if v.Kind == grok.InputCompleted {
			completed = true
		}
		if v.Kind == grok.StopSettled || v.Kind == grok.InputPermissionRejected {
			t.Error("Plan action changed original root category")
			return grokClaimUncertain()
		}
		return nil
	})
	responses := uint64(5)
	expected := 1
	if f.scenario == "revised" {
		responses, expected = 7, 2
	}
	if err != nil || !completed || len(arrivals) != expected || result.Reason != grok.EndTurn || result.Meta.Input != 11 || result.Meta.Usage.Input != responses*11 {
		t.Fatal("native Worker Plan did not complete", err)
	}
	for _, arrival := range arrivals {
		d, err := api.InspectPlan(arrival)
		if err != nil || !d.Resolved || !d.Delivered || d.ProblemCode != "" {
			t.Fatal("Worker Plan lost native resolution", err)
		}
	}
	if p.state.Pending != nil || p.state.LastSequence != 0 {
		t.Fatal("Plan journal invented public publication")
	}
	raw, err := security.ReadPrivate(journal.path, maxGrokClaimBytes)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	path := f.path
	f.mu.Unlock()
	for _, private := range []string{path, workerQuestionText, workerQuestionAnswer, workerPlanOriginal, workerPlanRevised} {
		if private != "" && strings.Contains(string(raw), private) {
			t.Fatal("Plan journal retained private content")
		}
	}
	if err := api.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readGrokClaims(p.config.Root, journal.state.Reference); err != nil {
		t.Fatal("cleanup lost original Plan records", err)
	}
}
