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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestManualNativeRepeatedCompactionAndRestoration(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("private pinned native protocol probe")
	}
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		body, e := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if e != nil || r.Method != "POST" || r.URL.Path != "/responses" || !strings.Contains(string(body), "fixture-model") {
			t.Error("native compaction changed the original provider/model")
		}
		if strings.Contains(string(body), "Successor private input") && (!strings.Contains(string(body), "Private compaction fixture summary.") || strings.Count(string(body), "Successor private input") != 1) {
			t.Error("fresh native continuation lost compacted context or repeated input")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range []any{
			map[string]any{"type": "response.created", "response": map[string]any{"id": fmt.Sprintf("resp_compact_%d", n), "status": "in_progress"}},
			map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message", "id": fmt.Sprintf("msg_compact_%d", n), "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Private compaction fixture summary."}}}},
			map[string]any{"type": "response.completed", "response": map[string]any{"id": fmt.Sprintf("resp_compact_%d", n), "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}},
		} {
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
	}))
	defer provider.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg := nativeFixtureConfig(t, binary, provider.URL)
	c, e := Open(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	settings := ThreadSettings{Model: "fixture-model", Provider: "delidev_fixture", Effort: "high", Cwd: cfg.Process.Cwd, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}}
	bound, e := c.StartThread(ctx, domain.NewID(), settings)
	if e != nil {
		t.Fatal(e)
	}
	input := domain.NewID()
	turn, e := c.StartTurn(ctx, domain.NewID(), input, domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Original private compaction input"})
	if e != nil {
		t.Fatal(e)
	}
	finishNativeForkTurn(t, ctx, c)

	source := ContinuationCheckpoint{ThreadID: bound.Thread.ID, SessionID: bound.Thread.SessionID, TurnID: turn.TurnID, Status: TurnCompleted, Mode: domain.ExecuteMode, Effective: *bound.Effective, Inputs: []HistoricalInput{{ID: input, PromptDigest: sha256.Sum256([]byte("Original private compaction input"))}}}
	// Pinned thread/resume has no experimentalRawEvents field. Resumed
	// compaction retains unavailable live response usage rather than inventing
	// counts from overlapping thread/tokenUsage snapshots.
	var previous *CompactedCheckpoint
	for n := 0; n < 2; n++ {
		if n != 0 {
			if e := VerifyCompactionRollout(ctx, cfg.Home, *previous); e != nil {
				t.Fatal("private native rollout changed before restoration", e)
			}
			cfg.Process.OwnerID = domain.NewID()
			c, e = Open(ctx, cfg)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			if _, e = c.ResumeThread(ctx, domain.NewID(), source.ThreadID, settings); e != nil {
				t.Fatal(e)
			}
			if _, e = c.VerifyCompactedContinuation(ctx, domain.NewID(), *previous); e != nil {
				t.Fatal("fresh compacted history verification", e)
			}
		}
		action := domain.NewID()
		if e = c.StartCompaction(ctx, action, source, previous); e != nil {
			t.Fatal("original native manual compaction", e)
		}
		if e = c.StartCompaction(ctx, action, source, previous); e == nil {
			t.Fatal("manual compaction was sent twice")
		}
		if _, e = c.RetainCompactedCheckpoint(ctx); e == nil {
			t.Fatal("acknowledgment granted an early checkpoint")
		}
		stages := []CompactionStage{}
		usage := 0
		for {
			ev, err := c.NextEvent(ctx)
			if err != nil {
				t.Fatal("compaction lifecycle", err)
			}
			if ev.Kind == CompactionEvent {
				if ev.Compaction == nil || ev.Compaction.ActionID != action || ev.Compaction.Trigger != ManualCompaction || !ev.Correlated {
					t.Fatal("native compaction lost original action ownership")
				}
				stages = append(stages, ev.Compaction.Stage)
			}
			if ev.Kind == ResponseUsageEvent && ev.TurnID == c.execution.compaction.turnID && !ev.Late {
				usage++
			}
			if ev.Kind == TurnCompletedEvent && ev.TurnID == c.execution.compaction.turnID && !ev.Late {
				if ev.Turn.Status != TurnCompleted {
					t.Fatal("native compaction failed")
				}
				break
			}
		}
		if len(stages) != 2 || stages[0] != CompactionStarted || stages[1] != CompactionCompleted || usage > 1 || n == 0 && usage != 1 || n > 0 && usage != 0 {
			t.Fatal("incomplete original compaction lifecycle", "iteration", n, "stages", stages, "live_usage_count", usage)
		}
		retained, err := c.RetainCompactedCheckpoint(ctx)
		if err != nil {
			a := c.execution.compaction
			turns, readErr := c.compactionTurnsLocked(ctx)
			t.Log("retention closed state", "ack", a.acknowledged, "terminal", a.terminal, "item", a.itemID != "", "active", c.execution.active != "", "paused", c.execution.paused, "before", len(a.before), "after", len(turns), "history_read_ok", readErr == nil)
			if len(turns) > len(a.before) {
				var tw turnWire
				de := domain.Decode(turns[len(turns)-1], &tw)
				for _, raw := range tw.Items {
					var item map[string]json.RawMessage
					_ = json.Unmarshal(raw, &item)
					var typ, id string
					_ = json.Unmarshal(item["type"], &typ)
					_ = json.Unmarshal(item["id"], &id)
					keys := []string{}
					for key := range item {
						keys = append(keys, key)
					}
					t.Log("history compaction identity", "decode_ok", de == nil, "turn_matches", tw.ID == a.turnID, "status", tw.Status, "view", tw.ItemsView, "items", len(tw.Items), "type", typ, "item_matches", id == a.itemID, "keys", keys)
				}
				t.Log("history prefix unchanged", historyDigest(turns[:len(a.before)]) == historyDigest(a.before), "last_native_turn_ok", verifyCompactionTurn(turns[len(turns)-1], CompactionRecord{ActionID: a.actionID, TurnID: a.turnID, ItemID: a.itemID}) == nil)
			}
			t.Fatal("complete native checkpoint", err)
		}
		if len(retained.Records) != n+1 || retained.Source.TurnID != source.TurnID || retained.Source.Inputs[0] != source.Inputs[0] {
			t.Fatal("repeated native compaction lost its original history lineage")
		}
		if e = c.Close(); e != nil {
			t.Fatal(e)
		}
		previous = &retained
	}
	if e = VerifyCompactionRollout(ctx, cfg.Home, *previous); e != nil {
		t.Fatal("private native rollout changed during cleanup", e)
	}
	cfg.Process.OwnerID = domain.NewID()
	c, e = Open(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if _, e = c.ResumeThread(ctx, domain.NewID(), source.ThreadID, settings); e != nil {
		t.Fatal(e)
	}
	if _, e = c.VerifyCompactedContinuation(ctx, domain.NewID(), *previous); e != nil {
		t.Fatal("final fresh native checkpoint verification", e)
	}
	attemptsBeforeSuccessor := requests.Load()
	if _, e = c.StartTurn(ctx, domain.NewID(), domain.NewID(), domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Successor private input"}); e != nil {
		t.Fatal(e)
	}
	finishNativeForkTurn(t, ctx, c)
	if requests.Load() != attemptsBeforeSuccessor+1 || requests.Load() < 3 || requests.Load() > 4 {
		t.Fatal("native compaction repeated a side effect or inferred during preparation")
	}
	t.Log("Original native provider attempts", requests.Load())
	t.Log("Pinned native conversation, two once-only manual compactions, complete original input/history lineage and fresh-process continuation passed against a scripted loopback provider; no real account acceptance")
}
