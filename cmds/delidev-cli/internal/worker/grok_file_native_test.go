package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
)

func nativeGrokWriteProvider(t *testing.T, w http.ResponseWriter, raw []byte, path, model string, rejected bool) {
	t.Helper()
	var body struct {
		Tools    []json.RawMessage `json:"tools"`
		Messages []struct {
			Role    string          `json:"role"`
			Tool    string          `json:"tool_call_id"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(raw, &body) != nil {
		t.Error("invalid native file provider input")
		return
	}
	completed := false
	for _, message := range body.Messages {
		if message.Role == "tool" {
			var content string
			if rejected || message.Tool != "original-worker-write" || json.Unmarshal(message.Content, &content) != nil || !strings.Contains(content, "Wrote file successfully") {
				t.Error("unexpected native file result")
				return
			}
			completed = true
		}
	}
	write := func(delta map[string]any, finish any, usage bool) {
		chunk := map[string]any{"id": "chat-worker-file", "object": "chat.completion.chunk", "created": 1, "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
		if usage {
			chunk["usage"] = map[string]any{"prompt_tokens": 11, "completion_tokens": 5, "total_tokens": 16}
		}
		encoded, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
	}
	finish := "stop"
	if len(body.Tools) > 0 && !completed {
		args, _ := json.Marshal(map[string]any{"file_path": path, "content": "Written Worker file.\n"})
		write(map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "original-worker-write", "type": "function", "function": map[string]any{"name": "write", "arguments": string(args)}}}}, nil, false)
		finish = "tool_calls"
	} else {
		write(map[string]any{"role": "assistant", "content": "Original Worker file completed."}, nil, false)
	}
	write(map[string]any{}, finish, true)
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

func nativeGrokWorkerWrite(t *testing.T, ctx context.Context, api *grok.OwnedAPI, p *ExecutionPublisher, journal *grokClaimJournal, session domain.ID, path string, rejected bool) {
	t.Helper()
	request := domain.NewID()
	var arrival domain.ID
	completed, denied := false, false
	result, err := api.RunFileTools(ctx, p.input.TurnRequestID, p.input.Input.Prompt, func(callback context.Context, v grok.InputObservation) error {
		claims, err := readGrokClaims(p.config.Root, journal.state.Reference)
		if err != nil || len(claims) < 4 || len(claims) > 5 || claims[3].Input.NativePromptID != v.NativePromptID || v.InputID != p.input.TurnRequestID {
			return grokClaimUncertain()
		}
		if v.Permission != nil {
			if len(claims) != 4 || arrival != "" || v.FileTool == nil || v.FileTool.Permission == nil {
				t.Error("native file proposal was not original")
				return grokClaimUncertain()
			}
			before, e := os.ReadFile(path)
			if e != nil || string(before) != "Original Worker file.\n" {
				t.Error("file changed before permission")
				return grokClaimUncertain()
			}
			arrival = v.Permission.ArrivalID
			decision := grok.AllowFileOnce
			if rejected {
				decision = grok.RejectFileOnce
			}
			delivery, e := api.ReplyFilePermission(callback, request, arrival, decision)
			if e != nil || !delivery.Claimed || !delivery.Delivered || delivery.Resolved {
				t.Error("native reply delivery failed", e)
				return grokClaimUncertain()
			}
			retained, e := readGrokClaims(p.config.Root, journal.state.Reference)
			if e != nil || len(retained) != 5 || retained[4].FileReply == nil || *retained[4].FileReply != delivery.Claim || delivery.Claim.ProposalDigest != v.Permission.ProposalDigest || delivery.Claim.NativeSessionID != session {
				t.Error("native reply lost synchronized Worker ownership")
				return grokClaimUncertain()
			}
		}
		if v.Kind == grok.InputCompleted {
			completed = true
		}
		if v.Kind == grok.InputPermissionRejected {
			denied = true
		}
		return nil
	})
	if rejected {
		if err == nil || domain.SafeError(err).Code != domain.Canceled || !denied || completed || result.Meta.Usage.Input != 11 {
			t.Fatal("native Worker file rejection lost usage", err)
		}
	} else if err != nil || !completed || denied || result.Meta.Usage.Input != 22 {
		t.Fatal("native Worker file completion failed", err)
	}
	observation, e := api.InspectFilePermission(arrival)
	if e != nil || !observation.Resolved || !observation.Delivered || observation.ProblemCode != "" {
		t.Fatal("original native reply did not resolve", e)
	}
	expected := "Written Worker file.\n"
	if rejected {
		expected = "Original Worker file.\n"
	}
	after, e := os.ReadFile(path)
	if e != nil || string(after) != expected {
		t.Fatal("original Worker reply and native file effect disagreed")
	}
	if _, e := api.ReplyFilePermission(ctx, domain.NewID(), arrival, grok.AllowFileOnce); e == nil {
		t.Fatal("original Worker reply replayed")
	}
	if p.state.Pending != nil || p.state.LastSequence != 0 {
		t.Fatal("file claims fabricated durable public events")
	}
	if err := api.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readGrokClaims(p.config.Root, journal.state.Reference); err != nil {
		t.Fatal("cleanup lost original Worker claims", err)
	}
}
