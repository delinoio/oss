// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"crypto/sha256"
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
)

func TestManualNativeForkSmoke(t *testing.T) {
	manualNativeForkSmoke(t, false)
}

func TestManualNativeSidechatForkSmoke(t *testing.T) {
	manualNativeForkSmoke(t, true)
}

func manualNativeForkSmoke(t *testing.T, sidechat bool) {
	t.Helper()
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit pinned native fork with temporary state and scripted loopback provider")
	}
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil || r.Method != "POST" || r.URL.Path != "/responses" {
			t.Error("unexpected provider request")
		}
		if n == 2 && (!strings.Contains(string(raw), "Original fork input") || !strings.Contains(string(raw), "Fork fixture complete") || !strings.Contains(string(raw), "Independent child input")) {
			t.Error("child lost original native context")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		id := fmt.Sprintf("resp_fork_%d", n)
		for _, event := range []any{
			map[string]any{"type": "response.created", "response": map[string]any{"id": id, "status": "in_progress"}},
			map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message", "id": fmt.Sprintf("msg_fork_%d", n), "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Fork fixture complete"}}}},
			map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}},
		} {
			bytes, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\n", bytes)
		}
	}))
	defer provider.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cfg := nativeFixtureConfig(t, binary, provider.URL)
	settings := ThreadSettings{Model: "fixture-model", Provider: "delidev_fixture", Effort: "high", Cwd: filepath.Join(filepath.Dir(cfg.Home), "workspace"), Options: domain.AgentOptions{Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}}
	if sidechat {
		settings.Options.Permission = domain.PermissionWorkspaceWrite
	}
	client, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	bound, err := client.StartThread(ctx, domain.NewID(), settings)
	if err != nil {
		t.Fatal(err)
	}
	input := domain.NewID()
	turn, err := client.StartTurn(ctx, domain.NewID(), input, domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Original fork input"})
	if err != nil {
		t.Fatal(err)
	}
	finishNativeForkTurn(t, ctx, client)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	cfg.Process.OwnerID = domain.NewID()
	reader, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := ContinuationCheckpoint{ThreadID: bound.Thread.ID, SessionID: bound.Thread.SessionID, TurnID: turn.TurnID, Status: TurnCompleted, Mode: domain.ExecuteMode, Effective: *bound.Effective, Inputs: []HistoricalInput{{ID: input, PromptDigest: sha256.Sum256([]byte("Original fork input"))}}}
	source, inspectErr := reader.InspectForkSource(ctx, checkpoint)
	closeErr := reader.Close()
	if inspectErr != nil || closeErr != nil {
		t.Fatalf("source inspection: %v; cleanup: %v", inspectErr, closeErr)
	}
	childCfg := nativeFixtureConfig(t, binary, provider.URL)
	if sidechat {
		childCfg.Process.Logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
		childCfg.Sidechat = ReadOnlySidechatV1
		childCfg.Process.Cwd = settings.Cwd
		settings.Options.Permission, settings.Options.ApprovalPolicy = domain.PermissionReadOnly, string(ApprovalNever)
	}
	child, err := Open(ctx, childCfg)
	if err != nil {
		problem := domain.SafeError(err)
		t.Fatalf("child handshake: %s; %s", problem.Code, problem.Guidance)
	}
	defer child.Close()
	if !sidechat {
		settings.Cwd = filepath.Join(filepath.Dir(childCfg.Home), "workspace")
	}
	fork, err := child.ForkThread(ctx, domain.NewID(), source, settings)
	if err != nil {
		problem := domain.SafeError(err)
		t.Fatalf("native fork: %s; %s", problem.Code, problem.Guidance)
	}
	if fork.Thread.ID == bound.Thread.ID || requests.Load() != 1 {
		t.Fatal("fork changed source identity or requested inference")
	}
	if sidechat && (!nativePathEqual(fork.Effective.Cwd, bound.Effective.Cwd) || !sidechatEffective(*fork.Effective)) {
		t.Fatal("Sidechat gained workspace ownership or lost read-only authority")
	}
	checkpoint.ThreadID, checkpoint.SessionID, checkpoint.Effective = fork.Thread.ID, fork.Thread.SessionID, *fork.Effective
	if _, err := child.VerifyContinuation(ctx, domain.NewID(), checkpoint, ContinueAfterSuccess); err != nil {
		t.Fatal("child boundary", err)
	}
	if _, err := child.StartTurn(ctx, domain.NewID(), domain.NewID(), domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Independent child input"}); err != nil {
		t.Fatal(err)
	}
	finishNativeForkTurn(t, ctx, child)
	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
	if source.Verify(ctx) != nil || requests.Load() != 2 {
		t.Fatal("source bytes changed or extra input executed")
	}
	t.Log("Pinned installed native fork retained exact terminal history, used independent cwd/runtime, continued once and preserved original rollout bytes; loopback fixtures only")
}

func finishNativeForkTurn(t *testing.T, ctx context.Context, client *Client) {
	t.Helper()
	for {
		event, err := client.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == TurnCompletedEvent {
			if event.Turn == nil || event.Turn.Status != TurnCompleted || !event.Correlated {
				t.Fatal("unconfirmed terminal")
			}
			return
		}
	}
}
