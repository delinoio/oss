package codex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type fixtureHistoryNotification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func (f *threadFixture) handleContinuation(id json.RawMessage, method string, raw json.RawMessage, write func(json.RawMessage, any)) bool {
	if method == "fixture/history" {
		var params struct {
			Page            json.RawMessage             `json:"page"`
			ChangeAfterRead bool                        `json:"changeAfterRead,omitempty"`
			Notify          *fixtureHistoryNotification `json:"notify,omitempty"`
		}
		if domain.Decode(raw, &params) != nil || f.thread == nil {
			os.Exit(60)
		}
		f.history = params.Page
		f.historyChangeAfterRead = params.ChangeAfterRead
		f.historyNotification = params.Notify
		write(id, map[string]any{})
		return true
	}
	if method != "thread/turns/list" {
		return false
	}
	var params struct {
		ThreadID  domain.ID `json:"threadId"`
		Limit     int       `json:"limit"`
		Direction string    `json:"sortDirection"`
		View      string    `json:"itemsView"`
	}
	if domain.Decode(raw, &params) != nil || f.thread == nil || params.ThreadID != f.thread["id"] || params.Limit != 1 || params.Direction != "desc" || params.View != "full" {
		os.Exit(61)
	}
	if file := os.Getenv("DELIDEV_CODEX_CAPTURE"); file != "" {
		out, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(62)
		}
		_ = json.NewEncoder(out).Encode(map[string]any{"method": method, "params": params})
		_ = out.Close()
	}
	if f.mode == "thread-continuation-unsupported" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": id, "error": map[string]any{"code": -32601, "message": "fixture-protected-history-error"}})
		return true
	}
	if f.mode == "thread-continuation-late" {
		time.Sleep(200 * time.Millisecond)
	}
	write(id, f.history)
	if n := f.historyNotification; n != nil {
		f.notify(n.Method, n.Params)
		f.historyNotification = nil
	}
	if f.historyChangeAfterRead {
		f.thread["modelProvider"] = "changed-after-history"
	}
	if f.mode == "thread-continuation-became-active" {
		f.thread["status"] = map[string]any{"type": "active", "activeFlags": []string{}}
	}
	return true
}

func continuationFixture(t *testing.T, mode string) (*Client, string, ContinuationCheckpoint, map[string]any) {
	t.Helper()
	c, capture := openThreadFixture(t, "thread-continuation-"+mode)
	bound, err := c.ResumeThread(context.Background(), domain.NewID(), domain.NewID(), threadSettings(t))
	if err != nil {
		t.Fatal(err)
	}
	p := ContinuationCheckpoint{ThreadID: bound.Thread.ID, SessionID: bound.Thread.SessionID, TurnID: domain.NewID(), Status: TurnCompleted, Mode: domain.ExecuteMode, Effective: *bound.Effective}
	items := []any{}
	for _, prompt := range []string{"Original input 한글 🐦", "Later same-turn steer"} {
		id := domain.NewID()
		p.Inputs = append(p.Inputs, HistoricalInput{ID: id, PromptDigest: sha256.Sum256([]byte(prompt))})
		items = append(items, map[string]any{"type": "userMessage", "id": string(domain.NewID()), "clientId": id, "content": []any{map[string]any{"type": "text", "text": prompt, "text_elements": []any{}}}})
	}
	items = append(items, map[string]any{"type": "agentMessage", "id": string(domain.NewID()), "text": "fixture-private-history"})
	turn := fixtureTurn(p.TurnID, p.Status)
	turn["items"], turn["itemsView"] = items, "full"
	page := map[string]any{"data": []any{turn}, "nextCursor": "opaque-older-turns", "backwardsCursor": "opaque-reverse-cursor"}
	fixtureSignal(t, c, "history", map[string]any{"page": page})
	return c, capture, p, page
}

func TestContinuationRequiresNativeHistoryBeforeAnotherSend(t *testing.T) {
	c, capture, checkpoint, _ := continuationFixture(t, "ready")
	ctx := context.Background()
	_, err := c.StartTurn(ctx, domain.NewID(), domain.NewID(), input(domain.PlanMode))
	assertCode(t, err, domain.RecoveryRequired)
	_, err = c.Steer(ctx, domain.NewID(), domain.NewID(), checkpoint.TurnID, input(domain.ExecuteMode))
	assertCode(t, err, domain.RecoveryRequired)
	if len(requestsOf(t, capture, "turn/start")) != 0 || len(requestsOf(t, capture, "turn/steer")) != 0 {
		t.Fatal("resumed idle metadata authorized native input")
	}
	observed, err := c.VerifyContinuation(ctx, domain.NewID(), checkpoint, ContinueAfterSuccess)
	if err != nil || observed.ID != checkpoint.TurnID || observed.Status != TurnCompleted {
		t.Fatalf("continuation verification failed: %v", err)
	}
	if len(requestsOf(t, capture, "thread/turns/list")) != 1 || len(requestsOf(t, capture, "thread/read")) != 2 {
		t.Fatal("continuation did not inspect exact recent history and metadata")
	}
	// Returned observations and caller checkpoints cannot rewrite retained state.
	observed.Status = TurnRunning
	checkpoint.Effective.Model = "changed-by-caller"
	checkpoint.Inputs[0].PromptDigest = sha256.Sum256([]byte("changed-by-caller"))
	for _, old := range checkpoint.Inputs {
		_, err = c.StartTurn(ctx, domain.NewID(), old.ID, input(domain.PlanMode))
		assertCode(t, err, domain.Conflict)
	}
	_, err = c.VerifyContinuation(ctx, domain.NewID(), checkpoint, ContinueAfterSuccess)
	assertCode(t, err, domain.Conflict)
	next, err := c.StartTurn(ctx, domain.NewID(), domain.NewID(), input(domain.PlanMode))
	if err != nil || next.TurnID == checkpoint.TurnID {
		t.Fatalf("fresh continuation was not sent: %v", err)
	}
	if len(requestsOf(t, capture, "turn/start")) != 1 || len(requestsOf(t, capture, "thread/turns/list")) != 1 {
		t.Fatal("continuation replayed native operations")
	}
	nextKind(t, c, TurnStartedEvent)
	message := nextKind(t, c, MessageCompletedEvent)
	if message.Message.ClientInputID != next.InputID || message.Late {
		t.Fatal("continuation published prior history as a fresh message")
	}
}

func TestContinuationInvalidHistoryNeverAuthorizesResend(t *testing.T) {
	tests := []struct {
		name   string
		change func(map[string]any, map[string]any)
	}{
		{"empty", func(page, _ map[string]any) { page["data"] = []any{} }},
		{"extra-turn", func(page, turn map[string]any) { page["data"] = []any{turn, turn} }},
		{"foreign-turn", func(_, turn map[string]any) { turn["id"] = domain.NewID() }},
		{"active", func(_, turn map[string]any) { turn["status"] = TurnRunning }},
		{"changed-outcome", func(_, turn map[string]any) { turn["status"] = TurnInterrupted }},
		{"summary", func(_, turn map[string]any) { turn["itemsView"] = "summary" }},
		{"unloaded", func(_, turn map[string]any) { turn["itemsView"] = "notLoaded" }},
		{"missing-view", func(_, turn map[string]any) { delete(turn, "itemsView") }},
		{"missing-inputs", func(_, turn map[string]any) { turn["items"] = []any{} }},
		{"duplicate-item", func(_, turn map[string]any) { items := turn["items"].([]any); turn["items"] = append(items, items[0]) }},
		{"unknown-field", func(page, _ map[string]any) { page["newMeaning"] = true }},
		{"oversized-cursor", func(page, _ map[string]any) { page["nextCursor"] = strings.Repeat("x", 4097) }},
		{"wrong-input", func(_, turn map[string]any) { turn["items"].([]any)[0].(map[string]any)["clientId"] = domain.NewID() }},
		{"missing-input-id", func(_, turn map[string]any) { delete(turn["items"].([]any)[0].(map[string]any), "clientId") }},
		{"reordered-inputs", func(_, turn map[string]any) { items := turn["items"].([]any); items[0], items[1] = items[1], items[0] }},
		{"changed-prompt", func(_, turn map[string]any) {
			turn["items"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"] = "changed"
		}},
		{"rich-prompt", func(_, turn map[string]any) {
			turn["items"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text_elements"] = []any{map[string]any{"unknown": true}}
		}},
		{"extra-content", func(_, turn map[string]any) {
			item := turn["items"].([]any)[0].(map[string]any)
			item["content"] = append(item["content"].([]any), map[string]any{"type": "text", "text": "extra"})
		}},
		{"non-text", func(_, turn map[string]any) {
			turn["items"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["type"] = "image"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c, capture, checkpoint, page := continuationFixture(t, "ready")
			test.change(page, page["data"].([]any)[0].(map[string]any))
			fixtureSignal(t, c, "history", map[string]any{"page": page})
			_, err := c.VerifyContinuation(context.Background(), domain.NewID(), checkpoint, ContinueAfterSuccess)
			assertCode(t, err, domain.RecoveryRequired)
			_, err = c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
			assertCode(t, err, domain.RecoveryRequired)
			_, err = c.VerifyContinuation(context.Background(), domain.NewID(), checkpoint, ContinueAfterSuccess)
			assertCode(t, err, domain.RecoveryRequired)
			if len(requestsOf(t, capture, "turn/start")) != 0 || len(requestsOf(t, capture, "thread/turns/list")) != 1 {
				t.Fatal("mismatched history authorized another attempt")
			}
		})
	}
}

func TestContinuationTerminalFailureNeedsExplicitResume(t *testing.T) {
	for _, status := range []TurnStatus{TurnInterrupted, TurnFailed} {
		t.Run(string(status), func(t *testing.T) {
			c, capture, checkpoint, page := continuationFixture(t, "ready")
			checkpoint.Status = status
			turn := page["data"].([]any)[0].(map[string]any)
			turn["status"] = status
			if status == TurnFailed {
				turn["error"] = map[string]any{"message": "fixture-protected-history-error"}
			}
			fixtureSignal(t, c, "history", map[string]any{"page": page})
			_, err := c.VerifyContinuation(context.Background(), domain.NewID(), checkpoint, ContinueAfterSuccess)
			assertCode(t, err, domain.Conflict)
			if len(requestsOf(t, capture, "thread/turns/list")) != 0 {
				t.Fatal("failure continued automatically")
			}
			result, err := c.VerifyContinuation(context.Background(), domain.NewID(), checkpoint, ResumeAfterTerminal)
			if err != nil || result.Status != status {
				t.Fatalf("explicit resume failed: %v", err)
			}
			raw, err := json.Marshal(result)
			if err != nil || strings.Contains(string(raw), "fixture-protected") {
				t.Fatal("history diagnostic was exposed")
			}
			if _, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestContinuationRejectsChangedOriginalAuthority(t *testing.T) {
	tests := []struct {
		name   string
		change func(*ContinuationCheckpoint)
	}{
		{"thread", func(p *ContinuationCheckpoint) { p.ThreadID = domain.NewID() }},
		{"session", func(p *ContinuationCheckpoint) { p.SessionID = domain.NewID() }},
		{"model", func(p *ContinuationCheckpoint) { p.Effective.Model = "other" }},
		{"provider", func(p *ContinuationCheckpoint) { p.Effective.Provider = "other" }},
		{"effort-default", func(p *ContinuationCheckpoint) { p.Effective.Effort = nil }},
		{"tier-default", func(p *ContinuationCheckpoint) { p.Effective.ServiceTier = nil }},
		{"cwd", func(p *ContinuationCheckpoint) { p.Effective.Cwd = "/other" }},
		{"permission", func(p *ContinuationCheckpoint) { p.Effective.Sandbox.Type = FullAccess }},
		{"network", func(p *ContinuationCheckpoint) { p.Effective.Sandbox.NetworkAccess = true }},
		{"tmp", func(p *ContinuationCheckpoint) { p.Effective.Sandbox.ExcludeSlashTmp = true }},
		{"tmp-env", func(p *ContinuationCheckpoint) { p.Effective.Sandbox.ExcludeTmpdirEnvVar = true }},
		{"writable-root", func(p *ContinuationCheckpoint) { p.Effective.Sandbox.WritableRoots = []string{"/other"} }},
		{"reviewer", func(p *ContinuationCheckpoint) { p.Effective.ApprovalsReviewer = "auto_review" }},
		{"approval", func(p *ContinuationCheckpoint) { p.Effective.ApprovalPolicy = ApprovalNever }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c, capture, checkpoint, _ := continuationFixture(t, "ready")
			test.change(&checkpoint)
			_, err := c.VerifyContinuation(context.Background(), domain.NewID(), checkpoint, ResumeAfterTerminal)
			assertCode(t, err, domain.RecoveryRequired)
			if len(requestsOf(t, capture, "thread/turns/list")) != 0 || len(requestsOf(t, capture, "turn/start")) != 0 {
				t.Fatal("changed authority reached native work")
			}
		})
	}
}

func TestContinuationCancellationAndConcurrentVerification(t *testing.T) {
	c, capture, checkpoint, _ := continuationFixture(t, "ready")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.VerifyContinuation(ctx, domain.NewID(), checkpoint, ContinueAfterSuccess)
	if err == nil || len(requestsOf(t, capture, "thread/turns/list")) != 0 {
		t.Fatal("canceled verification consumed native work")
	}
	results := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := c.VerifyContinuation(context.Background(), domain.NewID(), checkpoint, ContinueAfterSuccess)
			results <- err
		}()
	}
	group.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if domain.SafeError(err).Code == domain.Conflict {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 || len(requestsOf(t, capture, "thread/turns/list")) != 1 {
		t.Fatal("concurrent verification was not serialized")
	}
}

func TestContinuationPreservesPriorRecoveryAndPause(t *testing.T) {
	for _, state := range []string{"recovery", "pause"} {
		t.Run(state, func(t *testing.T) {
			c, capture, checkpoint, _ := continuationFixture(t, "ready")
			if state == "recovery" {
				fixtureSignal(t, c, "notify", map[string]any{"method": "thread/settings/updated", "params": map[string]any{"threadId": checkpoint.ThreadID}})
				for c.problem == nil {
					_, err := c.NextEvent(context.Background())
					if err != nil {
						assertCode(t, err, domain.RecoveryRequired)
					}
				}
			} else {
				fixtureSignal(t, c, "notify", map[string]any{"method": "thread/status/changed", "params": map[string]any{"threadId": checkpoint.ThreadID, "status": map[string]any{"type": "notLoaded"}}})
				nextKind(t, c, ThreadStatusEvent)
			}
			_, err := c.VerifyContinuation(context.Background(), domain.NewID(), checkpoint, ResumeAfterTerminal)
			if err == nil {
				t.Fatal("continuation cleared another recovery or pause")
			}
			_, err = c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
			assertCode(t, err, domain.RecoveryRequired)
			if len(requestsOf(t, capture, "thread/turns/list")) != 0 {
				t.Fatal("unresolved state reached native continuation")
			}
		})
	}
}

func TestContinuationInvalidCheckpointDoesNotConsumeNativeRequest(t *testing.T) {
	c, capture, checkpoint, _ := continuationFixture(t, "ready")
	request := domain.NewID()
	for _, change := range []func(*ContinuationCheckpoint){
		func(p *ContinuationCheckpoint) { p.ThreadID = "bad" },
		func(p *ContinuationCheckpoint) { p.TurnID = "bad" },
		func(p *ContinuationCheckpoint) { p.SessionID = "bad" },
		func(p *ContinuationCheckpoint) { p.Status = TurnRunning },
		func(p *ContinuationCheckpoint) { p.Mode = "unknown" },
		func(p *ContinuationCheckpoint) { p.Inputs = nil },
		func(p *ContinuationCheckpoint) { p.Inputs = make([]HistoricalInput, maxTrackedTurns) },
		func(p *ContinuationCheckpoint) { p.Inputs = []HistoricalInput{{ID: "bad"}} },
		func(p *ContinuationCheckpoint) { p.Inputs = []HistoricalInput{p.Inputs[0], p.Inputs[0]} },
	} {
		invalid := checkpoint
		change(&invalid)
		_, err := c.VerifyContinuation(context.Background(), request, invalid, ContinueAfterSuccess)
		assertCode(t, err, domain.InvalidArgument)
	}
	if len(requestsOf(t, capture, "thread/turns/list")) != 0 {
		t.Fatal("invalid checkpoint reached native state")
	}
	if _, err := c.VerifyContinuation(context.Background(), request, checkpoint, ContinueAfterSuccess); err != nil {
		t.Fatal(err)
	}
}

func TestContinuationUnavailableAndChangedStateKeepInputBlocked(t *testing.T) {
	for _, mode := range []string{"unsupported", "became-active", "late"} {
		t.Run(mode, func(t *testing.T) {
			c, capture, checkpoint, _ := continuationFixture(t, mode)
			ctx := context.Background()
			if mode == "late" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 60*time.Millisecond)
				defer cancel()
			}
			_, err := c.VerifyContinuation(ctx, domain.NewID(), checkpoint, ContinueAfterSuccess)
			if err == nil {
				t.Fatal("unavailable or changed native history was accepted")
			}
			if mode == "unsupported" {
				assertCode(t, err, domain.Unsupported)
			}
			if mode == "became-active" {
				assertCode(t, err, domain.Conflict)
			}
			_, err = c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
			assertCode(t, err, domain.RecoveryRequired)
			if len(requestsOf(t, capture, "turn/start")) != 0 || len(requestsOf(t, capture, "thread/turns/list")) != 1 {
				t.Fatal("history failure caused automatic native replay")
			}
		})
	}
}
