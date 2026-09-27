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

// Raw prompt/reply sends remain test-only until the durable original tool,
// response and publication controller is composed. This invokes only installed
// native code, generated private files and a scripted loopback provider.
func TestManualNativeGrokFileTools(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_GROK_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit private native Grok binary required")
	}
	for _, mode := range []string{"read", "read-stream", "read-outside", "write", "owned-read", "owned-read-stream", "owned-read-outside"} {
		t.Run(mode, func(t *testing.T) { nativeFileTool(t, binary, mode) })
	}
}

func nativeFileTool(t *testing.T, binary, mode string) {
	t.Helper()
	owned := strings.HasPrefix(mode, "owned-")
	mode = strings.TrimPrefix(mode, "owned-")
	config, logs := fixtureAPIConfig(t, "native-file-tool")
	config.Probe.Process.Executable = binary
	target := "fixture.txt"
	if mode == "read-outside" {
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		target = filepath.Join(root, "external.txt")
	}
	actual := target
	if !filepath.IsAbs(actual) {
		actual = filepath.Join(config.Workspace, target)
	}
	const original = "Original fixture first line.\nOriginal fixture second line.\n"
	const written = "Written fixture only.\n"
	if err := os.WriteFile(actual, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Uint32
	var observedToolResult atomic.Bool
	deltaSeen := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api-proxy/v1/chat/completions" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer "+apiFixtureToken {
			t.Error("unexpected file-tool provider authority")
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
			t.Error("native file tool changed selected model")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if calls.Add(1) > 8 {
			t.Error("native fixture provider request bound exceeded")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		toolResult := false
		for _, raw := range body.Messages {
			var message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
				Tool    string          `json:"tool_call_id"`
			}
			if json.Unmarshal(raw, &message) != nil {
				t.Error("invalid fixture message")
				return
			}
			if message.Role == "tool" {
				var content string
				if message.Tool != "call_delidev_read" || json.Unmarshal(message.Content, &content) != nil || mode != "write" && content != "1→"+original || mode == "write" && !strings.Contains(content, "Wrote file successfully") {
					t.Error("native tool result lost original output")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				toolResult = true
				observedToolResult.Store(true)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeChunk := func(delta map[string]any, finish any, usage bool) {
			chunk := map[string]any{"id": "chat-file-tool-fixture", "object": "chat.completion.chunk", "created": 1, "model": turnFixtureModel, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
			if usage {
				chunk["usage"] = map[string]any{"prompt_tokens": 11, "completion_tokens": 5, "total_tokens": 16}
			}
			encoded, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
			w.(http.Flusher).Flush()
		}
		finish := "stop"
		if len(body.Tools) > 0 && !toolResult {
			arguments, name := map[string]any{"target_file": target}, readFileTool
			if mode == "write" {
				name, arguments = writeFileTool, map[string]any{"file_path": actual, "content": written}
			}
			raw, _ := json.Marshal(arguments)
			first, rest := string(raw), ""
			if mode == "read-stream" {
				first, rest = string(raw[:len(raw)/2]), string(raw[len(raw)/2:])
			}
			writeChunk(map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call_delidev_read", "type": "function", "function": map[string]any{"name": name, "arguments": first}}}}, nil, false)
			if rest != "" {
				select {
				case <-deltaSeen:
				case <-r.Context().Done():
					return
				}
				writeChunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": rest}}}}, nil, false)
			}
			finish = "tool_calls"
		} else {
			writeChunk(map[string]any{"role": "assistant", "content": "Tool fixture complete."}, nil, false)
		}
		writeChunk(map[string]any{}, finish, true)
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer provider.Close()
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
				t.Error("native file-tool runtime retained execution token")
			}
			return err
		}); err != nil {
			t.Error(err)
		}
		for _, private := range []string{config.Token, config.Workspace, config.Model, original, written, actual} {
			if strings.Contains(logs.String(), private) {
				t.Error("file-tool diagnostics exposed private contents")
			}
		}
	}()
	session, err := api.Create(ctx, domain.NewID(), domain.NewID(), func(_ context.Context, c CreationClaim) error { return c.Validate() })
	if err != nil {
		t.Fatal(err)
	}
	const input = "Use the original private fixture file."
	if owned {
		request := domain.NewID()
		var claims []InputClaim
		accepted, completed := false, false
		deltas, tools, responses := 0, 0, 0
		result, err := api.RunReadFiles(ctx, request, input, func(_ context.Context, c InputClaim) error {
			if c.Validate() != nil || c.RequestID != request || c.NativeSessionID != session {
				t.Fatal("changed original native Read claim")
			}
			claims = append(claims, c)
			return nil
		}, func(callback context.Context, event InputObservation) error {
			if event.InputID != request || !nativeUUID(event.NativePromptID, 4) || completed {
				t.Fatal("foreign or late original Read observation")
			}
			switch event.Kind {
			case InputAccepted:
				if accepted || len(claims) != 2 {
					t.Fatal("Read acceptance preceded native binding")
				}
				accepted = true
			case InputFileTool:
				if !accepted || event.FileTool == nil || event.FileTool.Permission != nil {
					t.Fatal("unowned or unsupported native Read fact")
				}
				if event.FileTool.Delta != nil {
					deltas++
					if deltas == 1 {
						close(deltaSeen)
					}
					if _, err := api.StopText(callback, domain.NewID(), func(context.Context, StopClaim) error {
						t.Error("Read granted unimplemented Stop authority")
						return nil
					}); err == nil || domain.SafeError(err).Code != domain.Unsupported {
						t.Fatal("Read borrowed plain-text Stop")
					}
				}
				if event.FileTool.Observation != nil && event.FileTool.Observation.Phase == fileToolCompleted {
					tools++
				}
			case InputResponse:
				if !accepted || event.Response == nil || event.Response.Input != 11 || event.Response.Output != 5 {
					t.Fatal("native response accounting changed")
				}
				responses++
				// The controller retains original accounting before this callback.
				event.Response.Input = 999
			case InputText, InputTitle:
				if !accepted {
					t.Fatal("unaccepted Read output")
				}
			case InputCompleted:
				if !accepted || tools != 1 || responses != 2 || event.Result == nil {
					t.Fatal("Read completed before independent facts")
				}
				completed = true
			default:
				t.Fatal("unknown Read observation")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !completed || result.Meta.Input != 11 || result.Meta.Total != 16 || result.Meta.Usage.Input != 22 || result.Meta.Usage.Total != 32 || mode == "read-stream" && deltas < 2 || !observedToolResult.Load() {
			t.Fatal("Read result collapsed original last/aggregate/context facts")
		}
		if _, err := api.CloseText(ctx, domain.NewID(), func(context.Context, ClosureClaim) error { t.Error("Read acquired plain-text closure"); return nil }); err == nil {
			t.Fatal("tool history acquired plain-text closure")
		}
		if _, err := api.RunReadFiles(ctx, domain.NewID(), input, func(context.Context, InputClaim) error { t.Error("Read replay claimed"); return nil }, func(context.Context, InputObservation) error { return nil }); err == nil {
			t.Fatal("native Read boundary reopened")
		}
		after, err := os.ReadFile(actual)
		if err != nil || string(after) != original {
			t.Fatal("Read changed original fixture file")
		}
		return
	}
	type reply struct {
		response nativewire.Response
		err      error
	}
	replies, done := make(chan reply, 1), make(chan struct{})
	reader, wake := context.WithCancel(ctx)
	defer func() { cancel(); wake(); _ = api.Close(); <-done }()
	go func() {
		defer close(done)
		r, e := api.wire.Call(ctx, domain.NewID(), "session/prompt", promptParams{Session: session, Prompt: []promptText{{Type: "text", Text: input}}})
		replies <- reply{r, e}
		wake()
	}()
	queue := inputQueue{session: session, text: input}
	var observer *fileToolObserver
	var response nativewire.Response
	rpc, idle := false, false
	deltas, permissions, completions := 0, 0, 0
	for events := 0; !rpc || !idle; events++ {
		if events >= 128 {
			t.Fatal("native file-tool event bound exceeded")
		}
		if !rpc {
			select {
			case result := <-replies:
				if result.err != nil || result.response.ErrorCode != nil {
					t.Fatal("native file-tool RPC failed", result.err)
				}
				response, rpc = result.response, true
			default:
			}
		}
		if rpc && idle {
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
			t.Fatal("native tool observation failed", err)
		}
		if event.Method == "_x.ai/queue/changed" {
			if err := queue.observe(event.Params); err != nil {
				t.Fatal(err)
			}
			if queue.running && observer == nil {
				observer, err = newFileToolObserver(session, queue.prompt)
				if err != nil {
					t.Fatal(err)
				}
			}
			continue
		}
		var variant struct {
			Update struct {
				Kind string `json:"sessionUpdate"`
			} `json:"update"`
		}
		_ = json.Unmarshal(event.Params, &variant)
		toolEvent := event.Kind == nativewire.ServerRequest
		switch variant.Update.Kind {
		case "tool_call_delta_chunk", "tool_call", "tool_call_update", "pending_interaction", "interaction_resolved":
			toolEvent = true
		}
		if toolEvent {
			if observer == nil || !queue.running || queue.cleared {
				t.Fatal("unowned native file tool")
			}
			fact, err := observer.observe(event)
			if err != nil {
				t.Fatalf("native tool %s/%s: %v", event.Method, variant.Update.Kind, err)
			}
			if fact.Delta != nil {
				deltas++
				if deltas == 1 {
					close(deltaSeen)
				}
			}
			if fact.Permission != nil {
				permissions++
				if mode != "write" || fact.Permission.Tool.Input.Path != actual || fact.Permission.Tool.Input.Content != written || fact.Permission.Options[1].Kind != fileAllowOnce {
					t.Fatal("foreign fixture permission")
				}
				before, err := os.ReadFile(actual)
				if err != nil || string(before) != original {
					t.Fatal("write preceded original permission")
				}
				if err := api.wire.Reply(ctx, event, map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": fact.Permission.Options[1].ID}}); err != nil {
					t.Fatal(err)
				}
			}
			if fact.Observation != nil && fact.Observation.Phase == fileToolCompleted {
				completions++
			}
		}
		if event.Method == "_x.ai/sessions/changed" {
			state, err := parseActivity(event.Params, session, config.Workspace)
			if err != nil {
				t.Fatal(err)
			}
			idle = state == idleActivity
		}
	}
	var result PromptResult
	if decode(response.Result, &result) != nil || result.Reason != EndTurn || result.Meta.Session != session || result.Meta.Prompt != queue.prompt || !queue.cleared || !observer.settled() || completions != 1 || !observedToolResult.Load() || mode == "write" && permissions != 1 || mode != "write" && permissions != 0 || mode == "read-stream" && deltas < 2 {
		t.Fatal("incomplete original file-tool facts")
	}
	after, err := os.ReadFile(actual)
	expected := original
	if mode == "write" {
		expected = written
	}
	if err != nil || string(after) != expected {
		t.Fatal("native fixture file effect changed")
	}
}
