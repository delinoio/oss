package codex

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"maps"
	"os"
	"testing"
)

func (f *threadFixture) handleSubagentRead(id json.RawMessage, method string, raw json.RawMessage, write func(json.RawMessage, any)) bool {
	if f.mode != "thread-subagent-read" || method != "thread/list" && method != "thread/turns/list" {
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
		if params["ancestorThreadId"] != string(f.thread["id"].(domain.ID)) {
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
	turn["items"] = []any{map[string]any{"type": "agentMessage", "id": "native-child-message", "text": "Stored native child output", "phase": "final_answer"}}
	write(id, map[string]any{"data": []any{turn}, "nextCursor": nil, "backwardsCursor": nil})
	return true
}

func TestSubagentDescendantInspectionReadsHistoryWithoutChildControls(t *testing.T) {
	c, capture := openThreadFixture(t, "thread-subagent-read")
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
	activity := Event{Kind: SubagentActivityEvent, ThreadID: c.thread, AgentThreadID: domain.ID(e.Subagents[0].NativeID), ItemID: "original-activity", Correlated: true}
	observed, err := c.ObserveResolvedActivity(context.Background(), activity)
	if err != nil || observed.Subagents[0].Source != domain.CodexActivitySource || observed.Subagents[0].SourceID != activity.ItemID || observed.Subagents[0].Output != nil {
		t.Fatal("activity lost original source identity or invented output", err)
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
	e, err = observeFixture(c, "thread/tokenUsage/updated", map[string]any{"threadId": child, "turnId": domain.NewID(), "tokenUsage": map[string]any{"total": usageCounts(100), "last": usageCounts(100), "modelContextWindow": nil}})
	if err != nil || e.Kind != SubagentEvent || e.Usage != nil || e.Subagents[0].Usage.Scope != domain.SubagentCumulativeUsage {
		t.Fatal("child usage acquired root attribution", err)
	}
	item["senderThreadId"] = domain.NewID()
	if _, err := observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 1, "item": item}); err == nil {
		t.Fatal("foreign sender accepted")
	}
}
