package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"testing"
)

func TestClaudeCitationPublicationPreservesOriginalStreamAndBlocksUnprovedHistory(t *testing.T) {
	f := newClaudePublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	seq := uint64(3)
	i := uint32(0)
	u := domain.ClaudeMessageUpdate{ID: domain.NewID(), NativeID: "msg_citation", Model: f.input.Configuration.NativeModel, Mutation: domain.ClaudeMessageStart}
	publish := func() {
		t.Helper()
		e := f.event(domain.ExecutionClaudeMessageObserved, seq)
		e.ClaudeMessage = &u
		r := f.publish(t, e)
		if replay, err := f.call(r); err != nil || !replay.Msg.Replayed {
			t.Fatal("citation receipt replay failed", err)
		}
		seq++
	}
	publish()
	u.Mutation, u.Index, u.Block, u.Citations = domain.ClaudeBlockStart, &i, &domain.ClaudeTextBlock{Kind: domain.ClaudeText}, &domain.ClaudeCitationCollection{Null: true}
	publish()
	citation := domain.ClaudeCitation{Kind: domain.ClaudeWebCitation, Text: "Original quote", Web: &domain.ClaudeWebLocation{URL: "https://fixture.invalid/source"}}
	u.Mutation, u.Block, u.Citations, u.Citation = domain.ClaudeBlockCitation, nil, nil, &citation
	publish()
	u.Mutation, u.Citation, u.Block, u.Citations, u.CitationCompletion = domain.ClaudeBlockComplete, nil, &domain.ClaudeTextBlock{Kind: domain.ClaudeText}, &domain.ClaudeCitationCollection{Entries: []domain.ClaudeCitation{}}, domain.ClaudeCitationsMatched
	before, _ := f.service.Store.Get(context.Background(), domain.MessageKind, u.ID)
	e := f.event(domain.ExecutionClaudeMessageObserved, seq)
	e.ClaudeMessage = &u
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("discarded citation accepted as matched")
	}
	after, _ := f.service.Store.Get(context.Background(), domain.MessageKind, u.ID)
	if before.Revision != after.Revision {
		t.Fatal("rejected citation altered message")
	}
	u.CitationCompletion = domain.ClaudeCitationsOmitted
	publish()
	u.Mutation, u.Block, u.Citations, u.CitationCompletion = domain.ClaudeBlockStop, nil, nil, ""
	publish()
	u.Mutation, u.Index = domain.ClaudeMessageStop, nil
	publish()
	row, _ := f.service.Store.Get(context.Background(), domain.MessageKind, u.ID)
	m, err := store.Decode[domain.ExecutionMessage](row)
	if err != nil || m.State != domain.MessageComplete || len(m.Claude.Blocks[0].Citations.Deltas) != 1 || !m.Claude.Blocks[0].Citations.Initial.Null || len(m.Claude.Blocks[0].Citations.Completed.Entries) != 0 {
		t.Fatal("original citations changed", err)
	}
	err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		ready, err := tx.ClaudeRootContentContinuation(f.input.ExecutionID)
		if err != nil {
			return err
		}
		if ready {
			t.Fatal("citation publication invented native history authority")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
