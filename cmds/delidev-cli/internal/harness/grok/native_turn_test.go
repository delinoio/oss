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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

// This opt-in test validates actual pinned schemas through a scripted private
// provider. Its raw transport sends are deliberately test-only: they do not
// implement the durable input/interaction/publication controller.
func TestManualNativeGrokTextCompletion(t *testing.T) {
	nativeTextCompletion(t, false)
}

func TestManualNativeGrokOwnedTextInput(t *testing.T) {
	nativeTextCompletion(t, true)
}

func nativeTextCompletion(t *testing.T, owned bool) {
	t.Helper()
	binary := os.Getenv("DELIDEV_NATIVE_GROK_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit private native Grok Build binary required")
	}
	var calls atomic.Uint32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api-proxy/v1/chat/completions" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer "+apiFixtureToken {
			t.Error("unexpected native provider authority or operation")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
		var body struct {
			Model string `json:"model"`
		}
		if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &body) != nil || body.Model != turnFixtureModel {
			t.Error("native model or request bound changed")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{
			`{"id":"chat-fixture","object":"chat.completion.chunk","created":1,"model":"fixture-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Private fixture response."},"finish_reason":null}]}`,
			`{"id":"chat-fixture","object":"chat.completion.chunk","created":1,"model":"fixture-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":5,"total_tokens":16}}`,
			`[DONE]`,
		} {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
	}))
	defer provider.Close()
	config, logs := fixtureAPIConfig(t, "native-turn")
	config.Probe.Process.Executable = binary
	config.ServerOrigin, config.Model = provider.URL, turnFixtureModel
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	api, err := openAPI(ctx, config)
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
				t.Error("native input persisted its execution credential")
			}
			return err
		}); err != nil {
			t.Error(err)
		}
		for _, private := range []string{config.Token, config.Model, config.Workspace, "Private fixture response."} {
			if strings.Contains(logs.String(), private) {
				t.Error("private native input or output logged")
			}
		}
	}()
	session, err := api.Create(ctx, domain.NewID(), domain.NewID(), func(_ context.Context, claim CreationClaim) error { return claim.Validate() })
	if err != nil {
		t.Fatal(err)
	}
	const input = "Return a private fixture response."
	if owned {
		request := domain.NewID()
		claims := []InputClaim{}
		accepted, completed := false, false
		var output strings.Builder
		result, err := api.RunText(ctx, request, input, func(_ context.Context, claim InputClaim) error {
			if err := claim.Validate(); err != nil {
				return err
			}
			if claim.RequestID != request || claim.NativeSessionID != session {
				t.Error("original native input claim changed")
			}
			if claim.Phase == ClaimInput && calls.Load() != 0 {
				t.Error("inference preceded original input claim")
			}
			claims = append(claims, claim)
			return nil
		}, func(_ context.Context, observation InputObservation) error {
			if observation.InputID != request || !nativeUUID(observation.NativePromptID, 4) || completed {
				t.Error("foreign or late original input publication")
			}
			switch observation.Kind {
			case InputAccepted:
				if accepted {
					t.Error("input accepted twice")
				}
				accepted = true
			case InputText:
				if !accepted || observation.Chunk == nil {
					t.Error("unaccepted text")
				}
				output.WriteString(observation.Chunk.Update.Content.Text)
			case InputTitle:
				if !accepted {
					t.Error("unaccepted title")
				}
			case InputCompleted:
				if !accepted || observation.Result == nil {
					t.Error("unaccepted completion")
				}
				completed = true
			default:
				t.Error("unknown original observation")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("owned original input: %v; %s", err, logs.String())
		}
		if !accepted || !completed || len(claims) != 2 || claims[0].BodyDigest != claims[1].BodyDigest || claims[1].NativePromptID != result.Meta.Prompt || output.String() != "Private fixture response." || result.Meta.Usage.Input != 11 || result.Meta.Usage.Output != 5 {
			t.Fatal("incomplete owned native input")
		}
		if _, err := api.RunText(ctx, domain.NewID(), input, func(context.Context, InputClaim) error { t.Error("second native input claimed"); return nil }, func(context.Context, InputObservation) error { return nil }); err == nil {
			t.Fatal("original input boundary reopened")
		}
		return
	}

	response, err := api.wire.Call(ctx, domain.NewID(), "session/prompt", map[string]any{"sessionId": session, "prompt": []any{map[string]any{"type": "text", "text": input}}})
	if err != nil || response.ErrorCode != nil {
		t.Fatal("native fixture prompt failed", err)
	}
	var advertised PromptResult
	if decode(response.Result, &advertised) != nil {
		t.Fatal("incompatible native prompt envelope")
	}
	prompt := advertised.Meta.Prompt
	result, err := parsePromptResult(response.Result, session, prompt, turnFixtureModel)
	if err != nil {
		t.Fatal(err)
	}
	var turn TurnCompleted
	var completion PromptCompleted
	var text strings.Builder
	queue := inputQueue{session: session, text: input}
	completed := false
	for !completed {
		event, err := api.wire.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind != nativewire.Notification {
			t.Fatal("unexpected native fixture interaction")
		}
		switch event.Method {
		case "_x.ai/queue/changed":
			if err := queue.observe(event.Params); err != nil || queue.prompt != prompt {
				t.Fatal("invalid original native queue binding", err)
			}
		case "session/update", "_x.ai/session_notification":
			var variant struct {
				Update struct {
					Kind string `json:"sessionUpdate"`
				} `json:"update"`
			}
			if json.Unmarshal(event.Params, &variant) != nil {
				t.Fatal("invalid fixture event")
			}
			switch variant.Update.Kind {
			case "agent_message_chunk":
				chunk, err := parseTextChunk(event.Params, session, prompt)
				if err != nil || !queue.running || queue.cleared {
					t.Fatal("unowned native text", err)
				}
				text.WriteString(chunk.Update.Content.Text)
			case "turn_completed":
				turn, err = parseTurnCompleted(event.Params, session, prompt, turnFixtureModel)
				if err != nil {
					t.Fatal(err)
				}
			case "available_commands_update", "session_info_update", "session_summary_generated", "response_completed":
				if _, err := parsePassiveObservation(event.Params, event.Method, session, prompt); err != nil {
					t.Fatal("incompatible original passive event", err)
				}

			default:
				t.Fatal("unobserved native fixture event variant")
			}
		case "_x.ai/session/prompt_complete":
			completion, err = parsePromptCompleted(event.Params, session, prompt)
			if err != nil {
				t.Fatal(err)
			}
			completed = true
		case "_x.ai/sessions/changed":
			if _, err := parseActivity(event.Params, session, config.Workspace); err != nil {
				t.Fatal("changed original native activity", err)
			}
		default:
			t.Fatal("unobserved native fixture notification")
		}
	}
	if !queue.queued || !queue.running || !queue.cleared || text.String() != "Private fixture response." || result.Reason != EndTurn || result.Meta.Usage.Input != 11 || result.Meta.Usage.Output != 5 || calls.Load() == 0 {
		t.Fatal("native fixture evidence incomplete")
	}
	if err := matchCompletion(result, turn, completion, turnFixtureModel); err != nil {
		t.Fatal(err)
	}
}
