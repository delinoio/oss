package opencode

import (
	"bytes"
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

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestManualNativeOpenCodeReadTool(t *testing.T) {
	if os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE") == "" {
		t.Skip("explicit private native OpenCode tool fixture")
	}
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing=%t", missing), func(t *testing.T) { nativeReadToolFixture(t, missing) })
	}
}

func nativeReadToolFixture(t *testing.T, missing bool) {
	const sentinel = "private-read-result-sentinel"
	const callID = "call_private_native_read"
	key := string(domain.NewID())
	var file atomic.Value
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPBody+1))
		if err != nil || domain.Decode(raw, &body) != nil || r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+key || !scalar(body["model"], fixtureSettings().Model) || string(body["stream"]) != "true" {
			t.Error("native read fixture provider scope mismatch")
			w.WriteHeader(400)
			return
		}
		index := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		delta := map[string]any{"role": "assistant"}
		finish := "stop"
		switch index {
		case 1:
			var tools []struct {
				Type     string `json:"type"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			}
			if json.Unmarshal(body["tools"], &tools) != nil {
				t.Error("native provider tools were not advertised")
			}
			found := false
			for _, tool := range tools {
				found = found || tool.Type == "function" && tool.Function.Name == "read"
			}
			if !found {
				t.Error("native Read tool is missing")
			}
			args, _ := json.Marshal(map[string]any{"filePath": file.Load().(string)})
			delta["tool_calls"] = []any{map[string]any{"index": 0, "id": callID, "type": "function", "function": map[string]any{"name": "read", "arguments": string(args)}}}
			finish = "tool_calls"
		case 2:
			if !bytes.Contains(body["messages"], []byte(sentinel)) || !bytes.Contains(body["messages"], []byte(callID)) {
				t.Error("native Read result was not preserved in the original provider conversation")
			}
			delta["content"] = "Private read complete."
		default:
			t.Error("unexpected native provider retry or auxiliary call")
			w.WriteHeader(500)
			return
		}
		for _, chunk := range []map[string]any{
			{"id": fmt.Sprintf("chatcmpl-private-%d", index), "object": "chat.completion.chunk", "created": 1, "model": "private-model", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}},
			{"id": fmt.Sprintf("chatcmpl-private-%d", index), "object": "chat.completion.chunk", "created": 1, "model": "private-model", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}, "usage": map[string]any{"prompt_tokens": 20, "completion_tokens": 4, "total_tokens": 24}},
		} {
			data, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer provider.Close()
	api, ctx := nativeSessionFixture(t, provider.URL, key)
	path := filepath.Join(api.cwd, sentinel+".txt")
	if !missing {
		if err := os.WriteFile(path, []byte(sentinel+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	file.Store(path)
	settings := fixtureSettings()
	settings.Permission = append(settings.Permission, PermissionRule{Permission: "read", Pattern: "*", Action: PermissionAllow})
	id, err := api.create(ctx, domain.NewID(), settings)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := api.openEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := api.submit(ctx, domain.NewID(), fixtureMessageID, fixturePartID, "Read the private fixture file and report completion."); err != nil {
		t.Fatal(err)
	}
	states := map[ToolState]bool{}
	var toolPart, toolMessage string
	finished, idle := false, false
	terminal := ToolCompleted
	if missing {
		terminal = ToolError
	}
	for count := 0; count < 512 && !(finished && idle && states[terminal]); count++ {
		event, err := stream.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		properties, err := object(event.Properties)
		if err != nil {
			t.Fatal("native tool event property shape")
		}
		switch event.Kind {
		case MessagePartUpdatedEvent:
			part, err := decodeNativePart(properties["part"])
			if err != nil || part.SessionID != id {
				t.Fatalf("native tool part: %v", err)
			}
			if part.Tool == nil {
				continue
			}
			if part.Tool.CallID != callID || part.Tool.Name != "read" {
				t.Fatal("native tool call identity changed")
			}
			if toolPart == "" {
				toolPart, toolMessage = part.ID, part.MessageID
			} else if toolPart != part.ID || toolMessage != part.MessageID {
				t.Fatal("native tool ownership changed between states")
			}
			states[part.Tool.State] = true
			if part.Tool.State == ToolCompleted && (missing || part.Tool.Output == nil || !strings.Contains(*part.Tool.Output, sentinel) || part.Tool.Timing == nil || part.Tool.Timing.End == nil) {
				t.Fatal("native Read lost its original output or completion time")
			}
			if part.Tool.State == ToolError && (!missing || part.Tool.Error == nil || !strings.Contains(*part.Tool.Error, sentinel) || part.Tool.Output != nil || part.Tool.Timing == nil || part.Tool.Timing.End == nil) {
				t.Fatal("native Read lost original failure or fabricated output")
			}
			serialized, _ := json.Marshal(part)
			if bytes.Contains(serialized, []byte(path)) || bytes.Contains(serialized, []byte(sentinel)) {
				t.Fatal("private tool payload escaped ordinary serialization")
			}
		case MessageUpdatedEvent:
			message, err := decodeNativeMessage(properties["info"])
			if err != nil || message.SessionID != id {
				t.Fatalf("native tool message: %v", err)
			}
			if message.Assistant != nil && message.Assistant.Completed != nil && message.Assistant.Finish != nil && *message.Assistant.Finish == FinishStop {
				finished = message.Assistant.ParentID == fixtureMessageID && message.Assistant.Error == nil
			}
		case PermissionAskedEvent:
			t.Fatal("explicit fixture Read policy was not applied")
		case SessionIdleEvent:
			idle = scalar(properties["sessionID"], id)
		}
	}
	if !states[ToolPending] || !states[ToolRunning] || !states[terminal] || !finished || !idle || calls.Load() != 2 {
		t.Fatalf("native tool observations: pending=%t running=%t completed=%t error=%t finished=%t idle=%t requests=%d", states[ToolPending], states[ToolRunning], states[ToolCompleted], states[ToolError], finished, idle, calls.Load())
	}
	if receipt, err := api.inspectInput(ctx); err != nil || !receipt.Recorded {
		t.Fatal("native tool activity lost original input storage")
	}
}
