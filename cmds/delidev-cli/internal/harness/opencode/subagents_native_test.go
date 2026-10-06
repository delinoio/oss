// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestManualNativeOpenCodeForegroundChild(t *testing.T) {
	if os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE") == "" {
		t.Skip("explicit isolated pinned native foreground child fixture")
	}
	key := string(domain.NewID())
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPBody+1))
		if err != nil || domain.Decode(raw, &body) != nil || r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+key || !scalar(body["model"], fixtureSettings().Model) || string(body["stream"]) != "true" {
			t.Error("foreground child escaped the original provider scope")
			w.WriteHeader(400)
			return
		}
		call := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if call == 1 {
			_, _ = io.WriteString(w, `data: {"id":"chatcmpl-task","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"task-private","type":"function","function":{"name":"task","arguments":"{\"description\":\"Inspect private fixture\",\"prompt\":\"Reply with child fixture.\",\"subagent_type\":\"general\"}"}}]},"finish_reason":null}]}`+"\n\n")
			_, _ = io.WriteString(w, `data: {"id":"chatcmpl-task","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":4,"total_tokens":24}}`+"\n\ndata: [DONE]\n\n")
		} else {
			_, _ = io.WriteString(w, `data: {"id":"chatcmpl-child","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Private child fixture response"},"finish_reason":null}]}`+"\n\n")
			_, _ = io.WriteString(w, `data: {"id":"chatcmpl-child","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":30,"completion_tokens":6,"total_tokens":36}}`+"\n\ndata: [DONE]\n\n")
		}
	}))
	defer provider.Close()
	settings := fixtureSettings()
	settings.Permission = []PermissionRule{{Permission: "*", Pattern: "*", Action: PermissionAllow}}
	api, ctx := nativeSessionFixtureWithSettings(t, provider.URL, key, StopOnInteractionRejection, settings)
	if _, err := api.create(ctx, domain.NewID(), settings); err != nil {
		t.Fatal(err)
	}
	stream, err := api.openEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := api.submit(ctx, domain.NewID(), fixtureMessageID, fixturePartID, "Delegate the private fixture once."); err != nil {
		t.Fatal(err)
	}
	observer, err := api.observeInput(ctx, fixtureNativeRoot())
	if err != nil {
		t.Fatal(err)
	}
	observed, output, usage, terminal := false, false, false, false
	for count := 0; count < 1024; count++ {
		event, err := stream.Next(ctx)
		if err != nil {
			t.Fatalf("original event read count=%d provider_calls=%d: %v", count, calls.Load(), err)
		}
		value, err := api.observeOwnedEvent(ctx, observer, event)
		if err != nil {
			t.Fatalf("original child projection rejected kind=%s: %v", event.Kind, err)
		}
		for _, child := range value.Children {
			if child.ParentID != api.creation.identity.id || child.ParentToolID == "" || child.OpenCodeTool == nil {
				t.Fatal("child lost dual original ownership")
			}
			observed = true
			output = output || child.Output != nil
			usage = usage || child.Usage != nil
			terminal = terminal || child.Status.Terminal()
		}
		if observer.snapshot().SettledObserved {
			break
		}
	}
	if !observed || !output || !usage || !terminal || calls.Load() != 3 {
		t.Fatalf("child coverage observed=%t output=%t usage=%t terminal=%t provider_calls=%d", observed, output, usage, terminal, calls.Load())
	}
	if err := api.verifyForegroundChildren(ctx); err != nil {
		t.Fatal(err)
	}
}
