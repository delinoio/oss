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
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type interactionFixtureMode string

const (
	interactionProposalFixture  interactionFixtureMode = "proposal"
	interactionReplyFixture     interactionFixtureMode = "reply"
	interactionRejectionFixture interactionFixtureMode = "reject"
)

func TestManualNativeOpenCodeInteractionProposals(t *testing.T) {
	if os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE") == "" {
		t.Skip("explicit private native OpenCode interaction proposals")
	}
	for _, kind := range []InteractionKind{PermissionInteraction, QuestionInteraction} {
		t.Run(string(kind), func(t *testing.T) { nativeInteractionProposalFixture(t, kind, interactionProposalFixture) })
	}
}

func TestManualNativeOpenCodeInteractionReplies(t *testing.T) {
	if os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE") == "" {
		t.Skip("explicit private native OpenCode interaction replies")
	}
	for _, kind := range []InteractionKind{PermissionInteraction, QuestionInteraction} {
		t.Run(string(kind), func(t *testing.T) { nativeInteractionProposalFixture(t, kind, interactionReplyFixture) })
	}
}

func TestManualNativeOpenCodeInteractionRejections(t *testing.T) {
	if os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE") == "" {
		t.Skip("explicit private native OpenCode interaction rejection")
	}
	for _, kind := range []InteractionKind{PermissionInteraction, QuestionInteraction} {
		t.Run(string(kind), func(t *testing.T) { nativeInteractionProposalFixture(t, kind, interactionRejectionFixture) })
	}
}

func nativeInteractionProposalFixture(t *testing.T, kind InteractionKind, mode interactionFixtureMode) {
	respond, reject := mode != interactionProposalFixture, mode == interactionRejectionFixture
	key := string(domain.NewID())
	const callID = "call_private_interaction"
	const sentinel = "private-interaction-sentinel"
	var args atomic.Value
	var calls atomic.Int32
	tool, expected := "read", PermissionAskedEvent
	if kind == QuestionInteraction {
		tool, expected = "question", QuestionAskedEvent
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPBody+1))
		var body map[string]json.RawMessage
		if err != nil || domain.Decode(raw, &body) != nil || r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+key || !scalar(body["model"], fixtureSettings().Model) || string(body["stream"]) != "true" {
			t.Error("native proposal provider scope mismatch or unapproved continuation")
			w.WriteHeader(400)
			return
		}
		index := calls.Add(1)
		if index > 2 || index == 2 && (!respond || reject) {
			t.Error("native interaction continued without an original response")
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if index == 2 {
			if !bytes.Contains(body["messages"], []byte(callID)) || !bytes.Contains(body["messages"], []byte(sentinel)) || kind == QuestionInteraction && (!bytes.Contains(body["messages"], []byte("First, Second"))) {
				t.Error("native continuation lost original interaction result")
			}
			_, _ = io.WriteString(w, `data: {"id":"chatcmpl-private-second","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Private interaction complete."},"finish_reason":null}]}`+"\n\n")
			_, _ = io.WriteString(w, `data: {"id":"chatcmpl-private-second","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":4,"total_tokens":24}}`+"\n\ndata: [DONE]\n\n")
			return
		}
		for _, chunk := range []map[string]any{
			{"id": "chatcmpl-private", "object": "chat.completion.chunk", "created": 1, "model": "private-model", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": callID, "type": "function", "function": map[string]any{"name": tool, "arguments": args.Load().(string)}}}}, "finish_reason": nil}}},
			{"id": "chatcmpl-private", "object": "chat.completion.chunk", "created": 1, "model": "private-model", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}}},
		} {
			raw, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer provider.Close()
	api, ctx := nativeSessionFixture(t, provider.URL, key)
	path := filepath.Join(api.cwd, sentinel+".txt")
	if err := os.WriteFile(path, []byte(sentinel), 0600); err != nil {
		t.Fatal(err)
	}
	argument := map[string]any{"filePath": path}
	if kind == QuestionInteraction {
		argument = map[string]any{"questions": []any{map[string]any{"question": sentinel, "header": "Choice", "options": []any{map[string]any{"label": "First", "description": sentinel}, map[string]any{"label": "Second", "description": "Another choice"}}, "multiple": true}}}
	}
	raw, _ := json.Marshal(argument)
	args.Store(string(raw))
	id, err := api.create(ctx, domain.NewID(), fixtureSettings())
	if err != nil {
		t.Fatal(err)
	}
	stream, err := api.openEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := api.submit(ctx, domain.NewID(), fixtureMessageID, fixturePartID, "Exercise the private interaction proposal."); err != nil {
		t.Fatal(err)
	}
	observer, err := api.observeInput(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	var interactionID string
	for i := 0; i < 512; i++ {
		event, err := stream.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		observation, err := observer.observe(ctx, event)
		if err != nil {
			t.Fatalf("native interaction event %s: %v", event.Kind, err)
		}
		if respond && interactionID != "" && observer.snapshot().SettledObserved {
			receipt, err := observer.interactionReceipt(interactionID)
			expectedCalls := int32(2)
			if reject {
				expectedCalls = 1
			}
			if err != nil || !receipt.HTTPAccepted || !receipt.NativeAccepted || calls.Load() != expectedCalls || observer.snapshot().NeedsRecovery || observer.snapshot().RejectedInteraction != reject {
				t.Fatal("native response did not preserve independent HTTP, acceptance and terminal observations")
			}
			if _, err := api.replyInteraction(ctx, observer, domain.NewID(), interactionID, InteractionResponse{}); err == nil {
				t.Fatal("completed native interaction granted a second send")
			}
			return
		}
		if event.Kind != expected {
			continue
		}
		request := observation.Interaction
		if request == nil || request.Kind != kind || request.SessionID != id || request.Tool == nil || request.Tool.CallID != callID || interactionID != "" {
			t.Fatalf("native original interaction proposal: %v", err)
		}
		part := observer.parts[observer.calls[callID]]
		if part == nil || part.value.MessageID != request.Tool.MessageID || part.value.Tool.State != ToolPending && part.value.Tool.State != ToolRunning || part.value.Tool.Name != tool {
			if part != nil {
				t.Logf("native proposal tool facts: state=%s message_matches=%t name_matches=%t", part.value.Tool.State, part.value.MessageID == request.Tool.MessageID, part.value.Tool.Name == tool)
			} else {
				t.Log("native proposal arrived before its tool-part observation")
			}
			t.Fatal("native proposal lost its original pending/running tool")
		}
		// Native question execution can publish its request before the
		// processor publishes applied running input. This preserves proposal
		// ownership only; pending state cannot be promoted into applied input.
		t.Logf("native proposal observation: kind=%s tool_state=%s", kind, part.value.Tool.State)
		if kind == PermissionInteraction && (request.Permission.Name != "read" || len(request.Permission.Patterns) != 1 || len(request.Permission.Always) != 1) {
			t.Fatal("native Read permission scope changed")
		}
		if kind == QuestionInteraction && (len(request.Questions) != 1 || request.Questions[0].Text != sentinel || len(request.Questions[0].Options) != 2 || request.Questions[0].Multiple == nil || !*request.Questions[0].Multiple || request.Questions[0].Custom != nil) {
			t.Fatal("native question shape/defaults changed")
		}
		if observer.snapshot().TerminalObserved || calls.Load() != 1 {
			t.Fatal("unanswered proposal became completion or continuation")
		}
		if receipt, err := api.inspectInput(ctx); err != nil || !receipt.Recorded {
			t.Fatal("pending native interaction lost original input storage")
		}
		if !respond {
			// Owned process cleanup cancels this proposal-only fixture's
			// pending request without claiming successful tool completion.
			return
		}
		interactionID = request.ID
		response := InteractionResponse{Answers: [][]string{{"First", "Second"}}}
		if reject {
			response = InteractionResponse{Reject: true}
		}
		if kind == PermissionInteraction {
			decision := PermissionOnce
			if reject {
				decision = PermissionReject
			}
			response = InteractionResponse{Decision: &decision}
		}
		receipt, err := api.replyInteraction(ctx, observer, domain.NewID(), request.ID, response)
		if err != nil || !receipt.HTTPAccepted || receipt.NativeAccepted || receipt.ArrivalID != event.ID || receipt.InteractionID != request.ID {
			t.Fatalf("native original reply delivery: %v", err)
		}
	}
	t.Fatal("native harness did not settle the requested interaction scenario")
}
