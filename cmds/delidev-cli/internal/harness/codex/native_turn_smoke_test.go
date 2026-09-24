package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestManualNativeTurnControls(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit installed native harness with local scripted responses only")
	}
	for _, action := range []string{"plan", "steer", "interrupt"} {
		t.Run(action, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			started := make(chan struct{})
			released := make(chan struct{})
			canceled := make(chan struct{}, 1)
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(released) }) }
			defer release()
			var requests atomic.Int64
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count := requests.Add(1)
				if r.Method != "POST" || r.URL.Path != "/responses" {
					t.Error("unexpected native fixture operation")
					http.Error(w, "unsupported", 404)
					return
				}
				body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
				var request struct {
					Model        string
					Reasoning    struct{ Effort string }
					Instructions string
				}
				if err != nil || json.Unmarshal(body, &request) != nil || request.Model != "fixture-model" || request.Reasoning.Effort != "high" || request.Instructions == "" || !strings.Contains(string(body), "Temporary non-inference thread validation.") {
					t.Error("native turn changed model, effort or additive instructions")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				send := func(event any) {
					raw, _ := json.Marshal(event)
					_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
					w.(http.Flusher).Flush()
				}
				responseID := fmt.Sprintf("resp_fixture_%d", count)
				send(map[string]any{"type": "response.created", "response": map[string]any{"id": responseID, "status": "in_progress"}})
				if count == 1 && action != "plan" {
					close(started)
					select {
					case <-released:
					case <-r.Context().Done():
						canceled <- struct{}{}
						return
					case <-ctx.Done():
						return
					}
				}
				send(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message", "id": fmt.Sprintf("msg_fixture_%d", count), "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Native fixture complete."}}}})
				send(map[string]any{"type": "response.completed", "response": map[string]any{"id": responseID, "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}})
			}))
			t.Cleanup(provider.Close)
			cfg := nativeFixtureConfig(t, binary, provider.URL)
			client, err := Open(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := client.Close(); err != nil {
					t.Error(err)
				}
			})
			settings := ThreadSettings{Model: "fixture-model", Provider: "delidev_fixture", Effort: "high", Cwd: cfg.Process.Cwd, Instructions: "Temporary non-inference thread validation.", Options: domain.AgentOptions{Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}}
			if _, err := client.StartThread(ctx, domain.NewID(), settings); err != nil {
				t.Fatal(err)
			}
			mode := domain.ExecuteMode
			if action == "plan" {
				mode = domain.PlanMode
			}
			initialInput := domain.NewID()
			turn, err := client.StartTurn(ctx, domain.NewID(), initialInput, domain.SessionInput{Mode: mode, Prompt: "Return the local fixture response only."})
			if err != nil {
				t.Fatal(err)
			}
			if action != "plan" {
				select {
				case <-started:
				case <-ctx.Done():
					t.Fatal("native request did not reach local fixture")
				}
			}
			steeredInput := domain.NewID()
			if action == "steer" {
				ack, err := client.Steer(ctx, domain.NewID(), steeredInput, turn.TurnID, domain.SessionInput{Mode: mode, Prompt: "Include this explicit steered input in the same turn."})
				if err != nil || ack.TurnID != turn.TurnID {
					t.Fatalf("native expected-turn steer failed: %v", err)
				}
				release()
			}
			if action == "interrupt" {
				if _, err := client.Interrupt(ctx, domain.NewID(), turn.TurnID); err != nil {
					t.Fatal(err)
				}
			}
			seenInputs := map[domain.ID]bool{}
			for {
				event, err := client.NextEvent(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if event.Message != nil && event.Message.Role == UserRole {
					seenInputs[event.Message.ClientInputID] = true
				}
				if event.Kind != TurnCompletedEvent {
					continue
				}
				expected := TurnCompleted
				if action == "interrupt" {
					expected = TurnInterrupted
				}
				if !event.Correlated || event.TurnID != turn.TurnID || event.Turn.Status != expected {
					t.Fatal("native terminal identity/status mismatch")
				}
				break
			}
			if !seenInputs[initialInput] {
				t.Fatal("native user input lost its accepted client identity")
			}
			if action == "steer" && !seenInputs[steeredInput] {
				t.Fatal("native steer did not materialize its client input identity")
			}
			if action == "interrupt" {
				_, err = client.StartTurn(ctx, domain.NewID(), domain.NewID(), input(mode))
				assertCode(t, err, domain.Conflict)
				// Native terminal state does not prove that a model response
				// transport or other descendants have been released. Stop's
				// owner must close and reconcile the process scope as well.
				if err := client.Close(); err != nil {
					t.Fatal(err)
				}
				select {
				case <-canceled:
				case <-ctx.Done():
					t.Fatal("owned native cleanup did not close its model connection")
				}
			}
			t.Logf("Codex %s: native %s completed with %d local scripted requests; no external provider or account", SupportedVersion, action, requests.Load())
		})
	}
}
