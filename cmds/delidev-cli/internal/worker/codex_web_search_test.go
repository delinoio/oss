// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"testing"
)

func TestCodexWebSearchPublicationPreservesNativeEvidence(t *testing.T) {
	for _, mode := range []domain.WebSearchMode{domain.WebSearchCached, domain.WebSearchLive} {
		c, rpc := codexPublisherFixture(t)
		result := `[{"opaque":9007199254740993}]`
		native := &codex.Tool{ID: "native-search", Kind: codex.WebSearchTool, Status: codex.ToolRunning, WebSearch: &domain.CodexWebSearchObservation{Query: "Original", Mode: mode, Source: "openai", Results: &result}}
		event := codex.Event{Kind: codex.ToolStartedEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: native.ID, Tool: native, Correlated: true}
		if _, err := c.PublishCore(context.Background(), event); err != nil {
			t.Fatal(err)
		}
		event.Kind = codex.ToolCompletedEvent
		native.Status = codex.ToolCompleted
		if _, err := c.PublishCore(context.Background(), event); err != nil {
			t.Fatal(err)
		}
		var first, last domain.ExecutionEvent
		if domain.Decode(rpc.events[0], &first) != nil || domain.Decode(rpc.events[1], &last) != nil || first.Tool.ID != last.Tool.ID || *last.Tool.Snapshot.WebSearch.Results != result || last.Tool.Snapshot.WebSearch.Mode != mode {
			t.Fatal("search publication changed original identity or content")
		}
		before := len(rpc.events)
		if _, err := c.PublishCore(context.Background(), event); err == nil || len(rpc.events) != before {
			t.Fatal("replayed completion produced another observation")
		}
	}
}
