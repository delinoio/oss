package opencode

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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func fixtureOwnedAPIConfig(t *testing.T) apiSessionConfig {
	t.Helper()
	probe, _ := fixtureConfig(t, "native-api")
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return apiSessionConfig{
		Probe: probe, Workspace: workspace, NativeRoot: filepath.VolumeName(workspace) + string(filepath.Separator),
		ServerOrigin: "http://127.0.0.1:1", Token: apiproxy.TokenPrefix + base64.RawURLEncoding.EncodeToString(make([]byte, 32)),
		Settings: fixtureSettings(), ContextLimit: 32000, OutputLimit: 1000, Rejection: StopOnInteractionRejection,
		Claim: func(context.Context, SessionClaim) error {
			t.Error("initialization cannot claim native work")
			return sessionConflict()
		},
	}
}

func TestManualNativeOpenCodeOwnedAPIInitialization(t *testing.T) {
	nativeOwnedAPISession(t, false, false)
}

func TestManualNativeOpenCodeOwnedAPIInput(t *testing.T) {
	nativeOwnedAPISession(t, true, false)
}

func TestManualNativeOpenCodeOwnedAPIContextMismatch(t *testing.T) {
	nativeOwnedAPISession(t, false, true)
}

func nativeOwnedAPISession(t *testing.T, input, mismatch bool) {
	t.Helper()
	executable := os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE")
	if executable == "" {
		t.Skip("explicit isolated native OpenCode owned API initializer")
	}
	requireNoManagedOpenCodeConfig(t)
	config := fixtureOwnedAPIConfig(t)
	var logs bytes.Buffer
	config.Probe.Process.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	defer func() {
		if t.Failed() {
			t.Logf("owned native phase diagnostics: %s", logs.String())
		}
	}()
	config.Probe.Process.Executable = executable
	var requests atomic.Int32
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var body map[string]json.RawMessage
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPBody+1))
		if !input || err != nil || domain.Decode(raw, &body) != nil || r.Method != http.MethodPost || r.URL.Path != apiproxy.Prefix+"/chat/completions" || r.Header.Get("Authorization") != "Bearer "+config.Token || !scalar(body["model"], config.Settings.Model) || string(body["stream"]) != "true" {
			t.Error("owned native request escaped the fixed scripted relay scope")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-private","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Private owned fixture response"},"finish_reason":null}]}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-private","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":4,"total_tokens":24}}`+"\n\ndata: [DONE]\n\n")
	}))
	defer relay.Close()
	config.ServerOrigin = relay.URL
	var claims []SessionClaim
	config.Claim = func(_ context.Context, claim SessionClaim) error {
		claims = append(claims, claim)
		raw, _ := json.Marshal(claims)
		return security.WriteAtomic(filepath.Join(filepath.Dir(config.Probe.Home), "claims.json"), raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if mismatch {
		config.NativeRoot = filepath.Dir(config.Workspace)
	}
	api, err := openAPISession(ctx, config)
	if mismatch {
		if err == nil || api != nil || domain.SafeError(err).Code != domain.Unsupported || len(claims) != 0 || requests.Load() != 0 {
			t.Fatal("foreign native project root gained execution authority")
		}
		if !strings.Contains(logs.String(), `"phase":"/path"`) {
			t.Fatal("mismatch test did not reach native path validation")
		}
		if err := process.ReconcileOwner(config.Probe.Process.Directory, config.Probe.Process.OwnerID); err != nil {
			t.Fatal("failed owned initialization did not complete native cleanup")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := api.closeOwned(cleanup); err != nil {
			t.Error(err)
		}
		if err := process.ReconcileOwner(config.Probe.Process.Directory, config.Probe.Process.OwnerID); err != nil {
			t.Error(err)
		}
	}()
	if !api.apiVerified || api.runtimeRoot != config.NativeRoot || api.runtimeRead || len(claims) != 0 || api.alive() != nil {
		t.Fatal("owned native initialization did not preserve its exact live context")
	}
	if _, err := api.create(ctx, domain.NewID(), config.Settings); err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].Kind != CreateSessionMutation || requests.Load() != 0 {
		t.Fatal("owned initialization or creation performed unrequested inference")
	}
	if !strings.HasSuffix(api.apiProfile.BaseURL, apiproxy.Prefix) {
		t.Fatal("owned API origin did not use the fixed relay path")
	}
	if !input {
		return
	}
	stream, err := api.openEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := api.submit(ctx, domain.NewID(), fixtureMessageID, fixturePartID, "Reply using the private owned fixture response."); err != nil {
		t.Fatal(err)
	}
	if _, err := api.observeInput(ctx, config.Workspace); err == nil {
		t.Fatal("unverified project root replaced initializer evidence")
	}
	observer, err := api.observeInput(ctx, config.NativeRoot)
	if err != nil {
		t.Fatal(err)
	}
	for count := 0; count < 256 && !observer.snapshot().SettledObserved; count++ {
		event, err := stream.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := observer.observe(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	progress := observer.snapshot()
	if !progress.SettledObserved || !progress.UserSeen || !progress.InputPartSeen || requests.Load() != 1 || len(claims) != 2 {
		t.Fatal("owned native input did not settle with exact original claims")
	}
}
