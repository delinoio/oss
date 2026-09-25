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

func TestManualNativeContentAndTurnUsage(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native binary and private scripted provider required")
	}
	cfg, logs := apiFixtureConfig(t, "native-content")
	cfg.Process.Executable = binary
	cfg.Model = "fixture-model"
	cfg.Permission = DefaultPermission
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/provider/messages" || r.Header.Get("X-Api-Key") != nativeAPIUpstreamKey || r.Header.Get("Authorization") != "" {
			t.Error("native content request changed its provider authority")
			w.WriteHeader(400)
			return
		}
		var request struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, maxStreamFrame)).Decode(&request) != nil || request.Model != cfg.Model || !request.Stream {
			t.Error("native content request changed model or transport")
			w.WriteHeader(400)
			return
		}
		turn := calls.Add(1)
		if turn > 2 {
			t.Error("native content request was retried")
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		emit := func(value map[string]any) {
			raw, _ := json.Marshal(value)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", value["type"], raw)
		}
		emit(map[string]any{"type": "message_start", "message": map[string]any{"id": fmt.Sprintf("msg_multi_%d", turn), "type": "message", "role": "assistant", "content": []any{}, "model": cfg.Model, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 3 + (turn-1)*10, "cache_creation_input_tokens": 5, "cache_read_input_tokens": 7, "output_tokens": 0}}})
		for index, text := range []string{"First visible block.", "Second visible block."} {
			emit(map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "text", "text": ""}})
			emit(map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "text_delta", "text": text}})
			emit(map[string]any{"type": "content_block_stop", "index": index})
		}
		emit(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 11}})
		emit(map[string]any{"type": "message_stop"})
	}))
	defer provider.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	authority := nativeAPIAuthority{ctx: ctx, scope: apiproxy.Scope{ExecutionID: domain.NewID(), SessionID: cfg.SessionID, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(), NativeModel: cfg.Model, Provider: domain.Provider{Name: "Content fixture", Endpoint: provider.URL + "/provider", Protocol: domain.AnthropicMessages, Authentication: domain.APIKeyAuth}, Operations: []apiproxy.Operation{apiproxy.MessageCreate}}}
	relay := httptest.NewServer(apiproxy.New(authority, slog.New(slog.NewJSONHandler(io.Discard, nil))))
	defer relay.Close()
	cfg.API.ServerOrigin = relay.URL
	s, err := OpenAPIStream(ctx, cfg)
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
		for _, private := range []string{nativeAPIFixtureToken, nativeAPIUpstreamKey, "First visible block.", "Second visible block."} {
			if strings.Contains(logs.String(), private) {
				t.Error("private content entered lifecycle logs")
			}
		}
	}()
	for turn := int64(1); turn <= 2; turn++ {
		input := domain.NewID()
		const prompt = "Observe multiple native content blocks."
		binding, err := BindExecution(cfg, input, prompt)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SendInput(ctx, input, cfg.SessionID, prompt); err != nil {
			t.Fatal(err)
		}
		var completed []string
		var nativeIDs []string
		var result *NativeResult
		var commandClosed, messageClosed bool
		for result == nil || !commandClosed {
			event, err := s.Next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			observation, err := binding.Observe(event)
			if err != nil {
				t.Fatal(err)
			}
			for _, content := range observation.Content {
				if content.Kind == ContentCompleted {
					if content.MessageID != fmt.Sprintf("msg_multi_%d", turn) || content.Index == nil || *content.Index != uint32(len(completed)) || content.Block == nil || content.Block.Kind != TextBlock {
						t.Fatal("native completed content lost provider message or block identity")
					}
					completed = append(completed, *content.Block.Text)
					nativeIDs = append(nativeIDs, observation.NativeID)
				}
				if content.Kind == ProviderMessageUpdated && (content.Usage == nil || content.Usage.Output == nil || *content.Usage.Output != 11) {
					t.Fatal("native message usage update lost its counter")
				}
				if content.Kind == ProviderMessageFinished {
					messageClosed = true
				}
			}
			if observation.Kind == InputFinished {
				result = observation.Result
			}
			if observation.Kind == CommandObserved && observation.Command == CommandCompleted {
				commandClosed = true
			}
		}
		if len(completed) != 2 || completed[0] != "First visible block." || completed[1] != "Second visible block." || nativeIDs[0] == nativeIDs[1] || !messageClosed || !result.Successful() || !binding.accepted || result.Usage == nil {
			t.Fatal("native block sequence or original completion was lost")
		}
		usage := result.Usage
		main := usage.MainLoop
		model := usage.Models[cfg.Model]
		wantInput := int64(3)
		if turn == 2 {
			wantInput = 16
		}
		if main == nil || main.Input == nil || *main.Input != 3+(turn-1)*10 || main.Output == nil || *main.Output != 11 || main.CacheWrite == nil || *main.CacheWrite != 5 || main.CacheRead == nil || *main.CacheRead != 7 || model.Input == nil || *model.Input != wantInput || model.Output == nil || *model.Output != turn*11 || model.CacheWrite == nil || *model.CacheWrite != turn*5 || model.CacheRead == nil || *model.CacheRead != turn*7 || usage.NativeCostUSD == nil || model.CostUSD == nil {
			t.Fatal("turn-local usage was confused with cumulative native model ledger")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("native provider call count changed")
	}
}
