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
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestManualNativeExplicitCompaction(t *testing.T) {
	for _, fixture := range []struct {
		name                   string
		rejected, insufficient bool
	}{
		{"successful", false, false}, {"provider-rejection", true, false}, {"insufficient-history", false, true},
	} {
		for _, next := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/continue-%t", fixture.name, next), func(t *testing.T) { nativeManualCompactionFixture(t, fixture.rejected, fixture.insufficient, next) })
		}
	}
}

func nativeManualCompactionFixture(t *testing.T, rejected, insufficient, continueInput bool) {
	failed := rejected || insufficient
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native binary and private scripted provider required")
	}
	cfg, logs := apiFixtureConfig(t, "native-compaction")
	// Exercise compaction against a native-known context window. Every provider
	// response remains scripted and local; the model name grants no inference.
	cfg.Process.Executable, cfg.Model, cfg.Permission = binary, "claude-sonnet-4-6", DefaultPermission
	var calls atomic.Int64
	var compactedRequest, retainedRequest atomic.Bool
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
		if n == 3 && rejected {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"Private fixture compaction rejection"}}`)
			return
		}
		if n == 4 || (insufficient && n == 2) {
			// Independently inspect the actual post-compaction provider request:
			// large original context is dropped while recent conversation remains.
			body := string(request.Messages)
			retainedRequest.Store(strings.Contains(body, strings.Repeat("fixture ", 100)) && strings.Contains(body, strings.Repeat("Private fixture conversation response. ", 100)))
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
	var proofs []HistoryMessageProof
	var compactions []HistoryCompactionProof
	var actions []HistoryCompactionActionProof
	var nativeBoundary, nativeEcho, nativeOutput StreamEvent
	var actionID domain.ID
	var originalInput domain.ID
	var originalTurn string
	for turn := 0; turn < 4; turn++ {
		if turn == 3 && !continueInput {
			break
		}
		if turn == 1 && insufficient {
			continue
		}
		if turn == 2 {
			actionID = domain.NewID()
			originalInput, originalTurn = s.current.input, s.current.turnID
			if _, err := s.StartCompaction(ctx, actionID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.StartCompaction(ctx, domain.NewID()); err == nil {
				t.Fatal("overlapping compaction reached native input")
			}
			if _, err := s.SendInput(ctx, domain.NewID(), "Must remain unsent", ContinueSuccessfulRun); err == nil {
				t.Fatal("input overtook native compaction")
			}
		} else {
			prompt := "Continue the private compaction fixture."
			if turn == 0 {
				prompt = strings.Repeat("fixture ", 30000)
			}
			intent := ContinueSuccessfulRun
			if turn == 3 && failed {
				if _, err := s.SendInput(ctx, domain.NewID(), prompt, intent); err == nil {
					t.Fatal("failed compaction automatically advanced input")
				}
				intent = ResumeTerminalRun
			}
			if _, err := s.SendInput(ctx, domain.NewID(), prompt, intent); err != nil {
				t.Fatal(err)
			}
		}
		idle, success, boundary, summary, echo, output, diagnostic := false, false, false, false, false, false, false
		for !idle {
			observed, err := s.Next(ctx)
			if err != nil {
				t.Fatal("native manual compaction lifecycle failed", err, logs.String())
			}
			if turn == 2 {
				if observed.ActionID != actionID || observed.InputID != "" || observed.Accepted || observed.Result != nil || observed.Kind == InputFinished || observed.Kind == InputAccepted {
					t.Fatal("compaction fabricated a conversation outcome")
				}
				if observed.Kind == CompactionObserved {
					nativeBoundary = *observed.Native
					boundary = observed.Compaction.Trigger == ManualCompaction
				}
				if observed.Kind == CompactionSummaryObserved {
					summary = observed.Summary.Text != nil && len(observed.Summary.Blocks) == 0
					proof, err := ObserveMainCompaction(nativeBoundary, *observed.Native, cfg.SessionID)
					if err != nil {
						t.Fatal(err)
					}
					compactions = append(compactions, proof)
				}
				if observed.Kind == CompactionCommandObserved {
					if observed.CompactCommand.Kind == CompactionCommandEcho {
						nativeEcho = *observed.Native
					} else {
						nativeOutput = *observed.Native
					}
					echo = echo || observed.CompactCommand.Kind == CompactionCommandEcho
					output = output || observed.CompactCommand.Kind == CompactionCommandOutput
					diagnostic = diagnostic || observed.CompactCommand.Kind == CompactionCommandDiagnostic
				}
				if observed.Kind == CompactionResultObserved {
					expected := CompactSucceeded
					if failed {
						expected = CompactFailed
					}
					success = observed.CompactResult.Status == expected && !observed.CompactResult.Error
				}
			} else {
				if observed.Kind == InputFinished {
					success = observed.Result.Successful()
				}
				if observed.Native != nil && (observed.Native.Type == "user" || observed.Native.Type == "assistant") {
					proof, err := ObserveMainHistoryMessage(*observed.Native, cfg.SessionID)
					if err != nil {
						t.Fatal(err)
					}
					proofs = append(proofs, proof)
				}
			}
			idle = observed.Kind == RunStateObserved && observed.Run.State == RunIdle
		}
		if !success {
			t.Fatal("original native operation did not finish successfully")
		}
		if turn == 2 {
			status, boundaryID := CompactSucceeded, ""
			if failed {
				status = CompactFailed
			} else {
				boundaryID = compactions[len(compactions)-1].NativeID
			}
			proof, err := ObserveCompactionActionHistory(nativeEcho, nativeOutput, cfg.SessionID, actionID, proofs[len(proofs)-1].NativeID, status, boundaryID)
			if err != nil {
				t.Fatal(err)
			}
			actions = append(actions, proof)
			if boundary == failed || summary == failed || !echo || output == failed || diagnostic != failed || s.current.input != originalInput || s.current.turnID != originalTurn || !s.current.terminal.Successful() {
				t.Fatal("manual command overwrote original conversation ownership")
			}
			if _, err := s.StartCompaction(ctx, actionID); err == nil {
				t.Fatal("native compaction action was replayed")
			}
		}
	}
	expectedCalls := int64(4)
	if insufficient {
		expectedCalls = 2
	}
	if !continueInput {
		expectedCalls--
	}
	if calls.Load() != expectedCalls || (continueInput && (compactedRequest.Load() == failed || retainedRequest.Load() != failed)) {
		t.Fatal("native manual compaction retried or lost its context boundary", calls.Load())
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := security.ReadPrivate(filepath.Join(cfg.Home, "projects", "delidev", string(cfg.SessionID)+".jsonl"), maxHistoryTranscript)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyMainTranscriptWithActions(ctx, raw, cfg.SessionID, cfg.Workspace, proofs, compactions, actions)
	wantMessages, wantCompactions, wantActionMessages, wantDiagnostics := uint32(6), uint32(1), uint32(2), uint32(0)
	if failed {
		wantCompactions, wantActionMessages, wantDiagnostics = 0, 1, 1
	}
	if insufficient {
		wantMessages = 4
	}
	if !continueInput {
		wantMessages -= 2
	}
	if err != nil || verified.MatchedMessages != wantMessages || verified.AdditionalMessages != 1 || verified.CompactionActions != 1 || verified.ActionMessages != wantActionMessages || verified.StoredDiagnostics != wantDiagnostics || verified.Compactions != wantCompactions || verified.SummaryMessages != wantCompactions || (verified.CompactedMessages == 0) != failed {
		t.Fatal("manual history lost original conversation or action provenance", err, verified)
	}
}
