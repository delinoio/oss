// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func TestCurrentTimeReplyPublishesNoInputOrInteractionAuthority(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	before := len(rpc.events)
	event := codex.Event{Kind: codex.CurrentTimeRepliedEvent, Correlated: true, ThreadID: c.thread}
	if handled, err := c.PublishCore(context.Background(), event); err != nil || !handled || c.finished || c.blocked || len(rpc.events) != before {
		t.Fatal("clock response changed input publication", handled, err)
	}
	c.finished = true
	if handled, err := c.PublishCore(context.Background(), event); err != nil || !handled || len(rpc.events) != before {
		t.Fatal("technical response rewrote completed input", handled, err)
	}
	event.TurnID = c.turn
	if handled, err := c.PublishCore(context.Background(), event); err == nil || handled {
		t.Fatal("clock response acquired native turn authority")
	}
}
