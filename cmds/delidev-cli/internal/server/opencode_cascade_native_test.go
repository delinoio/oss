package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

type nativeOpenCodeProviderMessage struct {
	Role       string
	Content    json.RawMessage
	ToolCallID string `json:"tool_call_id"`
}

func isNativeOpenCodeCascade(p nativeOpenCodePublication) bool {
	return p == nativePermissionAlwaysCascadePublication || p == nativePermissionRejectCascadePublication || p == nativePermissionCorrectionCascadePublication
}

func TestManualNativeOpenCodePublishesPolicyCascades(t *testing.T) {
	for _, p := range []nativeOpenCodePublication{nativePermissionAlwaysCascadePublication, nativePermissionRejectCascadePublication, nativePermissionCorrectionCascadePublication} {
		t.Run(fmt.Sprint(p), func(t *testing.T) { nativeRegisteredOpenCode(t, domain.ExecuteMode, false, p) })
	}
}

func serveNativeOpenCodeCascade(t *testing.T, w http.ResponseWriter, messages []nativeOpenCodeProviderMessage, paths []string, p nativeOpenCodePublication, call int32) {
	t.Helper()
	if call > 1 {
		if p != nativePermissionAlwaysCascadePublication {
			t.Error("native rejection cascade continued despite its stop policy")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		count := 2
		if call == 3 {
			count = 3
		}
		for i := 0; i < count; i++ {
			found := 0
			for _, message := range messages {
				if message.Role != "tool" || message.ToolCallID != fmt.Sprintf("call_registered_cascade_%d", i) {
					continue
				}
				var result string
				if json.Unmarshal(message.Content, &result) != nil || !strings.Contains(result, "Original native Read fixture.") {
					t.Error("native always policy lost an original Read result")
				}
				found++
			}
			if found != 1 {
				t.Error("native always policy changed original tool result ownership")
			}
		}
	}
	if call == 3 {
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-cascade-final","object":"chat.completion.chunk","created":1,"model":"fixture-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Original native always cascade completed."},"finish_reason":null}]}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-cascade-final","object":"chat.completion.chunk","created":1,"model":"fixture-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":4,"total_tokens":24}}`+"\n\ndata: [DONE]\n\n")
		return
	}
	toolCalls := []any{}
	for i, path := range paths {
		if call == 1 && i == 2 || call == 2 && i != 2 {
			continue
		}
		args, _ := json.Marshal(map[string]any{"filePath": path})
		toolCalls = append(toolCalls, map[string]any{"index": len(toolCalls), "id": fmt.Sprintf("call_registered_cascade_%d", i), "type": "function", "function": map[string]any{"name": "read", "arguments": string(args)}})
	}
	for _, choice := range []map[string]any{
		{"index": 0, "delta": map[string]any{"role": "assistant", "tool_calls": toolCalls}, "finish_reason": nil},
		{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"},
	} {
		raw, _ := json.Marshal(map[string]any{"id": "chatcmpl-cascade", "object": "chat.completion.chunk", "created": 1, "model": "fixture-model", "choices": []any{choice}})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

func observeNativeOpenCodeCascade(t *testing.T, ctx context.Context, f *publicationFixture, publisher *worker.OpenCodeEventPublisher, observation opencode.Observation, pending []store.Record, p nativeOpenCodePublication) []store.Record {
	t.Helper()
	n := observation.Interaction
	rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.InteractionKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(pending) >= 2 || len(rows) != len(pending)+1 || n.Kind != opencode.PermissionInteraction {
		t.Fatal("native cascade invented an additional permission request")
	}
	for _, row := range rows {
		value, err := store.Decode[domain.ExecutionInteraction](row)
		if err != nil {
			t.Fatal(err)
		}
		if value.NativeRequestID.Text != n.ID {
			continue
		}
		if value.Closure != domain.InteractionOpen || value.ApprovalResponse != nil || value.OpenCodeClosure != nil || value.OpenCode == nil || value.OpenCode.NativeEventID != observation.EventID || value.OpenCode.NativeMessageID != n.Tool.MessageID || value.OpenCode.CallID != n.Tool.CallID {
			t.Fatal("original cascade proposal lost its original ownership")
		}
		pending = append(pending, row)
		break
	}
	if len(pending) != len(rows) {
		t.Fatal("original cascade request disappeared")
	}
	if len(pending) == 2 {
		value, err := store.Decode[domain.ExecutionInteraction](pending[0])
		if err != nil {
			t.Fatal(err)
		}
		respondOriginalOpenCodeFixture(t, ctx, f, publisher, pending[0], value, false, p)
	}
	return pending
}

func verifyNativeOpenCodeCascade(t *testing.T, ctx context.Context, f *publicationFixture, path string, p nativeOpenCodePublication) {
	t.Helper()
	rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.InteractionKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatal("native policy lost a request or invented another permission")
	}
	var direct, automatic domain.ExecutionInteraction
	var directID domain.ID
	for _, row := range rows {
		value, err := store.Decode[domain.ExecutionInteraction](row)
		if err != nil || value.Closure != domain.InteractionNativeClosed || value.Response != nil {
			t.Fatal("native cascade lost independent request closure")
		}
		if value.ApprovalResponse != nil {
			direct = value
			directID = row.ID
		} else {
			automatic = value
		}
	}
	decision := nativePolicyPermissionResponse(p).OpenCode.Decision
	if direct.OpenCodeClosure != nil || direct.ApprovalResponse == nil || direct.ApprovalResponse.State != domain.ApprovalResponseAccepted || direct.ApprovalResponse.Acceptance == nil || direct.ApprovalResponse.Acceptance.OpenCode == nil || direct.ApprovalResponse.Input.OpenCode.Decision != decision {
		t.Fatal("original direct policy response lost its acceptance")
	}
	proof := automatic.OpenCodeClosure
	if proof == nil || proof.Validate() != nil || proof.Decision != decision || proof.ProposalEventID != automatic.OpenCode.NativeEventID || len(proof.Sources) != 1 || proof.Sources[0].InteractionID != directID || proof.Sources[0].NativeRequestID != direct.NativeRequestID.Text {
		t.Fatal("automatic native closure fabricated a response or lost observed context")
	}
	raw, err := security.ReadPrivate(path, 1<<20)
	var journal struct {
		Claims []opencode.SessionClaim `json:"claims"`
	}
	if err != nil || json.Unmarshal(raw, &journal) != nil || len(journal.Claims) != 3 || journal.Claims[2].RequestID != direct.ApprovalResponse.ID || journal.Claims[2].BodyDigest != direct.ApprovalResponse.Acceptance.OpenCode.BodyDigest || journal.Claims[2].InteractionID != direct.NativeRequestID.Text {
		t.Fatal("native cascade fabricated another owner response claim")
	}
}
