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

func TestManualNativeBashToolRetainedHistory(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		name := "bash"
		if mixed {
			name = "bash-and-read"
		}
		t.Run(name, func(t *testing.T) { nativeBashToolRetainedHistory(t, mixed) })
	}
}

func TestManualNativeInlineBashTaskRetainedHistory(t *testing.T) {
	nativeBashToolRetainedHistory(t, false, true)
}

func nativeBashToolRetainedHistory(t *testing.T, mixed bool, taskCases ...bool) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native binary and private scripted provider required")
	}
	cfg, logs := apiFixtureConfig(t, "native-bash-history")
	cfg.Process.Executable, cfg.Model, cfg.Permission = binary, "fixture-model", DefaultPermission
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const content = "Private retained Bash tool fixture."
	command := `printf 'Private retained Bash tool fixture.' >> bash-marker.txt; cat bash-marker.txt`
	taskCase := len(taskCases) == 1 && taskCases[0]
	if taskCase {
		command = "sleep 4; " + command
	}
	firstCalls, toolCount, messages := int64(2), 1, uint32(4)
	if mixed {
		firstCalls, toolCount, messages = 3, 2, 6
	}
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/provider/messages" || r.Header.Get("X-Api-Key") != nativeAPIUpstreamKey || r.Header.Get("Authorization") != "" {
			t.Error("native Bash fixture authority changed")
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
			nativeFixtureToolResponse(w, n, "Bash", "toolu_retained_bash", map[string]any{"command": command, "description": "Run the private retained Bash fixture"})
		case 2, 3, 4:
			result := nativeFixtureToolResults(t, raw)["toolu_retained_bash"]
			if result.Error || !bytes.Contains(result.Content, []byte(content)) {
				t.Error("original Bash result missing from provider context")
				w.WriteHeader(400)
				return
			}
			if n > firstCalls+1 {
				t.Error("unexpected repeated inference")
				w.WriteHeader(400)
				return
			}
			if mixed && n == 2 {
				nativeFixtureToolResponse(w, n, "Read", "toolu_bash_marker_read", map[string]any{"file_path": filepath.Join(cfg.Workspace, "bash-marker.txt")})
				return
			}
			if mixed {
				read := nativeFixtureToolResults(t, raw)["toolu_bash_marker_read"]
				if read.Error || !bytes.Contains(read.Content, []byte(content)) {
					t.Error("original mixed Read result missing")
					w.WriteHeader(400)
					return
				}
			}
			if n == firstCalls+1 && bytes.Count(raw, []byte("Continue from the original Bash result.")) != 1 {
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
	authority := &rotatingNativeAPIAuthority{token: nativeAPIFixtureToken, authority: nativeAPIAuthority{ctx: ctx, scope: apiproxy.Scope{ExecutionID: domain.NewID(), SessionID: cfg.SessionID, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(), NativeModel: cfg.Model, Provider: domain.Provider{Name: "Bash history fixture", Endpoint: provider.URL + "/provider", Protocol: domain.AnthropicMessages, Authentication: domain.APIKeyAuth}, Operations: []apiproxy.Operation{apiproxy.MessageCreate}}}}
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
	if _, err := session.SendInput(ctx, input, "Run the private fixture command.", ContinueSuccessfulRun); err != nil {
		t.Fatal(err)
	}
	var arrival domain.ID
	var original *interactionState
	callbacks := 0
	finished := false
	for {
		observed, err := session.Next(ctx)
		if err != nil {
			t.Fatal("native Bash lifecycle failed", err, logs.String())
		}
		if observed.Kind == InputFinished {
			finished = observed.InputID == input && observed.Result.Successful()
		}
		if observed.Kind == InteractionObserved && observed.Interaction.Kind == InteractionRequested {
			if observed.Interaction.Request.Kind != ToolPermission || observed.Interaction.Request.ToolName != "Bash" {
				t.Fatal("unexpected native Bash callback")
			}
			callbacks++
			arrival = observed.Interaction.ArrivalID
			if err := session.Reply(ctx, observed.Interaction.ArrivalID, PermissionReply{Behavior: PermissionAllow}); err != nil {
				t.Fatal(err)
			}
		}
		if observed.Kind == RunStateObserved && observed.Run.State == RunIdle {
			break
		}
	}
	if !finished || calls.Load() != firstCalls || len(session.current.content.tools) != toolCount {
		t.Fatal("Bash fixture did not settle the original tools")
	}
	if callbacks != 1 || len(session.current.interactions) != 1 {
		t.Fatal("fixture did not retain one original approval", callbacks)
	}
	original = session.current.interactions[arrival]
	if !original.echoed || original.canceled || original.behavior != PermissionAllow {
		t.Fatal("fixture did not confirm original approval")
	}
	tool := session.current.content.tools["toolu_retained_bash"]
	if !tool.finished || tool.name != "Bash" || tool.parent != "" || tool.ownerInput != input || tool.ownerTurn != session.current.turnID {
		t.Fatal("original Bash tool ownership changed")
	}
	if taskCase {
		proofs, err := session.current.closedBashTasks()
		if err != nil || len(proofs) != 1 {
			t.Fatal("original completed inline Bash task did not qualify", err, len(session.current.tasks))
		}
	}
	closed, err := session.CloseForContinuation(ctx)
	if err != nil || closed.transcript.MatchedMessages != messages || closed.transcript.AdditionalMessages != 0 {
		t.Fatal("Bash tool retained history is incomplete", err, logs.String())
	}
	previous := session.config
	previous.API = APIConfig{ServerOrigin: relay.URL}
	raw, reference, err := closed.RetainCheckpoint(ctx)
	if err != nil {
		t.Fatal("Bash tool checkpoint could not be retained", err)
	}
	for _, private := range []string{command, content, cfg.Home, cfg.Workspace, nativeAPIFixtureToken, nativeAPIUpstreamKey, "Run the private fixture command."} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatal("Bash checkpoint exposed private content")
		}
	}
	closed, err = RestoreCheckpoint(ctx, previous, raw, reference)
	if err != nil {
		t.Fatal("Bash tool checkpoint did not restore", err, logs.String())
	}
	var checkpoint sessionCheckpoint
	if json.Unmarshal(raw, &checkpoint) != nil || len(checkpoint.BashTools) != 1 || len(checkpoint.ToolApprovals) != 1 || len(checkpoint.ReadTools) != toolCount-1 {
		t.Fatal("checkpoint lost original Bash/approval profile")
	}
	if taskCase && len(checkpoint.BashTasks) != 1 {
		t.Fatal("checkpoint lost original task ownership")
	}
	if taskCase {
		proofs, err := closed.previous.current.closedBashTasks()
		if err != nil || len(proofs) != 1 {
			t.Fatal("restored task ownership changed", err)
		}
	}
	clear(raw)
	restored := closed.previous.current.interactions[arrival]
	if restored == nil || restored.input != original.input || restored.turn != original.turn || restored.request.RequestID != original.request.RequestID || restored.request.ToolID != original.request.ToolID || restored.reply != original.reply || restored.requestDigest != original.requestDigest || !restored.echoed || !restored.prepared {
		t.Fatal("checkpoint changed original approval")
	}
	if len(closed.previous.current.content.tools) != toolCount || closed.previous.current.content.tools["toolu_retained_bash"].ownerInput != input {
		t.Fatal("checkpoint lost original Bash ownership")
	}
	token := nativeContinuationToken(47)
	authority.rotate(token)
	session, err = ContinueAPISession(ctx, closed, domain.NewID(), APIConfig{ServerOrigin: relay.URL, Token: token}, ContinueSuccessfulRun)
	if err != nil {
		t.Fatal("Bash conversation could not replace its process", err, logs.String())
	}
	sessions = append(sessions, session)
	nextInput := domain.NewID()
	if _, err := session.SendInput(ctx, nextInput, "Continue from the original Bash result.", ContinueSuccessfulRun); err != nil {
		t.Fatal(err)
	}
	finished = false
	for {
		observed, err := session.Next(ctx)
		if err != nil {
			t.Fatal("resumed Bash conversation failed", err, logs.String())
		}
		if observed.Kind == InputFinished {
			finished = observed.InputID == nextInput && observed.Result.Successful()
		}
		if observed.Kind == RunStateObserved && observed.Run.State == RunIdle {
			break
		}
	}
	if !finished || calls.Load() != firstCalls+1 {
		t.Fatal("resumed Bash input did not complete exactly once")
	}
	closed, err = session.CloseForContinuation(ctx)
	if err != nil || closed.transcript.MatchedMessages != messages+2 {
		t.Fatal("original Bash proof did not survive the replacement", err, logs.String())
	}
	if actual, err := os.ReadFile(filepath.Join(cfg.Workspace, "bash-marker.txt")); err != nil || string(actual) != content {
		t.Fatal("native Bash side effect changed or repeated", err)
	}
	if _, _, err := closed.RetainCheckpoint(ctx); err != nil {
		t.Fatal("resumed original Bash ownership could not be retained", err)
	}
}
