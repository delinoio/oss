package server

import (
	"context"
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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

func TestManualNativeCodexUsesRegisteredServerRelay(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native harness with simulated execution readiness and a scripted loopback provider only")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("the selected native executable must be absolute")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/responses" || r.Header.Get("Authorization") != "Bearer temporary-upstream-fixture-key" || r.Header.Get("HTTP-Referer") != "https://deli.dev" {
			t.Error("registered native relay changed upstream authority")
			http.Error(w, "unsupported", http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var request struct{ Model string }
		if err != nil || json.Unmarshal(body, &request) != nil || request.Model != "fixture-model" || !strings.Contains(string(body), "Fixture prompt") {
			t.Error("registered native relay changed accepted model or input")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []any{
			map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_registered_fixture", "status": "in_progress"}},
			map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_registered_fixture", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Registered relay fixture complete."}}}},
			map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_registered_fixture", "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}},
		} {
			raw, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		}
	}))
	defer upstream.Close()
	f := publicationFixtureFromAuthority(t, newAuthorityFixture(t, upstream.URL))
	f.registerGrant(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"home", "codex", "config", "cache", "data", "state", "tmp", "workspace"} {
		if err := security.PrivateDir(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	publicationConfig := publicationWorkerConfig(t, f)
	publicationConfig.Root = filepath.Join(root, "worker")
	publisher, err := worker.OpenExecutionPublisher(publicationConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	nativeEvents := worker.NewCodexEventPublisher(publisher)
	env := []string{"HOME=" + filepath.Join(root, "home"), "USERPROFILE=" + filepath.Join(root, "home"), "CODEX_HOME=" + filepath.Join(root, "codex"), "XDG_CONFIG_HOME=" + filepath.Join(root, "config"), "XDG_CACHE_HOME=" + filepath.Join(root, "cache"), "XDG_DATA_HOME=" + filepath.Join(root, "data"), "XDG_STATE_HOME=" + filepath.Join(root, "state"), "APPDATA=" + filepath.Join(root, "config"), "LOCALAPPDATA=" + filepath.Join(root, "data"), "TMPDIR=" + filepath.Join(root, "tmp"), "TMP=" + filepath.Join(root, "tmp"), "TEMP=" + filepath.Join(root, "tmp")}
	lookup := filepath.Dir(binary)
	if runtime.GOOS == "windows" {
		system := os.Getenv("SystemRoot")
		if !filepath.IsAbs(system) {
			t.Fatal("native Windows system context is unavailable")
		}
		env = append(env, "SystemRoot="+system)
		lookup += string(os.PathListSeparator) + filepath.Join(system, "System32")
	} else {
		lookup += ":/usr/bin:/bin"
	}
	env = append(env, "PATH="+lookup)
	client, err := codex.Open(ctx, codex.Config{Mode: codex.ThreadProtocol, Version: f.input.Installation.Version, Home: filepath.Join(root, "codex"), API: &codex.APIConfig{ServerOrigin: f.http.URL, Token: f.token}, Process: process.Config{Directory: filepath.Join(root, "processes"), OwnerID: f.job, Executable: binary, Cwd: filepath.Join(root, "workspace"), Env: env}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	configuration := f.input.Configuration
	bound, err := client.StartThread(ctx, f.input.ThreadRequestID, codex.ThreadSettings{Model: configuration.NativeModel, Provider: codex.APIProvider, Effort: configuration.Effort, Cwd: filepath.Join(root, "workspace"), Instructions: configuration.Instructions, Options: configuration.Options})
	if err != nil {
		t.Fatal(err)
	}
	if err := nativeEvents.BindThread(ctx, bound); err != nil {
		t.Fatal(err)
	}
	turn, err := client.StartTurn(ctx, f.input.TurnRequestID, f.input.InputID, f.input.Input)
	if err != nil {
		t.Fatal(err)
	}
	if err := nativeEvents.AcceptInput(ctx, turn); err != nil {
		t.Fatal(err)
	}
	sawInput, sawOutput := false, false
	for {
		event, err := client.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		// This acceptance case verifies core message/terminal publication only.
		// Private extensions remain explicitly outside its integration evidence.
		if _, err := nativeEvents.PublishCore(ctx, event); err != nil {
			t.Fatal(err)
		}
		if event.Message != nil {
			if event.Message.Role == codex.UserRole && event.Message.ClientInputID == f.input.InputID && event.Message.Text == f.input.Input.Prompt {
				sawInput = true
			}
			if event.Message.Role == codex.AssistantRole && event.Message.Text == "Registered relay fixture complete." {
				sawOutput = true
			}
		}
		if event.Kind != codex.TurnCompletedEvent {
			continue
		}
		if event.Turn == nil || event.Turn.Status != codex.TurnCompleted || event.TurnID != turn.TurnID || !event.Correlated {
			t.Fatal("registered native execution lost its terminal identity")
		}
		break
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || !sawInput || !sawOutput {
		t.Fatal("registered execution did not retain its exact input/output with one provider request")
	}
	transcript, err := f.service.Store.List(ctx, store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(transcript) != 2 {
		t.Fatalf("native core transcript did not reach server storage: %v", err)
	}
	for _, record := range transcript {
		message, err := store.Decode[domain.ExecutionMessage](record)
		if err != nil || message.State != domain.MessageComplete || message.NativeThreadID != string(bound.Thread.ID) || message.NativeTurnID != string(turn.TurnID) {
			t.Fatal("stored native message lost its binding or completion")
		}
	}
	retained, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Decode[domain.Session](retained)
	if err != nil || session.Outcome != domain.ExecutionSucceeded || session.PendingInputs != 0 || session.Execution == nil || session.Execution.Observed.Model != configuration.NativeModel {
		t.Fatal("native core events did not update session acceptance and outcome")
	}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, secret := range []string{f.token, "temporary-upstream-fixture-key"} {
			if strings.Contains(string(body), secret) {
				t.Error("native runtime retained an execution credential or upstream key")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Log("installed Codex -> registered server relay -> scripted local provider -> Worker durable core-event outbox -> server transcript: exact input/model/native identities, server-only key and owned closure; dispatch readiness simulated, rich events unimplemented")
}
