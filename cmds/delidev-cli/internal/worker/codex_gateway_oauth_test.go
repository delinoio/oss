// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func TestCodexGatewayOAuthCannotPublishOrCompleteInput(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	before := len(rpc.events)
	event := codex.Event{Kind: codex.MetadataEvent, Metadata: codex.GatewayOAuthStatusDiscarded, Correlated: true, ThreadID: c.thread}
	for i := 0; i < 4; i++ {
		if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil || len(rpc.events) != before || c.finished || c.blocked {
			t.Fatal("passive gateway observation changed publication or completion", handled, err)
		}
	}
	terminal := codex.Event{Kind: codex.TurnCompletedEvent, ThreadID: c.thread, TurnID: c.turn, Correlated: true, Turn: &codex.Turn{ID: c.turn, Status: codex.TurnCompleted}}
	if handled, err := c.PublishCore(context.Background(), terminal); !handled || err != nil || !c.finished || len(rpc.events) != before+1 {
		t.Fatal("original turn completion was not retained", handled, err)
	}
	if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil || len(rpc.events) != before+1 {
		t.Fatal("late gateway metadata changed completion", handled, err)
	}
}

func TestCodexGatewayOAuthPreservesForeignAndRecoveryFences(t *testing.T) {
	for _, kind := range []string{"foreign-thread", "foreign-turn", "uncorrelated", "blocked", "extension"} {
		t.Run(kind, func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			before := len(rpc.events)
			event := codex.Event{Kind: codex.MetadataEvent, Metadata: codex.GatewayOAuthStatusDiscarded, Correlated: true, ThreadID: c.thread}
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
			}
			if handled, _ := c.PublishCore(context.Background(), event); handled || len(rpc.events) != before || c.finished {
				t.Fatal("unowned gateway status gained product authority")
			}
			if kind == "blocked" && !c.blocked {
				t.Fatal("gateway status cleared recovery")
			}
		})
	}
}
