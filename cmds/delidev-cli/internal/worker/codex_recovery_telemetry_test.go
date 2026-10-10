// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"testing"
)

func TestCodexNativeRetryAndAuthRecoveryDoNotReplaceInputOrTerminal(t *testing.T) {
	for _, retry := range []bool{true, false} {
		c, rpc := codexTerminalStatusFixture(t)
		before := len(rpc.events)
		input := c.acceptedInputs[0]
		ctx := context.Background()
		for _, kind := range []codex.MetadataKind{codex.AuthRecoveryStartedObserved, codex.AuthRecoveryCompletedObserved} {
			handled, err := c.PublishCore(ctx, codex.Event{Kind: codex.MetadataEvent, Metadata: kind, ThreadID: c.thread, TurnID: c.turn, Correlated: true})
			if !handled || err != nil || len(rpc.events) != before {
				t.Fatal("auth recovery published authority", handled, err)
			}
		}
		handled, err := c.PublishCore(ctx, codex.Event{Kind: codex.NoticeEvent, Notice: domain.NativeWarning, NativeError: &codex.NativeErrorObservation{WillRetry: retry}, ThreadID: c.thread, TurnID: c.turn, Correlated: true})
		if !handled || err != nil || c.finished || c.blocked || len(c.acceptedInputs) != 1 || c.acceptedInputs[0] != input || len(rpc.events) != before+1 || func() bool {
			var observed domain.ExecutionEvent
			return domain.Decode(rpc.events[len(rpc.events)-1], &observed) != nil || observed.Kind != domain.ExecutionNoticeObserved
		}() {
			t.Fatal("error replaced accepted input or result", handled, err)
		}
		status := codex.TurnCompleted
		var problem *domain.Error
		if !retry {
			status = codex.TurnFailed
			problem = domain.Fail(domain.Unavailable, "Native failed.", "Reconcile explicitly.")
		}
		terminal := codex.Event{Kind: codex.TurnCompletedEvent, ThreadID: c.thread, TurnID: c.turn, Correlated: true, Turn: &codex.Turn{ID: c.turn, Status: status, Problem: problem}}
		handled, err = c.PublishCore(ctx, terminal)
		if !handled || err != nil || !c.finished || len(rpc.events) != before+2 {
			t.Fatal("native terminal lost", handled, err)
		}
		if handled, _ := c.PublishCore(ctx, terminal); handled || len(rpc.events) != before+2 {
			t.Fatal("terminal replay produced another result")
		}
	}
}

func TestCodexAuthRecoveryRejectsUnscopedForeignAndTerminalReplay(t *testing.T) {
	for _, scope := range []string{"unscoped", "foreign", "uncorrelated", "finished", "blocked"} {
		c, rpc := codexTerminalStatusFixture(t)
		before := len(rpc.events)
		event := codex.Event{Kind: codex.MetadataEvent, Metadata: codex.AuthRecoveryCompletedObserved, Correlated: true, ThreadID: c.thread, TurnID: c.turn}
		switch scope {
		case "unscoped":
			event.TurnID = ""
		case "foreign":
			event.TurnID = domain.NewID()
		case "uncorrelated":
			event.Correlated = false
		case "finished":
			c.finished = true
		case "blocked":
			c.blocked = true
		}
		handled, err := c.PublishCore(context.Background(), event)
		if handled || err == nil || len(rpc.events) != before {
			t.Fatal("recovery replay gained authority", scope)
		}
	}
}
