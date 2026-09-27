package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestClaudeMessagePublicationKeepsOriginalProviderAndBlockLifecycles(t *testing.T) {
	f := newClaudePublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	sequence := uint64(2)
	u := domain.ClaudeMessageUpdate{ID: domain.NewID(), NativeID: "msg_original", Model: f.input.Configuration.NativeModel, Mutation: domain.ClaudeMessageStart}
	publish := func() {
		t.Helper()
		sequence++
		e := f.event(domain.ExecutionClaudeMessageObserved, sequence)
		e.ClaudeMessage = &u
		request := f.publish(t, e)
		if response, err := f.call(request); err != nil || !response.Msg.Replayed {
			t.Fatal("original content receipt did not replay", err)
		}
	}
	publish()
	for _, changed := range []string{"product-id", "model", "native-id", "index", "premature-stop"} {
		bad := u
		index := uint32(0)
		bad.Mutation, bad.Index, bad.Block = domain.ClaudeBlockStart, &index, &domain.ClaudeTextBlock{Kind: domain.ClaudeText}
		switch changed {
		case "product-id":
			bad.ID = domain.NewID()
		case "model":
			bad.Model = "foreign"
		case "native-id":
			bad.NativeID = "foreign"
		case "index":
			index = 1
		case "premature-stop":
			bad.Mutation, bad.Index, bad.Block = domain.ClaudeBlockStop, &index, nil
		}
		e := f.event(domain.ExecutionClaudeMessageObserved, sequence+1)
		e.ClaudeMessage = &bad
		if _, err := f.call(f.requestEvent(t, e)); err == nil {
			t.Fatal("changed original message gained block authority", changed)
		}
	}
	for n, kind := range []domain.ClaudeTextKind{domain.ClaudeThinking, domain.ClaudeText, domain.ClaudeText} {
		index := uint32(n)
		text := "Original ordered content"
		u.Mutation, u.Index, u.Block = domain.ClaudeBlockStart, &index, &domain.ClaudeTextBlock{Kind: kind}
		publish()
		u.Mutation, u.Block, u.Delta = domain.ClaudeBlockAppend, nil, &text
		publish()
		u.Mutation, u.Block, u.Delta = domain.ClaudeBlockComplete, &domain.ClaudeTextBlock{Kind: kind, Text: text}, nil
		publish()
		u.Mutation, u.Block = domain.ClaudeBlockStop, nil
		publish()
	}
	u.Mutation, u.Index = domain.ClaudeMessageStop, nil
	publish()
	r, err := f.service.Store.Get(context.Background(), domain.MessageKind, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Decode[domain.ExecutionMessage](r)
	if err != nil || value.NativeID != "msg_original" || value.Claude == nil || len(value.Claude.Blocks) != 3 || value.State != domain.MessageComplete || value.Text != "" || value.LastSequence != sequence {
		t.Fatal("native blocks were flattened or lost", err)
	}
	if _, err := f.call(f.requestEvent(t, func() domain.ExecutionEvent {
		e := f.event(domain.ExecutionClaudeMessageObserved, sequence+1)
		e.ClaudeMessage = &u
		return e
	}())); err == nil {
		t.Fatal("provider message stop applied twice")
	}
	duplicate := u
	duplicate.ID, duplicate.Mutation = domain.NewID(), domain.ClaudeMessageStart
	e := f.event(domain.ExecutionClaudeMessageObserved, sequence+1)
	e.ClaudeMessage = &duplicate
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("original provider identity was duplicated under another product record")
	}
}
