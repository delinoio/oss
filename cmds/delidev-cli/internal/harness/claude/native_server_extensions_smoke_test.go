package claude

import (
	"bytes"
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

func TestManualNativeServerExtensions(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native binary and private scripted provider required")
	}
	for _, fixture := range serverExtensionFixtures() {
		t.Run(fixture.id, func(t *testing.T) {
			cfg, logs := apiFixtureConfig(t, "native-server-extension")
			cfg.Process.Executable, cfg.Model, cfg.Permission = binary, "fixture-model", DefaultPermission
			var calls atomic.Int64
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/provider/messages" || r.Header.Get("X-Api-Key") != nativeAPIUpstreamKey || r.Header.Get("Authorization") != "" || calls.Add(1) != 1 {
					t.Error("native extension provider authority/count changed")
					w.WriteHeader(400)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				message := contentMessage("msg_extension_fixture")
				message["model"] = cfg.Model
				for _, event := range []map[string]any{
					{"type": "message_start", "message": message},
					{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": ServerToolUseBlock, "id": "srvtoolu_extension", "name": fixture.name, "input": map[string]any{}}},
					{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": `{"original":"private provider input"}`}},
					{"type": "content_block_stop", "index": 0},
					{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": fixture.kind, "tool_use_id": "srvtoolu_extension", "content": fixture.content}},
					{"type": "content_block_stop", "index": 1},
					{"type": "content_block_start", "index": 2, "content_block": map[string]any{"type": "text", "text": ""}},
					{"type": "content_block_delta", "index": 2, "delta": map[string]any{"type": "text_delta", "text": "Native provider extension answer."}},
					{"type": "content_block_stop", "index": 2},
					{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 7}},
					{"type": "message_stop"},
				} {
					raw, _ := json.Marshal(event)
					_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], raw)
				}
			}))
			defer provider.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			authority := nativeAPIAuthority{ctx: ctx, scope: apiproxy.Scope{ExecutionID: domain.NewID(), SessionID: cfg.SessionID, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(), NativeModel: cfg.Model, Provider: domain.Provider{Name: "Extension fixture", Endpoint: provider.URL + "/provider", Protocol: domain.AnthropicMessages, Authentication: domain.APIKeyAuth, Enabled: new(true)}, Operations: []apiproxy.Operation{apiproxy.MessageCreate}}}
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
				for _, private := range []string{nativeAPIFixtureToken, nativeAPIUpstreamKey, "private provider", "private encrypted"} {
					if strings.Contains(logs.String(), private) {
						t.Error("native extension content entered logs")
					}
				}
			}()
			if _, err := s.SendInput(ctx, domain.NewID(), "Observe the native provider extension.", ContinueSuccessfulRun); err != nil {
				t.Fatal(err)
			}
			var observed, succeeded, idle bool
			for !idle {
				observation, err := s.Next(ctx)
				if err != nil {
					t.Fatal("native provider extension lifecycle failed", err, logs.String())
				}
				for _, content := range observation.Content {
					if content.Kind != ContentCompleted || content.Block == nil || content.Block.ServerResult == nil {
						continue
					}
					result := content.Block.ServerResult
					var original struct {
						Content json.RawMessage `json:"content"`
					}
					if json.Unmarshal(result.Native, &original) != nil {
						t.Fatal("invalid retained original result")
					}
					want, _ := streamReplyDigest(fixture.content)
					got, _ := streamReplyDigest(original.Content)
					if observed || result.ID != "srvtoolu_extension" || result.Name != fixture.name || result.Kind != fixture.kind || want != got {
						t.Fatal("native extension lost original operation or result bytes")
					}
					published, _ := json.Marshal(content)
					if bytes.Contains(published, []byte("private provider")) || bytes.Contains(published, []byte("private encrypted")) {
						t.Fatal("native extension bypassed private publication")
					}
					observed = true
				}
				if observation.Kind == InputFinished {
					succeeded = observation.Result.Successful()
				}
				idle = observation.Kind == RunStateObserved && observation.Run.State == RunIdle
			}
			if !observed || !succeeded || calls.Load() != 1 || s.current.content.openTools != 0 || len(s.current.content.tools) != 0 || !s.current.content.serverTools["srvtoolu_extension"].finished || s.current.advertisedTools["UninstalledProviderTool"] {
				t.Fatal("native provider extension acquired local authority or lost completion")
			}
		})
	}
}
