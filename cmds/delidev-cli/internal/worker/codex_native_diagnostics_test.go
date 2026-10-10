// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func TestCodexDiagnosticMetadataWaitsForOneOriginalResult(t *testing.T) {
	for _, status := range []codex.TurnStatus{codex.TurnCompleted, codex.TurnFailed} {
		t.Run(string(status), func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			before := len(rpc.events)
			event := codex.Event{Kind: codex.MetadataEvent, Metadata: codex.NativeDiagnosticObserved, Correlated: true, ThreadID: c.thread, TurnID: c.turn}
			for i := 0; i < 3; i++ {
				if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil || c.finished || c.blocked || len(rpc.events) != before {
					t.Fatal("native diagnostic published or settled input", err)
				}
			}
			turn := &codex.Turn{ID: c.turn, Status: status}
			if status == codex.TurnFailed {
				turn.Problem = domain.Fail(domain.Unavailable, "The native Codex turn failed.", "Reconcile native state explicitly.")
			}
			terminal := codex.Event{Kind: codex.TurnCompletedEvent, Correlated: true, ThreadID: c.thread, TurnID: c.turn, Turn: turn}
			if handled, err := c.PublishCore(context.Background(), terminal); !handled || err != nil || !c.finished || len(rpc.events) != before+1 {
				t.Fatal("native outcome lost or duplicated", err)
			}
			var published domain.ExecutionEvent
			if err := json.Unmarshal(rpc.events[len(rpc.events)-1], &published); err != nil {
				t.Fatal(err)
			}
			want := domain.ExecutionSucceeded
			if status == codex.TurnFailed {
				want = domain.ExecutionFailed
			}
			if published.Kind != domain.ExecutionTurnFinished || published.Outcome != want {
				t.Fatal("diagnostic replaced the native outcome")
			}
			// Turn-scoped terminal telemetry cannot reopen or republish this result.
			event.Late = true
			if handled, err := c.PublishCore(context.Background(), event); handled || err == nil || !c.blocked || len(rpc.events) != before+1 {
				t.Fatal("late diagnostic reopened original result", err)
			}
		})
	}
}

func TestCodexDiagnosticMetadataPreservesOwnershipAndRecoveryFences(t *testing.T) {
	for _, scenario := range []string{"foreign-thread", "foreign-turn", "uncorrelated", "blocked", "extension"} {
		t.Run(scenario, func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			before := len(rpc.events)
			event := codex.Event{Kind: codex.MetadataEvent, Metadata: codex.NativeDiagnosticObserved, Correlated: true, ThreadID: c.thread, TurnID: c.turn}
			switch scenario {
			case "foreign-thread":
				event.ThreadID = domain.NewID()
			case "foreign-turn":
				event.TurnID = domain.NewID()
			case "uncorrelated":
				event.Correlated = false
			case "blocked":
				c.blocked = true
			case "extension":
				event.Kind = codex.NativeExtensionEvent
			}
			if handled, err := c.PublishCore(context.Background(), event); handled || c.finished || len(rpc.events) != before {
				t.Fatal("foreign/recovery diagnostic gained authority", err)
			}
			if scenario == "blocked" && !c.blocked {
				t.Fatal("diagnostic cleared recovery")
			}
		})
	}
}
