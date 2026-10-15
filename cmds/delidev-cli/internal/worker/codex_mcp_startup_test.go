// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func TestCodexProjectMCPStartupDoesNotCompleteOrPublish(t *testing.T) {
	for _, scenario := range []string{"ordinary", "foreign-thread", "uncorrelated", "blocked"} {
		t.Run(scenario, func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			before := len(rpc.events)
			event := codex.Event{Kind: codex.MetadataEvent, Metadata: codex.MCPStartupObserved, Correlated: true, ThreadID: c.thread}
			switch scenario {
			case "foreign-thread":
				event.ThreadID = domain.NewID()
			case "uncorrelated":
				event.Correlated = false
			case "blocked":
				c.blocked = true
			}
			handled, err := c.PublishCore(context.Background(), event)
			if scenario == "ordinary" {
				if err != nil || !handled {
					t.Fatal("validated startup blocked execution", err)
				}
			} else if handled {
				t.Fatal("unowned startup gained publication authority")
			}
			if len(rpc.events) != before || c.finished || scenario == "ordinary" && c.blocked || scenario == "blocked" && !c.blocked {
				t.Fatal("passive startup changed execution state")
			}
			if scenario == "ordinary" {
				terminal := codex.Event{Kind: codex.TurnCompletedEvent, Correlated: true, ThreadID: c.thread, TurnID: c.turn, Turn: &codex.Turn{ID: c.turn, Status: codex.TurnCompleted}}
				if handled, err := c.PublishCore(context.Background(), terminal); !handled || err != nil || !c.finished {
					t.Fatal("original turn could not complete", err)
				}
			}
		})
	}
}
