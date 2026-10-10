// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"testing"
)

func TestCodexAsyncMessagePreservesDurableReceiptAndNoResponse(t *testing.T) {
	for _, lose := range []bool{false, true} {
		c, rpc := codexTerminalStatusFixture(t)
		delivery := domain.CodexAsyncMessage
		original := &domain.CodexMessageContent{DeliveryPresent: true, Delivery: &delivery, QuestionsPresent: true, Questions: []domain.CodexEmbeddedQuestion{{Title: "original", Options: nil}, {Title: "empty", Options: []string{}}, {Title: "choices", Options: []string{"one", "two"}}}}
		event := codex.Event{Kind: codex.MessageStartedEvent, Correlated: true, ThreadID: c.thread, TurnID: c.turn, ItemID: "original-message", Message: &codex.Message{ID: "original-message", Role: codex.AssistantRole, Text: "synthetic", Codex: original}}
		rpc.lose = lose
		before := len(rpc.events)
		handled, err := c.PublishCore(context.Background(), event)
		if !handled || (err != nil) != lose {
			t.Fatal("original publication classification changed", err)
		}
		if len(c.interactions) != 0 || len(c.questionResponses) != 0 || len(c.acceptedInputs) != 1 {
			t.Fatal("embedded questions created an interaction/input")
		}
		if lose {
			rpc.lose = false
			if err := c.publisher.ReplayPending(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(rpc.events) != before+2 || !bytes.Equal(rpc.events[before], rpc.events[before+1]) || rpc.requests[before] != rpc.requests[before+1] {
				t.Fatal("uncertain publication reconstructed original receipt")
			}
			if _, err := c.PublishCore(context.Background(), event); err == nil {
				t.Fatal("blocked native controller acquired replay authority")
			}
			continue
		}
		var published domain.ExecutionEvent
		if domain.Decode(rpc.events[before], &published) != nil || published.Message == nil || published.Message.Codex == nil || len(published.Message.Codex.Questions) != 3 {
			t.Fatal("typed metadata dropped")
		}
		original.Questions[2].Options[0] = "changed after publication"
		if c.messages[event.ItemID].Codex.Questions[2].Options[0] != "one" {
			t.Fatal("original observed metadata borrowed mutable storage")
		}
	}
}
