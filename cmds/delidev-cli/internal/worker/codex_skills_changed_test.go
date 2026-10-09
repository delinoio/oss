// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func TestCodexSkillsChangedMetadataIsDiscardedWithoutPublication(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	ctx := context.Background()
	before := len(rpc.events)
	// Process metadata intentionally has no native turn identity. It still binds
	// to the already accepted original execution and cannot complete its input.
	event := codex.Event{Kind: codex.MetadataEvent, Metadata: codex.SkillsChangedDiscarded, Correlated: true, ThreadID: c.thread}
	for i := 0; i < 3; i++ {
		handled, err := c.PublishCore(ctx, event)
		if err != nil || !handled || c.finished || c.blocked || len(rpc.events) != before {
			t.Fatal("passive metadata changed publication", handled, err)
		}
	}
	terminal := codex.Event{Kind: codex.TurnCompletedEvent, Correlated: true, ThreadID: c.thread, TurnID: c.turn, Turn: &codex.Turn{ID: c.turn, Status: codex.TurnCompleted}}
	if handled, err := c.PublishCore(ctx, terminal); !handled || err != nil || !c.finished {
		t.Fatal("original turn did not complete", handled, err)
	}
	completed := len(rpc.events)
	if completed != before+1 {
		t.Fatal("unexpected completion publication", before, completed)
	}
	if handled, err := c.PublishCore(ctx, event); !handled || err != nil || len(rpc.events) != completed {
		t.Fatal("late process metadata changed completed input", handled, err)
	}
}
func TestCodexSkillsChangedCannotRelaxForeignOrRecoveryFences(t *testing.T) {
	for _, kind := range []string{"foreign-thread", "foreign-turn", "uncorrelated", "blocked", "extension", "unknown-metadata"} {
		t.Run(kind, func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			before := len(rpc.events)
			event := codex.Event{Kind: codex.MetadataEvent, Metadata: codex.SkillsChangedDiscarded, Correlated: true, ThreadID: c.thread}
			switch kind {
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
			case "unknown-metadata":
				event.Metadata = "unknown"
			}
			handled, err := c.PublishCore(context.Background(), event)
			if handled || len(rpc.events) != before || c.finished {
				t.Fatal("unowned metadata gained authority", handled, err)
			}
			if kind == "blocked" && !c.blocked {
				t.Fatal("metadata cleared recovery")
			}
		})
	}
}
