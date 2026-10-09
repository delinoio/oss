// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func (f *threadFixture) handleRevert(id json.RawMessage, method string, raw json.RawMessage, write func(json.RawMessage, any)) bool {
	if !strings.HasPrefix(f.mode, "thread-revert-") {
		return false
	}
	capture := func() {
		out, e := os.OpenFile(os.Getenv("DELIDEV_CODEX_CAPTURE"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			os.Exit(70)
		}
		json.NewEncoder(out).Encode(map[string]any{"method": method, "params": raw})
		out.Close()
	}
	switch method {
	case "thread/goal/get":
		write(id, map[string]any{"goal": nil})
		return true
	case "thread/queue/list":
		write(id, map[string]any{"data": []any{}, "nextCursor": nil})
		return true
	case "thread/list":
		write(id, map[string]any{"data": []any{}, "nextCursor": nil})
		return true
	case "thread/turns/list":
		var p struct {
			Direction string    `json:"sortDirection"`
			Limit     int       `json:"limit"`
			Cursor    *string   `json:"cursor"`
			Thread    domain.ID `json:"threadId"`
			View      string    `json:"itemsView"`
		}
		if domain.Decode(raw, &p) != nil || p.Thread != f.thread["id"] || p.View != "full" {
			os.Exit(71)
		}
		var page struct {
			Data []json.RawMessage `json:"data"`
		}
		if json.Unmarshal(f.history, &page) != nil {
			os.Exit(72)
		}
		capture()
		if p.Direction == "desc" {
			slices.Reverse(page.Data)
		}
		if p.Limit == 1 && len(page.Data) > 1 {
			page.Data = page.Data[:1]
		}
		// One turn per page exercises the response anchor and continuation cursors.
		offset := 0
		if p.Cursor != nil && strings.HasPrefix(*p.Cursor, "page-") {
			offset, _ = strconv.Atoi(strings.TrimPrefix(*p.Cursor, "page-"))
		}
		if p.Direction == "desc" && p.Cursor != nil && *p.Cursor == "revert-anchor" {
			offset = 0
		}
		if offset > len(page.Data) {
			os.Exit(73)
		}
		data := page.Data[offset:]
		var next any
		if p.Limit == 50 && len(data) > 1 {
			data = data[:1]
			next = "page-" + strconv.Itoa(offset+1)
		}
		if f.mode == "thread-revert-cycle" && p.Limit == 50 {
			next = "page-" + strconv.Itoa(offset+1)
		}
		write(id, map[string]any{"data": data, "nextCursor": next, "backwardsCursor": nil})
		return true
	case "thread/revert":
		capture()
		var p struct {
			Thread domain.ID `json:"threadId"`
			Before domain.ID `json:"beforeTurnId"`
		}
		if domain.Decode(raw, &p) != nil || p.Thread != f.thread["id"] {
			os.Exit(74)
		}
		var page struct {
			Data []json.RawMessage `json:"data"`
		}
		json.Unmarshal(f.history, &page)
		index := -1
		for n, v := range page.Data {
			var turn turnWire
			json.Unmarshal(v, &turn)
			if turn.ID == p.Before {
				index = n
			}
		}
		if index < 0 {
			os.Exit(75)
		}
		page.Data = page.Data[:index]
		f.history, _ = json.Marshal(page)
		if f.mode == "thread-revert-lost" {
			json.NewEncoder(os.Stdout).Encode(map[string]any{"id": id, "error": map[string]any{"code": -32603, "message": "lost acknowledgment"}})
			return true
		}
		var cursor any
		if index > 0 {
			cursor = "revert-anchor"
		}
		write(id, map[string]any{"thread": f.thread, "turnsBackwardsCursor": cursor, "itemsBackwardsCursor": nil})
		return true
	}
	return false
}
func revertFixture(t *testing.T, mode string) (*Client, string, ContinuationCheckpoint, []domain.ID, []HistoricalInput) {
	c, capture := openThreadFixture(t, "thread-revert-"+mode)
	bound, err := c.ResumeThread(context.Background(), domain.NewID(), domain.NewID(), threadSettings(t))
	if err != nil {
		t.Fatal(err)
	}
	ids, inputs := []domain.ID{}, []HistoricalInput{}
	turns := []any{}
	for _, text := range []string{"earlier prompt", "middle prompt", "latest prompt"} {
		id, input := domain.NewID(), HistoricalInput{ID: domain.NewID(), PromptDigest: sha256.Sum256([]byte(text))}
		ids = append(ids, id)
		inputs = append(inputs, input)
		turn := fixtureTurn(id, TurnCompleted)
		turn["itemsView"] = "full"
		turn["items"] = []any{map[string]any{"type": "userMessage", "id": string(domain.NewID()), "clientId": input.ID, "content": []any{map[string]any{"type": "text", "text": text, "text_elements": []any{}}}}}
		turns = append(turns, turn)
	}
	fixtureSignal(t, c, "history", map[string]any{"page": map[string]any{"data": turns, "nextCursor": nil, "backwardsCursor": nil}})
	source := ContinuationCheckpoint{ThreadID: bound.Thread.ID, SessionID: bound.Thread.SessionID, TurnID: ids[2], Status: TurnCompleted, Mode: domain.ExecuteMode, Effective: *bound.Effective, Inputs: []HistoricalInput{inputs[2]}}
	path := filepath.Join(c.home, "sessions", "fixture.jsonl")
	security.PrivateDir(filepath.Dir(path))
	os.WriteFile(path, []byte("retained native history"), 0600)
	fixtureSignal(t, c, "metadata", map[string]any{"path": path})
	if _, err := c.VerifyContinuation(context.Background(), domain.NewID(), source, ContinueAfterSuccess); err != nil {
		t.Fatal(err)
	}
	return c, capture, source, ids, inputs
}
func TestRevertExactEarlierTurnEmptyHistoryAndOnceOnlyMutation(t *testing.T) {
	for _, index := range []int{0, 1, 2} {
		t.Run(string(rune('0'+index)), func(t *testing.T) {
			c, capture, source, ids, inputs := revertFixture(t, "ready")
			claims := 0
			var intent RevertIntent
			p, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[index], ids[index], func(v RevertIntent) error { claims++; intent = v; return nil })
			if err != nil || p.Revert == nil || len(p.Revert.RetainedTurnIDs) != index || p.TurnsCount != uint32(index) || claims != 1 || len(intent.ExpectedHistory) != index {
				t.Fatal("exact prefix not retained", err)
			}
			if c.Version() != "0.162.0" || len(requestsOf(t, capture, "thread/revert")) != 1 {
				t.Fatal("wrong pinned profile or mutation count")
			}
			if _, err := c.RevertThread(context.Background(), intent.ActionID, source, inputs[index], ids[index], func(RevertIntent) error { claims++; return nil }); err == nil || claims != 1 {
				t.Fatal("second mutation claimed")
			}
		})
	}
}
func TestRevertLostAcknowledgmentOnlyReconcilesOriginalPrefix(t *testing.T) {
	c, capture, source, ids, inputs := revertFixture(t, "lost")
	var intent RevertIntent
	if _, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[1], ids[1], func(v RevertIntent) error { intent = v; return nil }); err == nil {
		t.Fatal("lost response released input")
	}
	// Observation uses the retained exact intent, never another mutation. A new
	// native process normally resumes this same home; fixture clears only its
	// per-process failure to exercise that closed observation independently.
	c.problem = nil
	c.execution.paused = false
	proof, err := c.ReconcileRevert(context.Background(), intent)
	if err != nil || proof.TurnsCount != 1 || len(requestsOf(t, capture, "thread/revert")) != 1 {
		t.Fatal("original reconciliation resent or changed target", err)
	}
	changed := intent
	changed.ExpectedHistory = []json.RawMessage{}
	if _, err := c.ReconcileRevert(context.Background(), changed); err == nil {
		t.Fatal("changed retained prefix accepted")
	}
}
func TestRevertRefusesStalePromptActiveTurnAndPaginationCycle(t *testing.T) {
	for _, mode := range []string{"stale", "active", "cycle"} {
		t.Run(mode, func(t *testing.T) {
			fixtureMode := "ready"
			if mode == "cycle" {
				fixtureMode = mode
			}
			c, capture, source, ids, inputs := revertFixture(t, fixtureMode)
			if mode == "stale" {
				inputs[0].PromptDigest = sha256.Sum256([]byte("changed"))
			}
			if mode == "active" {
				c.execution.active = ids[2]
			}
			claims := 0
			_, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[0], ids[0], func(RevertIntent) error { claims++; return nil })
			if err == nil || claims != 0 || len(requestsOf(t, capture, "thread/revert")) != 0 {
				t.Fatal("unproved target sent")
			}
		})
	}
}

func TestRevertProfileRejectsUnprovedActualNativeVersion(t *testing.T) {
	config := fixtureConfig(t, "thread-revert-before-0")
	config.Mode, config.RevertHistory = ThreadProtocol, true
	config.Process.Env = append(config.Process.Env, "DELIDEV_CODEX_VERSION_FIXTURE=0.161.0")
	client, err := Open(context.Background(), config)
	if client != nil {
		client.Close()
		t.Fatal("adopted an unproved Revert profile")
	}
	if err == nil {
		t.Fatal("missing actual native profile refusal")
	}
}
