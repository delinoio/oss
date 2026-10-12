// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func TestCodexWebSearchPublicationPreservesNativeEvidence(t *testing.T) {
	for _, mode := range []domain.WebSearchMode{domain.WebSearchCached, domain.WebSearchLive} {
		c, rpc := codexTerminalStatusFixture(t)
		start := len(rpc.events)
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
		if domain.Decode(rpc.events[start], &first) != nil || domain.Decode(rpc.events[start+1], &last) != nil || first.Tool.ID != last.Tool.ID || *last.Tool.Snapshot.WebSearch.Results != result || last.Tool.Snapshot.WebSearch.Mode != mode {
			t.Fatal("search publication changed original identity or content")
		}
		before := len(rpc.events)
		if _, err := c.PublishCore(context.Background(), event); err == nil || len(rpc.events) != before {
			t.Fatal("replayed completion produced another observation")
		}
	}
}

func TestCodexWebSearchLostAcknowledgementReplaysOnlyExactOutbox(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	raw := `[{"original":9007199254740993}]`
	native := &codex.Tool{ID: "original-search", Kind: codex.WebSearchTool, Status: codex.ToolRunning, WebSearch: &domain.CodexWebSearchObservation{Query: "Original", Mode: domain.WebSearchLive, Source: "openai", Results: &raw}}
	event := codex.Event{Kind: codex.ToolStartedEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: native.ID, Tool: native, Correlated: true}
	rpc.lose = true
	if _, err := c.PublishCore(context.Background(), event); err == nil || c.publisher.state.Pending == nil || !c.blocked {
		t.Fatal("lost acknowledgement did not retain original observation")
	}
	last := len(rpc.events) - 1
	rpc.lose = false
	if err := c.publisher.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rpc.requests[last] != rpc.requests[last+1] || !bytes.Equal(rpc.events[last], rpc.events[last+1]) || c.publisher.state.Pending != nil {
		t.Fatal("recovery changed original observation identity or bytes")
	}
	before := len(rpc.events)
	if _, err := c.PublishCore(context.Background(), event); err == nil || len(rpc.events) != before {
		t.Fatal("native search observation was retransmitted")
	}
}
