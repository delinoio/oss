package claude

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestManualNativeWriteEditToolRetainedHistory(t *testing.T) {
	for _, overwrite := range []bool{false, true} {
		name := "create"
		if overwrite {
			name = "overwrite"
		}
		t.Run(name, func(t *testing.T) { nativeWriteEditToolRetainedHistory(t, overwrite) })
	}
}

func nativeWriteEditToolRetainedHistory(t *testing.T, overwrite bool) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native binary and private scripted provider required")
	}
	cfg, logs := apiFixtureConfig(t, "native-write-edit-history")
	cfg.Process.Executable, cfg.Model, cfg.Permission = binary, "fixture-model", DefaultPermission
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const before = "Original private file content.\n"
	const content = "First private written content.\n"
	const after = "Final private edited content.\n"
	path := filepath.Join(cfg.Workspace, "file-fixture.txt")
	if overwrite {
		if err := os.WriteFile(path, []byte(before), 0600); err != nil {
			t.Fatal(err)
		}
	}
	type invocation struct {
		name, id string
		input    map[string]any
	}
	callsPlan := []invocation{}
	if overwrite {
		callsPlan = append(callsPlan, invocation{"Read", "toolu_initial_read", map[string]any{"file_path": path}})
	}
	callsPlan = append(callsPlan,
		invocation{"Write", "toolu_original_write", map[string]any{"file_path": path, "content": content}},
		invocation{"Read", "toolu_written_read", map[string]any{"file_path": path}},
		invocation{"Edit", "toolu_original_edit", map[string]any{"file_path": path, "old_string": content, "new_string": after, "replace_all": false}})
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
		n := calls.Add(1)
		results := nativeFixtureToolResults(t, raw)
		for i := int64(0); i < n-1 && i < int64(len(callsPlan)); i++ {
			result, exists := results[callsPlan[i].id]
			if !exists || result.Error {
				t.Error("original file tool failed or disappeared", callsPlan[i].name)
				w.WriteHeader(400)
				return
			}
		}
		if n <= int64(len(callsPlan)) {
			call := callsPlan[n-1]
			nativeFixtureToolResponse(w, n, call.name, call.id, call.input)
			return
		}
		if n > int64(len(callsPlan)+2) {
			t.Error("unexpected repeated native inference")
			w.WriteHeader(400)
			return
		}
		if n == int64(len(callsPlan)+2) && bytes.Count(raw, []byte("Continue from the original edited file.")) != 1 {
			t.Error("replacement lost or repeated new input")
			w.WriteHeader(400)
			return
		}
		if actual, err := os.ReadFile(path); err != nil || string(actual) != after {
			t.Error("native file effects changed", err)
			w.WriteHeader(400)
			return
		}
		nativeFixtureTextResponse(w, n)
	}))
	defer provider.Close()
	authority := &rotatingNativeAPIAuthority{token: nativeAPIFixtureToken, authority: nativeAPIAuthority{ctx: ctx, scope: apiproxy.Scope{ExecutionID: domain.NewID(), SessionID: cfg.SessionID, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(), NativeModel: cfg.Model, Provider: domain.Provider{Name: "Write/Edit history fixture", Endpoint: provider.URL + "/provider", Protocol: domain.AnthropicMessages, Authentication: domain.APIKeyAuth, Enabled: new(true)}, Operations: []apiproxy.Operation{apiproxy.MessageCreate}}}}
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
	if _, err := session.SendInput(ctx, input, "Write and edit the private fixture file.", ContinueSuccessfulRun); err != nil {
		t.Fatal(err)
	}
	finished := false
	for {
		observed, err := session.Next(ctx)
		if err != nil {
			t.Fatal("native file tool lifecycle failed", err, logs.String())
		}
		if observed.Kind == InputFinished {
			finished = observed.InputID == input && observed.Result.Successful()
		}
		if observed.Kind == InteractionObserved && observed.Interaction.Kind == InteractionRequested {
			if observed.Interaction.Request.Kind != ToolPermission {
				t.Fatal("unexpected file-tool callback kind")
			}
			if err := session.Reply(ctx, observed.Interaction.ArrivalID, PermissionReply{Behavior: PermissionAllow}); err != nil {
				t.Fatal(err)
			}
		}
		if observed.Kind == RunStateObserved && observed.Run.State == RunIdle {
			break
		}
	}
	if !finished || calls.Load() != int64(len(callsPlan)+1) || len(session.current.content.tools) != len(callsPlan) || len(session.current.interactions) != 2 {
		t.Fatal("native file tools did not settle")
	}
	messages := uint32(2 + 2*len(callsPlan))
	closed, err := session.CloseForContinuation(ctx)
	if err != nil || closed.transcript.MatchedMessages != messages || closed.transcript.AdditionalMessages != 0 {
		t.Fatal("File tool retained history is incomplete", err, logs.String())
	}
	previous := session.config
	previous.API = APIConfig{ServerOrigin: relay.URL}
	raw, reference, err := closed.RetainCheckpoint(ctx)
	if err != nil {
		t.Fatal("File tool checkpoint could not be retained", err)
	}
	for _, private := range []string{path, before, content, after, cfg.Home, cfg.Workspace, nativeAPIFixtureToken, nativeAPIUpstreamKey, "Write and edit the private fixture file."} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatal("File tool checkpoint exposed private content")
		}
	}
	closed, err = RestoreCheckpoint(ctx, previous, raw, reference)
	if err != nil {
		t.Fatal("File tool checkpoint did not restore", err, logs.String())
	}
	var cp sessionCheckpoint
	if json.Unmarshal(raw, &cp) != nil || len(cp.WriteTools) != 1 || len(cp.EditTools) != 1 || len(cp.ReadTools) != len(callsPlan)-2 || len(cp.ToolApprovals) != 2 {
		t.Fatal("checkpoint lost original file-tool families or approvals")
	}
	clear(raw)
	if len(closed.previous.current.content.tools) != len(callsPlan) || len(closed.previous.current.interactions) != 2 {
		t.Fatal("checkpoint lost original file-tool ownership")
	}
	token := nativeContinuationToken(47)
	authority.rotate(token)
	session, err = ContinueAPISession(ctx, closed, domain.NewID(), APIConfig{ServerOrigin: relay.URL, Token: token}, ContinueSuccessfulRun)
	if err != nil {
		t.Fatal("File conversation could not replace its process", err, logs.String())
	}
	sessions = append(sessions, session)
	nextInput := domain.NewID()
	if _, err := session.SendInput(ctx, nextInput, "Continue from the original edited file.", ContinueSuccessfulRun); err != nil {
		t.Fatal(err)
	}
	finished = false
	for {
		observed, err := session.Next(ctx)
		if err != nil {
			t.Fatal("resumed file conversation failed", err, logs.String())
		}
		if observed.Kind == InteractionObserved {
			t.Fatal("replacement repeated an original approval")
		}
		if observed.Kind == InputFinished {
			finished = observed.InputID == nextInput && observed.Result.Successful()
		}
		if observed.Kind == RunStateObserved && observed.Run.State == RunIdle {
			break
		}
	}
	if !finished || calls.Load() != int64(len(callsPlan)+2) {
		t.Fatal("resumed file-tool input did not complete exactly once")
	}
	closed, err = session.CloseForContinuation(ctx)
	if err != nil || closed.transcript.MatchedMessages != messages+2 {
		t.Fatal("original file-tool proof did not survive the replacement", err, logs.String())
	}
	if _, _, err := closed.RetainCheckpoint(ctx); err != nil {
		t.Fatal("resumed original file-tool ownership could not be retained", err)
	}
}
