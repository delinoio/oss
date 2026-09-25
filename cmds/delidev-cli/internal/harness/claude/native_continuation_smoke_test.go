package claude

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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

type rotatingNativeAPIAuthority struct {
	mu        sync.Mutex
	token     string
	authority nativeAPIAuthority
}

func (a *rotatingNativeAPIAuthority) Acquire(ctx context.Context, token string) (*apiproxy.Lease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if token != a.token || ctx.Err() != nil {
		return nil, domain.Fail(domain.Unauthenticated, "Invalid fixture authority.", "")
	}
	return &apiproxy.Lease{Scope: a.authority.scope, Context: a.authority.ctx, Release: func() {}, Key: func(context.Context) ([]byte, error) { return []byte(nativeAPIUpstreamKey), nil }}, nil
}

func (a *rotatingNativeAPIAuthority) rotate(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.token = token
	a.authority.scope.ExecutionID = domain.NewID()
}

func nativeContinuationToken(n byte) string {
	return apiproxy.TokenPrefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{n}, 32))
}

func TestManualNativeClosedSessionContinuation(t *testing.T) {
	nativeClosedSessionContinuation(t, false)
}

func TestManualNativeCheckpointContinuation(t *testing.T) {
	nativeClosedSessionContinuation(t, true)
}

func nativeClosedSessionContinuation(t *testing.T, retained bool) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native binary and private scripted provider required")
	}
	for _, permission := range []NativePermission{DefaultPermission, PlanPermission} {
		t.Run(string(permission), func(t *testing.T) {
			cfg, logs := apiFixtureConfig(t, "native-continuation")
			cfg.Process.Executable, cfg.Model, cfg.Permission = binary, "fixture-model", permission
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			var calls atomic.Int64
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/provider/messages" || r.Header.Get("X-Api-Key") != nativeAPIUpstreamKey || r.Header.Get("Authorization") != "" {
					t.Error("native resumed provider authority changed")
					w.WriteHeader(400)
					return
				}
				n := calls.Add(1)
				var request struct {
					Model  string          `json:"model"`
					System json.RawMessage `json:"system"`
					Output struct {
						Effort string `json:"effort"`
					} `json:"output_config"`
					Messages []struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					} `json:"messages"`
				}
				if json.NewDecoder(io.LimitReader(r.Body, maxStreamFrame)).Decode(&request) != nil || request.Model != cfg.Model || request.Output.Effort != string(cfg.Effort) || !bytes.Contains(request.System, []byte(cfg.Instructions)) || !bytes.Contains(request.System, []byte("Claude Code")) || n > 3 {
					t.Error("resumed native history or immutable settings changed", "call", n, "model", request.Model == cfg.Model, "effort", request.Output.Effort == string(cfg.Effort), "instructions", bytes.Contains(request.System, []byte(cfg.Instructions)), "base", bytes.Contains(request.System, []byte("Claude Code")), "messages", len(request.Messages))
					w.WriteHeader(400)
					return
				}
				original := 0
				for _, m := range request.Messages {
					body := string(m.Content)
					if !strings.Contains(body, "Fixture request") && !strings.Contains(body, "Fixture response") {
						if m.Role != "system" {
							t.Error("unobserved ordinary provider message")
						}
						continue
					}
					role, text := "user", fmt.Sprintf("Fixture request %d.", original/2+1)
					if original%2 == 1 {
						role, text = "assistant", fmt.Sprintf("Fixture response %d.", original/2+1)
					}
					if m.Role != role || nativeFixtureProviderText(m.Content, original == 0) != text {
						t.Error("process replacement lost or duplicated original conversation", original)
					}
					original++
				}
				if int64(original) != 2*n-1 {
					t.Error("original conversation message count changed", n, original)
				}
				nativeFixtureTextResponse(w, n)
			}))
			defer provider.Close()
			authority := &rotatingNativeAPIAuthority{token: nativeAPIFixtureToken, authority: nativeAPIAuthority{ctx: ctx, scope: apiproxy.Scope{ExecutionID: domain.NewID(), SessionID: cfg.SessionID, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(), NativeModel: cfg.Model, Provider: domain.Provider{Name: "Continuation fixture", Endpoint: provider.URL + "/provider", Protocol: domain.AnthropicMessages, Authentication: domain.APIKeyAuth}, Operations: []apiproxy.Operation{apiproxy.MessageCreate}}}}
			relay := httptest.NewServer(apiproxy.New(authority, slog.New(slog.NewJSONHandler(io.Discard, nil))))
			defer relay.Close()
			cfg.API.ServerOrigin = relay.URL
			s, err := OpenAPISession(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			all := []*APISession{s}
			defer func() {
				for _, session := range all {
					if err := session.Close(); err != nil {
						t.Error(err)
					}
					if err := process.ReconcileOwner(cfg.Process.Directory, session.config.Process.OwnerID); err != nil {
						t.Error(err)
					}
				}
			}()
			var priorInput domain.ID
			for turn := 1; turn <= 3; turn++ {
				input := domain.NewID()
				if priorInput != "" {
					if _, err := s.SendInput(ctx, priorInput, "Never replay", ContinueSuccessfulRun); err == nil {
						t.Fatal("old input replayed after replacement")
					}
				}
				if _, err := s.SendInput(ctx, input, fmt.Sprintf("Fixture request %d.", turn), ContinueSuccessfulRun); err != nil {
					t.Fatal(err)
				}
				idle, finished := false, false
				for !idle {
					observed, err := s.Next(ctx)
					if err != nil {
						t.Fatal("resumed native lifecycle failed", err, logs.String())
					}
					if observed.Kind == InputFinished {
						finished = observed.InputID == input && observed.Result.Successful()
					}
					idle = observed.Kind == RunStateObserved && observed.Run.State == RunIdle
					// Display consumers cannot change the session-owned original proof.
					if observed.Native != nil {
						clear(observed.Native.Body)
					}
				}
				if !finished {
					t.Fatal("resumed original input did not finish")
				}
				closed, err := s.CloseForContinuation(ctx)
				if err != nil || closed.transcript.MatchedMessages != uint32(2*turn) {
					t.Fatal("closed native history did not match original ledger", err)
				}
				encoded, _ := json.Marshal(closed)
				if string(encoded) != "{}" {
					t.Fatal("opaque handoff exposed private state")
				}
				if turn == 3 {
					break
				}
				if retained {
					configuration := s.config
					configuration.API = APIConfig{ServerOrigin: relay.URL}
					raw, reference, err := closed.RetainCheckpoint(ctx)
					if err != nil {
						t.Fatal("native checkpoint retention failed", err)
					}
					for _, private := range []string{cfg.Home, cfg.Workspace, cfg.Instructions, nativeAPIFixtureToken, nativeAPIUpstreamKey, "Fixture request", "Fixture response"} {
						if bytes.Contains(raw, []byte(private)) {
							t.Fatal("checkpoint contains private runtime content")
						}
					}
					closed, err = RestoreCheckpoint(ctx, configuration, raw, reference)
					if err != nil {
						t.Fatal("independently pinned native checkpoint did not restore", err)
					}
					clear(raw)
				}
				token := nativeContinuationToken(byte(30 + turn))
				authority.rotate(token)
				if _, err := authority.Acquire(ctx, nativeAPIFixtureToken); err == nil {
					t.Fatal("old execution credential remained active")
				}
				s, err = ContinueAPISession(ctx, closed, domain.NewID(), APIConfig{ServerOrigin: relay.URL, Token: token}, ContinueSuccessfulRun)
				if err != nil {
					t.Fatal("original native session failed process replacement", err, logs.String())
				}
				all = append(all, s)
				if _, err := ContinueAPISession(ctx, closed, domain.NewID(), APIConfig{ServerOrigin: relay.URL, Token: nativeContinuationToken(99)}, ContinueSuccessfulRun); err == nil {
					t.Fatal("closed handoff launched twice")
				}
				priorInput = input
			}
			if calls.Load() != 3 {
				t.Fatal("process replacement retried provider inference", calls.Load())
			}
			for _, private := range []string{cfg.Home, cfg.Workspace, cfg.Instructions, nativeAPIFixtureToken, nativeAPIUpstreamKey, "Fixture request", "Fixture response"} {
				if strings.Contains(logs.String(), private) {
					t.Fatal("continuation leaked private data")
				}
			}
		})
	}
}
