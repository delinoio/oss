package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// This fixture does not override managed policy. Production must independently
// validate effective settings/providers before it can expose this transport.
func requireNoManagedOpenCodeConfig(t *testing.T) {
	t.Helper()
	var paths []string
	switch runtime.GOOS {
	case "darwin":
		current, err := user.Current()
		if err != nil {
			t.Fatal("cannot inspect native managed configuration scope")
		}
		paths = []string{"/Library/Application Support/opencode", "/Library/Managed Preferences/ai.opencode.managed.plist", filepath.Join("/Library/Managed Preferences", current.Username, "ai.opencode.managed.plist")}
	case "windows":
		root := os.Getenv("ProgramData")
		if root == "" {
			root = `C:\ProgramData`
		}
		paths = []string{filepath.Join(root, "opencode")}
	default:
		paths = []string{"/etc/opencode"}
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.IsDir() {
			t.Fatal("native managed configuration is present or cannot be inspected")
		}
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 0 {
			t.Fatal("native managed configuration is present or cannot be inspected")
		}
	}
}

func nativeSessionFixture(t *testing.T, providerURL, key string) (*sessionAPI, context.Context) {
	t.Helper()
	executable := os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE")
	if executable == "" {
		t.Skip("explicit private native OpenCode session fixture")
	}
	if !filepath.IsAbs(executable) {
		t.Fatal("select an absolute native executable")
	}
	requireNoManagedOpenCodeConfig(t)
	config, logs := fixtureConfig(t, "native-session")
	env, err := probeEnvironment(config)
	if err != nil {
		t.Fatal(err)
	}
	root := config.Process.Cwd
	cwd := filepath.Join(root, "workspace")
	if err := security.PrivateDir(cwd); err != nil {
		t.Fatal(err)
	}
	settings := fixtureSettings()
	nativeConfig, _ := json.Marshal(map[string]any{
		"autoupdate": false, "share": "disabled", "model": settings.Provider + "/" + settings.Model, "small_model": settings.Provider + "/" + settings.Model,
		"experimental": map[string]any{"continue_loop_on_deny": false},
		"provider": map[string]any{settings.Provider: map[string]any{
			"npm": "@ai-sdk/openai-compatible", "name": "Private fixture", "options": map[string]any{"baseURL": providerURL + "/v1", "apiKey": key},
			"models": map[string]any{settings.Model: map[string]any{"name": "Private fixture", "limit": map[string]any{"context": 32000, "output": 1000}}},
		}},
	})
	for i, value := range env {
		if strings.HasPrefix(value, "OPENCODE_CONFIG_CONTENT=") {
			env[i] = "OPENCODE_CONFIG_CONTENT=" + string(nativeConfig)
		}
	}
	for _, key := range []string{"OPENCODE_DISABLE_DEFAULT_PLUGINS", "OPENCODE_DISABLE_CLAUDE_CODE", "OPENCODE_DISABLE_EXTERNAL_SKILLS", "OPENCODE_DISABLE_LSP_DOWNLOAD", "OPENCODE_EXPERIMENTAL_DISABLE_FILEWATCHER", "OPENCODE_PURE"} {
		env = append(env, key+"=true")
	}
	// Native instance initialization attempts its own plugin dependency
	// installation. Keep this opt-in fixture offline with a fresh npm cache;
	// never synthesize an installed package or alter the user's global cache.
	env = append(env, "npm_config_offline=true", "npm_config_cache="+filepath.Join(root, "cache", "npm"))
	password := string(domain.NewID()) + string(domain.NewID())
	env = append(env, "OPENCODE_SERVER_USERNAME=delidev", "OPENCODE_SERVER_PASSWORD="+password)
	reservation, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reservation.Close() })
	port := strconv.Itoa(reservation.Addr().(*net.TCPAddr).Port)
	address := net.JoinHostPort("127.0.0.1", port)
	origin := "http://" + address
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	t.Cleanup(cancel)
	output := &startupOutput{expected: "opencode server listening on " + origin, ready: make(chan struct{}), cancel: cancel}
	config.Process.Executable, config.Process.Env = executable, env
	config.Process.Args = []string{"serve", "--hostname=127.0.0.1", "--port=" + port, "--mdns=false"}
	config.Process.Stdout, config.Process.Stderr = output, &diagnosticOutput{output: output}
	handle, err := process.Start(ctx, config.Process)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		if err := handle.Close(); err != nil {
			t.Errorf("native cleanup: %v", err)
		}
		_ = handle.Wait()
		if err := process.ReconcileOwner(config.Process.Directory, config.Process.OwnerID); err != nil {
			t.Errorf("native ownership: %v", err)
		}
		if output.status() != nil || len(output.buffer) != 0 {
			t.Error("unhandled native process output")
		}
		if t.Failed() {
			// Only structured owner/phase/error-code facts are captured here;
			// native stdout, stderr and HTTP bodies remain private.
			t.Logf("owned native phase diagnostics: %s", logs.String())
		}
	})
	if err := reservation.Close(); err != nil {
		t.Fatal(err)
	}
	if err := handle.Resume(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-output.ready:
	case <-ctx.Done():
		t.Fatal("owned native server startup timed out")
	case <-handle.Done():
		t.Fatal("owned native server exited before startup")
	}
	client, transport := probeHTTPClient(address)
	t.Cleanup(transport.CloseIdleConnections)
	for _, credential := range []string{"", "incorrect-" + password} {
		if _, err := readHTTP(ctx, client, origin, "/global/health", credential, 401); err != nil {
			t.Fatal(err)
		}
	}
	health, err := readHTTP(ctx, client, origin, "/global/health", password, 200)
	if err != nil || validateHealth(health) != nil {
		t.Fatal("native profile health mismatch")
	}
	api := &sessionAPI{client: client, origin: origin, password: password, cwd: cwd, gate: make(chan struct{}, 1), owner: config.Process.OwnerID, logger: config.Process.Logger, rejectionPolicy: StopOnInteractionRejection}
	api.alive = func() error {
		select {
		case <-handle.Done():
			return unavailable()
		default:
			if problem := output.status(); problem != nil {
				return problem
			}
			return nil
		}
	}
	var claims []SessionClaim
	api.claim = func(_ context.Context, claim SessionClaim) error {
		for _, existing := range claims {
			if existing.RequestID == claim.RequestID {
				return sessionConflict()
			}
		}
		claims = append(claims, claim)
		raw, _ := json.Marshal(claims)
		return security.WriteAtomic(filepath.Join(filepath.Dir(root), "native-session-claims.json"), raw)
	}
	return api, ctx
}

type lostInputResponse struct{ http.RoundTripper }

func (l lostInputResponse) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := l.RoundTripper.RoundTrip(request)
	if err == nil && request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/prompt_async") {
		_ = response.Body.Close()
		return nil, errors.New("private lost native response fixture")
	}
	return response, err
}

func TestManualNativeOpenCodeSessionInput(t *testing.T) {
	if os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE") == "" {
		t.Skip("explicit private native OpenCode session fixture")
	}
	for _, lose := range []bool{false, true} {
		t.Run(fmt.Sprintf("lost-response=%t", lose), func(t *testing.T) {
			key := string(domain.NewID())
			var mu sync.Mutex
			calls := 0
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				raw, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPBody+1))
				if err != nil || domain.Decode(raw, &body) != nil || r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+key || !scalar(body["model"], fixtureSettings().Model) || string(body["stream"]) != "true" {
					t.Error("native provider request escaped its explicit private profile")
					w.WriteHeader(400)
					return
				}
				mu.Lock()
				calls++
				mu.Unlock()
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, `data: {"id":"chatcmpl-private","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Private fixture response"},"finish_reason":null}]}`+"\n\n")
				_, _ = io.WriteString(w, `data: {"id":"chatcmpl-private","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":4,"total_tokens":24}}`+"\n\ndata: [DONE]\n\n")
			}))
			t.Cleanup(provider.Close)
			t.Cleanup(unblock)
			api, ctx := nativeSessionFixture(t, provider.URL, key)
			id, err := api.create(ctx, domain.NewID(), fixtureSettings())
			if err != nil || !nativeID(id, "ses") {
				t.Fatalf("native create: %v", err)
			}
			if lose {
				api.client.Transport = lostInputResponse{api.client.Transport}
			}
			receipt, err := api.submit(ctx, domain.NewID(), fixtureMessageID, fixturePartID, "Reply with the private fixture response.")
			if lose != (err != nil) || receipt.HTTPAccepted == lose || receipt.Recorded {
				t.Fatalf("native scheduling receipt: %+v, %v", receipt, err)
			}
			var observed InputReceipt
			for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
				observed, err = api.inspectInput(ctx)
				if err != nil || observed.Recorded {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if err != nil || !observed.Recorded || observed.HTTPAccepted == lose || observed.MessageID != fixtureMessageID || observed.SessionID != id {
				t.Fatalf("native original input storage: %+v, %v", observed, err)
			}
			// The actual provider is still blocked. Native input persistence and
			// HTTP scheduling cannot possibly establish assistant completion here.
			if _, err := api.submit(ctx, domain.NewID(), fixtureMessageID, fixturePartID, "Must not repeat"); err == nil {
				t.Fatal("stored input authorized another native send")
			}
			unblock()
			for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
				mu.Lock()
				count := calls
				mu.Unlock()
				if count > 0 {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			mu.Lock()
			count := calls
			mu.Unlock()
			if count != 1 {
				t.Fatalf("native provider call count: %d", count)
			}
		})
	}
}
