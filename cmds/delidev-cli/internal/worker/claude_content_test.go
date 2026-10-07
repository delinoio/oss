package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

func newClaudeContentFixture(t *testing.T) (*ClaudeContentPublisher, *openCodeBindingRPC) {
	t.Helper()
	b, rpc := newClaudeBindingFixture(t, domain.ExecuteMode)
	initialized, accepted, applied := claudeBindingObservations(b)
	claimClaudeInput(t, b)
	if err := b.BindSession(context.Background(), initialized, applied); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptInput(context.Background(), accepted); err != nil {
		t.Fatal(err)
	}
	c, err := OpenClaudeContentPublisher(b)
	if err != nil {
		t.Fatal(err)
	}
	return c, rpc
}

func claudeContentObservation(c *ClaudeContentPublisher, content claude.ContentEvent) claude.LifecycleObservation {
	content.MessageID, content.Model = "msg_original", c.binding.publisher.input.Configuration.NativeModel
	return claude.LifecycleObservation{Kind: claude.ContentObserved, SessionID: c.binding.journal.SessionID, InputID: c.binding.journal.InputID, TurnID: c.binding.turn, NativeID: string(domain.NewID()), Accepted: true, Content: []claude.ContentEvent{content}}
}

func TestClaudeContentPublishesOrderedBlocksAndOnlyReceiptRetries(t *testing.T) {
	c, rpc := newClaudeContentFixture(t)
	ctx := context.Background()
	rpc.lose = true
	if err := c.PublishInput(ctx); err == nil {
		t.Fatal("lost user publication was accepted")
	}
	original := len(rpc.events) - 1
	rpc.lose = false
	if err := c.ReplayPending(ctx); err != nil {
		t.Fatal(err)
	}
	if !c.inputPublished || rpc.requests[original] != rpc.requests[original+1] || !bytes.Equal(rpc.events[original], rpc.events[original+1]) {
		t.Fatal("input receipt replay repeated native input or substituted its event")
	}
	publish := func(content claude.ContentEvent) {
		t.Helper()
		handled, err := c.PublishObservation(ctx, claudeContentObservation(c, content))
		if !handled || err != nil {
			t.Fatal("original content was not published", err)
		}
	}
	publish(claude.ContentEvent{Kind: claude.ProviderMessageStarted})
	for index, kind := range []claude.ContentBlockKind{claude.ThinkingBlock, claude.RedactedThinkingBlock, claude.TextBlock, claude.TextBlock} {
		i, empty, value := uint32(index), "", "Original native text 🐦"
		block := &claude.NativeContentBlock{Kind: kind}
		if kind == claude.TextBlock {
			block.Text = &empty
		}
		if kind == claude.ThinkingBlock {
			block.Thinking = &empty
		}
		publish(claude.ContentEvent{Kind: claude.ContentStarted, Index: &i, Block: block})
		if kind != claude.RedactedThinkingBlock {
			delta := claude.TextDelta
			if kind == claude.ThinkingBlock {
				delta = claude.ThinkingDelta
			}
			o := claudeContentObservation(c, claude.ContentEvent{Kind: claude.ContentChanged, Index: &i, DeltaKind: delta, Delta: &value})
			rpc.lose = true
			if _, err := c.PublishObservation(ctx, o); err == nil {
				t.Fatal("lost delta acknowledgment disappeared")
			}
			last := len(rpc.events) - 1
			if _, err := c.PublishObservation(ctx, o); err == nil {
				t.Fatal("pending content permitted another original event")
			}
			rpc.lose = false
			if err := c.ReplayPending(ctx); err != nil {
				t.Fatal(err)
			}
			if rpc.requests[last] != rpc.requests[last+1] || !bytes.Equal(rpc.events[last], rpc.events[last+1]) {
				t.Fatal("delta receipt changed identity or bytes")
			}
			if kind == claude.TextBlock {
				block.Text = &value
			} else {
				block.Thinking = &value
			}
		}
		if kind == claude.ThinkingBlock {
			publish(claude.ContentEvent{Kind: claude.ContentChanged, Index: &i, DeltaKind: claude.SignatureDelta})
		}
		publish(claude.ContentEvent{Kind: claude.ContentCompleted, Index: &i, Block: block})
		publish(claude.ContentEvent{Kind: claude.ContentStopped, Index: &i})
	}
	stop := claude.NativeStopReason("end_turn")
	publish(claude.ContentEvent{Kind: claude.ProviderMessageUpdated, StopReason: &stop})
	publish(claude.ContentEvent{Kind: claude.ProviderMessageFinished})
	if c.active != "" || c.messages["msg_original"].content != nil || c.messages["msg_original"].state != domain.MessageComplete {
		t.Fatal("completed original content was retained or its state lost")
	}
	for _, raw := range rpc.events {
		var e domain.ExecutionEvent
		if json.Unmarshal(raw, &e) != nil || e.Kind == domain.ExecutionTurnFinished || bytes.Contains(raw, []byte("signature")) {
			t.Fatal("text publication invented a terminal or exposed opaque data")
		}
	}
}

func TestClaudeContentRejectsUnownedAndUnsupportedObservations(t *testing.T) {
	for _, change := range []string{"session", "input", "turn", "model", "child", "unknown", "unaccepted", "duplicate", "citation", "tool"} {
		t.Run(change, func(t *testing.T) {
			c, rpc := newClaudeContentFixture(t)
			if err := c.PublishInput(context.Background()); err != nil {
				t.Fatal(err)
			}
			o := claudeContentObservation(c, claude.ContentEvent{Kind: claude.ProviderMessageStarted})
			if change == "citation" || change == "tool" || change == "duplicate" {
				if _, err := c.PublishObservation(context.Background(), o); err != nil {
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
			case "model":
				o.Content[0].Model = "foreign"
			case "child":
				o.Content[0].ParentToolID = "foreign-child"
			case "unknown":
				o.Content[0].Kind = claude.AssistantProblemObserved
			case "unaccepted":
				o.Accepted = false
			case "citation", "tool":
				i, text := uint32(0), ""
				block := &claude.NativeContentBlock{Kind: claude.TextBlock, Text: &text}
				if change == "citation" {
					block.Citations = &claude.NativeCitations{}
				} else {
					block.Kind, block.Text = claude.ToolUseBlock, nil
					block.Tool = &claude.NativeTool{}
				}
				o = claudeContentObservation(c, claude.ContentEvent{Kind: claude.ContentStarted, Index: &i, Block: block})
			}
			before := len(rpc.events)

			if change == "session" {
				_, err := c.PublishObservation(context.Background(), o)
				if err != nil {
					t.Fatal("session metadata blocked observation", err)
				}
				return
			}
			if _, err := c.PublishObservation(context.Background(), o); err == nil || len(rpc.events) != before {
				t.Fatal("unhandled content entered an apparently complete transcript")
			}
			if _, err := c.PublishObservation(context.Background(), claudeContentObservation(c, claude.ContentEvent{Kind: claude.ProviderMessageStarted})); err == nil {
				t.Fatal("unsupported original content failure was cleared")
			}
		})
	}
}
