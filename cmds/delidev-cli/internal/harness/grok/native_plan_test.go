package grok

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

const originalPlanContent = "# Original fixture plan\n\nRead the isolated fixture file.\n"
const revisedPlanContent = "# Revised fixture plan\n\nRead the corrected isolated fixture file.\n"
const originalPlanQuestion = "Which original Plan option?"

func TestManualNativeGrokOriginalPlanning(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_GROK_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit private native executable required")
	}
	for _, initial := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		for _, scenario := range []string{"approved", "cancelled", "abandoned", "revised", "approved-write", "abandoned-write"} {
			t.Run(string(initial)+"/"+scenario, func(t *testing.T) {
				config, logs := fixtureAPIConfig(t, "native-plan")
				config.Probe.Process.Executable, config.Mode = binary, initial
				ordinaryPath := filepath.Join(config.Workspace, "original.txt")
				if err := os.WriteFile(ordinaryPath, []byte("Original file.\n"), 0600); err != nil {
					t.Fatal(err)
				}
				var mu sync.Mutex
				var planFile string
				planReady := make(chan struct{})
				var calls atomic.Uint32
				ordinary := strings.HasSuffix(scenario, "-write")
				toolCount := 4
				if scenario == "revised" {
					toolCount = 6
				} else if ordinary {
					toolCount = 5
				}
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet && r.URL.Path == "/" {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if r.Method != http.MethodPost || r.URL.Path != "/api-proxy/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+apiFixtureToken || calls.Add(1) > 20 {
						t.Error("invalid original Plan provider authority")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
					var body struct {
						Model    string            `json:"model"`
						Tools    []json.RawMessage `json:"tools"`
						Messages []struct {
							Role    string          `json:"role"`
							ID      string          `json:"tool_call_id"`
							Content json.RawMessage `json:"content"`
						} `json:"messages"`
					}
					if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &body) != nil || body.Model != turnFixtureModel {
						t.Error("invalid original Plan provider body")
						return
					}
					count := 0
					for _, m := range body.Messages {
						if m.Role == "tool" {
							if m.ID != fmt.Sprintf("original-plan-%d", count) {
								t.Error("Plan tool result order changed")
								return
							}
							count++
						}
					}
					delta := map[string]any{"role": "assistant", "content": "Original Plan fixture completed."}
					finish := "stop"
					if len(body.Tools) > 0 && count < toolCount {
						var name fileToolName
						args := map[string]any{}
						switch count {
						case 0:
							name = enterPlanTool
						case 1:
							name = askQuestionTool
							args = map[string]any{"questions": []any{map[string]any{"question": originalPlanQuestion, "options": []any{map[string]any{"label": "Blue", "description": "Original blue option"}, map[string]any{"label": "Green", "description": "Original green option"}}}}}
						case 2, 4:
							select {
							case <-planReady:
							case <-r.Context().Done():
								return
							}
							mu.Lock()
							path := planFile
							mu.Unlock()
							content := originalPlanContent
							if count == 4 {
								if ordinary {
									path, content = ordinaryPath, "Updated original file.\n"
								} else {
									content = revisedPlanContent
								}
							}
							name = writeFileTool
							args = map[string]any{"file_path": path, "content": content}
						case 3, 5:
							name = exitPlanTool
						}
						arguments, _ := json.Marshal(args)
						delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("original-plan-%d", count), "type": "function", "function": map[string]any{"name": name, "arguments": string(arguments)}}}}
						finish = "tool_calls"
					}
					w.Header().Set("Content-Type", "text/event-stream")
					for _, part := range []any{
						map[string]any{"id": "chat-plan-fixture", "object": "chat.completion.chunk", "created": 1, "model": turnFixtureModel, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}},
						map[string]any{"id": "chat-plan-fixture", "object": "chat.completion.chunk", "created": 1, "model": turnFixtureModel, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}, "usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 5, "total_tokens": 16}},
					} {
						encoded, _ := json.Marshal(part)
						_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
					}
					_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
				}))
				defer provider.Close()
				config.ServerOrigin, config.Model = provider.URL, turnFixtureModel
				ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
				defer cancel()
				var inputs []InputClaim
				var modes []ModeClaim
				var plans []PlanClaim
				var questions []QuestionClaim
				var files []FilePermissionClaim
				api, err := OpenOwnedAPIWithPlanning(ctx, config, PlanningRecorders{
					Creation: func(_ context.Context, c CreationClaim) error { return c.Validate() },
					Mode:     func(_ context.Context, c ModeClaim) error { modes = append(modes, c); return c.Validate() },
					Input:    func(_ context.Context, c InputClaim) error { inputs = append(inputs, c); return c.Validate() },
					Question: func(_ context.Context, c QuestionClaim) error { questions = append(questions, c); return c.Validate() },
					File:     func(_ context.Context, c FilePermissionClaim) error { files = append(files, c); return c.Validate() },
					Plan:     func(_ context.Context, c PlanClaim) error { plans = append(plans, c); return c.Validate() },
				})
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := api.Close(); err != nil {
						t.Error(err)
					}
					if err := process.ReconcileOwner(config.Probe.Process.Directory, config.Probe.Process.OwnerID); err != nil {
						t.Error(err)
					}
					if err := filepath.WalkDir(filepath.Dir(config.Probe.Home), func(path string, entry os.DirEntry, err error) error {
						if err != nil || entry.IsDir() {
							return err
						}
						raw, err := os.ReadFile(path)
						if bytes.Contains(raw, []byte(config.Token)) {
							t.Error("native Plan runtime retained token")
						}
						return err
					}); err != nil {
						t.Error(err)
					}
					for _, private := range []string{config.Token, config.Workspace, originalPlanContent, revisedPlanContent, originalPlanQuestion} {
						if strings.Contains(logs.String(), private) {
							t.Error("Plan diagnostics exposed private content")
						}
					}
				}()
				session, err := api.Create(ctx, domain.NewID(), domain.NewID())
				if err != nil {
					t.Fatal(err)
				}
				if initial == domain.PlanMode {
					if _, err := api.SelectPlan(ctx, domain.NewID()); err != nil {
						t.Fatal(err)
					}
				}
				bound, err := api.SessionBinding(ctx)
				if err != nil || bound.NativeSessionID != session || bound.Model != config.Model || (bound.ModeBinding != nil) != (initial == domain.PlanMode) {
					t.Fatal("native Plan binding lost original session/mode", err)
				}
				before, err := os.ReadFile(api.connection.profile.path)
				if err != nil {
					t.Fatal(err)
				}
				accepted, completed := false, false
				observedModes := []NativeMode{}
				responses, completedTools, planWrites := 0, 0, 0
				var arrivals []domain.ID
				input := domain.NewID()
				result, err := api.RunPlanning(ctx, input, "Use the original native Plan workflow.", func(callback context.Context, v InputObservation) error {
					if v.InputID != input || !nativeUUID(v.NativePromptID, 4) || completed {
						t.Fatal("Plan observation lost original input")
					}
					if v.Kind == InputAccepted {
						if len(inputs) != 2 || accepted {
							t.Fatal("Plan acceptance preceded input binding")
						}
						accepted = true
					}
					if !accepted {
						t.Fatal("unowned Plan observation")
					}
					if v.Plan != nil {
						if o := v.Plan.Observation; o != nil && o.Phase == fileToolCompleted {
							completedTools++
							if o.Entered != nil {
								mu.Lock()
								planFile = o.Entered.Path
								mu.Unlock()
								close(planReady)
							}
						}
						if m := v.Plan.Mode; m != nil {
							observedModes = append(observedModes, m.Update.Mode)
						}
					}
					if v.QuestionOffer != nil {
						if v.Question == nil || v.Question.Request == nil || v.Question.Request.Mode != NativePlanMode {
							t.Fatal("Plan question mode changed")
						}
						if _, err := api.ReplyQuestion(callback, domain.NewID(), v.QuestionOffer.ArrivalID, QuestionAnswer{Outcome: QuestionAccepted, Answers: map[string]string{originalPlanQuestion: "Blue"}}); err != nil {
							return err
						}
					}
					if v.Question != nil && v.Question.Observation != nil && v.Question.Observation.Phase == fileToolCompleted {
						completedTools++
					}
					if v.FileTool != nil && v.FileTool.Observation != nil && v.FileTool.Observation.Phase == fileToolCompleted {
						completedTools++
						if v.FileTool.PlanFile != nil {
							planWrites++
							if v.FileTool.PlanFile.Revision != uint64(planWrites-1) || v.FileTool.InheritedPermission != "" || len(files) != 0 {
								t.Fatal("Plan write acquired synthetic permission")
							}
							v.FileTool.PlanFile.EntryToolID = "changed"
						}
					}
					if v.Permission != nil {
						if !ordinary || len(plans) != 1 || len(files) != 0 {
							t.Fatal("native Plan artifact requested ordinary file permission")
						}
						if _, err := api.ReplyFilePermission(callback, plans[0].RequestID, v.Permission.ArrivalID, AllowFileOnce); err == nil {
							t.Fatal("file response reused Plan operation")
						}
						if _, err := api.ReplyFilePermission(callback, domain.NewID(), v.Permission.ArrivalID, AllowFileOnce); err != nil {
							return err
						}
					}
					if v.PlanOffer != nil {
						if v.Plan == nil || v.Plan.Request == nil || v.Plan.Request.Session != session || v.PlanOffer.Origin.Revision != uint64(planWrites) {
							t.Fatal("Plan approval lost original revision")
						}
						outcome := PlanOutcome(strings.TrimSuffix(scenario, "-write"))
						expected := originalPlanContent
						if scenario == "revised" {
							outcome = PlanCancelled
							if len(plans) == 1 {
								outcome = PlanApproved
								expected = revisedPlanContent
							}
						}
						if v.Plan.Request.Content != expected {
							t.Fatal("Plan proposal changed original written content")
						}
						arrival := v.PlanOffer.ArrivalID
						arrivals = append(arrivals, arrival)
						v.PlanOffer.Origin.Revision = 999
						v.Plan.Request.Content = "changed"
						if _, err := api.ReplyPlan(callback, questions[0].RequestID, arrival, outcome); err == nil {
							t.Fatal("Plan response reused question operation")
						}
						delivery, err := api.ReplyPlan(callback, domain.NewID(), arrival, outcome)
						if err != nil || !delivery.Claimed || !delivery.Delivered || delivery.Resolved || delivery.Claim.Revision != uint64(planWrites) {
							t.Fatal("Plan delivery replaced independent native evidence", err)
						}
						if _, err := api.ReplyPlan(callback, domain.NewID(), arrival, outcome); err == nil {
							t.Fatal("Plan response replayed")
						}
					}
					if v.Kind == InputResponse {
						responses++
					}
					if v.Kind == InputCompleted {
						if completedTools != toolCount || responses != toolCount+1 {
							t.Fatal("Plan root completed before original tool/counter evidence")
						}
						completed = true
					}
					if v.Kind == StopSettled || v.Kind == InputPermissionRejected {
						t.Fatal("Plan revision/abandonment became Stop or rejection")
					}
					return nil
				})
				if err != nil || !completed || result.Reason != EndTurn || result.Meta.Input != 11 || result.Meta.Usage.Input != uint64(toolCount+1)*11 {
					t.Logf("Original Plan progress: responses=%d tools=%d writes=%d replies=%d modes=%v", responses, completedTools, planWrites, len(plans), observedModes)
					t.Log(logs.String())
					t.Fatal("original Plan workflow did not complete", err)
				}
				expectedPlans, expectedWrites := 1, 1
				if scenario == "revised" {
					expectedPlans, expectedWrites = 2, 2
				}
				if len(plans) != expectedPlans || planWrites != expectedWrites || len(questions) != 1 || ordinary != (len(files) == 1) {
					t.Fatal("original Plan claims changed")
				}
				for _, arrival := range arrivals {
					d, err := api.InspectPlan(arrival)
					if err != nil || !d.Resolved || d.ToolPhase != fileToolCompleted || d.ProblemCode != "" {
						t.Fatal("Plan response was not natively settled", err)
					}
				}
				expectedModes := 0
				if initial == domain.ExecuteMode {
					expectedModes++
				}
				if scenario != "cancelled" {
					expectedModes++
				}
				if len(observedModes) != expectedModes || initial == domain.PlanMode && len(modes) != 2 || initial == domain.ExecuteMode && len(modes) != 0 {
					t.Fatal("Plan mode observations invented or lost a transition")
				}
				mu.Lock()
				path := planFile
				mu.Unlock()
				content, err := os.ReadFile(path)
				expected := originalPlanContent
				if scenario == "revised" {
					expected = revisedPlanContent
				}
				if err != nil || string(content) != expected {
					t.Fatal("native plan file lost original revision", err)
				}
				file, err := os.ReadFile(ordinaryPath)
				expected = "Original file.\n"
				if ordinary {
					expected = "Updated original file.\n"
				}
				if err != nil || string(file) != expected {
					t.Fatal("native ordinary file changed without its separate approval", err)
				}
				after, err := os.ReadFile(api.connection.profile.path)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("native Plan changed owned configuration")
				}
				if _, err := api.CloseText(ctx, domain.NewID()); err == nil {
					t.Fatal("Plan acquired plain-text history")
				}
			})
		}
	}
}
