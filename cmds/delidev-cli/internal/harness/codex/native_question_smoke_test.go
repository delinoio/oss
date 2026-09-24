package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestManualNativeQuestionResponse(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit installed native harness with local scripted responses only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	provider, requests, received := nativeQuestionProvider(t)
	cfg := nativeFixtureConfig(t, binary, provider.URL)
	c, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	settings := ThreadSettings{Model: "fixture-model", Provider: "delidev_fixture", Effort: "high", Cwd: cfg.Process.Cwd, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}}
	if _, err := c.StartThread(ctx, domain.NewID(), settings); err != nil {
		t.Fatal(err)
	}
	turn, err := c.StartTurn(ctx, domain.NewID(), domain.NewID(), domain.SessionInput{Mode: domain.PlanMode, Prompt: "Return only the scripted local question fixture."})
	if err != nil {
		t.Fatal(err)
	}
	var answered, closed, accepted bool
	var arrival domain.ID
	functionOutputs := 0
	for {
		event, err := c.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == InteractionRequestedEvent {
			if answered || event.TurnID != turn.TurnID || event.ItemID != "call_question_fixture" || !event.Correlated || !event.Interaction.Questions.Blocking {
				t.Fatal("native question ownership mismatch")
			}
			arrival = event.Interaction.ID
			status, err := c.AnswerQuestions(ctx, domain.NewID(), arrival, turn.TurnID, QuestionAnswers{Answers: map[string][]string{"choice": {"First"}}})
			if err != nil || status.Delivery != QuestionTransmitted {
				t.Fatalf("native answer send: %v", err)
			}
			answered = true
		}
		if event.Kind == InteractionClosedEvent {
			if !answered || event.InteractionState.ID != arrival || event.InteractionState.Delivery != QuestionTransmitted || event.InteractionState.Closure != InteractionNativeClosed {
				t.Fatal("native resolution lost delivery identity")
			}
			closed = true
		}
		if event.Kind == QuestionAcceptedEvent {
			if !answered || !event.InteractionState.Accepted || event.InteractionState.ID != arrival || event.InteractionState.ResponseID == "" || event.InteractionState.Delivery != QuestionTransmitted {
				t.Fatal("native acceptance lost exact response binding")
			}
			accepted = true
		}
		if event.Native != nil && event.Native.Method == "item/completed" {
			var params struct{ Item struct{ Type string } }
			if json.Unmarshal(event.Native.Params, &params) == nil && params.Item.Type == "functionCallOutput" {
				functionOutputs++
			}
		}
		if event.Kind == TurnCompletedEvent {
			if event.TurnID != turn.TurnID || event.Turn.Status != TurnCompleted {
				t.Fatal("native question turn failed")
			}
			break
		}
	}
	if !answered || !closed || !accepted || c.execution.interactions.blocksInput() || !received.Load() || requests.Load() != 2 {
		t.Fatal("native question lifecycle incomplete")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("Codex %s: question sent, closed and accepted by exact native tool output; scripted model received exact answer; %d function-output item observations; no external account", SupportedVersion, functionOutputs)
}

// nativeQuestionProvider never sends a request outside its loopback listener.
func nativeQuestionProvider(t *testing.T) (*httptest.Server, *atomic.Int64, *atomic.Bool) {
	t.Helper()
	var requests atomic.Int64
	var received atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requests.Add(1)
		body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		var request struct {
			Model string `json:"model"`
			Input []struct {
				Type   string
				CallID string `json:"call_id"`
				Output json.RawMessage
			} `json:"input"`
			Tools []struct{ Name string } `json:"tools"`
		}
		if r.Method != "POST" || r.URL.Path != "/responses" || err != nil || json.Unmarshal(body, &request) != nil || request.Model != "fixture-model" || count > 2 {
			t.Error("unexpected scripted native question request")
			http.Error(w, "invalid fixture request", 400)
			return
		}
		if count == 1 {
			offered := false
			for _, tool := range request.Tools {
				offered = offered || tool.Name == "request_user_input"
			}
			if !offered {
				t.Error("native question tool was not offered")
				http.Error(w, "unsupported", 400)
				return
			}
		} else {
			for _, item := range request.Input {
				if item.Type != "function_call_output" || item.CallID != "call_question_fixture" {
					continue
				}
				var text string
				var response nativeQuestionResponse
				if json.Unmarshal(item.Output, &text) == nil && domain.Decode([]byte(text), &response) == nil && len(response.Answers) == 1 && len(response.Answers["choice"].Answers) == 1 && response.Answers["choice"].Answers[0] == "First" {
					received.Store(true)
				}
			}
			if !received.Load() {
				t.Error("native tool did not return the exact question answer")
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(event any) {
			raw, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
			w.(http.Flusher).Flush()
		}
		responseID := fmt.Sprintf("resp_question_fixture_%d", count)
		send(map[string]any{"type": "response.created", "response": map[string]any{"id": responseID, "status": "in_progress"}})
		var item any
		if count == 1 {
			arguments, _ := json.Marshal(map[string]any{"questions": []any{questionFixture()}})
			item = map[string]any{"type": "function_call", "id": "fc_question_fixture", "call_id": "call_question_fixture", "name": "request_user_input", "arguments": string(arguments)}
		} else {
			item = map[string]any{"type": "message", "id": "msg_question_fixture", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Native question fixture complete."}}}
		}
		send(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
		send(map[string]any{"type": "response.completed", "response": map[string]any{"id": responseID, "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}})
	}))
	t.Cleanup(provider.Close)
	return provider, &requests, &received
}
