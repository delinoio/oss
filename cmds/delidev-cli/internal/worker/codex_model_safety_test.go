// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func TestCodexModelSafetyMetadataHasNoPublicationAuthority(t *testing.T) {
	for _, scenario := range []string{"owned", "foreign-thread", "foreign-turn", "late", "uncorrelated", "blocked"} {
		t.Run(scenario, func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			before := len(rpc.events)
			event := codex.Event{Kind: codex.MetadataEvent, Metadata: codex.ModelSafetyObserved, Correlated: true, ThreadID: c.thread, TurnID: c.turn}
			switch scenario {
			case "foreign-thread":
				event.ThreadID = domain.NewID()
			case "foreign-turn":
				event.TurnID = domain.NewID()
			case "late":
				event.Late = true
			case "uncorrelated":
				event.Correlated = false
			case "blocked":
				c.blocked = true
			}
			handled, err := c.PublishCore(context.Background(), event)
			if scenario == "owned" {
				if !handled || err != nil || c.blocked {
					t.Fatal("owned metadata rejected", err)
				}
				terminal := codex.Event{Kind: codex.TurnCompletedEvent, Correlated: true, ThreadID: c.thread, TurnID: c.turn, Turn: &codex.Turn{ID: c.turn, Status: codex.TurnCompleted}}
				if len(rpc.events) != before || c.finished {
					t.Fatal("metadata published or settled input")
				}
				if handled, err := c.PublishCore(context.Background(), terminal); !handled || err != nil || !c.finished || len(rpc.events) != before+1 {
					t.Fatal("original completion changed", err)
				}
			} else if handled || err == nil || !c.blocked || c.finished || len(rpc.events) != before {
				t.Fatal("metadata relaxed original fence", err)
			}
		})
	}
}
