package worker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

type claudeCheckpointAuthority struct {
	mu    sync.Mutex
	token string
	scope apiproxy.Scope
	ctx   context.Context
}

const claudeCheckpointUpstreamKey = "server-only-private-checkpoint-fixture"

type claudeCheckpointLogWriter struct{ t *testing.T }

func (w claudeCheckpointLogWriter) Write(raw []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(raw)))
	return len(raw), nil
}

func (a *claudeCheckpointAuthority) Acquire(ctx context.Context, token string) (*apiproxy.Lease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if token != a.token || ctx.Err() != nil {
		return nil, domain.Fail(domain.Unauthenticated, "Invalid fixture authority.", "")
	}
	return &apiproxy.Lease{Scope: a.scope, Context: a.ctx, Release: func() {}, Key: func(context.Context) ([]byte, error) { return []byte(claudeCheckpointUpstreamKey), nil }}, nil
}

func claudeCheckpointToken(n byte) string {
	return apiproxy.TokenPrefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{n}, 32))
}

// This fixture exercises real native process/history and Worker file boundaries.
// The immutable assignment, acknowledged terminal and workspace lease metadata
// are modeled: it does not claim public Worker dispatch or account readiness.
func TestManualNativeClaudeWorkerCheckpoint(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit pinned native binary and private scripted provider required")
	}
	f, _ := claudeCheckpointMetadataFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runtimeRoot := filepath.Join(f.root, "runtimes", string(f.input.ExecutionID))
	env, err := harness.PrivateRuntimeEnvironment(runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	workdir := filepath.Join(f.root, "workspace")
	if err := security.PrivateDir(workdir); err != nil {
		t.Fatal(err)
	}
	f.input.Installation.ResolvedPath = binary
	f.input.Manifest, _ = json.Marshal(workspace.Manifest{Version: 1, SessionID: f.input.SessionID, MachineID: f.input.MachineID, Type: domain.Local, State: workspace.Ready, PrimaryPath: workdir})
	f.job.Input, _ = json.Marshal(f.input)
	f.ref.AssignmentInputDigest = executionInputDigest(f.job.Input)
	logger := slog.New(slog.NewJSONHandler(claudeCheckpointLogWriter{t}, nil))
	permission, effort, err := claudeExecutionSettings(f.input.Configuration, f.input.Input.Mode)
	if err != nil {
		t.Fatal(err)
	}
	cfg := claude.APIStreamConfig{Process: process.Config{Directory: filepath.Join(f.root, "processes"), OwnerID: f.jobID, Executable: binary, Cwd: runtimeRoot, Env: env, Logger: logger}, Version: claude.SupportedVersion, Home: filepath.Join(runtimeRoot, "claude"), Workspace: workdir, SessionID: f.input.SessionID, Model: f.input.Configuration.NativeModel, Effort: effort, Permission: permission, Instructions: f.input.Configuration.Instructions}
	const followup = "Private follow-up after retained checkpoint."
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/provider/messages" || r.Header.Get("X-Api-Key") != claudeCheckpointUpstreamKey || r.Header.Get("Authorization") != "" {
			t.Error("native provider authority changed")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		n := calls.Add(1)
		var request struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&request) != nil || request.Model != cfg.Model || n > 2 {
			t.Error("native request changed or repeated")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		original := 0
		texts := []string{f.input.Input.Prompt, "Private response 1.", followup}
		roles := []string{"user", "assistant", "user"}
		for _, m := range request.Messages {
			if m.Role == "system" {
				continue
			}
			if original >= len(texts) || m.Role != roles[original] || claudeCheckpointProviderText(m.Content, original == 0) != texts[original] {
				t.Error("native checkpoint lost, duplicated or changed conversation", original)
			}
			original++
		}
		if int64(original) != 2*n-1 {
			t.Error("native conversation count changed", original, n)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []map[string]any{
			{"type": "message_start", "message": map[string]any{"id": fmt.Sprintf("msg_private_checkpoint_%d", n), "type": "message", "role": "assistant", "content": []any{}, "model": cfg.Model, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}},
			{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
			{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": fmt.Sprintf("Private response %d.", n)}},
			{"type": "content_block_stop", "index": 0},
			{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 2}},
			{"type": "message_stop"},
		} {
			raw, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], raw)
		}
	}))
	defer provider.Close()
	authority := &claudeCheckpointAuthority{ctx: ctx, token: claudeCheckpointToken(41), scope: apiproxy.Scope{ExecutionID: f.input.ExecutionID, SessionID: f.input.SessionID, AccountID: f.input.AccountID, ConnectionID: f.input.ConnectionID, ProviderID: f.input.Configuration.ProviderID, ModelID: f.input.Configuration.ModelID, NativeModel: cfg.Model, Provider: domain.Provider{Name: "Private checkpoint fixture", Endpoint: provider.URL + "/provider", Protocol: domain.AnthropicMessages, Authentication: domain.APIKeyAuth}, Operations: []apiproxy.Operation{apiproxy.MessageCreate}}}
	relay := httptest.NewServer(apiproxy.New(authority, logger))
	defer relay.Close()
	cfg.API = claude.APIConfig{ServerOrigin: relay.URL, Token: authority.token}
	s, err := claude.OpenAPISession(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func(s *claude.APISession, owner domain.ID) {
		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Error(err)
			}
			if err := process.ReconcileOwner(cfg.Process.Directory, owner); err != nil {
				t.Error(err)
			}
		})
	}
	cleanup(s, f.jobID)
	finish := func(s *claude.APISession, input domain.ID, prompt string) string {
		t.Helper()
		if _, err := s.SendInput(ctx, input, prompt, claude.ContinueSuccessfulRun); err != nil {
			t.Fatal(err)
		}
		var turn string
		for {
			observed, err := s.Next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if observed.Kind == claude.InputFinished && observed.InputID == input && observed.Result.Successful() {
				turn = observed.TurnID
			}
			if observed.Kind == claude.RunStateObserved && observed.Run.State == claude.RunIdle {
				if turn == "" {
					t.Fatal("original input did not finish successfully")
				}
				return turn
			}
		}
	}
	f.completion.NativeTurnID = domain.NativeIdentity(finish(s, f.input.InputID, f.input.Input.Prompt))
	f.ref.Completion = f.completion
	closed, err := s.CloseForContinuation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wrong := f.completion
	wrong.Outcome = domain.ExecutionFailed
	if _, err := retainClaudeCompletion(ctx, f.root, f.jobID, f.job, f.input, wrong, closed); err == nil {
		t.Fatal("caller replaced the original native terminal outcome")
	}
	digest, err := retainClaudeCompletion(ctx, f.root, f.jobID, f.job, f.input, f.completion, closed)
	if err != nil {
		t.Fatal("native Worker checkpoint retention failed", err)
	}
	f.ref.Completion.Version, f.ref.Completion.NativeCheckpointDigest = 2, digest
	path, err := executionCheckpointPath(f.root, f.input.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := security.ReadPrivate(path, maxClaudeExecutionCheckpointBytes)
	if err != nil || executionInputDigest(raw) != digest {
		t.Fatal("retained file does not match original independent digest", err)
	}
	for _, private := range []string{f.input.Input.Prompt, "Private response", followup, cfg.API.Token, claudeCheckpointUpstreamKey, runtimeRoot, workdir} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatal("Worker checkpoint contains private content")
		}
	}
	foreign := f.ref
	foreign.AccountID = domain.NewID()
	if _, err := ReadClaudeExecutionCheckpoint(ctx, f.root, foreign, cfg); err == nil || calls.Load() != 1 {
		t.Fatal("foreign account obtained native history authority")
	}
	cfg.API.Token = ""
	closed, err = ReadClaudeExecutionCheckpoint(ctx, f.root, f.ref, cfg)
	if err != nil {
		t.Fatal("independently pinned Worker checkpoint did not restore", err)
	}
	owner, token := domain.NewID(), claudeCheckpointToken(42)
	authority.mu.Lock()
	authority.token, authority.scope.ExecutionID = token, domain.NewID()
	authority.mu.Unlock()
	s, err = claude.ContinueAPISession(ctx, closed, owner, claude.APIConfig{ServerOrigin: relay.URL, Token: token}, claude.ContinueSuccessfulRun)
	if err != nil {
		t.Fatal("restored Worker checkpoint did not continue", err)
	}
	cleanup(s, owner)
	finish(s, domain.NewID(), followup)
	closed, err = s.CloseForContinuation(ctx)
	if err != nil {
		t.Fatal("replacement native history did not close", err)
	}
	if _, _, err := closed.RetainCheckpoint(ctx); err != nil || calls.Load() != 2 {
		t.Fatal("replacement lost original proof or repeated inference", err)
	}
}

func claudeCheckpointProviderText(raw json.RawMessage, initial bool) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	if len(blocks) == 1 && blocks[0].Type == "text" {
		return blocks[0].Text
	}
	// The first native provider input includes its own date context, separate
	// from the exact original user prompt. Do not count that as another input.
	if initial && len(blocks) == 2 && blocks[0].Type == "text" && blocks[1].Type == "text" && strings.HasPrefix(blocks[0].Text, "<system-reminder>\n") && strings.HasSuffix(blocks[0].Text, "</system-reminder>\n\n") {
		return blocks[1].Text
	}
	return ""
}
