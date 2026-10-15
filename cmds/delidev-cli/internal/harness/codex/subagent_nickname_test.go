// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestAgentNicknameAcceptsOnlyBoundedNonblankText(t *testing.T) {
	for name, tc := range map[string]struct {
		raw  string
		want string
		ok   bool
	}{
		"missing":          {raw: "", ok: false},
		"null":             {raw: "null", ok: false},
		"blank":            {raw: `"   "`, ok: false},
		"number":           {raw: "7", ok: false},
		"object":           {raw: `{"name":"x"}`, ok: false},
		"control":          {raw: `"a\nb"`, ok: false},
		"too long":         {raw: `"` + strings.Repeat("a", 257) + `"`, ok: false},
		"exact limit":      {raw: `"` + strings.Repeat("가", 256) + `"`, want: strings.Repeat("가", 256), ok: true},
		"unicode":          {raw: `"Atlas 🚀"`, want: "Atlas 🚀", ok: true},
		"duplicate shaped": {raw: `"Nova"`, want: "Nova", ok: true},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := agentNickname(json.RawMessage(tc.raw))
			if ok != tc.ok || got != tc.want {
				t.Fatalf("nickname = %q, %v", got, ok)
			}
		})
	}
}

func TestSubagentInventoryAndStartCarryValidNicknamesBesideClosedObservations(t *testing.T) {
	c, _ := openSubagentFixture(t, "thread-subagent-read")
	if _, err := c.StartThread(context.Background(), domain.NewID(), threadSettings(t)); err != nil {
		t.Fatal(err)
	}
	turn := domain.NewID()
	c.execution.active = turn
	c.execution.turns[turn] = trackedTurn{Turn: Turn{ID: turn, Status: TurnRunning}}
	inventory, err := c.InspectDescendants(context.Background())
	if err != nil || len(inventory.Subagents) != 2 {
		t.Fatal("inventory failed", err)
	}
	// The blank nickname keeps its child valid and simply has no name entry.
	if len(inventory.SubagentNames) != 1 || inventory.SubagentNames[0].Name != "Atlas" {
		t.Fatal("inventory did not carry exactly the valid nickname", inventory.SubagentNames)
	}
	var named domain.SubagentObservation
	for _, child := range inventory.Subagents {
		if child.ID == inventory.SubagentNames[0].ChildID {
			named = child
		}
	}
	if named.ID == "" {
		t.Fatal("name was not joined to a product child ID")
	}
	encoded, err := json.Marshal(inventory.Subagents)
	if err != nil || strings.Contains(string(encoded), "Atlas") {
		t.Fatal("nickname leaked into the closed observation JSON")
	}
	parent := domain.ID(named.ParentID)
	nickname := json.RawMessage(`"Atlas Prime"`)
	started, err := observeFixture(c, "thread/started", map[string]any{"thread": threadWire{ID: domain.ID(named.NativeID), ParentThreadID: &parent, SessionID: c.execution.thread.SessionID, CLIVersion: SupportedVersion, ModelProvider: c.execution.settings.Provider, Status: ThreadStatus{Type: ThreadIdle}, AgentNickname: nickname}})
	if err != nil || started.Kind != SubagentEvent || len(started.SubagentNames) != 1 || started.SubagentNames[0].ChildID != named.ID || started.SubagentNames[0].Name != "Atlas Prime" {
		t.Fatal("a later valid observation did not update the name", err, started.SubagentNames)
	}
	omitted, err := observeFixture(c, "thread/started", map[string]any{"thread": threadWire{ID: domain.ID(named.NativeID), ParentThreadID: &parent, SessionID: c.execution.thread.SessionID, CLIVersion: SupportedVersion, ModelProvider: c.execution.settings.Provider, Status: ThreadStatus{Type: ThreadIdle}}})
	if err != nil || omitted.Kind != SubagentEvent || len(omitted.SubagentNames) != 0 {
		t.Fatal("an omitted nickname invalidated the child or invented a name", err)
	}
}
