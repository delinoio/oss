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
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

func TestManualNativeReadToolRetainedHistory(t *testing.T) {
	for _, permission := range []NativePermission{DefaultPermission, PlanPermission} {
		t.Run(string(permission), func(t *testing.T) { nativeReadToolRetainedHistory(t, permission, permission == PlanPermission) })
	}
}
func nativeReadToolRetainedHistory(t *testing.T, permission NativePermission, prefix bool) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native binary and private scripted provider required")
	}
	cfg, logs := apiFixtureConfig(t, "native-read-history")
	cfg.Process.Executable, cfg.Model, cfg.Permission = binary, "fixture-model", permission
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const content = "Private retained Read tool fixture.\n"
	path := filepath.Join(cfg.Workspace, "read-fixture.txt")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/provider/messages" || r.Header.Get("X-Api-Key") != nativeAPIUpstreamKey || r.Header.Get("Authorization") != "" {
			t.Error("native Read fixture authority changed")
			w.WriteHeader(400)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxStreamFrame+1))
		if err != nil || len(raw) > maxStreamFrame {
			t.Error("unbounded native fixture request")
			w.WriteHeader(400)
			return
		}
		switch n := calls.Add(1); n {
		case 1:
			if prefix {
				nativeReadPrefixedResponse(w, path)
			} else {
				nativeFixtureToolResponse(w, n, "Read", "toolu_retained_read", map[string]any{"file_path": path})
			}
		case 2, 3:
			result := nativeFixtureToolResults(t, raw)["toolu_retained_read"]
			if result.Error || !bytes.Contains(result.Content, []byte("Private retained Read tool fixture.")) {
				t.Error("original Read result missing from provider context")
				w.WriteHeader(400)
				return
			}
			if n == 3 && bytes.Count(raw, []byte("Continue from the original Read result.")) != 1 {
				t.Error("replacement lost or duplicated original new input")
				w.WriteHeader(400)
				return
			}
			nativeFixtureTextResponse(w, n)
		default:
			t.Error("unexpected repeated inference request")
			w.WriteHeader(400)
		}
	}))
	defer provider.Close()
	authority := &rotatingNativeAPIAuthority{token: nativeAPIFixtureToken, authority: nativeAPIAuthority{ctx: ctx, scope: apiproxy.Scope{ExecutionID: domain.NewID(), SessionID: cfg.SessionID, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(), NativeModel: cfg.Model, Provider: domain.Provider{Name: "Read history fixture", Endpoint: provider.URL + "/provider", Protocol: domain.AnthropicMessages, Authentication: domain.APIKeyAuth}, Operations: []apiproxy.Operation{apiproxy.MessageCreate}}}}
	relay := httptest.NewServer(apiproxy.New(authority, slog.New(slog.NewJSONHandler(io.Discard, nil))))
	defer relay.Close()
	cfg.API.ServerOrigin = relay.URL
	session, err := OpenAPISession(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	sessions := []*APISession{session}
	defer func() {
		for _, current := range sessions {
			if err := current.Close(); err != nil {
				t.Error(err)
			}
			if err := process.ReconcileOwner(cfg.Process.Directory, current.config.Process.OwnerID); err != nil {
				t.Error(err)
			}
		}
	}()
	input := domain.NewID()
	if _, err := session.SendInput(ctx, input, "Read the private fixture file.", ContinueSuccessfulRun); err != nil {
		t.Fatal(err)
	}
	finished := false
	for {
		observed, err := session.Next(ctx)
		if err != nil {
			t.Fatal("native Read lifecycle failed", err, logs.String())
		}
		if observed.Kind == InputFinished {
			finished = observed.InputID == input && observed.Result.Successful()
		}
		if observed.Kind == InteractionObserved || observed.Kind == TaskObserved {
			t.Fatal("Read fixture introduced unexpected callback or child ownership")
		}
		if observed.Kind == RunStateObserved && observed.Run.State == RunIdle {
			break
		}
	}
	if !finished || calls.Load() != 2 || len(session.current.content.tools) != 1 {
		t.Fatal("Read fixture did not settle exactly one original tool")
	}
	tool := session.current.content.tools["toolu_retained_read"]
	if !tool.finished || tool.name != "Read" || tool.parent != "" || tool.ownerInput != input || tool.ownerTurn != session.current.turnID {
		t.Fatal("original Read tool ownership changed")
	}
	messages := uint32(4)
	if prefix {
		messages++
	}
	closed, err := session.CloseForContinuation(ctx)
	if err != nil || closed.transcript.MatchedMessages != messages || closed.transcript.AdditionalMessages != 0 {
		t.Fatal("Read tool retained history is incomplete", err, logs.String())
	}
	previous := session.config
	previous.API = APIConfig{ServerOrigin: relay.URL}
	raw, reference, err := closed.RetainCheckpoint(ctx)
	if err != nil {
		t.Fatal("Read tool checkpoint could not be retained", err)
	}
	for _, private := range []string{path, content, cfg.Home, cfg.Workspace, nativeAPIFixtureToken, nativeAPIUpstreamKey, "Read the private fixture file."} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatal("Read checkpoint exposed private content")
		}
	}
	closed, err = RestoreCheckpoint(ctx, previous, raw, reference)
	if err != nil {
		t.Fatal("Read tool checkpoint did not restore", err, logs.String())
	}
	clear(raw)
	if len(closed.previous.current.content.tools) != 1 || closed.previous.current.content.tools["toolu_retained_read"].ownerInput != input {
		t.Fatal("checkpoint lost original Read ownership")
	}
	token := nativeContinuationToken(47)
	authority.rotate(token)
	session, err = ContinueAPISession(ctx, closed, domain.NewID(), APIConfig{ServerOrigin: relay.URL, Token: token}, ContinueSuccessfulRun)
	if err != nil {
		t.Fatal("Read conversation could not replace its process", err, logs.String())
	}
	sessions = append(sessions, session)
	nextInput := domain.NewID()
	if _, err := session.SendInput(ctx, nextInput, "Continue from the original Read result.", ContinueSuccessfulRun); err != nil {
		t.Fatal(err)
	}
	finished = false
	for {
		observed, err := session.Next(ctx)
		if err != nil {
			t.Fatal("resumed Read conversation failed", err, logs.String())
		}
		if observed.Kind == InputFinished {
			finished = observed.InputID == nextInput && observed.Result.Successful()
		}
		if observed.Kind == RunStateObserved && observed.Run.State == RunIdle {
			break
		}
	}
	if !finished || calls.Load() != 3 {
		t.Fatal("resumed Read input did not complete exactly once")
	}
	closed, err = session.CloseForContinuation(ctx)
	if err != nil || closed.transcript.MatchedMessages != messages+2 {
		t.Fatal("original Read proof did not survive the replacement", err, logs.String())
	}
	if _, _, err := closed.RetainCheckpoint(ctx); err != nil {
		t.Fatal("resumed original Read ownership could not be retained", err)
	}
}

func nativeReadPrefixedResponse(w http.ResponseWriter, path string) {
	input, _ := json.Marshal(map[string]any{"file_path": path})
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range []map[string]any{
		{"type": "message_start", "message": map[string]any{"id": "msg_fixture_1", "type": "message", "role": "assistant", "content": []any{}, "model": "fixture-model", "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}},
		{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
		{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "Reading the private fixture."}},
		{"type": "content_block_stop", "index": 0},
		{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "tool_use", "id": "toolu_retained_read", "name": "Read", "input": map[string]any{}}},
		{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(input)}},
		{"type": "content_block_stop", "index": 1},
		{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 3}},
		{"type": "message_stop"},
	} {
		raw, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], raw)
	}
}
