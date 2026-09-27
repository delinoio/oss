package worker

import (
	"bytes"
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

func TestClaudeCallbackSettlementDrainsOriginalResultsBeforeClosing(t *testing.T) {
	for _, echo := range []bool{false, true} {
		c, rpc, native, control, o := claudeReplyFixture(t, "")
		ctx := context.Background()
		if err := c.DeliverApprovalResponse(ctx, ctx, control, native); err != nil {
			t.Fatal(err)
		}
		arrival := o.Interaction.ArrivalID
		request := c.interactions[arrival].update.Claude
		if echo {
			o.Interaction.Kind, o.Interaction.Request = claude.InteractionReplyEchoed, nil
			o.Interaction.ReplyDigest = c.responses[arrival].journal.Native.BodyDigest
			if _, err := c.PublishInteractionObservation(ctx, o); err != nil {
				t.Fatal(err)
			}
		}
		index := request.Index
		result := claudeContentObservation(c, claude.ContentEvent{Kind: claude.ToolResultObserved, MessageID: request.NativeMessageID, Index: &index, ToolResult: &claude.NativeToolResult{ID: request.Tool.NativeID, Name: request.Tool.Name}})
		before := len(rpc.events)
		rpc.lose = true
		if _, err := c.PublishObservation(ctx, result); err == nil {
			t.Fatal("missing original acknowledgment accepted")
		}
		if c.interactionsSettled() {
			t.Fatal("pending publication closed the callback")
		}
		if !echo {
			if len(rpc.events) != before {
				t.Fatal("missing echo partially published result")
			}
			continue
		}
		rpc.lose = false
		if err := c.ReplayPending(ctx); err != nil {
			t.Fatal(err)
		}
		if !c.interactionsSettled() || len(rpc.events) != before+3 || rpc.requests[before] != rpc.requests[before+1] || !bytes.Equal(rpc.events[before], rpc.events[before+1]) {
			t.Fatal("result/settlement lost original receipt order")
		}
		var event domain.ExecutionEvent
		if domain.Decode(rpc.events[before+2], &event) != nil || event.Kind != domain.ExecutionClaudeCallbackSettled || event.ClaudeSettlement == nil || event.ClaudeSettlement.ArrivalID != arrival {
			t.Fatal("callback closure lost result evidence")
		}
		if err := c.DeliverApprovalResponse(ctx, ctx, control, native); err != nil || native.sends != 1 || rpc.claims != 1 {
			t.Fatal("closed callback sent response again", err)
		}
	}
}
