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
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

const mixedFixtureQuestion = "Which original mixed option?"

func TestManualNativeGrokMixedTools(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_GROK_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit private native executable required")
	}
	for _, test := range []struct {
		name                         string
		remember, cancel, rejectLast bool
	}{{name: "once"}, {name: "remembered", remember: true}, {name: "question-cancelled", remember: true, cancel: true}, {name: "last-write-rejected", rejectLast: true}} {
		t.Run(test.name, func(t *testing.T) {
			config, logs := fixtureAPIConfig(t, "native-mixed-tools")
			config.Probe.Process.Executable = binary
			path := filepath.Join(config.Workspace, "original.txt")
			const original = "Original mixed fixture.\n"
			written := func(index int) string { return fmt.Sprintf("Mixed original write %d.\n", index) }
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Uint32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/api-proxy/v1/chat/completions" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer "+apiFixtureToken || calls.Add(1) > 14 {
					t.Error("mixed fixture provider authority changed")
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
					t.Error("mixed fixture model changed")
					return
				}
				step := 0
				for _, message := range body.Messages {
					if message.Role != "tool" {
						continue
					}
					var content string
					if message.ID != fmt.Sprintf("mixed-tool-%d", step) || json.Unmarshal(message.Content, &content) != nil || content == "" {
						t.Error("mixed fixture tool lineage changed")
						return
					}
					step++
				}
				delta := map[string]any{"role": "assistant", "content": "Original mixed tools completed."}
				finish := "stop"
				if len(body.Tools) > 0 && step < 4 {
					var name fileToolName
					var arguments any
					switch step {
					case 0, 2:
						name, arguments = writeFileTool, map[string]any{"file_path": path, "content": written(step)}
					case 1:
						name, arguments = askQuestionTool, map[string]any{"questions": []any{map[string]any{"question": mixedFixtureQuestion, "options": []any{map[string]any{"label": "Blue", "description": "First"}, map[string]any{"label": "Green", "description": "Second"}}}}}
					case 3:
						name, arguments = readFileTool, map[string]any{"target_file": path}
					}
					args, _ := json.Marshal(arguments)
					delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("mixed-tool-%d", step), "type": "function", "function": map[string]any{"name": name, "arguments": string(args)}}}}
					finish = "tool_calls"
				}
				w.Header().Set("Content-Type", "text/event-stream")
				for _, chunk := range []any{
					map[string]any{"id": "chat-mixed", "object": "chat.completion.chunk", "created": 1, "model": turnFixtureModel, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}},
					map[string]any{"id": "chat-mixed", "object": "chat.completion.chunk", "created": 1, "model": turnFixtureModel, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}, "usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 5, "total_tokens": 16}},
				} {
					encoded, _ := json.Marshal(chunk)
					_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
				}
				_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer provider.Close()
			config.ServerOrigin, config.Model = provider.URL, turnFixtureModel
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			var fileClaims []FilePermissionClaim
			var questionClaims []QuestionClaim
			api, err := OpenOwnedAPIWithTools(ctx, config, func(_ context.Context, c CreationClaim) error { return c.Validate() }, func(_ context.Context, c InputClaim) error { return c.Validate() }, func(context.Context, ClosureClaim) error { return incompatible() }, func(_ context.Context, c FilePermissionClaim) error {
				fileClaims = append(fileClaims, c)
				return c.Validate()
			}, func(_ context.Context, c QuestionClaim) error {
				questionClaims = append(questionClaims, c)
				return c.Validate()
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
						t.Error("mixed runtime retained execution token")
					}
					return err
				}); err != nil {
					t.Error(err)
				}
				for _, private := range []string{config.Token, config.Workspace, mixedFixtureQuestion, original, written(0), written(2)} {
					if strings.Contains(logs.String(), private) {
						t.Error("mixed diagnostics exposed private content")
					}
				}
			}()
			if _, err := api.Create(ctx, domain.NewID(), domain.NewID()); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(api.connection.profile.path)
			if err != nil {
				t.Fatal(err)
			}
			completed, rejected, inherited := false, false, false
			fileTools, questionTools := 0, 0
			result, err := api.RunTools(ctx, domain.NewID(), "Use the original mixed fixture tools.", func(callback context.Context, v InputObservation) error {
				if v.Permission != nil {
					decision := AllowFileOnce
					if test.remember {
						decision = AllowFileSession
					}
					if v.Permission.ToolID == "mixed-tool-2" {
						if len(questionClaims) != 1 {
							t.Fatal("later Write preceded original question")
						}
						if _, err := api.ReplyFilePermission(callback, questionClaims[0].RequestID, v.Permission.ArrivalID, decision); err == nil {
							t.Fatal("question operation identity acquired file response")
						}
						if test.rejectLast {
							decision = RejectFileOnce
						}
					}
					if _, err := api.ReplyFilePermission(callback, domain.NewID(), v.Permission.ArrivalID, decision); err != nil {
						return err
					}
				}
				if v.QuestionOffer != nil {
					answer := QuestionAnswer{Outcome: QuestionAccepted, Answers: map[string]string{mixedFixtureQuestion: "Blue"}}
					if test.cancel {
						answer = QuestionAnswer{Outcome: QuestionCancelled}
					}
					if len(fileClaims) != 1 {
						t.Fatal("question preceded original Write response")
					}
					if _, err := api.ReplyQuestion(callback, fileClaims[0].RequestID, v.QuestionOffer.ArrivalID, answer); err == nil {
						t.Fatal("file response operation acquired question reply")
					}
					if _, err := api.ReplyQuestion(callback, domain.NewID(), v.QuestionOffer.ArrivalID, answer); err != nil {
						return err
					}
				}
				if v.FileTool != nil && v.FileTool.Observation != nil {
					tool := v.FileTool.Observation
					if tool.Phase == fileToolCompleted || tool.Phase == fileToolFailed {
						fileTools++
						if tool.ID == "mixed-tool-2" && test.remember {
							inherited = v.FileTool.InheritedPermission == fileClaims[0].ArrivalID
						}
					}
				}
				if v.Question != nil && v.Question.Observation != nil && v.Question.Observation.Phase == fileToolCompleted {
					questionTools++
				}
				completed = completed || v.Kind == InputCompleted
				rejected = rejected || v.Kind == InputPermissionRejected
				if v.Kind == StopSettled {
					t.Fatal("mixed reply became plain-text Stop")
				}
				return nil
			})
			expectedFiles, expectedClaims, expectedInput := 3, 2, uint64(55)
			expectedContent := written(2)
			if test.remember {
				expectedClaims = 1
			}
			if test.rejectLast {
				expectedFiles, expectedInput, expectedContent = 2, 33, written(0)
				if err == nil || domain.SafeError(err).Code != domain.Canceled || completed || !rejected || result.Reason != Cancelled {
					t.Fatal("mixed original Write rejection lost native result", err)
				}
			} else if err != nil || !completed || rejected || result.Reason != EndTurn {
				t.Fatal("mixed original input did not complete", err)
			}
			if fileTools != expectedFiles || questionTools != 1 || len(fileClaims) != expectedClaims || len(questionClaims) != 1 || inherited != test.remember || result.Meta.Usage.Input != expectedInput || result.Meta.Input != 11 {
				t.Fatal("mixed native facts or response scope changed")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != expectedContent {
				t.Fatal("mixed original file contents disagreed with replies")
			}
			after, err = os.ReadFile(api.connection.profile.path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("mixed tools changed original configuration")
			}
		})
	}
}
