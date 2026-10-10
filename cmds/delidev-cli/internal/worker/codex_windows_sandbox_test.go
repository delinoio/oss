// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func TestWindowsSandboxDiscardedTelemetryCannotCompleteOriginalTurn(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	before := len(rpc.events)
	event := codex.Event{Kind: codex.MetadataEvent, Metadata: codex.WindowsSandboxSetupDiscarded, Correlated: true, ThreadID: c.thread, WindowsSandboxSetup: &codex.WindowsSandboxSetupObservation{Mode: codex.WindowsSandboxElevated, Success: true}}
	for i := 0; i < 3; i++ {
		if handled, err := c.PublishCore(context.Background(), event); err != nil || !handled || c.finished || c.blocked || len(rpc.events) != before {
			t.Fatal("setup telemetry gained authority", handled, err)
		}
	}
	event.Metadata = codex.WindowsSandboxWarningDiscarded
	event.WindowsSandboxSetup = nil
	event.WindowsSandboxWarning = &codex.WindowsSandboxWarning{Classification: codex.WindowsWorldWritablePaths, SampleCount: 1}
	if handled, err := c.PublishCore(context.Background(), event); err != nil || !handled || len(rpc.events) != before {
		t.Fatal("warning replay published", handled, err)
	}
	event.Kind, event.Metadata, event.Notice = codex.NoticeEvent, "", domain.NativeWarning
	if handled, err := c.PublishCore(context.Background(), event); err != nil || !handled || len(rpc.events) != before+1 || c.finished {
		t.Fatal("generic warning failed", handled, err)
	}
	terminal := codex.Event{Kind: codex.TurnCompletedEvent, Correlated: true, ThreadID: c.thread, TurnID: c.turn, Turn: &codex.Turn{ID: c.turn, Status: codex.TurnCompleted}}
	if handled, err := c.PublishCore(context.Background(), terminal); !handled || err != nil || !c.finished {
		t.Fatal("original completion failed", handled, err)
	}
}

func TestWindowsSandboxTelemetryRetainsPublicationFences(t *testing.T) {
	for _, kind := range []string{"foreign-thread", "foreign-turn", "uncorrelated", "blocked", "invalid-mode", "missing-observation"} {
		t.Run(kind, func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			before := len(rpc.events)
			event := codex.Event{Kind: codex.MetadataEvent, Metadata: codex.WindowsSandboxSetupDiscarded, Correlated: true, ThreadID: c.thread, WindowsSandboxSetup: &codex.WindowsSandboxSetupObservation{Mode: codex.WindowsSandboxUnelevated, Success: true}}
			switch kind {
			case "foreign-thread":
				event.ThreadID = domain.NewID()
			case "foreign-turn":
				event.TurnID = domain.NewID()
			case "uncorrelated":
				event.Correlated = false
			case "blocked":
				c.blocked = true
			case "invalid-mode":
				event.WindowsSandboxSetup.Mode = "unknown"
			case "missing-observation":
				event.WindowsSandboxSetup = nil
			}
			handled, err := c.PublishCore(context.Background(), event)
			if handled || len(rpc.events) != before || c.finished {
				t.Fatal("unowned telemetry gained authority", handled, err)
			}
		})
	}
}
