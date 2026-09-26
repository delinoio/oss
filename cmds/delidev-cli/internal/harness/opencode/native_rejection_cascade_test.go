package opencode

import (
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

func TestManualNativeOpenCodePermissionRejectionCascade(t *testing.T) {
	if os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE") == "" {
		t.Skip("explicit private native OpenCode permission cascade")
	}
	nativePermissionCascadeFixture(t, PermissionReject)
}

func TestManualNativeOpenCodePermissionAlwaysCascade(t *testing.T) {
	if os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE") == "" {
		t.Skip("explicit private native OpenCode always cascade and later Read")
	}
	nativePermissionCascadeFixture(t, PermissionAlways)
}

func nativePermissionCascadeFixture(t *testing.T, decision PermissionDecision) {
	reject := decision == PermissionReject
	key := string(domain.NewID())
	var arguments atomic.Value
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPBody+1))
		if err != nil || domain.Decode(raw, &body) != nil || r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+key || !scalar(body["model"], fixtureSettings().Model) || string(body["stream"]) != "true" {
			t.Error("native rejection escaped provider scope or continued")
			w.WriteHeader(400)
			return
		}
		index := calls.Add(1)
		if index > 3 || reject && index != 1 {
			t.Error("native cascade continued beyond its original scope")
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if index > 1 {
			resultCount := 2
			if index == 3 {
				resultCount = 3
			}
			for i := 0; i < resultCount; i++ {
				result, valid := providerToolResult(body["messages"], fmt.Sprintf("call_private_cascade_%d", i))
				if !valid || !strings.Contains(result, "private fixture") {
					t.Error("native cascade lost an original tool-result message")
				}
			}
		}
		if index == 3 {
			_, _ = io.WriteString(w, `data: {"id":"chatcmpl-private-final","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Private always scope complete."},"finish_reason":null}]}`+"\n\n")
			_, _ = io.WriteString(w, `data: {"id":"chatcmpl-private-final","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":4,"total_tokens":24}}`+"\n\ndata: [DONE]\n\n")
			return
		}
		toolCalls := []any{}
		for i, argument := range arguments.Load().([]string) {
			if index == 1 && i == 2 || index == 2 && i != 2 {
				continue
			}
			toolCalls = append(toolCalls, map[string]any{"index": len(toolCalls), "id": fmt.Sprintf("call_private_cascade_%d", i), "type": "function", "function": map[string]any{"name": "read", "arguments": argument}})
		}
		for _, chunk := range []map[string]any{
			{"id": "chatcmpl-private", "object": "chat.completion.chunk", "created": 1, "model": "private-model", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "tool_calls": toolCalls}, "finish_reason": nil}}},
			{"id": "chatcmpl-private", "object": "chat.completion.chunk", "created": 1, "model": "private-model", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}}},
		} {
			raw, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer provider.Close()
	api, ctx := nativeSessionFixture(t, provider.URL, key)
	args := []string{}
	fileCount := 2
	if !reject {
		fileCount = 3
	}
	for i := 0; i < fileCount; i++ {
		path := filepath.Join(api.cwd, fmt.Sprintf("private-cascade-%d.txt", i))
		if err := os.WriteFile(path, []byte("private fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]any{"filePath": path})
		args = append(args, string(raw))
	}
	arguments.Store(args)
	if _, err := api.create(ctx, domain.NewID(), fixtureSettings()); err != nil {
		t.Fatal(err)
	}
	stream, err := api.openEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := api.submit(ctx, domain.NewID(), fixtureMessageID, fixturePartID, "Exercise two private Read permission requests."); err != nil {
		t.Fatal(err)
	}
	observer, err := api.observeInput(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	requests := []string{}
	for count := 0; count < 512; count++ {
		event, err := stream.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		observation, err := observer.observe(ctx, event)
		if err != nil {
			t.Fatalf("native rejection cascade event %s: %v", event.Kind, err)
		}
		if observation.Interaction != nil {
			if observation.Interaction.Kind != PermissionInteraction || observation.Interaction.Permission.Name != "read" {
				t.Fatal("unexpected native cascade permission")
			}
			requests = append(requests, observation.Interaction.ID)
			if len(requests) > 2 {
				t.Fatal("unexpected additional native permission")
			}
			if len(requests) == 2 {
				if receipt, err := api.replyInteraction(ctx, observer, domain.NewID(), requests[0], InteractionResponse{Decision: &decision}); err != nil || !receipt.HTTPAccepted {
					t.Fatalf("native rejection delivery: %v", err)
				}
			}
		}
		if len(requests) == 2 && observer.snapshot().SettledObserved {
			first, second := observer.interactions[requests[0]], observer.interactions[requests[1]]
			expectedCalls := int32(3)
			if reject {
				expectedCalls = 1
			}
			if observer.snapshot().RejectedInteraction != reject || observer.snapshot().NeedsRecovery || calls.Load() != expectedCalls || first.attempt == nil || !first.attempt.receipt.NativeAccepted || !first.closed || first.rejected != reject || second.attempt != nil || !second.closed || second.rejected != reject {
				t.Fatal("cascade fabricated a second response or lost original rejection outcome")
			}
			if reject && (len(second.rejectionSources) != 1 || second.rejectionSources[0] != requests[0]) {
				t.Fatal("native rejection lost its observed cause")
			}
			if !reject && (!first.alwaysAccepted || second.alwaysAccepted || len(second.alwaysObservations) != 1 || second.alwaysObservations[0] != requests[0] || len(observer.calls) != 3) {
				t.Fatal("native always closure invented another rule grant or lost later automatic Read")
			}
			return
		}
	}
	t.Fatal("native rejection did not settle its original permission cascade")
}
