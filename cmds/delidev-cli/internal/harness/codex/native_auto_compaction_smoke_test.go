// SPDX-License-Identifier: Apache-2.0
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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestManualNativeAutomaticCompactionRetainsCompleteContext(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit pinned native automatic compaction with private scripted provider")
	}
	var attempts atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil || r.Method != "POST" || r.URL.Path != "/responses" || !strings.Contains(string(body), "fixture-model") {
			t.Error("native automatic compaction changed provider/model")
		}
		tokens := int64(1)
		if n == 1 {
			tokens = 2000
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []any{
			map[string]any{"type": "response.created", "response": map[string]any{"id": fmt.Sprintf("resp_auto_%d", n), "status": "in_progress"}},
			map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message", "id": fmt.Sprintf("msg_auto_%d", n), "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Private automatic compaction response."}}}},
			map[string]any{"type": "response.completed", "response": map[string]any{"id": fmt.Sprintf("resp_auto_%d", n), "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": tokens, "output_tokens": 1, "total_tokens": tokens + 1}}},
		} {
			raw, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		}
	}))
	defer provider.Close()
	cfg := nativeFixtureConfig(t, binary, provider.URL)
	configPath := filepath.Join(cfg.Home, "config.toml")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath, append([]byte("model_context_window = 12000\nmodel_auto_compact_token_limit = 1000\n"), raw...), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	settings := ThreadSettings{Model: "fixture-model", Provider: "delidev_fixture", Effort: "high", Cwd: cfg.Process.Cwd, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}}
	client, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	bound, err := client.StartThread(ctx, domain.NewID(), settings)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	var source ContinuationCheckpoint
	for n := 0; n < 2; n++ {
		if n != 0 {
			if err = VerifyContinuationContextRollout(ctx, cfg.Home, source); err != nil {
				t.Fatal(err)
			}
			cfg.Process.OwnerID = domain.NewID()
			client, err = Open(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			bound, err = client.ResumeThread(ctx, domain.NewID(), source.ThreadID, settings)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = client.VerifyContinuation(ctx, domain.NewID(), source, ContinueAfterSuccess); err != nil {
				t.Fatal(err)
			}
		}
		prompt := fmt.Sprintf("Private automatic context input %d", n)
		input := domain.NewID()
		turn, err := client.StartTurn(ctx, domain.NewID(), input, domain.SessionInput{Mode: domain.ExecuteMode, Prompt: prompt})
		if err != nil {
			t.Fatal(err)
		}
		for {
			event, err := client.NextEvent(ctx)
			if err != nil {
				t.Fatal("original automatic lifecycle", err)
			}
			if event.Kind == CompactionEvent {
				if !event.Correlated || event.TurnID != turn.TurnID || event.Compaction == nil || event.Compaction.Trigger != AutomaticCompaction || event.Compaction.ActionID != "" {
					t.Fatal("automatic context borrowed manual/input authority")
				}
				if event.Compaction.Stage == CompactionCompleted {
					count++
				}
			}
			if event.Kind == TurnCompletedEvent && event.TurnID == turn.TurnID && !event.Late {
				if event.Turn.Status != TurnCompleted {
					t.Fatal("native automatic compaction failed")
				}
				break
			}
		}
		source = ContinuationCheckpoint{ThreadID: bound.Thread.ID, SessionID: bound.Thread.SessionID, TurnID: turn.TurnID, Status: TurnCompleted, Mode: domain.ExecuteMode, Effective: *bound.Effective, Inputs: []HistoricalInput{{ID: input, PromptDigest: sha256.Sum256([]byte(prompt))}}}
		source.Context, err = client.RetainContinuationContext(ctx, source)
		if err != nil {
			t.Fatal("complete automatic context retention", err)
		}
		if err = client.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if count == 0 || source.Context == nil || len(source.Context.Records) != count || attempts.Load() != int64(2+count) {
		t.Fatal("automatic context was emulated, lost or counted as another input", count, attempts.Load())
	}
	if err = VerifyContinuationContextRollout(ctx, cfg.Home, source); err != nil {
		t.Fatal(err)
	}
	cfg.Process.OwnerID = domain.NewID()
	client, err = Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err = client.ResumeThread(ctx, domain.NewID(), source.ThreadID, settings); err != nil {
		t.Fatal(err)
	}
	if _, err = client.VerifyContinuation(ctx, domain.NewID(), source, ContinueAfterSuccess); err != nil {
		t.Fatal("fresh complete automatic context verification", err)
	}
	t.Log("Pinned automatic compaction retained original input ownership, independent live/durable context IDs and complete history/rollout lineage across fresh native processes; scripted loopback provider only")
}
