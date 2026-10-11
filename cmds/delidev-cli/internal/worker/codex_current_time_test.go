// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func TestCodexCurrentTimeTransmissionHasNoPublicationAuthority(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	before := len(rpc.events)
	event := codex.Event{Kind: codex.MetadataEvent, Metadata: codex.CurrentTimeReplySent, Correlated: true, ThreadID: c.thread}
	if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil || len(rpc.events) != before || c.finished || c.blocked {
		t.Fatal("technical response acknowledged or completed product input", handled, err)
	}
}
