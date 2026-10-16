// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func writeNicknames(t *testing.T, path string, values map[string]string) {
	t.Helper()
	raw := map[string]json.RawMessage{}
	for key, value := range values {
		raw[key] = json.RawMessage(value)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func nicknameFixture(t *testing.T, initial map[string]string) (*Client, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nicknames.json")
	writeNicknames(t, path, initial)
	t.Setenv("DELIDEV_CODEX_NICKNAME_FILE", path)
	c, _ := openSubagentFixture(t, "thread-subagent-read")
	if _, err := c.StartThread(context.Background(), domain.NewID(), threadSettings(t)); err != nil {
		t.Fatal(err)
	}
	turn := domain.NewID()
	c.execution.active = turn
	c.execution.turns[turn] = trackedTurn{Turn: Turn{ID: turn, Status: TurnRunning}}
	return c, path
}

func namesByNative(c *Client, event Event) map[string]string {
	native := map[domain.ID]string{}
	for _, child := range event.Subagents {
		native[child.ID] = child.NativeID
	}
	result := map[string]string{}
	for _, name := range event.SubagentNames {
		result[native[name.ID]] = name.Name
	}
	return result
}

func TestCodexNicknameAcceptedOnlyAsBoundedNonBlankText(t *testing.T) {
	for name, tc := range map[string]struct {
		raw      string
		accepted bool
	}{
		"plain":    {`"Atlas"`, true},
		"unicode":  {`"` + strings.Repeat("가", domain.MaxSubagentDisplayNameRunes) + `"`, true},
		"missing":  {``, false},
		"null":     {`null`, false},
		"blank":    {`"  "`, false},
		"empty":    {`""`, false},
		"long":     {`"` + strings.Repeat("가", domain.MaxSubagentDisplayNameRunes+1) + `"`, false},
		"number":   {`7`, false},
		"object":   {`{"name":"Atlas"}`, false},
		"control":  {`"At\nlas"`, false},
		"nul":      {`"At\u0000las"`, false},
		"emptyArr": {`[]`, false},
		// Malformed surrogate escapes must be rejected on the raw JSON text,
		// because decoding them yields an otherwise valid U+FFFD.
		"loneHigh":        {`"\ud800"`, false},
		"loneLow":         {`"\udc00"`, false},
		"trailingHigh":    {`"Atlas\ud83d"`, false},
		"highThenScalar":  {`"\ud83d\u0041"`, false},
		"highThenHigh":    {`"\ud800\ud800"`, false},
		"lowThenHigh":     {`"\ude00\ud83d"`, false},
		"highThenLiteral": {`"\ud83dx"`, false},
		"validPair":       {`"At\ud83d\ude00las"`, true},
		"escapedEscape":   {`"\\ud800"`, true},
	} {
		t.Run(name, func(t *testing.T) {
			values := map[string]string{"1": `"Second"`}
			if tc.raw != "" {
				values["0"] = tc.raw
			}
			c, _ := nicknameFixture(t, values)
			event, err := c.InspectDescendants(context.Background())
			if err != nil || len(event.Subagents) != 2 {
				t.Fatal("invalid optional nickname invalidated otherwise valid telemetry", err)
			}
			names := namesByNative(c, event)
			if names[event.Subagents[1].NativeID] != "Second" {
				t.Fatal("independent valid nickname was lost")
			}
			_, got := names[event.Subagents[0].NativeID]
			if got != tc.accepted {
				t.Fatalf("nickname acceptance = %v, want %v", got, tc.accepted)
			}
			if tc.accepted && names[event.Subagents[0].NativeID] == "" {
				t.Fatal("accepted nickname is empty")
			}
		})
	}
}

func TestCodexNicknameKeepsLastValidAndUpdatesFromLaterObservation(t *testing.T) {
	c, path := nicknameFixture(t, map[string]string{"0": `"Atlas"`, "1": `"Nova"`})
	first, err := c.InspectDescendants(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := namesByNative(c, first); got[first.Subagents[0].NativeID] != "Atlas" || got[first.Subagents[1].NativeID] != "Nova" {
		t.Fatal("initial names were not observed")
	}
	// Omitted, null and invalid later observations never erase the last valid name.
	writeNicknames(t, path, map[string]string{"0": `null`, "1": `"   "`})
	second, err := c.InspectDescendants(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := namesByNative(c, second); got[second.Subagents[0].NativeID] != "Atlas" || got[second.Subagents[1].NativeID] != "Nova" {
		t.Fatal("later omitted nickname erased the last valid name")
	}
	writeNicknames(t, path, map[string]string{"0": `"Atlas II"`})
	third, err := c.InspectDescendants(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := namesByNative(c, third); got[third.Subagents[0].NativeID] != "Atlas II" || got[third.Subagents[1].NativeID] != "Nova" {
		t.Fatal("later valid nickname did not update the child")
	}
	// Names join by product child ID, so equal names remain separate children.
	writeNicknames(t, path, map[string]string{"0": `"Same"`, "1": `"Same"`})
	fourth, err := c.InspectDescendants(context.Background())
	if err != nil || len(fourth.SubagentNames) != 2 || fourth.SubagentNames[0].ID == fourth.SubagentNames[1].ID {
		t.Fatal("duplicate names merged children", err)
	}
	if err := domain.ValidateSubagentDisplayNames(fourth.SubagentNames, fourth.Subagents); err != nil {
		t.Fatal("observed names do not join their batch", err)
	}
}

func TestCodexNicknameRetainedOnlyAfterChildOwnershipIsProven(t *testing.T) {
	c, turn := observationClient()
	child, parent := domain.NewID(), c.thread
	started := func(nickname string) (Event, error) {
		thread := threadWire{ID: child, ParentThreadID: &parent, SessionID: domain.NewID(), CLIVersion: SupportedVersion, ModelProvider: c.execution.settings.Provider, Status: ThreadStatus{Type: ThreadIdle}, AgentNickname: json.RawMessage(nickname)}
		return observeFixture(c, "thread/started", map[string]any{"thread": thread})
	}
	// A start notification may precede the original spawn evidence and for a
	// foreign parent never proves ownership, so no name is retained.
	for _, parentID := range []domain.ID{c.thread, domain.NewID()} {
		parent = parentID
		if event, err := started(`"Early"`); err != nil || event.Kind != MetadataEvent || len(c.subagentNames) != 0 || len(event.SubagentNames) != 0 {
			t.Fatal("unproved child retained a nickname", err)
		}
	}
	parent = c.thread
	spawn := map[string]any{"type": "collabAgentToolCall", "id": "original-spawn", "tool": "spawnAgent", "status": "completed", "senderThreadId": c.thread, "receiverThreadIds": []domain.ID{child}, "agentsStates": map[string]any{string(child): map[string]any{"status": "running", "message": nil}}}
	proved, err := observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 1, "item": spawn})
	if err != nil || len(proved.Subagents) != 1 || len(c.subagentNames) != 0 {
		t.Fatal("original spawn changed names", err)
	}
	named, err := started(`"Atlas"`)
	if err != nil || namesByNative(c, named)[string(child)] != "Atlas" {
		t.Fatal("proved child lost its nickname", err)
	}
	// A changed parent is rejected and must not replace the retained name.
	parent = domain.NewID()
	if _, err := started(`"Intruder"`); err == nil || c.subagentNames[string(child)] != "Atlas" {
		t.Fatal("rejected notification replaced the retained nickname")
	}
	// Malformed surrogate escapes never replace or create a retained name.
	parent = c.thread
	for _, raw := range []string{`"\ud800"`, `"\udc00"`, `null`, `"  "`} {
		if _, err := started(raw); err != nil || c.subagentNames[string(child)] != "Atlas" {
			t.Fatal("invalid nickname changed the retained name", raw, err)
		}
	}
}

func TestCodexNicknameInventoryStagesUntilBatchIsAccepted(t *testing.T) {
	c, path := nicknameFixture(t, map[string]string{"0": `"Atlas"`, "1": `"Nova"`})
	first, err := c.InspectDescendants(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before := map[string]string{}
	for key, value := range c.subagentNames {
		before[key] = value
	}
	// A known child reported under a different parent proves no ownership; the
	// whole inventory is rejected and no name from it is retained.
	c.subagents[first.Subagents[1].NativeID] = func() domain.SubagentObservation {
		o := c.subagents[first.Subagents[1].NativeID]
		o.ParentID = string(domain.NewID())
		return o
	}()
	writeNicknames(t, path, map[string]string{"0": `"Renamed"`, "1": `"Other"`})
	if _, err := c.InspectDescendants(context.Background()); err == nil {
		t.Fatal("inventory with a reparented child was accepted")
	}
	if len(c.subagentNames) != len(before) {
		t.Fatal("rejected inventory changed the retained names")
	}
	for key, value := range before {
		if c.subagentNames[key] != value {
			t.Fatal("rejected inventory replaced a retained name", key)
		}
	}
}
