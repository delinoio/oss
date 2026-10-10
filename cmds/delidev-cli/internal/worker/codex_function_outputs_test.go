// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"testing"
)

func functionPublicationFixture(c *CodexEventPublisher) codex.Event {
	text := "<script>inert()</script>"
	value := &domain.CodexFunctionOutput{Version: 1, ID: domain.NewID(), NativeID: "original-function", Name: "original_tool", Stage: domain.CodexFunctionOutputStarted, Output: domain.CodexFunctionOutputBody{Variant: domain.CodexFunctionContents, Contents: []domain.CodexFunctionContent{{Type: domain.CodexFunctionText, Text: &text}, {Type: domain.CodexFunctionImage, ReferenceKind: domain.CodexFunctionFileID, ReferencePresent: true}, {Type: domain.CodexFunctionEncrypted, Present: true}}}}
	return codex.Event{Kind: codex.FunctionOutputEvent, ThreadID: c.thread, TurnID: c.turn, Correlated: true, ItemID: value.NativeID, FunctionOutput: value}
}
func TestCodexFunctionOutputOriginalOutboxLifecycleAndNoCompletion(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	c.functionOutputSupported = true
	event := functionPublicationFixture(c)
	before := len(rpc.events)
	if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil {
		t.Fatal(err)
	}
	retained := c.functionOutputs[event.ItemID]
	if retained.ID.Validate() != nil || retained.Output.Text != nil || retained.Output.Contents != nil {
		t.Fatal("identity map retained native output content")
	}
	event.FunctionOutput.Stage = domain.CodexFunctionOutputCompleted
	if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil {
		t.Fatal(err)
	}
	if c.finished || c.blocked || len(rpc.events) != before+2 || c.publisher.state.LastSequence != 4 {
		t.Fatal("result completed original execution")
	}
	var started, completed domain.ExecutionEvent
	if domain.Decode(rpc.events[before], &started) != nil || domain.Decode(rpc.events[before+1], &completed) != nil || started.CodexFunctionOutput.ID != completed.CodexFunctionOutput.ID || started.CodexFunctionOutput.NativeID != completed.CodexFunctionOutput.NativeID || started.CodexFunctionOutput.Namespace != nil || completed.Kind != domain.ExecutionCodexFunctionOutputObserved {
		t.Fatal("ordered original lifecycle identity changed")
	}
	if _, err := c.PublishCore(context.Background(), event); err == nil || len(rpc.events) != before+2 {
		t.Fatal("duplicate output retransmitted")
	}
}
func TestCodexFunctionOutputLostAcknowledgmentReplaysExactReceiptOnly(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	c.functionOutputSupported = true
	rpc.lose = true
	event := functionPublicationFixture(c)
	before := len(rpc.events)
	if _, err := c.PublishCore(context.Background(), event); err == nil || !c.blocked || c.publisher.state.Pending == nil {
		t.Fatal("missing acknowledgment lost durable uncertainty")
	}
	original := bytes.Clone(rpc.events[before])
	request := rpc.requests[before]
	if _, err := c.PublishCore(context.Background(), event); err == nil || len(rpc.events) != before+1 {
		t.Fatal("blocked mapper regenerated output")
	}
	rpc.lose = false
	if err := c.publisher.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(rpc.events) != before+2 || !bytes.Equal(original, rpc.events[before+1]) || rpc.requests[before+1] != request || c.finished {
		t.Fatal("replay replaced original receipt or inferred completion")
	}
}
func TestCodexFunctionOutputRejectsForeignLateAndUnnegotiated(t *testing.T) {
	for _, fault := range []string{"capability", "foreign-thread", "foreign-turn", "uncorrelated", "late", "complete-before-start", "name-change", "namespace-change", "cross-kind"} {
		t.Run(fault, func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			c.functionOutputSupported = true
			event := functionPublicationFixture(c)
			if fault == "name-change" || fault == "namespace-change" {
				if _, err := c.PublishCore(context.Background(), event); err != nil {
					t.Fatal(err)
				}
				event.FunctionOutput.Stage = domain.CodexFunctionOutputCompleted
			}
			switch fault {
			case "capability":
				c.functionOutputSupported = false
			case "foreign-thread":
				event.ThreadID = domain.NewID()
			case "foreign-turn":
				event.TurnID = domain.NewID()
			case "uncorrelated":
				event.Correlated = false
			case "late":
				event.Late = true
			case "complete-before-start":
				event.FunctionOutput.Stage = domain.CodexFunctionOutputCompleted
			case "name-change":
				event.FunctionOutput.Name = "changed"
			case "namespace-change":
				s := ""
				event.FunctionOutput.Namespace = &s
			case "cross-kind":
				c.messages[event.ItemID] = domain.ExecutionMessageUpdate{ID: domain.NewID()}
			}
			before := len(rpc.events)
			if _, err := c.PublishCore(context.Background(), event); err == nil || len(rpc.events) != before {
				t.Fatal("unsupported observation gained publication")
			}
		})
	}
}
