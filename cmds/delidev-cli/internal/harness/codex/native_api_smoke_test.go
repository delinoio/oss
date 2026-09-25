package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
)

func TestManualNativeExecutionProxyConfiguration(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit installed harness with a private scripted loopback proxy only")
	}
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != apiproxy.Prefix+"/responses" || r.Header.Get("Authorization") != "Bearer "+apiFixtureToken() {
			t.Error("native API request escaped its configured execution authority")
			http.Error(w, "unsupported", http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil || !strings.Contains(string(body), "fixture-model") || strings.Contains(string(body), apiFixtureToken()) {
			t.Error("native request changed model or serialized the execution token")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []any{
			map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_proxy_fixture", "status": "in_progress"}},
			map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_proxy_fixture", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Proxy fixture complete."}}}},
			map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_proxy_fixture", "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}},
		} {
			raw, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		}
	}))
	defer provider.Close()
	cfg := nativeFixtureConfig(t, binary, "http://127.0.0.1:1")
	cfg.API = &APIConfig{ServerOrigin: provider.URL, Token: apiFixtureToken()}
	// A competing user config cannot win over the explicit execution binding.
	// No credential is written to the runtime or used by this local fixture.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	settings := ThreadSettings{Model: "fixture-model", Provider: APIProvider, Effort: "high", Cwd: cfg.Process.Cwd, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}}
	if _, err := client.StartThread(ctx, domain.NewID(), settings); err != nil {
		t.Fatal(err)
	}
	if _, err := client.StartTurn(ctx, domain.NewID(), domain.NewID(), domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Return the scripted local fixture response only."}); err != nil {
		t.Fatal(err)
	}
	extensions := map[string]bool{}
	for {
		event, err := client.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Native != nil && !extensions[event.Native.Method] {
			extensions[event.Native.Method] = true
			t.Logf("private native extension method in scripted fixture: %s", event.Native.Method)
		}
		if event.Kind != TurnCompletedEvent {
			continue
		}
		if event.Turn == nil || event.Turn.Status != TurnCompleted || !event.Correlated {
			t.Fatal("native execution did not complete its scoped fixture turn")
		}
		break
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatal("native execution retried or changed the selected endpoint")
	}
	if err := filepath.WalkDir(filepath.Dir(cfg.Home), func(path string, entry os.DirEntry, walkErr error) error {
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
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(contents), apiFixtureToken()) {
			t.Error("native runtime persisted its execution credential")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Log("installed Codex sent one authenticated private execution request to the fixed loopback proxy route; no user account or external provider")
}
