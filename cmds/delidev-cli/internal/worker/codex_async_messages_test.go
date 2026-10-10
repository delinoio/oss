// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"testing"
)

func TestAsyncMessageWorkerNegotiationAndLifecycle(t *testing.T) {
	for _, supported := range []bool{false, true} {
		c, rpc := codexTerminalStatusFixture(t)
		c.asyncMessageSupported = supported
		delivery := "async"
		metadata := &domain.CodexAsyncMessage{Version: 1, DeliveryPresent: true, Delivery: &delivery, QuestionsPresent: true, Questions: []domain.CodexAsyncQuestion{{Title: "q", Options: []string{"b", "a"}}}}
		message := &codex.Message{ID: "async-message", Role: codex.AssistantRole, CodexAsyncMessage: metadata}
		event := codex.Event{Kind: codex.MessageStartedEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: message.ID, Correlated: true, Message: message}
		before := len(rpc.events)
		handled, err := c.PublishCore(context.Background(), event)
		if !supported {
			if err == nil || handled || len(rpc.events) != before {
				t.Fatal("unnegotiated profile published")
			}
			continue
		}
		if err != nil || !handled {
			t.Fatal(err)
		}
		event.Kind = codex.MessageCompletedEvent
		message.Text = "final"
		if handled, err := c.PublishCore(context.Background(), event); err != nil || !handled || c.finished || len(rpc.events) != before+2 {
			t.Fatal("inert message gained turn completion or lost metadata", err)
		}
	}
}
