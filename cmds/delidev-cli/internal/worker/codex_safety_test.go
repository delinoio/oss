package worker

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"testing"
)

func TestCodexSafetyMetadataCannotGrantPublicationOrModelSuccess(t *testing.T) {
	for _, kind := range []codex.MetadataKind{codex.ModelVerificationObserved, codex.SafetyBufferingObserved, codex.ModerationMetadataObserved, codex.ModelRerouteObserved} {
		c, rpc := codexTerminalStatusFixture(t)
		before := len(rpc.events)
		e := codex.Event{Kind: codex.MetadataEvent, Metadata: kind, Correlated: true, ThreadID: c.thread, TurnID: c.turn}
		handled, err := c.PublishCore(context.Background(), e)
		reroute := kind == codex.ModelRerouteObserved
		if !handled || (err != nil) != reroute || c.blocked != reroute || c.finished || len(rpc.events) != before {
			t.Fatal("metadata granted authority or lost reroute fence")
		}
		terminal := codex.Event{Kind: codex.TurnCompletedEvent, Correlated: true, ThreadID: c.thread, TurnID: c.turn, Turn: &codex.Turn{ID: c.turn, Status: codex.TurnCompleted}}
		_, err = c.PublishCore(context.Background(), terminal)
		if (err == nil) == reroute || c.finished == reroute {
			t.Fatal("original completion/model fence lost")
		}
	}
}
