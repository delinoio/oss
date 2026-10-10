// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func TestCodexGatewayMetadataCannotPublishOrSettleInput(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	before := len(rpc.events)
	event := codex.Event{Kind: codex.MetadataEvent, Metadata: codex.GatewayOAuthStatusDiscarded, Correlated: true, ThreadID: c.thread}
	for i := 0; i < 3; i++ {
		if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil || c.finished || c.blocked || len(rpc.events) != before {
			t.Fatal("gateway metadata changed original input publication", handled, err)
		}
	}
	event.ThreadID = domain.NewID()
	if handled, err := c.PublishCore(context.Background(), event); handled || err == nil || len(rpc.events) != before {
		t.Fatal("foreign gateway metadata gained original execution authority", handled, err)
	}
}
