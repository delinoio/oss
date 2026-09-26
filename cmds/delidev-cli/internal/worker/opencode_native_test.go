package worker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type nativeOpenCodeWorkerAuthority struct {
	token, key string
	scope      apiproxy.Scope
	requests   atomic.Int32
	releases   atomic.Int32
}

func (a *nativeOpenCodeWorkerAuthority) Acquire(ctx context.Context, token string) (*apiproxy.Lease, error) {
	if token != a.token {
		return nil, domain.Fail(domain.Unauthenticated, "Unknown private fixture authority.", "")
	}
	a.requests.Add(1)
	return &apiproxy.Lease{Scope: a.scope, Context: ctx, Key: func(context.Context) ([]byte, error) { return []byte(a.key), nil }, Release: func() { a.releases.Add(1) }}, nil
}

// The original Worker publisher/journal, native process and server relay are
// real. Account registration and upstream inference remain isolated fixtures;
// no public execution event or report is asserted by this integration.
func TestManualNativeOpenCodeWorkerOriginalClaims(t *testing.T) {
	nativeOpenCodeWorkerFixture(t, false, false)
}

func TestManualNativeOpenCodeWorkerBindings(t *testing.T) {
	nativeOpenCodeWorkerFixture(t, true, false)
}

func TestManualNativeOpenCodeWorkerRejectsChangedInput(t *testing.T) {
	nativeOpenCodeWorkerFixture(t, false, true)
}

func nativeOpenCodeWorkerFixture(t *testing.T, publishBindings, changedInput bool) {
	t.Helper()
	executable := os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE")
	if executable == "" {
		t.Skip("explicit pinned native binary and private runtime required")
	}
	modes := []domain.SessionMode{domain.ExecuteMode, domain.PlanMode}
	if changedInput {
		modes = modes[:1]
	}
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			p, journal, _ := newOpenCodeClaimsFixtureMode(t, mode)
			var bindings *OpenCodeBindingPublisher
			var bindingRPC *openCodeBindingRPC
			if publishBindings {
				bindingRPC = &openCodeBindingRPC{t: t, publisher: p}
				p.config.Client = bindingRPC
				var err error
				bindings, err = newOpenCodeBindingPublisher(p, journal)
				if err != nil {
					t.Fatal(err)
				}
			}
			settings, err := openCodeExecutionSettings(p.input.Configuration, mode, "Private original Worker session")
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(p.config.Root, "runtimes", string(p.input.ExecutionID))
			env, err := harness.PrivateRuntimeEnvironment(root)
			if err != nil {
				t.Fatal(err)
			}
			workspace := filepath.Join(p.config.Root, "workspace")
			if err := security.PrivateDir(workspace); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			authority := &nativeOpenCodeWorkerAuthority{token: apiproxy.TokenPrefix + base64.RawURLEncoding.EncodeToString(make([]byte, 32)), key: "private-server-key-" + string(domain.NewID())}
			t.Cleanup(func() {
				// Native process and both HTTP server defers have joined before
				// this read, including when an earlier assertion failed.
				if strings.Contains(logs.String(), authority.token) || strings.Contains(logs.String(), authority.key) {
					t.Error("original Worker native diagnostics disclosed credentials")
				}
			})
			var upstream atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstream.Add(1)
				raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
				var body map[string]json.RawMessage
				if err != nil || domain.Decode(raw, &body) != nil || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+authority.key || r.Header.Get("HTTP-Referer") != "https://deli.dev" || bytes.Contains(raw, []byte(authority.token)) {
					t.Error("Worker native input escaped its original fixture relay scope")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				var model string
				if json.Unmarshal(body["model"], &model) != nil || model != p.input.Configuration.NativeModel {
					t.Error("Worker native input changed its immutable model")
				}
				claims, err := readOpenCodeClaims(p.config.Root, journal.state.Reference)
				if err != nil || len(claims) != 2 || claims[0].RequestID != p.input.ThreadRequestID || claims[1].RequestID != p.input.TurnRequestID || claims[1].Kind != opencode.SubmitInputMutation {
					t.Error("actual native inference preceded the synchronized original Worker claims")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				for _, choice := range []map[string]any{
					{"index": 0, "delta": map[string]any{"role": "assistant", "content": "Original Worker fixture completed."}, "finish_reason": nil},
					{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"},
				} {
					chunk, _ := json.Marshal(map[string]any{"id": "chatcmpl-worker-private", "object": "chat.completion.chunk", "created": 1, "model": model, "choices": []any{choice}})
					_, _ = io.WriteString(w, "data: "+string(chunk)+"\n\n")
				}
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
			}))
			defer provider.Close()
			authority.scope = apiproxy.Scope{ExecutionID: p.input.ExecutionID, SessionID: p.input.SessionID, AccountID: p.input.AccountID, ConnectionID: p.input.ConnectionID, ProviderID: p.input.Configuration.ProviderID, ModelID: p.input.Configuration.ModelID, NativeModel: p.input.Configuration.NativeModel, Provider: domain.Provider{Name: "Private Worker fixture", Endpoint: provider.URL + "/v1", Protocol: domain.OpenAIChat, Authentication: domain.BearerAuth}, Operations: []apiproxy.Operation{apiproxy.ChatCompletion}}
			relay := httptest.NewServer(apiproxy.New(authority, logger))
			defer relay.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			cfg := opencode.APIExecutionConfig{
				Probe:     opencode.ProbeConfig{Process: process.Config{Directory: filepath.Join(p.config.Root, "processes"), OwnerID: p.job, Executable: executable, Cwd: root, Env: env, Logger: logger}, Version: opencode.SupportedVersion, Home: filepath.Join(root, "opencode")},
				Workspace: workspace, NativeRoot: filepath.VolumeName(workspace) + string(filepath.Separator), ServerOrigin: relay.URL, Token: authority.token,
				Settings: settings.Session, Instructions: settings.Instructions, Rejection: settings.Rejection, Claim: journal.Claim,
			}
			api, err := opencode.OpenOwnedAPI(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
				defer stop()
				if err := api.Close(cleanup); err != nil {
					t.Error(err)
				}
				if err := process.ReconcileOwnerContext(cleanup, cfg.Probe.Process.Directory, p.job); err != nil {
					t.Error(err)
				}
			}()
			observed, err := api.InitialSettings(ctx)
			if err != nil || observed.ValidateForInput(p.input.Configuration, mode) != nil {
				t.Fatal("Worker settings did not match the original native initialization")
			}
			session, err := api.CreateSession(ctx, p.input.ThreadRequestID)
			if err != nil {
				t.Fatal(err)
			}
			if publishBindings {
				bindingRPC.lose = true
				if bindings.BindSession(ctx, p.input.ThreadRequestID, session, observed) == nil {
					t.Fatal("expected uncertain original binding acknowledgement")
				}
				bindingRPC.lose = false
				if err := bindings.ReplayPending(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := api.StartText(ctx, p.input.ThreadRequestID, p.input.Input.Prompt); err == nil {
				t.Fatal("original creation identity was reused for native input")
			}
			if _, err := api.StartText(ctx, domain.NewID(), ""); err == nil {
				t.Fatal("invalid input consumed original subscription authority")
			}
			if _, err := api.StartText(ctx, domain.NewID(), strings.Repeat("\x01", 256<<10)); err == nil {
				t.Fatal("JSON-expanded input consumed original subscription authority")
			}
			if changedInput {
				if _, err := api.StartText(ctx, p.input.TurnRequestID, p.input.Input.Prompt+" changed input"); err == nil || !journal.failed || upstream.Load() != 0 || authority.requests.Load() != 0 {
					t.Fatal("changed immutable input reached native inference authority")
				}
				if _, err := api.StartText(ctx, p.input.TurnRequestID, p.input.Input.Prompt); err == nil {
					t.Fatal("failed original input claim regained send authority")
				}
				claims, err := readOpenCodeClaims(p.config.Root, journal.state.Reference)
				if err != nil || len(claims) != 1 || claims[0].Kind != opencode.CreateSessionMutation || p.state.LastSequence != 0 {
					t.Fatal("failed input claim rewrote native creation or invented publication")
				}
				return
			}
			if _, err := api.StartText(ctx, p.input.TurnRequestID, p.input.Input.Prompt); err != nil {
				t.Fatal(err)
			}
			var progress opencode.Progress
			for i := 0; i < 256; i++ {
				if _, err := api.Next(ctx); err != nil {
					t.Fatal(err)
				}
				progress, err = api.Progress(ctx)
				if err != nil || progress.SettledObserved {
					break
				}
			}
			claims, err := readOpenCodeClaims(p.config.Root, journal.state.Reference)
			if err != nil || len(claims) != 2 || !progress.SettledObserved || progress.NeedsRecovery || progress.SessionID != session || progress.MessageID != claims[1].MessageID || progress.RequestID != p.input.TurnRequestID {
				t.Fatal("Worker/native original input boundary did not settle exactly")
			}
			receipt, err := api.InspectInput(ctx)
			if err != nil || !receipt.Recorded || receipt.SessionID != session || receipt.MessageID != claims[1].MessageID || receipt.PartID != claims[1].PartID {
				t.Fatal("Worker claims did not retain exact actual native storage")
			}
			expectedSequence := uint64(0)
			if publishBindings {
				bindingRPC.lose = true
				if bindings.AcceptInput(ctx, receipt) == nil {
					t.Fatal("expected uncertain original acceptance acknowledgement")
				}
				bindingRPC.lose = false
				if err := bindings.ReplayPending(ctx); err != nil {
					t.Fatal(err)
				}
				expectedSequence = 2
				if len(bindingRPC.requests) != 4 || bindingRPC.requests[0] != bindingRPC.requests[1] || bindingRPC.requests[2] != bindingRPC.requests[3] || !bytes.Equal(bindingRPC.events[0], bindingRPC.events[1]) || !bytes.Equal(bindingRPC.events[2], bindingRPC.events[3]) {
					t.Fatal("actual native binding replaced an uncertain outbox request")
				}
			}
			if p.state.LastSequence != expectedSequence || p.state.Pending != nil || upstream.Load() != 1 || authority.requests.Load() != 1 || authority.releases.Load() != 1 {
				t.Fatal("private integration replayed inference or invented public publication")
			}
			if _, err := api.StartText(ctx, domain.NewID(), "unrequested replacement"); err == nil {
				t.Fatal("settled original input authorized another send")
			}
			retained, err := readOpenCodeClaims(p.config.Root, journal.state.Reference)
			if err != nil || len(retained) != 2 || upstream.Load() != 1 {
				t.Fatal("rejected replacement changed original claims or provider requests")
			}
		})
	}
}
