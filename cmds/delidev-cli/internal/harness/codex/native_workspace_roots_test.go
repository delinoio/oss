package codex

import (
	"bytes"
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
)

func TestManualNativeMultipleWorkspaceRoots(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit installed Codex with private workspace roots only")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("native executable must be absolute")
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("thread binding performed inference")
		http.Error(w, "unexpected inference", http.StatusForbidden)
	}))
	defer provider.Close()
	for _, permission := range []domain.PermissionMode{domain.PermissionDefault, domain.PermissionReadOnly, domain.PermissionWorkspaceWrite, domain.PermissionFullAccess} {
		t.Run(string(permission), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cfg := nativeFixtureConfig(t, binary, provider.URL)
			primary := filepath.Join(filepath.Dir(cfg.Home), "workspace")
			secondary := filepath.Join(filepath.Dir(cfg.Home), "second repository")
			if err := os.Mkdir(secondary, 0o700); err != nil {
				t.Fatal(err)
			}
			settings := ThreadSettings{Model: "fixture-model", Provider: "delidev_fixture", Cwd: primary, WorkspaceRoots: []string{secondary, primary}, Options: domain.AgentOptions{Permission: permission, ApprovalPolicy: "on-request"}}
			client, err := Open(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := client.Close(); err != nil {
					t.Error(err)
				}
			}()
			result, err := client.StartThread(ctx, domain.NewID(), settings)
			if err != nil {
				t.Fatal("native workspace root binding failed", err)
			}
			if result.Effective == nil || !matchWorkspaceRoots(settings, result.Effective.WorkspaceRoots) {
				t.Fatal("native did not retain both exact workspace roots")
			}
			if result.Effective.Sandbox.Type == WorkspaceWrite && !matchWritableRoots(settings, result.Effective.Sandbox.WritableRoots) {
				t.Fatal("native writable authority differs from prepared repositories")
			}
			if !nativePathEqual(result.Effective.Cwd, primary) {
				t.Fatal("native primary directory changed")
			}
		})
	}
}

func TestManualNativeWritesMultipleWorkspaceRoots(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit installed Codex and private scripted tool execution only")
	}
	if runtime.GOOS == "windows" {
		t.Skip("this installed-harness fixture uses a POSIX shell builtin")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	roots := []string{filepath.Join(root, "secondary repository"), filepath.Join(root, "primary repository")}
	commands := make([]string, len(roots))
	for i, path := range roots {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		target := "'" + strings.ReplaceAll(filepath.Join(path, "native.txt"), "'", "'\"'\"'") + "'"
		commands[i] = "printf 'native-multiple-root' > " + target
	}
	command := strings.Join(commands, " && ")
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		if err != nil || r.Method != "POST" || r.URL.Path != "/responses" || call > 2 {
			t.Error("unexpected native tool request")
			http.Error(w, "invalid", 400)
			return
		}
		if call == 2 && !bytes.Contains(body, []byte("call_multiroot")) {
			t.Error("native tool result was not retained")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(value any) {
			raw, _ := json.Marshal(value)
			fmt.Fprintf(w, "data: %s\n\n", raw)
			w.(http.Flusher).Flush()
		}
		responseID := fmt.Sprintf("resp_multiroot_%d", call)
		send(map[string]any{"type": "response.created", "response": map[string]any{"id": responseID, "status": "in_progress"}})
		var item map[string]any
		if call == 1 {
			arguments, _ := json.Marshal(map[string]any{"cmd": command, "login": false, "yield_time_ms": 1000, "max_output_tokens": 1000})
			item = map[string]any{"type": "function_call", "id": "fc_multiroot", "call_id": "call_multiroot", "name": "exec_command", "arguments": string(arguments)}
		} else {
			item = map[string]any{"type": "message", "id": "msg_multiroot", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Both native roots written."}}}
		}
		send(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
		send(map[string]any{"type": "response.completed", "response": map[string]any{"id": responseID, "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}})
	}))
	defer provider.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg := nativeFixtureConfig(t, binary, provider.URL)
	client, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	}()
	settings := ThreadSettings{Model: "fixture-model", Provider: "delidev_fixture", Cwd: roots[1], WorkspaceRoots: roots, Options: domain.AgentOptions{Permission: domain.PermissionWorkspaceWrite, ApprovalPolicy: "on-request"}}
	if _, err := client.StartThread(ctx, domain.NewID(), settings); err != nil {
		t.Fatal(err)
	}
	if _, err := client.StartTurn(ctx, domain.NewID(), domain.NewID(), domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Write only the scripted native fixture files in both selected roots."}); err != nil {
		t.Fatal(err)
	}
	sawTool := false
	for {
		event, err := client.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == InteractionRequestedEvent {
			t.Fatal("prepared writable roots unexpectedly required broader approval")
		}
		if event.Kind == ToolCompletedEvent {
			if event.Tool == nil || event.Tool.Status != ToolCompleted || event.Tool.Command == nil || event.Tool.Command.ExitCode == nil || *event.Tool.Command.ExitCode != 0 {
				t.Fatal("native multi-root command failed")
			}
			sawTool = true
		}
		if event.Kind == TurnCompletedEvent {
			if event.Turn.Status != TurnCompleted || !sawTool || calls.Load() != 2 {
				t.Fatal("native multi-root turn did not complete exactly once")
			}
			break
		}
	}
	for _, path := range roots {
		raw, err := os.ReadFile(filepath.Join(path, "native.txt"))
		if err != nil || string(raw) != "native-multiple-root" {
			t.Fatal("native command did not write selected repository", err)
		}
	}
}
