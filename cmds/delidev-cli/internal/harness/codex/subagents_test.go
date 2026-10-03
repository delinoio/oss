// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openSubagentFixture(t *testing.T, mode string) (*Client, string) {
	t.Helper()
	config := fixtureConfig(t, mode)
	config.Mode = ThreadProtocol
	capture := filepath.Join(t.TempDir(), "requests.jsonl")
	config.Process.Env = append(config.Process.Env, "DELIDEV_CODEX_CAPTURE="+capture)
	// This fixture performs several independent history round trips under the
	// race detector. Its lifetime must include all reads and explicit cleanup.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	client, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	return client, capture
}

func (f *threadFixture) handleSubagentRead(id json.RawMessage, method string, raw json.RawMessage, write func(json.RawMessage, any)) bool {
	if f.mode != "thread-subagent-read" && f.mode != "thread-subagent-live" || method != "thread/list" && method != "thread/turns/list" {
		return false
	}
	var params map[string]any
	if json.Unmarshal(raw, &params) != nil || f.thread == nil {
		os.Exit(71)
	}
	if capture := os.Getenv("DELIDEV_CODEX_CAPTURE"); capture != "" {
		out, err := os.OpenFile(capture, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(72)
		}
		_ = json.NewEncoder(out).Encode(map[string]any{"method": method, "params": params})
		_ = out.Close()
	}
	if method == "thread/list" {
		if params["ancestorThreadId"] != string(f.thread["id"].(domain.ID)) || params["useStateDbOnly"] != true {
			os.Exit(73)
		}
		if f.subagents == nil {
			a, b := maps.Clone(f.thread), maps.Clone(f.thread)
			a["id"], b["id"] = domain.NewID(), domain.NewID()
			a["parentThreadId"] = f.thread["id"]
			b["parentThreadId"] = a["id"]
			f.subagents = []map[string]any{b, a}
		}
		write(id, map[string]any{"data": f.subagents, "nextCursor": nil, "backwardsCursor": nil})
		return true
	}
	found := false
	for _, child := range f.subagents {
		if params["threadId"] == string(child["id"].(domain.ID)) {
			found = true
		}
	}
	if !found || params["limit"] != float64(1) || params["itemsView"] != "full" {
		os.Exit(74)
	}
	turn := fixtureTurn(domain.NewID(), TurnCompleted)
	if f.mode == "thread-subagent-live" {
		turn = fixtureTurn(domain.NewID(), TurnRunning)
	}
	turn["items"] = []any{map[string]any{"type": "agentMessage", "id": "native-child-message", "text": "Stored native child output", "phase": "final_answer"}}
	write(id, map[string]any{"data": []any{turn}, "nextCursor": nil, "backwardsCursor": nil})
	return true
}

func TestSubagentDescendantInspectionReadsHistoryWithoutChildControls(t *testing.T) {
	c, capture := openSubagentFixture(t, "thread-subagent-read")
	if _, err := c.StartThread(context.Background(), domain.NewID(), threadSettings(t)); err != nil {
		t.Fatal(err)
	}
	turn := domain.NewID()
	c.execution.active = turn
	c.execution.turns[turn] = trackedTurn{Turn: Turn{ID: turn, Status: TurnRunning}}
	e, err := c.InspectDescendants(context.Background())
	if err != nil || e.Kind != SubagentEvent || len(e.Subagents) != 2 {
		t.Fatal("descendant history observation failed", err)
	}
	for _, child := range e.Subagents {
		if child.Output == nil || child.Output.Text != "Stored native child output" || child.Status != domain.SubagentCompleted || child.ObservedModel != nil {
			t.Fatal("history coverage or missing model changed")
		}
	}
	childFromRead := e.Subagents[0]
	parentFromRead := domain.ID(childFromRead.ParentID)
	notice, err := observeFixture(c, "thread/started", map[string]any{"thread": threadWire{ID: domain.ID(childFromRead.NativeID), ParentThreadID: &parentFromRead, SessionID: c.execution.thread.SessionID, CLIVersion: SupportedVersion, ModelProvider: c.execution.settings.Provider, Status: ThreadStatus{Type: ThreadIdle}}})
	if err != nil || notice.Kind != SubagentEvent || notice.Subagents[0].ID != childFromRead.ID {
		t.Fatal("start notification lost inventory-proved ownership", err)
	}
	activity := Event{Kind: SubagentActivityEvent, ThreadID: c.thread, AgentThreadID: domain.ID(e.Subagents[0].NativeID), ItemID: "original-activity", Correlated: true}
	observed, err := c.ObserveResolvedActivity(context.Background(), activity)
	if err != nil || observed.Subagents[0].Source != domain.CodexActivitySource || observed.Subagents[0].SourceID != activity.ItemID || observed.Subagents[0].Output != nil {
		t.Fatal("activity lost original source identity or invented output", err)
	}
	// Native closeAgent can follow completed output. Reading that older turn or
	// receiving its late terminal notification must retain explicit shutdown.
	child := domain.ID(e.Subagents[0].NativeID)
	closed, err := observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 1, "item": map[string]any{"type": "collabAgentToolCall", "id": "native-close", "tool": "closeAgent", "status": "completed", "senderThreadId": c.thread, "receiverThreadIds": []domain.ID{child}, "agentsStates": map[string]any{string(child): map[string]any{"status": "shutdown", "message": nil}}}})
	if err != nil || closed.Subagents[0].Status != domain.SubagentShutdown {
		t.Fatal("native close after completion was rejected", err)
	}
	late, err := observeFixture(c, "turn/completed", map[string]any{"threadId": child, "turn": fixtureTurn(domain.NewID(), TurnCompleted)})
	if err != nil || late.Subagents[0].Status != domain.SubagentShutdown {
		t.Fatal("late child turn reopened shutdown", err)
	}
	read, err := c.InspectDescendants(context.Background())
	if err != nil {
		t.Fatal("closed child history could not be read", err)
	}
	for _, observed := range read.Subagents {
		if observed.NativeID == string(child) && observed.Status != domain.SubagentShutdown {
			t.Fatal("stored child turn reopened shutdown")
		}
	}
	for _, call := range capturedThreads(t, capture) {
		if call["method"] != "thread/start" && call["method"] != "thread/list" && call["method"] != "thread/turns/list" {
			t.Fatal("inspection invoked native child control", call["method"])
		}
	}
}

func TestSubagentCanonicalCollaborationKeepsRequestedModelAndChildScope(t *testing.T) {
	c, turn := observationClient()
	child := domain.NewID()
	item := map[string]any{"type": "collabAgentToolCall", "id": "spawn-native", "tool": "spawnAgent", "status": "completed", "senderThreadId": c.thread, "receiverThreadIds": []domain.ID{child}, "prompt": nil, "model": "requested-model", "reasoningEffort": nil, "agentsStates": map[string]any{string(child): map[string]any{"status": "running", "message": nil}}}
	e, err := observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 1, "item": item})
	if err != nil || e.Kind != SubagentEvent || len(e.Subagents) != 1 || e.Subagents[0].ObservedModel != nil || e.Subagents[0].RequestedModel == nil {
		t.Fatal("canonical child was fabricated or lost", err)
	}
	activity := Event{Kind: SubagentActivityEvent, ThreadID: c.thread, AgentThreadID: child, ItemID: "original-activity", Correlated: true}
	c.control = make(chan struct{}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := c.ObserveResolvedActivity(ctx, activity); err != nil {
		t.Fatal(err)
	}
	item["model"] = "different-requested-model"
	if _, err := observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 1, "item": item}); err == nil {
		t.Fatal("activity omission allowed replacement of original requested model")
	}
	if model := c.subagents[string(child)].RequestedModel; model == nil || *model != "requested-model" {
		t.Fatal("rejected spawn or activity erased original model")
	}
	e, err = observeFixture(c, "thread/tokenUsage/updated", map[string]any{"threadId": child, "turnId": domain.NewID(), "tokenUsage": map[string]any{"total": usageCounts(100), "last": usageCounts(100), "modelContextWindow": nil}})
	if err != nil || e.Kind != SubagentEvent || e.Usage != nil || e.Subagents[0].Usage.Scope != domain.SubagentCumulativeUsage {
		t.Fatal("child usage acquired root attribution", err)
	}
	item["senderThreadId"] = domain.NewID()
	if _, err := observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 1, "item": item}); err == nil {
		t.Fatal("foreign sender accepted")
	}
}

func TestSubagentActivityUsesCanonicalStringKind(t *testing.T) {
	c, turn := observationClient()
	child := domain.NewID()
	for _, kind := range []string{"started", "interacted", "interrupted", "completed"} {
		event, err := observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 1, "item": map[string]any{"type": "subAgentActivity", "id": "native-activity", "kind": kind, "agentThreadId": child, "agentPath": "/native-child"}})
		if err != nil || event.Kind != SubagentActivityEvent || event.AgentThreadID != child || len(c.subagents) != 0 {
			t.Fatal("canonical activity was rejected or invented ancestry", kind, err)
		}
	}
	if _, err := c.observeSubagentActivity(json.RawMessage(`{"type":"subAgentActivity","id":"native-activity","kind":{"type":"completed"},"agentThreadId":"`+string(child)+`","agentPath":"/native-child"}`), turn); err == nil {
		t.Fatal("noncanonical activity kind accepted")
	}
}

func TestSubagentThreadStartedRequiresPriorOwnershipEvidence(t *testing.T) {
	c, turn := observationClient()
	child := domain.NewID()
	parent := c.thread
	thread := threadWire{ID: child, ParentThreadID: &parent, SessionID: c.execution.thread.SessionID, CLIVersion: SupportedVersion, ModelProvider: c.execution.settings.Provider, Status: ThreadStatus{Type: ThreadIdle}}
	for _, parentID := range []domain.ID{c.thread, domain.NewID()} {
		parent = parentID
		event, err := observeFixture(c, "thread/started", map[string]any{"thread": thread})
		if err != nil || event.Kind != MetadataEvent || len(event.Subagents) != 0 || len(c.subagents) != 0 || c.execution.active != turn {
			t.Fatal("unproved start notification invented child ownership or changed the root", err)
		}
	}
	parent = c.thread
	spawn := map[string]any{"type": "collabAgentToolCall", "id": "original-spawn", "tool": "spawnAgent", "status": "completed", "senderThreadId": c.thread, "receiverThreadIds": []domain.ID{child}, "agentsStates": map[string]any{string(child): map[string]any{"status": "running", "message": nil}}}
	proved, err := observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 1, "item": spawn})
	if err != nil || proved.Kind != SubagentEvent || len(proved.Subagents) != 1 {
		t.Fatal("original spawn after an early notification was lost", err)
	}
	observed, err := observeFixture(c, "thread/started", map[string]any{"thread": thread})
	if err != nil || observed.Kind != SubagentEvent || observed.Subagents[0].ID != proved.Subagents[0].ID || observed.Subagents[0].ParentID != string(c.thread) {
		t.Fatal("proved start notification changed original child ownership", err)
	}
	parent = domain.NewID()
	if _, err := observeFixture(c, "thread/started", map[string]any{"thread": thread}); err == nil || c.subagents[string(child)].ParentID != string(c.thread) {
		t.Fatal("changed parent notification was accepted or reparented the child")
	}
}

func TestSubagentInventoryAfterParentCompletionFindsUnannouncedLiveChildren(t *testing.T) {
	c, capture := openSubagentFixture(t, "thread-subagent-live")
	if _, err := c.StartThread(context.Background(), domain.NewID(), threadSettings(t)); err != nil {
		t.Fatal(err)
	}
	turn := domain.NewID()
	c.execution.turns[turn] = trackedTurn{Turn: Turn{ID: turn, Status: TurnCompleted}}
	if len(c.subagents) != 0 {
		t.Fatal("fixture invented observed spawn ownership")
	}
	event, err := c.InspectDescendants(context.Background())
	if err != nil || event.Kind != SubagentEvent || event.TurnID != turn || len(event.Subagents) != 2 {
		t.Fatal("completed root could not inspect unannounced descendants", err)
	}
	state, err := domain.ApplySubagents(nil, string(c.thread), event.Subagents)
	if err != nil || state.Closed() {
		t.Fatal("parent completion erased independent live cleanup", err)
	}
	for _, call := range capturedThreads(t, capture) {
		if call["method"] != "thread/start" && call["method"] != "thread/list" && call["method"] != "thread/turns/list" {
			t.Fatal("completion inspection issued child controls")
		}
	}
}
