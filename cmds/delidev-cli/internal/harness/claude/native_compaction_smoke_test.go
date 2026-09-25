package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

func TestManualNativeAutomaticCompaction(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native binary and private scripted provider required")
	}
	cfg, logs := apiFixtureConfig(t, "native-compaction")
	// Exercise compaction against a native-known context window. Every provider
	// response remains scripted and local; the model name grants no inference.
	cfg.Process.Executable, cfg.Model, cfg.Permission = binary, "claude-sonnet-4-6", DefaultPermission
	var calls atomic.Int64
	var compactedRequest atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/provider/messages" || r.Header.Get("X-Api-Key") != nativeAPIUpstreamKey || r.Header.Get("Authorization") != "" {
			t.Error("compaction provider authority changed")
			w.WriteHeader(400)
			return
		}
		var request struct {
			Model    string          `json:"model"`
			Stream   bool            `json:"stream"`
			Messages json.RawMessage `json:"messages"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, maxStreamFrame)).Decode(&request) != nil || request.Model != cfg.Model {
			t.Error("compaction provider selection changed")
			w.WriteHeader(400)
			return
		}
		n := calls.Add(1)
		if n > 4 {
			t.Error("unexpected compaction provider retry")
			w.WriteHeader(400)
			return
		}
		if n == 4 {
			// Independently inspect the actual post-compaction provider request:
			// large original context is dropped while recent conversation remains.
			body := string(request.Messages)
			compactedRequest.Store(!strings.Contains(body, strings.Repeat("fixture ", 100)) && !strings.Contains(body, strings.Repeat("Private fixture conversation response. ", 100)) && strings.Contains(body, "Continue the private compaction fixture."))
		}
		answer := "Private fixture conversation response."
		if n == 1 {
			answer = strings.Repeat("Private fixture conversation response. ", 4000)
		}
		if n == 3 {
			answer = "<summary>Private fixture conversation retained by native compaction.</summary>"
		}
		message := contentMessage(fmt.Sprintf("msg_compact_%d", n))
		message["model"] = cfg.Model
		input := uint64(5)
		if n == 2 {
			input = 180000
		}
		message["usage"] = map[string]any{"input_tokens": input, "output_tokens": 0}
		if !request.Stream {
			message["content"] = []any{map[string]any{"type": "text", "text": answer}}
			message["stop_reason"] = "end_turn"
			message["usage"].(map[string]any)["output_tokens"] = 8
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(message)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []map[string]any{
			{"type": "message_start", "message": message},
			{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
			{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": answer}},
			{"type": "content_block_stop", "index": 0},
			{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 8}},
			{"type": "message_stop"},
		} {
			raw, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], raw)
		}
	}))
	defer provider.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	authority := nativeAPIAuthority{ctx: ctx, scope: apiproxy.Scope{ExecutionID: domain.NewID(), SessionID: cfg.SessionID, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(), NativeModel: cfg.Model, Provider: domain.Provider{Name: "Compaction fixture", Endpoint: provider.URL + "/provider", Protocol: domain.AnthropicMessages, Authentication: domain.APIKeyAuth}, Operations: []apiproxy.Operation{apiproxy.MessageCreate}}}
	relay := httptest.NewServer(apiproxy.New(authority, slog.New(slog.NewJSONHandler(io.Discard, nil))))
	defer relay.Close()
	cfg.API.ServerOrigin = relay.URL
	s, err := OpenAPISession(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
		if err := process.ReconcileOwner(cfg.Process.Directory, cfg.Process.OwnerID); err != nil {
			t.Error(err)
		}
		for _, private := range []string{nativeAPIFixtureToken, nativeAPIUpstreamKey, "Private fixture"} {
			if strings.Contains(logs.String(), private) {
				t.Error("compaction private content entered logs")
			}
		}
	}()
	var boundaries int
	var boundary StreamEvent
	var boundaryID string
	var proofs []HistoryMessageProof
	var compactions []HistoryCompactionProof
	for turn := 0; turn < 3; turn++ {
		prompt := "Continue the private compaction fixture."
		if turn == 0 {
			prompt = strings.Repeat("fixture ", 30000)
		}
		if _, err := s.SendInput(ctx, domain.NewID(), prompt, ContinueSuccessfulRun); err != nil {
			t.Fatal(err)
		}
		idle, success := false, false
		for !idle {
			event, err := s.Next(ctx)
			if err != nil {
				t.Fatal("native compaction lifecycle failed", err, logs.String())
			}
			if event.Kind == CompactionObserved {
				if event.Accepted || event.InputID != "" || event.Compaction.Trigger != AutomaticCompaction {
					t.Fatal("native compaction replaced input authority")
				}
				boundaries++
				boundary = *event.Native
				boundaryID = event.NativeID
			}
			if event.Kind == CompactionSummaryObserved {
				if event.Accepted || event.InputID != "" || event.Summary.BoundaryID != boundaryID || len(event.Summary.Blocks) == 0 {
					t.Fatal("native compaction summary changed input or boundary ownership")
				}
				proof, err := ObserveMainCompaction(boundary, *event.Native, cfg.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				compactions = append(compactions, proof)
			} else if event.Native.Type == "user" || event.Native.Type == "assistant" {
				proof, err := ObserveMainHistoryMessage(*event.Native, cfg.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				proofs = append(proofs, proof)
			}
			if event.Kind == InputFinished {
				success = event.Result.Successful()
			}
			idle = event.Kind == RunStateObserved && event.Run.State == RunIdle
		}
		if !success {
			t.Fatal("native compaction input did not finish")
		}
	}
	if boundaries != 1 || len(compactions) != 1 || calls.Load() != 4 || len(proofs) != 6 || !compactedRequest.Load() {
		t.Fatalf("native compaction observations: boundaries=%d requests=%d", boundaries, calls.Load())
	}
	closed, err := s.CloseForContinuation(ctx)
	if err != nil || closed.transcript.Compactions != 1 || closed.transcript.MatchedMessages != 6 {
		t.Fatal("automatic compaction lost its session-owned ledger", err)
	}
	verified, err := ReadMainTranscript(ctx, cfg.Home, cfg.SessionID, cfg.Workspace, proofs, compactions, nil, cfg.Process.Logger)
	if err != nil || verified.MatchedMessages != 6 || verified.AdditionalMessages != 0 || verified.Compactions != 1 || verified.SummaryMessages != 1 || verified.CompactedMessages == 0 || verified.ActiveMatchedMessages == 0 || verified.ActiveMatchedMessages+verified.CompactedMessages != 6 {
		t.Fatal("native compaction history did not retain original and active evidence", err, verified)
	}
}
