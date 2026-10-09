// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
)

func TestSkillsChangedAcceptsOnlyEmptyNotification(t *testing.T) {
	for _, payload := range []string{"{}", " { \n } "} {
		c, turn := observationClient()
		settings := c.execution.settings
		paused := c.execution.paused
		for i := 0; i < 3; i++ {
			event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "skills/changed", Params: json.RawMessage(payload)})
			if err != nil || event.Kind != MetadataEvent || event.Metadata != SkillsChangedDiscarded || !event.Correlated || event.ThreadID != c.thread || event.TurnID != "" {
				t.Fatal(event, err)
			}
			if c.execution.active != turn || c.execution.paused != paused || !reflect.DeepEqual(c.execution.settings, settings) || c.problem != nil {
				t.Fatal("passive signal changed authority")
			}
		}
	}
	for _, payload := range []string{"", "null", "[]", "[{}]", "true", "0", `"signal"`, `{"unknown":true}`, `{"unknown":null}`, `{"unknown":1,"unknown":2}`, "{", "{}{}"} {
		t.Run(payload, func(t *testing.T) {
			c, _ := observationClient()
			if _, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "skills/changed", Params: json.RawMessage(payload)}); err == nil {
				t.Fatal("invalid payload accepted")
			}
		})
	}
	c, _ := observationClient()
	for _, native := range []nativewire.Event{{Kind: nativewire.ServerRequest, Method: "skills/changed", Params: json.RawMessage(`{}`)}, {Kind: nativewire.Notification, Method: "unknown/changed", Params: json.RawMessage(`{}`)}} {
		event, err := c.observeEventLocked(native)
		if err == nil && event.Kind != NativeExtensionEvent {
			t.Fatal("unsupported family gained metadata authority", event)
		}
	}
	unbound := &Client{}
	event, err := unbound.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "skills/changed", Params: json.RawMessage(`{}`)})
	if err != nil || event.Kind != NativeExtensionEvent {
		t.Fatal("unbound process gained execution observation", event, err)
	}
}

func TestSkillsChangedPreservesAcceptedOriginalTurns(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.PlanMode, domain.ExecuteMode} {
		for _, resumed := range []bool{false, true} {
			t.Run(string(mode)+map[bool]string{false: "/start", true: "/resume"}[resumed], func(t *testing.T) {
				var c *Client
				var capture string
				if resumed {
					var checkpoint ContinuationCheckpoint
					c, capture, checkpoint, _ = continuationFixture(t, "ready")
					if _, err := c.VerifyContinuation(context.Background(), domain.NewID(), checkpoint, ContinueAfterSuccess); err != nil {
						t.Fatal(err)
					}
				} else {
					c, capture, _, _ = boundTurnFixture(t, "normal")
				}
				originalSettings := c.execution.settings
				accepted, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(mode))
				if err != nil {
					t.Fatal(err)
				}
				message := nextKind(t, c, MessageCompletedEvent)
				if message.Message == nil || message.Message.Text != input(mode).Prompt {
					t.Fatal("original input not acknowledged", message)
				}
				for i := 0; i < 3; i++ {
					fixtureSignal(t, c, "notify", map[string]any{"method": "skills/changed", "params": map[string]any{}})
					event := nextKind(t, c, MetadataEvent)
					if event.Metadata != SkillsChangedDiscarded || event.TurnID != "" || event.ThreadID != c.thread || c.problem != nil {
						t.Fatal(event)
					}
					if c.execution.active != accepted.TurnID || c.execution.turns[accepted.TurnID].Mode != mode || !reflect.DeepEqual(c.execution.settings, originalSettings) {
						t.Fatal("original mode/settings/turn changed")
					}
				}
				fixtureSignal(t, c, "delta", map[string]any{})
				response := nextKind(t, c, MessageCompletedEvent)
				if response.Message == nil || response.Message.Text != "한글 🐦" || response.TurnID != accepted.TurnID {
					t.Fatal(response)
				}
				fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
				terminal := nextKind(t, c, TurnCompletedEvent)
				nextKind(t, c, ThreadStatusEvent)
				if terminal.TurnID != accepted.TurnID || terminal.Turn == nil || terminal.Turn.Status != TurnCompleted || c.execution.active != "" || c.problem != nil {
					t.Fatal("original completion failed", terminal, c.problem)
				}
				sends := requestsOf(t, capture, "turn/start")
				if len(sends) != 1 || len(requestsOf(t, capture, "turn/steer")) != 0 || len(requestsOf(t, capture, "skills/list")) != 0 || len(requestsOf(t, capture, "skills/extraRoots/set")) != 0 {
					t.Fatal("passive signal caused native work")
				}
				collaboration := sends[0]["collaborationMode"].(map[string]any)
				want := "default"
				if mode == domain.PlanMode {
					want = "plan"
				}
				if collaboration["mode"] != want {
					t.Fatal("sent mode changed", collaboration)
				}
				if resumed && len(requestsOf(t, capture, "thread/turns/list")) != 1 {
					t.Fatal("original history verification changed")
				}
			})
		}
	}
}

func TestSkillsChangedCannotRefreshMutatedSelectedSnapshot(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	source := filepath.Join(home, ".agents", "skills", "add-issue")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: add-issue\ndescription: fixture\n---\nOriginal marker\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := skills.Manager{Root: t.TempDir(), Home: home}
	scope := domain.SkillReadRequest{MachineID: domain.NewID(), AgentID: domain.NewID(), ActorID: domain.NewID(), AgentRevision: 1, WorkerDeviceID: domain.NewID(), WorkerInstanceID: domain.NewID()}
	inventory, err := m.List(ctx, scope, nil)
	if err != nil || len(inventory.Entries) != 1 {
		t.Fatal(inventory, err)
	}
	entry := inventory.Entries[0]
	scope.Selections = []domain.SkillBinding{{WorkerDeviceID: entry.WorkerDeviceID, InventoryID: entry.InventoryID, SkillID: entry.SkillID, ContentRevision: entry.ContentRevision, SnapshotID: domain.NewID()}}
	if err = m.Prepare(ctx, scope); err != nil {
		t.Fatal(err)
	}
	c, capture, _, _ := boundTurnFixture(t, "normal")
	c.skillsRoot = m.Root
	in := input(domain.ExecuteMode)
	in.Skills = scope.Selections
	if _, err = c.StartTurn(ctx, domain.NewID(), domain.NewID(), in); err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, MessageCompletedEvent)
	proofs, err := c.SkillInputProofs(ctx)
	if err != nil || len(proofs) != 1 {
		t.Fatal(proofs, err)
	}
	selected, err := m.Resolve(ctx, scope.Selections)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(selected[0].Path, []byte("mutated immutable snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		fixtureSignal(t, c, "notify", map[string]any{"method": "skills/changed", "params": map[string]any{}})
		event := nextKind(t, c, MetadataEvent)
		if event.Metadata != SkillsChangedDiscarded {
			t.Fatal(event)
		}
	}
	after, err := c.SkillInputProofs(ctx)
	if err != nil || !reflect.DeepEqual(proofs, after) {
		t.Fatal("original skill proof changed", after, err)
	}
	fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
	nextKind(t, c, TurnCompletedEvent)
	nextKind(t, c, ThreadStatusEvent)
	if _, err = c.StartTurn(ctx, domain.NewID(), domain.NewID(), in); err == nil {
		t.Fatal("notification admitted changed selected package")
	}
	if len(requestsOf(t, capture, "turn/start")) != 1 || len(requestsOf(t, capture, "skills/list")) != 1 || len(requestsOf(t, capture, "skills/extraRoots/set")) != 1 {
		t.Fatal("notification refreshed or resent selected package")
	}
}

func TestSkillsChangedInvalidPayloadRetainsAcceptedInputRecovery(t *testing.T) {
	for _, payload := range []any{nil, []any{}, "invalid", map[string]any{"unknown": true}} {
		t.Run(string(mustMarshalSkillSignal(t, payload)), func(t *testing.T) {
			c, capture, _, _ := boundTurnFixture(t, "normal")
			accepted, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.PlanMode))
			if err != nil {
				t.Fatal(err)
			}
			nextKind(t, c, MessageCompletedEvent)
			fixtureSignal(t, c, "notify", map[string]any{"method": "skills/changed", "params": payload})
			_, err = c.NextEvent(context.Background())
			assertCode(t, err, domain.RecoveryRequired)
			fixtureSignal(t, c, "notify", map[string]any{"method": "skills/changed", "params": map[string]any{}})
			event, err := c.NextEvent(context.Background())
			if err != nil || event.Metadata != SkillsChangedDiscarded || c.problem == nil || !c.execution.paused {
				t.Fatal("later metadata cleared recovery", event, err)
			}
			_, err = c.Steer(context.Background(), domain.NewID(), domain.NewID(), accepted.TurnID, input(domain.PlanMode))
			assertCode(t, err, domain.RecoveryRequired)
			_, err = c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.PlanMode))
			assertCode(t, err, domain.RecoveryRequired)
			if len(requestsOf(t, capture, "turn/start")) != 1 || len(requestsOf(t, capture, "turn/steer")) != 0 || len(requestsOf(t, capture, "skills/list")) != 0 {
				t.Fatal("recovery input was replayed")
			}
		})
	}
}
func mustMarshalSkillSignal(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
