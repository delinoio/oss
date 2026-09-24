package codex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestManualNativeThreadSmoke(t *testing.T) {
	// Explicitly opt in with an installed binary. Ordinary tests never launch
	// user harnesses. All state is private temporary data and the only selected
	// model authority is this loopback scripted provider, without credentials.
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native thread smoke with a local scripted provider only")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("the native fixture executable must be absolute")
	}
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestIndex := requests.Add(1)
		if r.Method != "POST" || r.URL.Path != "/responses" {
			t.Error("unexpected fixture operation")
			http.Error(w, "unsupported", 404)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil || !strings.Contains(string(body), "Temporary non-inference thread validation.") {
			t.Error("applied instructions were absent from fixture request")
		}
		var request struct {
			Model        string
			Reasoning    struct{ Effort string }
			Instructions string
		}
		if json.Unmarshal(body, &request) != nil || request.Model != "fixture-model" || request.Reasoning.Effort != "high" || request.Instructions == "" || request.Instructions == "Temporary non-inference thread validation." {
			t.Error("native model, effort or base instructions changed")
		}
		if strings.Count(string(body), "Return the local fixture response only.") != 1 || (requestIndex == 2 && (strings.Count(string(body), "Continue the same private fixture conversation.") != 1 || !strings.Contains(string(body), "Fixture complete."))) || requestIndex > 2 {
			t.Error("native continuation lost history or replayed an input")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		responseID := fmt.Sprintf("resp_fixture_%d", requestIndex)
		events := []any{
			map[string]any{"type": "response.created", "response": map[string]any{"id": responseID, "status": "in_progress"}},
			map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message", "id": fmt.Sprintf("msg_fixture_%d", requestIndex), "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Fixture complete."}}}},
			map[string]any{"type": "response.completed", "response": map[string]any{"id": responseID, "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}},
		}
		for _, event := range events {
			raw, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\n", raw)
		}
	}))
	defer provider.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg := nativeFixtureConfig(t, binary, provider.URL)
	root := filepath.Dir(cfg.Home)
	client, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	first := client
	t.Cleanup(func() {
		if err := first.Close(); err != nil {
			t.Error(err)
		}
	})
	settings := ThreadSettings{Model: "fixture-model", Provider: "delidev_fixture", Effort: "high", Cwd: filepath.Join(root, "workspace"), Instructions: "Temporary non-inference thread validation.", Options: domain.AgentOptions{Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}}
	result, err := client.StartThread(ctx, domain.NewID(), settings)
	if err != nil {
		_ = client.Close()
		t.Fatal(err)
	}
	if result.Thread == nil {
		t.Fatal("missing thread")
	}
	if result.Thread.History != LegacyHistory {
		t.Fatal("requested native history mode changed")
	}
	observed, err := client.ReadThread(ctx, domain.NewID(), result.Thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if observed.Status.Type != ThreadIdle {
		t.Fatalf("unexpected native status: %v", observed.Status)
	}
	inputID := domain.NewID()
	firstTurn, err := client.StartTurn(ctx, domain.NewID(), inputID, domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Return the local fixture response only."})
	if err != nil {
		t.Fatal(err)
	}
	for {
		event, err := client.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind != TurnCompletedEvent {
			continue
		}
		if event.Turn == nil || event.Turn.Status != TurnCompleted || !event.Correlated {
			t.Fatal("fixture turn did not complete")
		}
		break
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	cfg.Process.OwnerID = domain.NewID()
	client, err = Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	second := client
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Error(err)
		}
	})
	resumed, err := client.ResumeThread(ctx, domain.NewID(), result.Thread.ID, settings)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Thread == nil || resumed.Thread.ID != result.Thread.ID {
		t.Fatal("native resume identity changed")
	}
	checkpoint := ContinuationCheckpoint{ThreadID: result.Thread.ID, SessionID: result.Thread.SessionID, TurnID: firstTurn.TurnID, Status: TurnCompleted, Mode: domain.ExecuteMode, Effective: *result.Effective, Inputs: []HistoricalInput{{ID: inputID, PromptDigest: sha256.Sum256([]byte("Return the local fixture response only."))}}}
	nextInput := domain.SessionInput{Mode: domain.PlanMode, Prompt: "Continue the same private fixture conversation."}
	_, err = client.StartTurn(ctx, domain.NewID(), domain.NewID(), nextInput)
	assertCode(t, err, domain.RecoveryRequired)
	verified, err := client.VerifyContinuation(ctx, domain.NewID(), checkpoint, ContinueAfterSuccess)
	if err != nil || verified.ID != firstTurn.TurnID || verified.Status != TurnCompleted {
		t.Fatalf("native most recent turn verification failed: %v", err)
	}
	_, err = client.StartTurn(ctx, domain.NewID(), inputID, nextInput)
	assertCode(t, err, domain.Conflict)
	secondTurn, err := client.StartTurn(ctx, domain.NewID(), domain.NewID(), nextInput)
	if err != nil {
		t.Fatal(err)
	}
	for {
		event, err := client.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind != TurnCompletedEvent {
			continue
		}
		if event.Turn == nil || event.Turn.ID != secondTurn.TurnID || event.Turn.Status != TurnCompleted || !event.Correlated || event.Late {
			t.Fatal("continued native turn did not complete independently")
		}
		break
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("unexpected local fixture request count %d", requests.Load())
	}
	t.Logf("%s/%s Codex %s: resumed exact native thread, verified retained terminal input and continued once in Plan mode; two local scripted model responses; no external provider or user account", runtime.GOOS, runtime.GOARCH, SupportedVersion)
}

func nativeFixtureConfig(t *testing.T, binary, providerURL string) Config {
	t.Helper()
	if !filepath.IsAbs(binary) {
		t.Fatal("the native fixture executable must be absolute")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"home", "codex", "config", "cache", "data", "state", "tmp", "workspace"} {
		if err := security.PrivateDir(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	home := filepath.Join(root, "codex")
	configText := fmt.Sprintf("model_provider = \"delidev_fixture\"\nmodel = \"fixture-model\"\n[model_providers.delidev_fixture]\nname = \"DeliDev fixture\"\nbase_url = %q\nwire_api = \"responses\"\nrequires_openai_auth = false\nrequest_max_retries = 0\nstream_max_retries = 0\n", providerURL)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + filepath.Join(root, "home"), "USERPROFILE=" + filepath.Join(root, "home"), "CODEX_HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(root, "config"), "XDG_CACHE_HOME=" + filepath.Join(root, "cache"), "XDG_DATA_HOME=" + filepath.Join(root, "data"), "XDG_STATE_HOME=" + filepath.Join(root, "state"), "TMPDIR=" + filepath.Join(root, "tmp"), "TMP=" + filepath.Join(root, "tmp"), "TEMP=" + filepath.Join(root, "tmp"), "APPDATA=" + filepath.Join(root, "config"), "LOCALAPPDATA=" + filepath.Join(root, "data")}
	path := filepath.Dir(binary) + string(os.PathListSeparator)
	if runtime.GOOS == "windows" {
		systemRoot := os.Getenv("SystemRoot")
		if !filepath.IsAbs(systemRoot) {
			t.Fatal("native Windows system context is unavailable")
		}
		env = append(env, "SystemRoot="+systemRoot)
		path += filepath.Join(systemRoot, "System32")
	} else {
		path += "/usr/bin:/bin"
	}
	env = append(env, "PATH="+path)
	cfg := Config{Mode: ThreadProtocol, Version: SupportedVersion, Home: home, Process: process.Config{Directory: filepath.Join(root, "processes"), OwnerID: domain.NewID(), Executable: binary, Cwd: filepath.Join(root, "workspace"), Env: env}}
	return cfg
}
