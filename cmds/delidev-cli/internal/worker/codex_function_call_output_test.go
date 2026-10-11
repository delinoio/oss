// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"testing"
)

func TestFunctionCallOutputOneObservationNoAuthority(t *testing.T) {
	for _, change := range []string{"duplicate", "thread", "turn", "late"} {
		t.Run(change, func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			text := "inert"
			event := codex.Event{Kind: codex.FunctionCallOutputEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: "result", Correlated: true, FunctionCallOutput: &domain.FunctionCallOutputObservation{Name: "tool", Text: &text}}
			before := len(rpc.events)
			if handled, err := c.PublishCore(context.Background(), event); err != nil || !handled || c.finished || len(rpc.events) != before+1 {
				t.Fatal("missing independent evidence", err)
			}
			switch change {
			case "thread":
				event.ThreadID = domain.NewID()
			case "turn":
				event.TurnID = domain.NewID()
			case "late":
				event.Late = true
			}
			before = len(rpc.events)
			if _, err := c.PublishCore(context.Background(), event); err == nil || len(rpc.events) != before {
				t.Fatal("duplicate/foreign evidence published")
			}
		})
	}
}
