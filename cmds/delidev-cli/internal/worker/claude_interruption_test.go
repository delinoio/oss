package worker

import (
	"bytes"
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

func claudeInterruptionPublicationFixture(t *testing.T) (*ClaudeContentPublisher, *claudeReplyFixtureRPC, claude.LifecycleObservation, claude.LifecycleObservation) {
	t.Helper()
	c, rpc, native, control, echo := claudeReplyFixture(t, "")
	ctx := context.Background()
	// The shared tool fixture proposes two calls. Independently finish its
	// other call before the original callback's exclusive interruption claim.
	second := c.tools["tool_original_two"].content
	secondIndex := second.Index
	other := claudeContentObservation(c, claude.ContentEvent{Kind: claude.ToolResultObserved, Index: &secondIndex, ToolResult: &claude.NativeToolResult{ID: second.Reference.NativeID, Name: second.Reference.Name}})
	if _, err := c.PublishObservation(ctx, other); err != nil {
		t.Fatal(err)
	}
	message, interrupt := "Original interrupted denial", true
	rpc.reply = &domain.ClaudePermissionResponse{Behavior: domain.ClaudeReplyDeny, Message: &message, Interrupt: &interrupt}
	if err := c.DeliverApprovalResponse(ctx, ctx, control, native); err != nil {
		t.Fatal(err)
	}
	arrival := echo.Interaction.ArrivalID
	echo.Interaction.Kind, echo.Interaction.Request = claude.InteractionReplyEchoed, nil
	echo.Interaction.ReplyDigest = c.responses[arrival].journal.Native.BodyDigest
	if _, err := c.PublishInteractionObservation(ctx, echo); err != nil {
		t.Fatal(err)
	}
	r := c.interactions[arrival].update.Claude
	failed, index := true, r.Index
	tool := claudeContentObservation(c, claude.ContentEvent{Kind: claude.ToolResultObserved, MessageID: r.NativeMessageID, Index: &index, ToolResult: &claude.NativeToolResult{ID: r.Tool.NativeID, Name: r.Tool.Name, Error: &failed, NonExecution: &domain.ClaudeToolNonExecution{NativeID: r.Tool.NativeID, Kind: domain.ClaudeUserRejectedNonExecution}}})
	if _, err := c.PublishObservation(ctx, tool); err != nil {
		t.Fatal(err)
	}
	if c.interruption == nil || c.responses[arrival].input.Message != nil {
		t.Fatal("settlement failed to retain metadata or release response content")
	}
	text := domain.ClaudeDenialContextText
	content := claudeContentObservation(c, claude.ContentEvent{Kind: claude.NativeCallbackInterruptContext, CallbackArrivalID: arrival, Blocks: []claude.NativeContentBlock{{Kind: claude.TextBlock, Text: &text}}})
	content.Content[0].MessageID, content.Content[0].Model = "", ""
	result := claude.LifecycleObservation{Kind: claude.CallbackInterruptResultObserved, SessionID: content.SessionID, TurnID: content.TurnID, NativeID: string(domain.NewID()), CallbackArrivalID: arrival, Result: &claude.NativeResult{Kind: claude.ResultExecutionError, Reason: claude.AbortedTools, Error: true, Usage: &claude.ResultUsage{}}}
	return c, rpc, content, result
}

func TestClaudeInterruptionReplaysOnlyOriginalContextAndResultReceipts(t *testing.T) {
	c, rpc, content, result := claudeInterruptionPublicationFixture(t)
	for _, o := range []claude.LifecycleObservation{content, result} {
		before := len(rpc.events)
		rpc.lose = true
		if handled, err := c.PublishObservation(context.Background(), o); !handled || err == nil {
			t.Fatal("original acknowledgment was not lost")
		}
		rpc.lose = false
		if err := c.ReplayPending(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(rpc.events) != before+2 || rpc.requests[before] != rpc.requests[before+1] || !bytes.Equal(rpc.events[before], rpc.events[before+1]) {
			t.Fatal("interruption replay changed original event")
		}
		var e domain.ExecutionEvent
		if domain.Decode(rpc.events[before], &e) != nil || e.Kind != domain.ExecutionClaudeInterruptionObserved || e.ClaudeInterruption == nil || e.ClaudeInterruption.Observation.NativeEventID != o.NativeID {
			t.Fatal("native interruption identity lost")
		}
	}
	if c.interruption.contextID == "" || c.interruption.resultID == "" || !c.resultUsage || rpc.claims != 1 {
		t.Fatal("interruption lost closure or reclaimed a reply")
	}
}

func TestClaudeInterruptionRejectsForeignOrIncompleteNativeComposition(t *testing.T) {
	for _, scenario := range []string{"missing-settlement", "foreign-arrival", "foreign-turn", "changed-context", "extra-block", "mixed-provider", "before-context", "input", "accepted", "pending-task", "missing-usage", "duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			c, rpc, content, result := claudeInterruptionPublicationFixture(t)
			o := content
			switch scenario {
			case "missing-settlement":
				c.interruption = nil
			case "foreign-arrival":
				o.Content[0].CallbackArrivalID = domain.NewID()
			case "foreign-turn":
				o.TurnID = string(domain.NewID())
			case "changed-context":
				text := "Changed context"
				o.Content[0].Blocks[0].Text = &text
			case "extra-block":
				o.Content[0].Blocks = append(o.Content[0].Blocks, o.Content[0].Blocks[0])
			case "mixed-provider":
				o.Content[0].MessageID = "foreign_provider"
			case "before-context":
				o = result
			default:
				if _, err := c.PublishObservation(context.Background(), content); err != nil {
					t.Fatal(err)
				}
				o = result
				switch scenario {
				case "input":
					o.InputID = c.binding.journal.InputID
				case "accepted":
					o.Accepted = true
				case "pending-task":
					o.Result.KnownWork.PendingTasks = 1
				case "missing-usage":
					o.Result.Usage = nil
				case "duplicate":
					if _, err := c.PublishObservation(context.Background(), result); err != nil {
						t.Fatal(err)
					}
					o.NativeID = string(domain.NewID())
				}
			}
			before := len(rpc.events)
			if handled, err := c.PublishObservation(context.Background(), o); !handled || err == nil || len(rpc.events) != before {
				t.Fatal("invalid interruption partially published")
			}
		})
	}
}
