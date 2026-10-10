// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func TestShellEmptyAcknowledgmentDoesNotContainTurnOutcome(t *testing.T) {
	for _, raw := range []string{`null`, `{"turnId":"replacement"}`, `[]`} {
		if emptyShellResponse([]byte(raw)) {
			t.Fatalf("accepted %s", raw)
		}
	}
	if !emptyShellResponse([]byte(`{}`)) {
		t.Fatal("empty upstream response rejected")
	}
}

func TestNativeShellOwnsExactTurnAndUserProcess(t *testing.T) {
	c, _ := observationClient()
	request, turn := domain.NewID(), domain.NewID()
	c.execution.active = ""
	c.execution.activeShell = request
	c.execution.shells = map[domain.ID]*shellAttempt{request: {observation: ShellObservation{RequestID: request, ThreadID: c.thread, Delivery: ShellAcknowledged}, command: "cat example.txt", items: map[string]ToolStatus{}}}
	e, err := observeFixture(c, "turn/started", map[string]any{"threadId": c.thread, "turn": fixtureTurn(turn, TurnRunning)})
	if err != nil || e.Kind != NativeShellEvent || e.Shell.TurnID != turn {
		t.Fatalf("original shell turn lost: %v", err)
	}
	item := commandFixture()
	item["source"] = "agent"
	params := map[string]any{"threadId": c.thread, "turnId": turn, "item": item, "startedAtMs": 1}
	if _, err := observeFixture(c, "item/started", params); err == nil {
		t.Fatal("agent command synthesized shell authority")
	}
	item["source"] = "userShell"
	e, err = observeFixture(c, "item/started", params)
	if err != nil || e.Tool == nil || e.Tool.Command.Source != UserShellCommand {
		t.Fatalf("original user shell item rejected: %v", err)
	}
	if _, err := observeFixture(c, "turn/completed", map[string]any{"threadId": c.thread, "turn": fixtureTurn(turn, TurnCompleted)}); err == nil {
		t.Fatal("root completion ignored running original child")
	}
}
