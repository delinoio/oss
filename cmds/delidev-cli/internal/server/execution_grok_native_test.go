package server

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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

func TestManualNativeGrokOriginalServerBinding(t *testing.T) {
	executable := os.Getenv("DELIDEV_NATIVE_GROK_EXECUTABLE")
	if executable == "" {
		t.Skip("explicit private native executable required")
	}
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		for _, drop := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/lost-%d", mode, drop), func(t *testing.T) {
				var requests atomic.Uint32
				var f *publicationFixture
				var claimsPath string
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
					var body struct {
						Model string `json:"model"`
					}
					if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &body) != nil || body.Model != f.input.Configuration.NativeModel || r.Method != http.MethodPost || r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer temporary-upstream-fixture-key" || r.Header.Get("HTTP-Referer") != "https://deli.dev" || bytes.Contains(raw, []byte(f.token)) {
						t.Error("Grok request escaped original registered account scope")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					claims, err := security.ReadPrivate(claimsPath, 256<<10)
					if err != nil || !bytes.Contains(claims, []byte(f.input.TurnRequestID)) || !bytes.Contains(claims, []byte(`"phase":"claim-input"`)) {
						t.Error("native input preceded synchronized Worker ownership")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					for _, part := range []any{
						map[string]any{"id": "original-grok-binding-response", "object": "chat.completion.chunk", "created": 1, "model": body.Model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": "Original binding fixture completed."}, "finish_reason": nil}}},
						map[string]any{"id": "original-grok-binding-response", "object": "chat.completion.chunk", "created": 1, "model": body.Model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 5, "total_tokens": 16}},
					} {
						encoded, _ := json.Marshal(part)
						_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
					}
					_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
				}))
				defer provider.Close()
				f = publicationFixtureFromAuthority(t, newProfileAuthorityFixture(t, provider.URL, domain.GrokBuild, domain.OpenAIChat, func(i *domain.ExecutionJobInput) { i.Input.Mode = mode }, false))
				f.registerGrant(t)
				cfg := publicationWorkerConfig(t, f)
				if err := security.PrivateDir(cfg.Root); err != nil {
					t.Fatal(err)
				}
				var err error
				cfg.Root, err = filepath.EvalSymlinks(cfg.Root)
				if err != nil {
					t.Fatal(err)
				}
				var logs bytes.Buffer
				cfg.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
				client := &losePublicationAck{WorkerServiceClient: f.client, t: t, path: filepath.Join(cfg.Root, "jobs", string(f.job), "publication.json"), dropAt: drop}
				cfg.Client = client
				publisher, err := worker.OpenExecutionPublisher(cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer publisher.Close()
				binding, err := worker.OpenGrokBindingPublisher(publisher)
				if err != nil {
					t.Fatal(err)
				}
				defer binding.Close()
				claimsPath = filepath.Join(cfg.Root, "jobs", string(f.job), "grok-claims.json")
				root := filepath.Join(cfg.Root, "runtimes", string(f.input.ExecutionID))
				env, err := harness.PrivateRuntimeEnvironment(root)
				if err != nil {
					t.Fatal(err)
				}
				workspace := filepath.Join(cfg.Root, "workspace original 한글")
				if err := security.PrivateDir(workspace); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
				defer cancel()
				nativeConfig := grok.APIExecutionConfig{Probe: grok.ProbeConfig{Process: process.Config{Directory: filepath.Join(cfg.Root, "processes"), OwnerID: f.job, Executable: executable, Cwd: root, Env: env, Logger: cfg.Logger}, Version: grok.SupportedVersion, Home: filepath.Join(root, "grok")}, Workspace: workspace, Model: f.input.Configuration.NativeModel, ContextTokens: 32000, Mode: mode, ServerOrigin: f.http.URL, Token: f.token}
				var api *grok.OwnedAPI
				if mode == domain.PlanMode {
					api, err = grok.OpenOwnedAPIWithPlanQuestions(ctx, nativeConfig, binding.Creation, binding.Mode, binding.Input, func(context.Context, grok.QuestionClaim) error {
						t.Error("text binding fixture acquired a question response")
						return context.Canceled
					})
				} else {
					api, err = grok.OpenOwnedAPI(ctx, nativeConfig, binding.Creation, binding.Input, func(context.Context, grok.ClosureClaim) error {
						t.Error("binding fixture acquired a closure claim")
						return context.Canceled
					})
				}
				if err != nil {
					t.Fatal(err)
				}
				defer api.Close()
				session, err := api.Create(ctx, f.input.ThreadRequestID, f.input.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				if mode == domain.PlanMode {
					if _, err := api.SelectPlan(ctx, domain.NewID()); err != nil {
						t.Fatal(err)
					}
				}
				observed, err := api.SessionBinding(ctx)
				if err != nil || observed.NativeSessionID != session {
					t.Fatal("original session not ready", err)
				}
				err = binding.BindSession(ctx, observed)
				if drop == 1 {
					if err == nil {
						t.Fatal("fixture did not lose original binding acknowledgment")
					}
					before, _ := security.ReadPrivate(claimsPath, 256<<10)
					if err = binding.ReplayPending(ctx); err != nil {
						t.Fatal(err)
					}
					after, _ := security.ReadPrivate(claimsPath, 256<<10)
					if !bytes.Equal(before, after) {
						t.Fatal("binding replay changed original native claims")
					}
				} else if err != nil {
					t.Fatal(err)
				}
				accepted := false
				var prompt string
				emit := func(callback context.Context, v grok.InputObservation) error {
					if v.Kind != grok.InputAccepted {
						return nil
					}
					if accepted {
						t.Error("original input accepted twice")
						return context.Canceled
					}
					prompt = v.NativePromptID
					err := binding.AcceptInput(callback, v)
					if drop == 2 {
						if err == nil {
							t.Error("fixture did not lose input acknowledgment")
							return context.Canceled
						}
						before, _ := security.ReadPrivate(claimsPath, 256<<10)
						err = binding.ReplayPending(callback)
						after, _ := security.ReadPrivate(claimsPath, 256<<10)
						if !bytes.Equal(before, after) {
							t.Error("input replay changed original claims")
							return context.Canceled
						}
					}
					if err == nil {
						accepted = true
					}
					return err
				}
				if mode == domain.PlanMode {
					_, err = api.RunPlanQuestions(ctx, f.input.TurnRequestID, f.input.Input.Prompt, emit)
				} else {
					_, err = api.RunText(ctx, f.input.TurnRequestID, f.input.Input.Prompt, emit)
				}
				if err != nil || !accepted || requests.Load() == 0 || len(client.calls) != 3 || client.calls[drop-1] != client.calls[drop] {
					t.Log(logs.String())
					t.Fatal("original native/server binding failed", err)
				}
				record, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				retained, err := store.Decode[domain.Session](record)
				if err != nil || retained.Execution == nil || retained.Execution.LastSequence != 2 || retained.Execution.NativeThreadID != string(session) || retained.Execution.NativeTurnID != prompt || retained.Execution.Observed.ValidateForInput(f.input.Configuration, mode) != nil || retained.PendingInputs != 0 {
					t.Fatal("server lost original Grok mode/input evidence", err)
				}
				if err := api.Close(); err != nil {
					t.Fatal(err)
				}
				if err := process.ReconcileOwner(nativeConfig.Probe.Process.Directory, f.job); err != nil {
					t.Fatal(err)
				}
				if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
					if err != nil || entry.IsDir() {
						return err
					}
					raw, err := os.ReadFile(path)
					if bytes.Contains(raw, []byte(f.token)) || bytes.Contains(raw, []byte("temporary-upstream-fixture-key")) {
						t.Error("native runtime retained relay credentials")
					}
					return err
				}); err != nil {
					t.Fatal(err)
				}
				for _, private := range []string{f.token, "temporary-upstream-fixture-key", f.input.Input.Prompt, workspace, "Original binding fixture completed."} {
					if strings.Contains(logs.String(), private) {
						t.Fatal("Grok binding logs exposed private data")
					}
				}
			})
		}
	}
}
