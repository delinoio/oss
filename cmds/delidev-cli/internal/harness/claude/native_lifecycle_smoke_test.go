package claude

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

func TestManualNativeAPIErrorPreservesOriginalLifecycle(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native binary and private scripted provider required")
	}
	cfg, logs := apiFixtureConfig(t, "native-api-error")
	cfg.Process.Executable = binary
	cfg.Permission = DefaultPermission
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/provider/messages" || r.Header.Get("X-Api-Key") != nativeAPIUpstreamKey {
			t.Error("native failure request changed provider authority")
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"private-provider-diagnostic"}}`)
	}))
	defer provider.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	authority := nativeAPIAuthority{ctx: ctx, scope: apiproxy.Scope{ExecutionID: domain.NewID(), SessionID: cfg.SessionID, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(), NativeModel: cfg.Model, Provider: domain.Provider{Name: "Failure fixture", Endpoint: provider.URL + "/provider", Protocol: domain.AnthropicMessages, Authentication: domain.APIKeyAuth}, Operations: []apiproxy.Operation{apiproxy.MessageCreate}}}
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
		for _, private := range []string{nativeAPIFixtureToken, nativeAPIUpstreamKey, "private-provider-diagnostic", lifecycleFixtureText} {
			if strings.Contains(logs.String(), private) {
				t.Error("private failure content entered native lifecycle logs")
			}
		}
	}()
	input := domain.NewID()
	binding, err := BindExecution(cfg, input, lifecycleFixtureText)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SendInput(ctx, input, cfg.SessionID, lifecycleFixtureText); err != nil {
		t.Fatal(err)
	}
	for {
		event, err := s.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		observation, err := binding.Observe(event)
		if err != nil {
			t.Fatal(err)
		}
		if observation.Kind == UncorrelatedTermination {
			if observation.InputID != "" || observation.Accepted || observation.Result == nil || observation.Result.Successful() || !observation.Result.Error || observation.Result.Kind != ResultSuccess || observation.Result.Reason != APIError || requests.Load() != 1 || !binding.accepted {
				t.Fatal("uncorrelated native error became original-input completion or erased prior acceptance")
			}
			return
		}
	}
}

// This fixture executes the installed native adapter against a local-only provider.
// It proves protocol composition, not account or remote-provider acceptance.
func TestManualNativeScriptedTextCompletion(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native Claude executable required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	version, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	cfg, logs := apiFixtureConfig(t, "native-scripted-completion")
	cfg.Process.Executable = binary
	cfg.Version = strings.TrimSuffix(strings.TrimSpace(string(version)), " (Claude Code)")
	cfg.Model = "claude-sonnet-5-5"
	cfg.Permission = DefaultPermission
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "{}")
			return
		}
		if strings.HasSuffix(r.URL.Path, "/count_tokens") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"input_tokens":1}`)
			return
		}
		nativeFixtureTextResponse(w, 1)
	}))
	defer provider.Close()
	cfg.API.ServerOrigin = provider.URL
	s, err := OpenAPISession(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.SendInput(ctx, domain.NewID(), lifecycleFixtureText, ContinueSuccessfulRun); err != nil {
		t.Fatal(err)
	}
	for {
		observation, err := s.Next(ctx)
		if err != nil {
			t.Fatalf("native scripted completion failed: %v; safe diagnostics: %s", err, logs.String())
		}
		if observation.Kind == InputFinished {
			if observation.Result == nil || !observation.Result.Successful() {
				t.Fatal("native scripted result did not succeed")
			}
			break
		}
	}
}
