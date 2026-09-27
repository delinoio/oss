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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type nativeGrokWorkerAuthority struct {
	token, key string
	scope      apiproxy.Scope
	requests   atomic.Uint32
	releases   atomic.Uint32
}

func (a *nativeGrokWorkerAuthority) Acquire(ctx context.Context, token string) (*apiproxy.Lease, error) {
	if token != a.token {
		return nil, domain.Fail(domain.Unauthenticated, "Unknown private fixture authority.", "")
	}
	a.requests.Add(1)
	return &apiproxy.Lease{Scope: a.scope, Context: ctx, Key: func(context.Context) ([]byte, error) { return []byte(a.key), nil }, Release: func() { a.releases.Add(1) }}, nil
}

// Native ownership, the Worker journal and relay are real; account registration
// and upstream inference are fixtures. This does not assert public dispatch,
// durable event publication, history recovery or terminal cleanup reporting.
func TestManualNativeGrokWorkerOriginalClaims(t *testing.T) {
	executable := os.Getenv("DELIDEV_NATIVE_GROK_EXECUTABLE")
	if executable == "" {
		t.Skip("explicit pinned native binary and private runtime required")
	}
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprintf("changed-input-%t", changed), func(t *testing.T) {
			p, journal, _ := newGrokClaimsFixture(t)
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
			authority := &nativeGrokWorkerAuthority{token: apiproxy.TokenPrefix + base64.RawURLEncoding.EncodeToString(make([]byte, 32)), key: "private-server-key-" + string(domain.NewID())}
			t.Cleanup(func() {
				for _, private := range []string{authority.token, authority.key, p.input.Input.Prompt, workspace, "Original Worker fixture completed."} {
					if strings.Contains(logs.String(), private) {
						t.Error("native Worker diagnostics disclosed private content")
					}
				}
				if authority.requests.Load() != authority.releases.Load() {
					t.Error("native inference retained a relay lease")
				}
			})
			var upstream atomic.Uint32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstream.Add(1)
				raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
				var body struct {
					Model string `json:"model"`
				}
				if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &body) != nil || body.Model != p.input.Configuration.NativeModel || r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+authority.key || r.Header.Get("HTTP-Referer") != "https://deli.dev" || bytes.Contains(raw, []byte(authority.token)) {
					t.Error("native input escaped its original relay scope")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				claims, err := readGrokClaims(p.config.Root, journal.state.Reference)
				if err != nil || len(claims) < 3 || claims[0].Creation.RequestID != p.input.ThreadRequestID || claims[2].Input.RequestID != p.input.TurnRequestID {
					t.Error("native inference preceded synchronized original Worker claims")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				for _, chunk := range []string{
					`{"id":"chat-worker","object":"chat.completion.chunk","created":1,"model":"fixture-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Original Worker fixture completed."},"finish_reason":null}]}`,
					`{"id":"chat-worker","object":"chat.completion.chunk","created":1,"model":"fixture-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":5,"total_tokens":16}}`,
					`[DONE]`,
				} {
					_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
				}
			}))
			defer provider.Close()
			authority.scope = apiproxy.Scope{ExecutionID: p.input.ExecutionID, SessionID: p.input.SessionID, AccountID: p.input.AccountID, ConnectionID: p.input.ConnectionID, ProviderID: p.input.Configuration.ProviderID, ModelID: p.input.Configuration.ModelID, NativeModel: p.input.Configuration.NativeModel, Provider: domain.Provider{Name: "Private Worker fixture", Endpoint: provider.URL + "/v1", Protocol: domain.OpenAIChat, Authentication: domain.BearerAuth}, Operations: []apiproxy.Operation{apiproxy.ChatCompletion}}
			relay := httptest.NewServer(apiproxy.New(authority, logger))
			defer relay.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			cfg := grok.APIExecutionConfig{Probe: grok.ProbeConfig{Process: process.Config{Directory: filepath.Join(p.config.Root, "processes"), OwnerID: p.job, Executable: executable, Cwd: root, Env: env, Logger: logger}, Version: grok.SupportedVersion, Home: filepath.Join(root, "grok")}, Workspace: workspace, Model: p.input.Configuration.NativeModel, ContextTokens: 32000, ServerOrigin: relay.URL, Token: authority.token}
			api, err := grok.OpenOwnedAPI(ctx, cfg, journal.Creation, journal.Input, journal.Closure)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := api.Close(); err != nil {
					t.Error(err)
				}
				if err := process.ReconcileOwner(cfg.Probe.Process.Directory, p.job); err != nil {
					t.Error(err)
				}
				if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
					if err != nil || entry.IsDir() {
						return err
					}
					raw, err := os.ReadFile(path)
					if bytes.Contains(raw, []byte(authority.token)) || bytes.Contains(raw, []byte(authority.key)) {
						t.Error("native runtime persisted execution authority")
					}
					return err
				}); err != nil {
					t.Error(err)
				}
			}()
			session, err := api.Create(ctx, p.input.ThreadRequestID, p.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			input := p.input.Input.Prompt
			if changed {
				input += " replacement"
			}
			var text strings.Builder
			completed := false
			result, err := api.RunText(ctx, p.input.TurnRequestID, input, func(_ context.Context, observation grok.InputObservation) error {
				claims, err := readGrokClaims(p.config.Root, journal.state.Reference)
				if err != nil || len(claims) != 4 || claims[3].Input.NativePromptID != observation.NativePromptID || observation.InputID != p.input.TurnRequestID {
					return grokClaimUncertain()
				}
				if observation.Kind == grok.InputText {
					text.WriteString(observation.Chunk.Update.Content.Text)
				}
				if observation.Kind == grok.InputCompleted {
					completed = true
				}
				return nil
			})
			claims, readErr := readGrokClaims(p.config.Root, journal.state.Reference)
			if changed {
				if err == nil || !journal.failed || completed || readErr != nil || len(claims) != 2 || upstream.Load() != 0 || authority.requests.Load() != 0 {
					t.Fatal("changed immutable input reached inference or publication", err, readErr)
				}
			} else if err != nil || readErr != nil || !completed || len(claims) != 4 || claims[1].Creation.NativeSessionID != session || claims[3].Input.NativePromptID != result.Meta.Prompt || text.String() != "Original Worker fixture completed." || upstream.Load() == 0 {
				t.Fatal("original native Worker input lost its durable binding", err, readErr)
			}
			if _, err := api.RunText(ctx, p.input.TurnRequestID, p.input.Input.Prompt, func(context.Context, grok.InputObservation) error {
				t.Error("repeated native input published")
				return nil
			}); err == nil {
				t.Fatal("original input acquired replay authority")
			}
			if !changed {
				closed, err := api.CloseText(ctx, domain.NewID())
				claims, readErr := readGrokClaims(p.config.Root, journal.state.Reference)
				if err != nil || readErr != nil || len(claims) != 6 || claims[4].Closure.RequestID != closed.RequestID || claims[5].Closure.Phase != grok.BindClosure || closed.NativeSessionID != session || closed.NativePromptID != result.Meta.Prompt || closed.Summary != "Original Worker fixture completed." {
					t.Fatal("native closure lost original Worker evidence", err, readErr)
				}
				// There is no timing delay after native removal/process cleanup.
				// Persisted summary remains separate evidence from its live event.
				if err := security.CheckPrivateDir(cfg.Probe.Home); err != nil {
					t.Fatal(err)
				}
				// This fixture inspects only generated files beneath its private
				// home. Native Grok files can be 0644 inside that 0700 boundary;
				// production history needs its own anchored owner/path validator.
				raw, err := os.ReadFile(filepath.Join(cfg.Probe.Home, "sessions", url.PathEscape(workspace), string(session), "summary.json"))
				var summary struct {
					Text   string `json:"last_turn_summary"`
					Prompt string `json:"last_turn_summary_prompt_id"`
				}
				if err != nil || len(raw) > 64<<10 || json.Unmarshal(raw, &summary) != nil || summary.Text != closed.Summary || summary.Prompt != closed.NativePromptID {
					t.Fatal("native acknowledged closure lost its persisted summary", err)
				}
			}
			if p.state.LastSequence != 0 || p.state.Pending != nil {
				t.Fatal("private native claim invented public execution events")
			}
		})
	}
}
