// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"reflect"
	"testing"
)

func TestCodexAsyncMessageTranscriptAndOriginalReceipt(t *testing.T) {
	f := newPublicationFixture(t)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	delivery := domain.CodexAsyncMessage
	content := &domain.CodexMessageContent{DeliveryPresent: true, Delivery: &delivery, QuestionsPresent: true, Questions: []domain.CodexEmbeddedQuestion{{Title: "first", Options: nil}, {Title: "second", Options: []string{}}, {Title: "third", Options: []string{"one", "two"}}}}
	message := domain.ExecutionMessageUpdate{ID: domain.NewID(), NativeID: "async-original", Role: domain.AssistantMessage, Text: "synthetic", Codex: content}
	started := f.event(domain.ExecutionMessageStarted, 3)
	started.Message = &message
	receipt := f.publish(t, started)
	if reply, err := f.call(receipt); err != nil || !reply.Msg.Replayed {
		t.Fatal("lost original publication acknowledgment could not replay", err)
	}
	completed := f.event(domain.ExecutionMessageCompleted, 4)
	completed.Message = &message
	f.publish(t, completed)
	record, err := f.service.Store.Get(context.Background(), domain.MessageKind, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := store.Decode[domain.ExecutionMessage](record)
	if err != nil || !reflect.DeepEqual(retained.Codex, content) || retained.State != domain.MessageComplete || retained.InputID != "" {
		t.Fatal("ordered/null message metadata changed or acquired input authority", err)
	}
}
func TestCodexAsyncMessageCannotReplaceObservedQuestion(t *testing.T) {
	f := newPublicationFixture(t)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	content := &domain.CodexMessageContent{QuestionsPresent: true, Questions: []domain.CodexEmbeddedQuestion{{Title: "original", Options: nil}}}
	message := domain.ExecutionMessageUpdate{ID: domain.NewID(), NativeID: "async-original", Role: domain.AssistantMessage, Text: "synthetic", Codex: content}
	started := f.event(domain.ExecutionMessageStarted, 3)
	started.Message = &message
	f.publish(t, started)
	message.Codex = domain.CloneCodexMessage(content)
	message.Codex.Questions[0].Options = []string{}
	completed := f.event(domain.ExecutionMessageCompleted, 4)
	completed.Message = &message
	if _, err := f.call(f.requestEvent(t, completed)); err == nil {
		t.Fatal("original null options became an empty answer choice list")
	}
}
