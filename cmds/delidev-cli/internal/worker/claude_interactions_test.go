package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

func claudeInteractionObservation(c *ClaudeContentPublisher) claude.LifecycleObservation {
	tool := c.tools["tool_original_one"].content
	arrival := domain.NewID()
	request := &claude.NativeInteraction{Kind: claude.ToolPermission, ArrivalID: arrival, RequestID: "request_original", ToolID: tool.Reference.NativeID, ToolName: tool.Reference.Name, Input: json.RawMessage(tool.Proposal.Applied)}
	return claude.LifecycleObservation{Kind: claude.InteractionObserved, SessionID: c.binding.journal.SessionID, InputID: c.binding.journal.InputID, TurnID: c.binding.turn, Accepted: true, Interaction: &claude.InteractionObservation{Kind: claude.InteractionRequested, ArrivalID: arrival, InputID: c.binding.journal.InputID, TurnID: c.binding.turn, Request: request}}
}
func TestClaudeInteractionPublicationRetainsOriginalReceiptsAndCancellation(t *testing.T) {
	c, rpc, _ := claudeToolFixture(t, "")
	ctx := context.Background()
	o := claudeInteractionObservation(c)
	for _, cancel := range []bool{false, true} {
		if cancel {
			o.Interaction.Kind = claude.InteractionCanceled
			o.Interaction.Canceled = true
			o.Interaction.Request = nil
		}
		rpc.lose = true
		handled, err := c.PublishInteractionObservation(ctx, o)
		if !handled || err == nil {
			t.Fatal("lost original callback acknowledgment disappeared")
		}
		last := len(rpc.events) - 1
		rpc.lose = false
		if err := c.ReplayPending(ctx); err != nil {
			t.Fatal(err)
		}
		if rpc.requests[last] != rpc.requests[last+1] || !bytes.Equal(rpc.events[last], rpc.events[last+1]) {
			t.Fatal("callback retry changed original receipt")
		}
		value := c.interactions[o.Interaction.ArrivalID]
		if value.closed != cancel || c.interactionsSettled() != cancel {
			t.Fatal("callback closure was inferred or lost")
		}
		if cancel && value.update.Claude != nil {
			t.Fatal("closed request bytes retained in Worker")
		}
	}
}
func TestClaudeInteractionPublicationRejectsChangedOriginalOwnership(t *testing.T) {
	for _, change := range []string{"session", "input", "turn", "arrival", "tool", "applied", "child", "caller", "duplicate", "overlap", "premature-cancel"} {
		t.Run(change, func(t *testing.T) {
			c, rpc, _ := claudeToolFixture(t, "")
			o := claudeInteractionObservation(c)
			if change == "duplicate" || change == "overlap" {

				if _, err := c.PublishInteractionObservation(context.Background(), o); err != nil {
					t.Fatal(err)
				}
			}
			switch change {
			case "session":
				o.SessionID = domain.NewID()
			case "input":
				o.InputID = domain.NewID()
			case "turn":
				o.TurnID = string(domain.NewID())
			case "arrival":
				o.Interaction.Request.ArrivalID = domain.NewID()
			case "tool":
				o.Interaction.Request.ToolID = "foreign"
			case "applied":
				o.Interaction.Request.Input = json.RawMessage(`{}`)
			case "child":
				o.Interaction.Request.ParentToolID = "child"
			case "caller":
				o.Interaction.Request.CalledBy.ToolID = "parent"
			case "overlap":
				o.Interaction.ArrivalID = domain.NewID()
				o.Interaction.Request.ArrivalID = o.Interaction.ArrivalID
				o.Interaction.Request.RequestID = "new-request"
			case "premature-cancel":
				o.Interaction.Request = nil
				o.Interaction.Kind = claude.InteractionCanceled
				o.Interaction.Canceled = true
			}
			before := len(rpc.events)
			if change == "session" {
				_, err := c.PublishInteractionObservation(context.Background(), o)
				if err != nil {
					t.Fatal("session metadata blocked observation", err)
				}
				return
			}
			if _, err := c.PublishInteractionObservation(context.Background(), o); err == nil || len(rpc.events) != before {
				t.Fatal("invalid callback acquired publication authority")
			}
		})
	}
}
