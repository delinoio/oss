// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"testing"
)

func TestAsyncMessagePublicationReceiptReplay(t *testing.T) {
	f := newPublicationFixture(t)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	id := domain.NewID()
	delivery := "async"
	metadata := &domain.CodexAsyncMessage{Version: 1, DeliveryPresent: true, Delivery: &delivery, QuestionsPresent: true, Questions: []domain.CodexAsyncQuestion{{Title: "q", Options: nil}, {Title: "empty", Options: []string{}}, {Title: "choices", Options: []string{"b", "a"}}}}
	message := &domain.ExecutionMessageUpdate{ID: id, NativeID: "async-message", Role: domain.AssistantMessage, CodexAsyncMessage: metadata, Text: "original"}
	for i, kind := range []domain.ExecutionEventKind{domain.ExecutionMessageStarted, domain.ExecutionMessageCompleted} {
		e := f.event(kind, uint64(i+3))
		e.Message = message
		req := f.publish(t, e)
		reply, err := f.call(req)
		if err != nil || !reply.Msg.Replayed {
			t.Fatal("lost acknowledgement duplicated publication", err)
		}
	}
	r, err := f.service.Store.Get(context.Background(), domain.MessageKind, id)
	if err != nil {
		t.Fatal(err)
	}
	m, err := store.Decode[domain.ExecutionMessage](r)
	if err != nil || m.Text != "original" || m.State != domain.MessageComplete || m.NativeThreadID != string(f.thread) || m.NativeTurnID != string(f.turn) || !domain.EqualCodexAsyncMessage(m.CodexAsyncMessage, metadata) || m.FirstSequence != 3 || m.LastSequence != 4 {
		t.Fatal("durable async provenance lost or duplicated", err)
	}
}
