// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"testing"
)

func TestCodexThreadMetadataNeverPublishesOrDispatches(t *testing.T) {
	for _, kind := range []codex.MetadataKind{codex.ThreadMetadataDiscarded, codex.ThreadContextSupplementDiscarded} {
		c, rpc := codexTerminalStatusFixture(t)
		before := len(rpc.events)
		event := codex.Event{Kind: codex.MetadataEvent, Metadata: kind, Correlated: true, ThreadID: c.thread}
		for n := 0; n < 3; n++ {
			if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil || c.finished || c.blocked || len(rpc.events) != before {
				t.Fatal("passive native metadata changed original input", handled, err)
			}
		}
		terminal := codex.Event{Kind: codex.TurnCompletedEvent, Correlated: true, ThreadID: c.thread, TurnID: c.turn, Turn: &codex.Turn{ID: c.turn, Status: codex.TurnCompleted}}
		if handled, err := c.PublishCore(context.Background(), terminal); !handled || err != nil || !c.finished || len(rpc.events) != before+1 {
			t.Fatal("original completion lost or duplicated", handled, err)
		}
	}
}
func TestCodexThreadMetadataCannotClearRecoveryOrForeignScope(t *testing.T) {
	for _, state := range []string{"foreign", "uncorrelated", "blocked", "unknown"} {
		c, rpc := codexTerminalStatusFixture(t)
		before := len(rpc.events)
		event := codex.Event{Kind: codex.MetadataEvent, Metadata: codex.ThreadMetadataDiscarded, Correlated: true, ThreadID: c.thread}
		switch state {
		case "foreign":
			event.ThreadID = domain.NewID()
		case "uncorrelated":
			event.Correlated = false
		case "blocked":
			c.blocked = true
		case "unknown":
			event.Metadata = "unknown"
		}
		if handled, err := c.PublishCore(context.Background(), event); handled || c.finished || len(rpc.events) != before {
			t.Fatal("metadata relaxed ownership/recovery", state, err)
		}
	}
}
