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

// This native schema regression deliberately uses test-only transport sends.
// It does not grant the still-required durable original Stop/Worker controller.
func TestManualNativeGrokInterruptedText(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_GROK_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit private native binary required")
	}
	var calls atomic.Uint32
	var canceled atomic.Uint32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api-proxy/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+apiFixtureToken {
			t.Error("unexpected native authority")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
		var body struct {
			Model string `json:"model"`
		}
		if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &body) != nil || body.Model != turnFixtureModel {
			t.Error("native model mismatch")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chat-stop-fixture\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"fixture-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Partial original stop fixture.\"},\"finish_reason\":null}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		canceled.Add(1)
	}))
	defer provider.Close()
	config, logs := fixtureAPIConfig(t, "native-interrupted-text")
	config.Probe.Process.Executable = binary
	config.ServerOrigin, config.Model = provider.URL, turnFixtureModel
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
				t.Error("interrupted runtime retained execution authority")
			}
			return err
		}); err != nil {
			t.Error(err)
		}
		for _, private := range []string{config.Token, config.Workspace, config.Model, "Partial original stop fixture."} {
			if strings.Contains(logs.String(), private) {
				t.Error("interruption diagnostics exposed private content")
			}
		}
	}()
	session, err := api.Create(ctx, domain.NewID(), domain.NewID(), func(_ context.Context, claim CreationClaim) error { return claim.Validate() })
	if err != nil {
		t.Fatal(err)
	}
	type reply struct {
		Response nativewire.Response
		Err      error
	}
	replies := make(chan reply, 1)
	done := make(chan struct{})
	defer func() { cancel(); _ = api.Close(); <-done }()
	reader, wake := context.WithCancel(ctx)
	defer wake()
	go func() {
		defer close(done)
		response, err := api.wire.Call(ctx, domain.NewID(), "session/prompt", promptParams{Session: session, Prompt: []promptText{{Type: "text", Text: "Original interrupted fixture input."}}})
		replies <- reply{response, err}
		wake()
	}()
	queue := inputQueue{session: session, text: "Original interrupted fixture input."}
	var turn InterruptedTurnCompleted
	var completion InterruptedPromptCompleted
	var textOutput strings.Builder
	seenTurn, seenCompletion := false, false
	var lastEvent uint64
	hasEvent := false
	index := func(event string) {
		observed, err := eventIndex(event, session)
		if err != nil || hasEvent && observed <= lastEvent {
			t.Fatal("native interruption event order changed")
		}
		lastEvent, hasEvent = observed, true
	}
	var response nativewire.Response
	stopped, idle, rpc := false, false, false
	var readErr, errorRPC error
	for !rpc || !idle || !seenTurn || !seenCompletion {
		if !rpc {
			select {
			case result := <-replies:
				response, errorRPC, rpc = result.Response, result.Err, true
			default:
			}
		}
		if rpc && idle && seenTurn && seenCompletion {
			break
		}
		read := reader
		if rpc {
			read = ctx
		}
		event, err := api.wire.Next(read)
		if err != nil {
			if !rpc && reader.Err() != nil && ctx.Err() == nil {
				continue
			}
			readErr = err
			break
		}
		if event.Kind != nativewire.Notification {
			t.Fatal("unexpected native interruption interaction")
		}
		switch event.Method {
		case "_x.ai/queue/changed":
			if queue.observe(event.Params) != nil {
				t.Fatal("original interrupted input lost queue binding")
			}
		case "session/update", "_x.ai/session_notification":
			var variant struct {
				Update struct {
					Kind string `json:"sessionUpdate"`
				} `json:"update"`
			}
			if json.Unmarshal(event.Params, &variant) != nil {
				t.Fatal("invalid native update")
			}
			switch variant.Update.Kind {
			case "agent_message_chunk":
				chunk, err := parseTextChunk(event.Params, session, queue.prompt)
				if err != nil || event.Method != "session/update" || !queue.running || seenTurn {
					t.Fatal("foreign interrupted text", err)
				}
				index(chunk.Meta.Event)
				textOutput.WriteString(chunk.Update.Content.Text)
				if !stopped {
					if err := api.wire.Notify(ctx, "session/cancel", closeParams{Session: session}); err != nil {
						t.Fatal(err)
					}
					stopped = true
				}
			case "turn_completed":
				if !stopped || seenTurn || event.Method != "_x.ai/session_notification" {
					t.Fatal("unclaimed or duplicate native interruption")
				}
				turn, err = parseInterruptedTurn(event.Params, session, queue.prompt)
				if err != nil {
					t.Fatal(err)
				}
				index(turn.Meta.Event)
				seenTurn = true
			default:
				observed, err := parsePassiveObservation(event.Params, event.Method, session, queue.prompt)
				if err != nil {
					t.Fatal("unexpected interruption metadata", err)
				}
				if observed.event != "" {
					index(observed.event)
				}
			}
		case "_x.ai/session/prompt_complete":
			if !stopped || seenCompletion {
				t.Fatal("unclaimed or duplicate prompt completion")
			}
			completion, err = parseInterruptedPromptCompleted(event.Params, session, queue.prompt)
			if err != nil {
				t.Fatal(err)
			}
			seenCompletion = true
		case "_x.ai/sessions/changed":
			state, err := parseActivity(event.Params, session, config.Workspace)
			if err != nil {
				t.Fatal(err)
			}
			if state == idleActivity && stopped {
				idle = true
			}
		default:
			t.Fatal("unknown native interruption event")
		}

	}
	if !stopped || !idle || !rpc || !seenTurn || !seenCompletion || readErr != nil || errorRPC != nil || response.ErrorCode != nil || textOutput.String() != "Partial original stop fixture." {
		t.Fatal("native cancellation observation incomplete")
	}
	result, err := parseInterruptedPromptResult(response.Result, session, queue.prompt, config.Model)
	if err != nil || matchInterruption(result, turn, completion) != nil {
		t.Fatal("original interruption observations disagree", err)
	}
	if err := api.Close(); err != nil {
		t.Fatal(err)
	}
	provider.Close()
	if calls.Load() == 0 || calls.Load() != canceled.Load() {
		t.Fatal("original model requests remained after owned cleanup")
	}
	if ctx.Err() != nil {
		t.Fatal("interruption waited for caller deadline")
	}
}
