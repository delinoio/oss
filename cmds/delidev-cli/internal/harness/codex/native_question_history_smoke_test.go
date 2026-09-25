package codex

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// This private native experiment deliberately bypasses the production legacy
// profile. A paginated/raw observation does not enable either production API or
// establish semantic acceptance without an exact original tool-output binding.
func TestManualNativeQuestionHistory(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit installed native harness with local scripted responses only")
	}
	for _, history := range []HistoryMode{LegacyHistory, PaginatedHistory} {
		t.Run(string(history), func(t *testing.T) {
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
			params, err := settings.params()
			if err != nil {
				t.Fatal(err)
			}
			persisted, fallback := false, false
			params.HistoryMode, params.Ephemeral, params.AllowProviderModelFallback = history, &persisted, &fallback
			params.RawEvents = true
			started, err := c.wire.Call(ctx, domain.NewID(), "thread/start", params)
			if err != nil || started.ErrorCode != nil {
				t.Fatalf("native history start failed: %v, rejected=%t", err, started.ErrorCode != nil)
			}
			var bound boundThreadWire
			if domain.Decode(started.Result, &bound) != nil {
				t.Fatal("native history binding shape changed")
			}
			thread, err := decodeThread(bound.Thread)
			if err != nil || thread.HistoryMode != history || thread.SessionID != thread.ID || thread.Status.Type != ThreadIdle || bound.Model != settings.Model || bound.ModelProvider != settings.Provider || !nativePathEqual(bound.Cwd, settings.Cwd) || bound.ApprovalPolicy != ApprovalOnRequest || bound.Sandbox.Type != ReadOnly || bound.ApprovalsReviewer != "user" || bound.ReasoningEffort == nil || *bound.ReasoningEffort != settings.Effort {
				t.Fatal("native history settings changed")
			}
			turnResult, err := c.wire.Call(ctx, domain.NewID(), "turn/start", startTurnParams{ThreadID: thread.ID, Input: []nativeTextInput{{Type: nativeText, Text: "Return only the scripted local question fixture."}}, ClientInputID: domain.NewID(), Model: bound.Model, Effort: bound.ReasoningEffort, Cwd: bound.Cwd, ApprovalPolicy: bound.ApprovalPolicy, ApprovalsReviewer: bound.ApprovalsReviewer, Sandbox: bound.Sandbox, Collaboration: collaborationMode{Mode: nativePlan, Settings: collaborationSettings{Model: bound.Model, Effort: bound.ReasoningEffort}}})
			if err != nil || turnResult.ErrorCode != nil {
				t.Fatal("native history turn rejected")
			}
			turn, err := decodeTurnResponse(turnResult.Result)
			if err != nil || turn.Status != TurnRunning {
				t.Fatal("native history turn changed")
			}
			answered, rawOutputs, completedOutputs := false, 0, 0
			for {
				event, err := c.wire.Next(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if event.Kind == nativewire.ServerRequest {
					var question struct {
						ThreadID domain.ID `json:"threadId"`
						TurnID   domain.ID `json:"turnId"`
						ItemID   string    `json:"itemId"`
					}
					if answered || event.Method != "item/tool/requestUserInput" || json.Unmarshal(event.Params, &question) != nil || question.ThreadID != thread.ID || question.TurnID != turn.ID || question.ItemID != "call_question_fixture" {
						t.Fatal("native history question ownership changed")
					}
					if err := c.wire.Reply(ctx, event, nativeQuestionResponse{Answers: map[string]nativeQuestionAnswer{"choice": {Answers: []string{"First"}}}}); err != nil {
						t.Fatal(err)
					}
					answered = true
				}
				if event.Method == "item/completed" || event.Method == "rawResponseItem/completed" {
					var observation struct {
						ThreadID domain.ID `json:"threadId"`
						TurnID   domain.ID `json:"turnId"`
						Item     struct {
							Type   string
							ID     string
							CallID string `json:"call_id"`
							Output json.RawMessage
						}
					}
					if json.Unmarshal(event.Params, &observation) != nil || observation.ThreadID != thread.ID || observation.TurnID != turn.ID {
						t.Fatal("native history observation ownership changed")
					}
					if observation.Item.Type == "functionCallOutput" {
						completedOutputs++
					}
					if observation.Item.Type == "function_call_output" {
						var output string
						var answer nativeQuestionResponse
						if !answered || observation.Item.CallID != "call_question_fixture" || json.Unmarshal(observation.Item.Output, &output) != nil || domain.Decode([]byte(output), &answer) != nil || len(answer.Answers) != 1 || len(answer.Answers["choice"].Answers) != 1 || answer.Answers["choice"].Answers[0] != "First" {
							t.Fatal("native raw output does not bind the exact answer")
						}
						rawOutputs++
					}
				}
				if event.Method == "turn/completed" {
					var completion struct {
						ThreadID domain.ID `json:"threadId"`
						Turn     struct {
							ID     domain.ID
							Status TurnStatus
						}
					}
					if json.Unmarshal(event.Params, &completion) != nil || completion.ThreadID != thread.ID || completion.Turn.ID != turn.ID || completion.Turn.Status != TurnCompleted {
						t.Fatal("native history turn failed")
					}
					break
				}
			}
			if !answered || !received.Load() || requests.Load() != 2 || rawOutputs != 1 {
				t.Fatal("native history answer was not received exactly")
			}
			listed, err := c.wire.Call(ctx, domain.NewID(), "thread/items/list", map[string]any{"threadId": thread.ID, "turnId": turn.ID, "limit": 100, "sortDirection": "asc"})
			if err != nil {
				t.Fatal(err)
			}
			if history == LegacyHistory {
				if listed.ErrorCode == nil || *listed.ErrorCode != -32601 {
					t.Fatal("legacy history support changed")
				}
				t.Logf("legacy profile: %d raw output events, %d canonical output events; history list unsupported", rawOutputs, completedOutputs)
				return
			}
			if listed.ErrorCode != nil {
				t.Fatal("paginated history unavailable")
			}
			var page struct {
				Data []struct {
					TurnID domain.ID `json:"turnId"`
					Item   json.RawMessage
				}
				NextCursor      *string `json:"nextCursor"`
				BackwardsCursor *string `json:"backwardsCursor"`
			}
			if domain.Decode(listed.Result, &page) != nil || page.Data == nil || page.NextCursor != nil {
				t.Fatal("paginated native history shape changed")
			}
			outputs := 0
			for _, entry := range page.Data {
				if entry.TurnID != turn.ID {
					t.Fatal("foreign native history turn")
				}
				var item struct{ Type string }
				if json.Unmarshal(entry.Item, &item) != nil {
					t.Fatal("invalid native history item")
				}
				if item.Type == "functionCallOutput" {
					outputs++
				}
			}
			t.Logf("paginated profile: %d raw output events, %d canonical output events, %d history output items of %d; no external account", rawOutputs, completedOutputs, outputs, len(page.Data))
		})
	}
}
