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
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

func TestManualNativeGrokOriginalQuestions(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_GROK_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit private native executable required")
	}
	for _, mode := range []string{"question", "question-stream", "question-cancel", "question-multiple", "question-free", "question-skip"} {
		t.Run(mode, func(t *testing.T) {
			config, logs := fixtureAPIConfig(t, "native-question")
			config.Probe.Process.Executable = binary
			var mu sync.Mutex
			var calls int
			var returnedTools []json.RawMessage
			firstDelta := make(chan struct{})
			var observedDelta sync.Once
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/api-proxy/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+apiFixtureToken {
					t.Error("unexpected provider authority")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				mu.Lock()
				calls++
				bounded := calls <= 8
				mu.Unlock()
				if !bounded {
					t.Error("question provider request bound exceeded")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
				var body struct {
					Model    string            `json:"model"`
					Tools    []json.RawMessage `json:"tools"`
					Messages []json.RawMessage `json:"messages"`
				}
				if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &body) != nil || body.Model != turnFixtureModel {
					t.Error("invalid fixture request")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				toolResult := false
				for _, m := range body.Messages {
					var row struct {
						Role string `json:"role"`
					}
					_ = json.Unmarshal(m, &row)
					if row.Role == "tool" {
						var result struct {
							ID      string `json:"tool_call_id"`
							Content string `json:"content"`
						}
						if json.Unmarshal(m, &result) != nil || result.ID != "call_delidev_read" || result.Content == "" {
							t.Error("foreign native question result")
							return
						}
						toolResult = true
						mu.Lock()
						returnedTools = append(returnedTools, m)
						mu.Unlock()
					}
				}
				delta := map[string]any{"role": "assistant", "content": "Tool fixture complete."}
				finish := "stop"
				remainder := ""
				if len(body.Tools) > 0 && !toolResult {
					arguments := map[string]any{"questions": []any{map[string]any{"question": "Which original fixture color?", "options": []any{map[string]any{"label": "Blue", "description": "First fixture choice"}, map[string]any{"label": "Green", "description": "Second fixture choice"}}}}}
					if mode == "question-multiple" {
						arguments = map[string]any{"questions": []any{map[string]any{"question": "Which original fixture colors?", "multi_select": true, "options": []any{map[string]any{"label": "Blue", "description": "First fixture choice"}, map[string]any{"label": "Green", "description": "Second fixture choice"}}}, map[string]any{"question": "Which fixture note?", "options": []any{map[string]any{"label": "Brief", "description": "Short fixture"}, map[string]any{"label": "Long", "description": "Long fixture"}}}}}
					}
					args, _ := json.Marshal(arguments)
					first := string(args)
					if mode == "question-stream" {
						first, remainder = string(args[:len(args)/2]), string(args[len(args)/2:])
					}
					delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call_delidev_read", "type": "function", "function": map[string]any{"name": "ask_user_question", "arguments": first}}}}
					finish = "tool_calls"
				}
				w.Header().Set("Content-Type", "text/event-stream")
				for index, part := range []any{
					map[string]any{"id": "chat-read-fixture", "object": "chat.completion.chunk", "created": 1, "model": turnFixtureModel, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}},
					map[string]any{"id": "chat-read-fixture", "object": "chat.completion.chunk", "created": 1, "model": turnFixtureModel, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}, "usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 5, "total_tokens": 16}},
				} {
					encoded, _ := json.Marshal(part)
					_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
					if index == 0 && remainder != "" {
						w.(http.Flusher).Flush()
						select {
						case <-firstDelta:
						case <-r.Context().Done():
							return
						}
						chunk, _ := json.Marshal(map[string]any{"id": "chat-read-fixture", "object": "chat.completion.chunk", "created": 1, "model": turnFixtureModel, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": remainder}}}}, "finish_reason": nil}}})
						_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
					}
				}
				_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer provider.Close()
			config.ServerOrigin, config.Model = provider.URL, turnFixtureModel
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			var claims []InputClaim
			var replies []QuestionClaim
			api, err := OpenOwnedAPIWithQuestions(ctx, config, func(_ context.Context, c CreationClaim) error { return c.Validate() }, func(_ context.Context, c InputClaim) error { claims = append(claims, c); return c.Validate() }, func(context.Context, ClosureClaim) error { t.Error("question acquired closure"); return incompatible() }, func(_ context.Context, c QuestionClaim) error { replies = append(replies, c); return c.Validate() })
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
						t.Error("native question runtime retained execution token")
					}
					return err
				}); err != nil {
					t.Error(err)
				}
				for _, private := range []string{config.Token, config.Workspace, "Which original fixture", "A custom fixture answer.", "Private fixture note"} {
					if strings.Contains(logs.String(), private) {
						t.Error("question diagnostics exposed private contents")
					}
				}
			}()
			session, err := api.Create(ctx, domain.NewID(), domain.NewID())
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(api.connection.profile.path)
			if err != nil {
				t.Fatal(err)
			}
			input := domain.NewID()
			var arrival domain.ID
			accepted, completed := false, false
			responses, tools, deltas := 0, 0, 0
			var stages []questionInteractionStage
			result, err := api.RunQuestions(ctx, input, "Ask the original fixture questions.", func(callback context.Context, event InputObservation) error {
				if event.InputID != input || !nativeUUID(event.NativePromptID, 4) || completed {
					t.Fatal("changed question input identity")
				}
				switch event.Kind {
				case InputAccepted:
					if accepted || len(claims) != 2 {
						t.Fatal("question accepted before original binding")
					}
					accepted = true
				case InputQuestion:
					if !accepted || event.Question == nil {
						t.Fatal("unowned question fact")
					}
					if v := event.Question.Delta; v != nil {
						deltas++
						if mode == "question-stream" {
							if deltas == 2 && (v.Update.ID != nil || v.Update.Name != nil) {
								t.Fatal("fragmented question acquired invented native identities")
							}
							observedDelta.Do(func() { close(firstDelta) })
						}
					}
					if v := event.Question.Interaction; v != nil {
						stages = append(stages, v.Stage)
					}
					if v := event.Question.Observation; v != nil && v.Phase == fileToolCompleted {
						tools++
					}
					if request := event.Question.Request; request != nil {
						if event.QuestionOffer == nil || arrival != "" || request.Session != session || request.Mode != questionDefaultMode || len(stages) != 3 {
							t.Fatal("question borrowed permission resolution")
						}
						arrival = event.QuestionOffer.ArrivalID
						answer := QuestionAnswer{Outcome: QuestionAccepted, Answers: map[string]string{request.Questions[0].Question: "Blue"}}
						switch mode {
						case "question-cancel":
							answer = QuestionAnswer{Outcome: QuestionCancelled}
						case "question-multiple":
							answer.Answers = map[string]string{request.Questions[0].Question: "Blue, Green", request.Questions[1].Question: "A custom fixture answer."}
							if string(request.Questions[0].MultiSelect) != "true" || !isNull(request.Questions[1].MultiSelect) {
								t.Fatal("multiple selection metadata changed")
							}
						case "question-free":
							answer.Answers = map[string]string{request.Questions[0].Question: "A custom fixture answer."}
							answer.Annotations = map[string]QuestionAnnotation{request.Questions[0].Question: {Notes: "Private fixture note"}}
						case "question-skip":
							answer = QuestionAnswer{Outcome: QuestionSkipInterview, PartialAnswers: map[string]string{request.Questions[0].Question: "Blue"}}
						}
						body, err := answer.body(request.Questions)
						if err != nil {
							t.Fatal(err)
						}
						digest := fileDigest(body)
						request.Questions[0].Question = "changed"
						request.Questions[0].Options[0].Label = "changed"
						event.QuestionOffer.ToolID = "changed"
						delivery, err := api.ReplyQuestion(callback, domain.NewID(), arrival, answer)
						if err != nil || !delivery.Claimed || !delivery.Attempted || !delivery.Delivered || delivery.Resolved || delivery.ToolPhase != "" || delivery.Claim.BodyDigest != digest || delivery.Claim.NativeSessionID != session || delivery.Claim.ToolID != "call_delidev_read" {
							t.Fatal("question delivery lost original authority", err)
						}
						if _, err := api.ReplyQuestion(callback, domain.NewID(), arrival, answer); err == nil {
							t.Fatal("question response replayed")
						}
					}
				case InputResponse:
					if event.Response == nil || event.Response.Input != 11 || event.Response.Output != 5 {
						t.Fatal("question accounting changed")
					}
					responses++
				case InputText, InputTitle:
					if !accepted {
						t.Fatal("unaccepted question output")
					}
				case InputCompleted:
					if !accepted || arrival == "" || responses != 2 || tools != 1 || len(stages) != 4 || event.Result == nil {
						t.Fatal("question completed before independent facts")
					}
					completed = true
				default:
					t.Fatal("unexpected question observation")
				}
				return nil
			})
			if err != nil || !completed || len(replies) != 1 || result.Reason != EndTurn || result.Meta.Input != 11 || result.Meta.Usage.Input != 22 || result.Meta.Usage.Calls != 2 {
				t.Fatal("original question did not complete", err)
			}
			if mode == "question-stream" && deltas != 2 {
				t.Fatal("fragmented question did not retain both original chunks")
			}
			delivery, err := api.InspectQuestion(arrival)
			if err != nil || !delivery.Resolved || delivery.ToolPhase != fileToolCompleted || delivery.ProblemCode != "" {
				t.Fatal("question native resolution did not settle", err)
			}
			after, err := os.ReadFile(api.connection.profile.path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("question altered original native configuration")
			}
			if _, err := api.CloseText(ctx, domain.NewID()); err == nil {
				t.Fatal("question acquired plain-text history")
			}
			if _, err := api.RunQuestions(ctx, domain.NewID(), "Replay.", func(context.Context, InputObservation) error { return nil }); err == nil {
				t.Fatal("question input replayed")
			}
			mu.Lock()
			toolCount := len(returnedTools)
			mu.Unlock()
			if toolCount == 0 {
				t.Fatal("original question result never reached provider")
			}
		})
	}
}
