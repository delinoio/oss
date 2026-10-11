// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"testing"
)

func TestCodexAssistantOutboxKeepsOriginalMetadataAfterLostAcknowledgement(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	c.publisher.input.NativeAsyncMessages = true
	var metadata domain.CodexAssistantMetadata
	if domain.Decode([]byte(`{"delivery":"async","questions":[{"title":"q","options":["B","A"]}]}`), &metadata) != nil {
		t.Fatal("fixture invalid")
	}
	native := &codex.Message{ID: "original-assistant", Role: codex.AssistantRole, Text: "Original", CodexAssistant: &metadata}
	event := codex.Event{Kind: codex.MessageStartedEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: native.ID, Message: native, Correlated: true}
	rpc.lose = true
	if _, err := c.PublishCore(context.Background(), event); err == nil || !c.blocked || c.publisher.state.Pending == nil {
		t.Fatal("uncertainty lost original receipt")
	}
	last := len(rpc.events) - 1
	rpc.lose = false
	if err := c.publisher.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rpc.requests[last] != rpc.requests[last+1] || !bytes.Equal(rpc.events[last], rpc.events[last+1]) {
		t.Fatal("original bytes or identity changed")
	}
	before := len(rpc.events)
	if _, err := c.PublishCore(context.Background(), event); err == nil || len(rpc.events) != before {
		t.Fatal("message or input was retransmitted")
	}
}
func TestCodexAssistantKeepsQuestionOrderAndNoInteractionAuthority(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	c.publisher.input.NativeAsyncMessages = true
	var metadata domain.CodexAssistantMetadata
	_ = domain.Decode([]byte(`{"delivery":null,"questions":[{"title":"First","options":null},{"title":"Second","options":[]}]}`), &metadata)
	before := len(rpc.events)
	native := &codex.Message{ID: "original", Role: codex.AssistantRole, Text: "Original", CodexAssistant: &metadata}
	event := codex.Event{Kind: codex.MessageStartedEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: native.ID, Message: native, Correlated: true}
	if _, err := c.PublishCore(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	event.Kind = codex.MessageCompletedEvent
	if _, err := c.PublishCore(context.Background(), event); err != nil || c.finished || len(c.interactions) != 0 || len(rpc.events) != before+2 {
		t.Fatal("embedded question gained authority", err)
	}
	var first, last domain.ExecutionEvent
	if domain.Decode(rpc.events[before], &first) != nil || domain.Decode(rpc.events[before+1], &last) != nil || first.Message.ID != last.Message.ID || !domain.SameCodexAssistantMetadata(last.Message.CodexAssistant, &metadata) {
		t.Fatal("metadata changed")
	}
}
