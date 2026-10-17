// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func openSubagentFixture(t *testing.T, mode string) (*Client, string) {
	t.Helper()
	config := fixtureConfig(t, mode)
	config.Mode = ThreadProtocol
	capture := filepath.Join(t.TempDir(), "requests.jsonl")
	config.Process.Env = append(config.Process.Env, "DELIDEV_CODEX_CAPTURE="+capture)
	if nicknames := os.Getenv("DELIDEV_CODEX_NICKNAME_FILE"); nicknames != "" {
		config.Process.Env = append(config.Process.Env, "DELIDEV_CODEX_NICKNAME_FILE="+nicknames)
	}
	if visibility := os.Getenv("DELIDEV_CODEX_SUBAGENT_VISIBILITY_FILE"); visibility != "" {
		config.Process.Env = append(config.Process.Env, "DELIDEV_CODEX_SUBAGENT_VISIBILITY_FILE="+visibility)
	}
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
			a["sessionId"], b["sessionId"] = a["id"], b["id"]
			a["parentThreadId"] = f.thread["id"]
			b["parentThreadId"] = a["id"]
			f.subagents = []map[string]any{b, a}
		}
		// DELIDEV_CODEX_NICKNAME_FILE maps a child position to raw agentNickname
		// JSON; an absent position omits the field. Tests rewrite it between reads.
		if path := os.Getenv("DELIDEV_CODEX_NICKNAME_FILE"); path != "" {
			var nicknames map[string]json.RawMessage
			if data, err := os.ReadFile(path); err != nil || json.Unmarshal(data, &nicknames) != nil {
				os.Exit(75)
			}
			for i, child := range f.subagents {
				if raw, ok := nicknames[strconv.Itoa(i)]; ok {
					child["agentNickname"] = raw
				} else {
					delete(child, "agentNickname")
				}
			}
		}
		if path := os.Getenv("DELIDEV_CODEX_SUBAGENT_VISIBILITY_FILE"); path != "" {
			if data, err := os.ReadFile(path); err == nil && string(data) == "hide-once" {
				if os.WriteFile(path, []byte("visible"), 0600) != nil {
					os.Exit(76)
				}
				write(id, map[string]any{"data": []any{}, "nextCursor": nil, "backwardsCursor": nil})
				return true
			}
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

func TestSubagentEarlyStatusRequiresIndependentDescendantProof(t *testing.T) {
	c, _ := openSubagentFixture(t, "thread-subagent-read")
	if _, err := c.StartThread(context.Background(), domain.NewID(), threadSettings(t)); err != nil {
		t.Fatal(err)
	}
	turn := domain.NewID()
	c.execution.active = turn
	c.execution.turns[turn] = trackedTurn{Turn: Turn{ID: turn, Status: TurnRunning}}
	inventory, err := c.InspectDescendants(context.Background())
	if err != nil || len(inventory.Subagents) != 2 {
		t.Fatal("fixture inventory failed", err)
	}
	child := domain.ID(inventory.Subagents[0].NativeID)
	c.subagents = nil
	event, err := observeFixture(c, "thread/status/changed", map[string]any{"threadId": child, "status": ThreadStatus{Type: ThreadIdle}})
	if err != nil || event.Kind != MetadataEvent || len(c.subagents) != 0 || len(c.pendingChildStatuses) != 1 {
		t.Fatal("status before child start invented ownership", err)
	}
	if _, err := c.InspectDescendants(context.Background()); err != nil || len(c.pendingChildStatuses) != 0 {
		t.Fatal("independent inventory did not resolve early status", err)
	}
	foreign := domain.NewID()
	if _, err := observeFixture(c, "thread/status/changed", map[string]any{"threadId": foreign, "status": ThreadStatus{Type: ThreadIdle}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InspectDescendants(context.Background()); err == nil || len(c.pendingChildStatuses) != 1 {
		t.Fatal("unknown ancestry lost its completion fence")
	}
	// Canonical spawn proves publication ownership, but only a fresh inventory
	// can discharge the early-status fence while other siblings are present.
	spawn := map[string]any{"type": "collabAgentToolCall", "id": "missing-inventory-child", "tool": "spawnAgent", "status": "completed", "senderThreadId": c.thread, "receiverThreadIds": []domain.ID{foreign}, "agentsStates": map[string]any{string(foreign): map[string]any{"status": "running", "message": nil}}}
	if event, err := observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 1, "item": spawn}); err != nil || event.Kind != SubagentEvent {
		t.Fatal("canonical spawn did not establish child ownership", err)
	}
	if _, err := c.InspectDescendants(context.Background()); err == nil || len(c.pendingChildStatuses) != 1 {
		t.Fatal("published child missing from inventory lost its completion fence")
	}
}

func TestSubagentEarlyStatusRequiresExactEnvelope(t *testing.T) {
	c, turn := observationClient()
	child := domain.NewID()
	idle := ThreadStatus{Type: ThreadIdle}
	rejected := []struct {
		name   string
		params map[string]any
	}{
		{"extra top-level key", map[string]any{"threadId": child, "status": idle, "extra": true}},
		{"extra null top-level key", map[string]any{"threadId": child, "status": idle, "turnId": nil}},
		{"unknown nested key", map[string]any{"threadId": child, "status": map[string]any{"type": "idle", "extra": 1}}},
		{"invalid status type", map[string]any{"threadId": child, "status": "idle"}},
		{"unknown status variant", map[string]any{"threadId": child, "status": map[string]any{"type": "unknown"}}},
		{"missing status", map[string]any{"threadId": child}},
	}
	for _, tc := range rejected {
		event, err := observeFixture(c, "thread/status/changed", tc.params)
		if err == nil || event.Kind == MetadataEvent || len(c.pendingChildStatuses) != 0 || len(c.subagents) != 0 || c.execution.active != turn {
			t.Fatal("malformed early child status envelope was accepted or recorded state", tc.name, err)
		}
	}
	raw := nativewire.Event{Kind: nativewire.Notification, Method: "thread/status/changed", Params: json.RawMessage(`{"threadId":"` + string(child) + `","status":{"type":"idle"},"threadId":"` + string(child) + `"}`)}
	// Duplicate keys fail the shared strict decode before the child branch and
	// must never retain an unresolved identity.
	if _, handled, err := c.observeChildNative(raw); handled || err != nil || len(c.pendingChildStatuses) != 0 {
		t.Fatal("duplicate-key early child status envelope reached the child fence", handled, err)
	}
	// The exact envelope keeps the passive metadata and unresolved-child fence.
	event, err := observeFixture(c, "thread/status/changed", map[string]any{"threadId": child, "status": idle})
	if err != nil || event.Kind != MetadataEvent || len(c.pendingChildStatuses) != 1 || len(c.subagents) != 0 {
		t.Fatal("exact early child status envelope lost its unresolved ancestry fence", err)
	}
	// A rejected envelope for an already retained identity leaves the fence intact.
	if _, err := observeFixture(c, "thread/status/changed", map[string]any{"threadId": child, "status": idle, "extra": true}); err == nil || len(c.pendingChildStatuses) != 1 {
		t.Fatal("rejected envelope changed the retained fence", err)
	}
	// Root-thread status never enters the early child fence.
	before := len(c.pendingChildStatuses)
	_, _ = observeFixture(c, "thread/status/changed", map[string]any{"threadId": c.thread, "status": idle})
	if len(c.pendingChildStatuses) != before {
		t.Fatal("root thread status entered the unresolved child fence")
	}
	// Owned child status uses the owned branch and never adds a fence entry.
	owned := domain.NewID()
	spawn := map[string]any{"type": "collabAgentToolCall", "id": "exact-envelope-spawn", "tool": "spawnAgent", "status": "completed", "senderThreadId": c.thread, "receiverThreadIds": []domain.ID{owned}, "agentsStates": map[string]any{string(owned): map[string]any{"status": "running", "message": nil}}}
	if event, err := observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 1, "item": spawn}); err != nil || event.Kind != SubagentEvent {
		t.Fatal("canonical spawn did not establish child ownership", err)
	}
	_, _ = observeFixture(c, "thread/status/changed", map[string]any{"threadId": owned, "status": idle})
	if len(c.pendingChildStatuses) != before {
		t.Fatal("owned child status entered the unresolved child fence")
	}
}

func TestSubagentEarlyStatusRejectsCaseAliasedKeys(t *testing.T) {
	c, turn := observationClient()
	child := domain.NewID()
	rejected := []struct {
		name string
		raw  string
	}{
		{"capitalized status", `{"threadId":"` + string(child) + `","Status":{"type":"idle"}}`},
		{"uppercase status", `{"threadId":"` + string(child) + `","STATUS":{"type":"idle"}}`},
		{"both status spellings", `{"threadId":"` + string(child) + `","status":{"type":"idle"},"Status":{"type":"idle"}}`},
		{"capitalized nested type", `{"threadId":"` + string(child) + `","status":{"Type":"idle"}}`},
		{"capitalized nested active flags", `{"threadId":"` + string(child) + `","status":{"type":"active","ActiveFlags":["waitingOnApproval"]}}`},
		{"extra capitalized thread id", `{"threadId":"` + string(child) + `","ThreadID":"` + string(child) + `","status":{"type":"idle"}}`},
	}
	for _, tc := range rejected {
		raw := nativewire.Event{Kind: nativewire.Notification, Method: "thread/status/changed", Params: json.RawMessage(tc.raw)}
		event, handled, err := c.observeChildNative(raw)
		if err == nil || !handled || event.Kind == MetadataEvent || len(c.pendingChildStatuses) != 0 || len(c.subagents) != 0 || c.execution.active != turn {
			t.Fatal("case-aliased early child status envelope was accepted or recorded state", tc.name, handled, err)
		}
	}
	// A capitalized thread identity key never names a child identity.
	raw := nativewire.Event{Kind: nativewire.Notification, Method: "thread/status/changed", Params: json.RawMessage(`{"ThreadId":"` + string(child) + `","status":{"type":"idle"}}`)}
	if _, handled, err := c.observeChildNative(raw); handled || err != nil || len(c.pendingChildStatuses) != 0 {
		t.Fatal("capitalized thread identity key reached the child fence", handled, err)
	}
	// The canonical key spelling, in either object order, is still accepted.
	for _, params := range []string{
		`{"threadId":"` + string(child) + `","status":{"type":"idle"}}`,
		`{"status":{"type":"idle"},"threadId":"` + string(child) + `"}`,
	} {
		raw := nativewire.Event{Kind: nativewire.Notification, Method: "thread/status/changed", Params: json.RawMessage(params)}
		event, handled, err := c.observeChildNative(raw)
		if err != nil || !handled || event.Kind != MetadataEvent || len(c.pendingChildStatuses) != 1 {
			t.Fatal("canonical early child status envelope was rejected", handled, err)
		}
	}
}

func TestSubagentChildNativeEnvelopesRejectCaseAliasedKeys(t *testing.T) {
	c, turn := observationClient()
	child := domain.NewID()
	spawn := map[string]any{"type": "collabAgentToolCall", "id": "case-spawn", "tool": "spawnAgent", "status": "completed", "senderThreadId": c.thread, "receiverThreadIds": []domain.ID{child}, "agentsStates": map[string]any{string(child): map[string]any{"status": "running", "message": nil}}}
	if event, err := observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 1, "item": spawn}); err != nil || event.Kind != SubagentEvent {
		t.Fatal("canonical spawn did not establish child ownership", err)
	}
	id := string(child)
	completedTurn := `{"id":"` + string(domain.NewID()) + `","items":[],"status":"completed"}`
	for _, tc := range []struct{ name, method, canonical, aliased string }{
		{"turn completed envelope", "turn/completed", `{"threadId":"` + id + `","turn":` + completedTurn + `}`, `{"threadId":"` + id + `","Turn":` + completedTurn + `}`},
		{"turn completed turn", "turn/completed", `{"threadId":"` + id + `","turn":` + completedTurn + `}`, `{"threadId":"` + id + `","turn":{"id":"` + string(domain.NewID()) + `","items":[],"Status":"completed"}}`},
		{"agent message item", "item/completed", `{"threadId":"` + id + `","turnId":"` + string(turn) + `","item":{"type":"agentMessage","id":"m1","text":"hi"}}`, `{"threadId":"` + id + `","turnId":"` + string(turn) + `","item":{"type":"agentMessage","id":"m1","Text":"hi"}}`},
		{"token usage", "thread/tokenUsage/updated", `{"threadId":"` + id + `","turnId":"` + string(turn) + `","tokenUsage":{"total":{"inputTokens":1,"cachedInputTokens":0,"outputTokens":1,"reasoningOutputTokens":0,"totalTokens":2},"last":{"inputTokens":1,"cachedInputTokens":0,"outputTokens":1,"reasoningOutputTokens":0,"totalTokens":2},"modelContextWindow":null}}`, `{"threadId":"` + id + `","turnId":"` + string(turn) + `","tokenUsage":{"Total":{"inputTokens":1,"cachedInputTokens":0,"outputTokens":1,"reasoningOutputTokens":0,"totalTokens":2},"last":{"inputTokens":1,"cachedInputTokens":0,"outputTokens":1,"reasoningOutputTokens":0,"totalTokens":2},"modelContextWindow":null}}`},
	} {
		if _, handled, err := c.observeChildNative(nativewire.Event{Kind: nativewire.Notification, Method: tc.method, Params: json.RawMessage(tc.aliased)}); !handled || err == nil {
			t.Fatal("case-aliased child envelope was accepted", tc.name, handled, err)
		}
		if _, handled, err := c.observeChildNative(nativewire.Event{Kind: nativewire.Notification, Method: tc.method, Params: json.RawMessage(tc.canonical)}); !handled || err != nil {
			t.Fatal("canonical child envelope was rejected", tc.name, handled, err)
		}
	}
	started := func(key string) json.RawMessage {
		return json.RawMessage(`{"` + key + `":{"id":"` + id + `","sessionId":"` + string(c.execution.thread.SessionID) + `","cliVersion":"` + SupportedVersion + `","cwd":"/","modelProvider":"` + c.execution.settings.Provider + `","createdAt":1,"updatedAt":1,"ephemeral":false,"preview":"","projectId":null,"source":null,"status":{"type":"idle"},"turns":[],"parentThreadId":"` + string(c.thread) + `"}}`)
	}
	if _, handled, err := c.observeChildNative(nativewire.Event{Kind: nativewire.Notification, Method: "thread/started", Params: started("Thread")}); handled && err == nil {
		t.Fatal("capitalized thread envelope key was accepted")
	}
	if _, handled, err := c.observeChildNative(nativewire.Event{Kind: nativewire.Notification, Method: "thread/started", Params: started("thread")}); !handled || err != nil {
		t.Fatal("canonical thread/started envelope was rejected", handled, err)
	}
}

func TestSubagentOwnershipItemsRejectCaseAliasedKeys(t *testing.T) {
	c, turn := observationClient()
	child := domain.NewID()
	spawn := func(rename func(map[string]any)) map[string]any {
		item := map[string]any{"type": "collabAgentToolCall", "id": "case-alias-spawn", "tool": "spawnAgent", "status": "completed", "senderThreadId": c.thread, "receiverThreadIds": []domain.ID{child}, "agentsStates": map[string]any{string(child): map[string]any{"status": "running", "message": nil}}}
		if rename != nil {
			rename(item)
		}
		return item
	}
	aliased := map[string]func(map[string]any){
		"Tool instead of tool": func(m map[string]any) { m["Tool"] = m["tool"]; delete(m, "tool") },
		"both tool spellings":  func(m map[string]any) { m["Tool"] = m["tool"] },
		"nested agent state alias": func(m map[string]any) {
			m["agentsStates"] = map[string]any{string(child): map[string]any{"Status": "running", "message": nil}}
		},
	}
	for name, rename := range aliased {
		event, err := observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 1, "item": spawn(rename)})
		if err == nil || event.Kind == SubagentEvent || len(c.subagents) != 0 {
			t.Fatal("case-aliased collaboration item established child ownership", name, err)
		}
	}
	if event, err := observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 1, "item": spawn(nil)}); err != nil || event.Kind != SubagentEvent || len(c.subagents) != 1 {
		t.Fatal("canonical collaboration item did not establish child ownership", err)
	}
	activity := func(kindKey string) json.RawMessage {
		return json.RawMessage(`{"type":"subAgentActivity","id":"case-alias-activity","` + kindKey + `":"started","agentThreadId":"` + string(child) + `","agentPath":"/native-child"}`)
	}
	if _, err := c.observeSubagentActivity(activity("Kind"), turn); err == nil {
		t.Fatal("case-aliased activity item was accepted")
	}
	if _, err := c.observeSubagentActivity(activity("kind"), turn); err != nil {
		t.Fatal("canonical activity item was rejected", err)
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
	thread := threadWire{ID: child, ParentThreadID: &parent, SessionID: domain.NewID(), CLIVersion: SupportedVersion, ModelProvider: c.execution.settings.Provider, Status: ThreadStatus{Type: ThreadIdle}}
	for _, parentID := range []domain.ID{c.thread, domain.NewID()} {
		parent = parentID
		event, err := observeFixture(c, "thread/started", map[string]any{"thread": thread})
		if err != nil || event.Kind != MetadataEvent || len(event.Subagents) != 0 || len(c.subagents) != 0 || c.execution.active != turn {
			t.Fatal("unproved start notification invented child ownership or changed the root", err)
		}
	}
	parent = c.thread
	for _, status := range []any{ThreadStatus{Type: ThreadIdle}, map[string]any{"type": "active", "activeFlags": []string{}}, ThreadStatus{Type: ThreadNotLoaded}} {
		event, err := observeFixture(c, "thread/status/changed", map[string]any{"threadId": child, "status": status})
		if err != nil || event.Kind != MetadataEvent || len(c.subagents) != 0 || c.execution.active != turn {
			t.Fatal("early child status blocked the root or invented ownership", err)
		}
	}
	foreign, err := observeFixture(c, "thread/status/changed", map[string]any{"threadId": domain.NewID(), "status": ThreadStatus{Type: ThreadIdle}})
	if err != nil || foreign.Kind != MetadataEvent || len(c.pendingChildStatuses) != 2 || len(c.subagents) != 0 {
		t.Fatal("unannounced status lost its unresolved ancestry fence", err)
	}
	if _, err := observeFixture(c, "thread/status/changed", map[string]any{"threadId": child, "status": map[string]any{"type": "unknown"}}); err == nil {
		t.Fatal("malformed early child status was accepted")
	}
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

func TestCompletedChildQuestionsPreserveOutputAndRoot(t *testing.T) {
	for _, questions := range []string{"", `,"questions":null`, `,"questions":[]`} {
		t.Run(questions, func(t *testing.T) {
			c, turn := observationClient()
			child := domain.NewID()
			c.subagents = map[string]domain.SubagentObservation{string(child): {ID: domain.NewID(), NativeID: string(child), ParentID: string(c.thread), Status: domain.SubagentRunning}}
			paused := c.execution.paused
			item := json.RawMessage(`{"type":"agentMessage","id":"child-message","text":"Child output","phase":"final_answer","delivery":null` + questions + `}`)
			event, err := observeFixture(c, "item/completed", map[string]any{"threadId": child, "turnId": turn, "item": item})
			if err != nil || event.Kind != SubagentEvent || len(event.Subagents) != 1 {
				t.Fatal("canonical child message rejected", err)
			}
			output := event.Subagents[0].Output
			if output == nil || output.NativeMessageID != "child-message" || output.Text != "Child output" || event.Subagents[0].NativeID != string(child) || event.Subagents[0].ParentID != string(c.thread) {
				t.Fatal("original child output or ownership changed")
			}
			if c.execution.active != turn || c.execution.paused != paused {
				t.Fatal("child metadata changed healthy root")
			}
		})
	}
}

func TestCompletedChildQuestionsRetainPrivateAndStrictBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, extra      string
		private, invalid bool
	}{
		{"populated", `,"questions":[{"id":"private-question"}]`, true, false},
		{"populated-null", `,"questions":[null]`, true, false},
		{"malformed-string", `,"questions":"invalid"`, false, true},
		{"malformed-object", `,"questions":{}`, false, true},
		{"unknown", `,"questions":null,"unknown":true`, false, true},
		{"duplicate", `,"questions":null,"questions":[]`, false, true},
		{"invalid-output", `,"questions":null,"text":42`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, turn := observationClient()
			child := domain.NewID()
			c.subagents = map[string]domain.SubagentObservation{string(child): {ID: domain.NewID(), NativeID: string(child), ParentID: string(c.thread), Status: domain.SubagentRunning}}
			item := json.RawMessage(`{"type":"agentMessage","id":"child-message","text":"Child output","phase":"final_answer","delivery":null` + tc.extra + `}`)
			event, err := observeFixture(c, "item/completed", map[string]any{"threadId": child, "turnId": turn, "item": item})
			if tc.invalid && err == nil || tc.private && (err != nil || event.Kind != NativeExtensionEvent) {
				t.Fatal("private or strict child boundary changed", err)
			}
			if event.Kind == SubagentEvent || c.subagents[string(child)].Output != nil {
				t.Fatal("unsupported metadata published child output")
			}
		})
	}
	c, turn := observationClient()
	child := domain.NewID()
	c.subagents = map[string]domain.SubagentObservation{string(child): {ID: domain.NewID(), NativeID: string(child), ParentID: string(c.thread), Status: domain.SubagentRunning}}
	event, err := observeFixture(c, "item/completed", map[string]any{"threadId": domain.NewID(), "turnId": turn, "item": map[string]any{"type": "agentMessage", "id": "foreign-message", "text": "Foreign output", "phase": "final_answer", "delivery": nil, "questions": nil}})
	if event.Kind == SubagentEvent || c.subagents[string(child)].Output != nil || c.execution.active != turn {
		t.Fatal("foreign message acquired child or root authority", err)
	}
}

func TestSubagentObservedStatusWaitsForSuccessfulInventoryVisibility(t *testing.T) {
	path := filepath.Join(t.TempDir(), "visibility")
	if err := os.WriteFile(path, []byte("visible"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DELIDEV_CODEX_SUBAGENT_VISIBILITY_FILE", path)
	c, capture := openSubagentFixture(t, "thread-subagent-read")
	if _, err := c.StartThread(context.Background(), domain.NewID(), threadSettings(t)); err != nil {
		t.Fatal(err)
	}
	turn := domain.NewID()
	c.execution.active = turn
	c.execution.turns[turn] = trackedTurn{Turn: Turn{ID: turn, Status: TurnRunning}}
	first, err := c.InspectDescendants(context.Background())
	if err != nil || len(first.Subagents) != 2 {
		t.Fatal("initial ancestry unavailable", err)
	}
	child := domain.ID(first.Subagents[0].NativeID)
	c.subagents = nil
	if _, err := observeFixture(c, "thread/status/changed", map[string]any{"threadId": child, "status": ThreadStatus{Type: ThreadIdle}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("hide-once"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := c.InspectDescendants(context.Background())
	if err != nil || len(result.Subagents) != 2 || len(c.pendingChildStatuses) != 0 {
		t.Fatal("successful delayed inventory lost original child fence", err)
	}
	lists := 0
	for _, call := range capturedThreads(t, capture) {
		switch call["method"] {
		case "thread/list":
			lists++
		case "thread/start", "thread/turns/list":
		default:
			t.Fatal("visibility inspection gained child control")
		}
	}
	if lists != 3 {
		t.Fatal("successful missing snapshot did not perform exactly one new inventory read", lists)
	}
}
