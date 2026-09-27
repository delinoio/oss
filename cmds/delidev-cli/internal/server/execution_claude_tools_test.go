package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestClaudeToolsPublishAtomicallyWithOriginalProvider(t *testing.T) {
	f := newClaudePublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	sequence := uint64(2)
	u := domain.ClaudeMessageUpdate{ID: domain.NewID(), NativeID: "msg_tool_owner", Model: f.input.Configuration.NativeModel, Mutation: domain.ClaudeMessageStart}
	publish := func() {
		t.Helper()
		sequence++
		e := f.event(domain.ExecutionClaudeMessageObserved, sequence)
		e.ClaudeMessage = &u
		request := f.publish(t, e)
		if response, err := f.call(request); err != nil || !response.Msg.Replayed {
			t.Fatal("original tool receipt did not replay", err)
		}
	}
	publish()
	index, initial, delta := uint32(0), "{}", `{"file_path":"/private/fixture","large":9007199254740993}`
	ref := domain.ClaudeToolReference{ID: domain.NewID(), NativeID: "tool_original", Name: "Read"}
	tool := domain.ClaudeToolUpdate{Reference: ref, MessageID: u.ID, NativeMessageID: u.NativeID, Index: index, Mutation: domain.ClaudeToolStart, InitialInput: &initial}
	u.Mutation, u.Index, u.Block, u.Tool = domain.ClaudeBlockStart, &index, &domain.ClaudeTextBlock{Kind: domain.ClaudeToolUse, Tool: &ref}, &tool
	publish()
	tool.Mutation, tool.InitialInput, tool.Delta = domain.ClaudeToolInputAppend, nil, &delta
	u.Mutation, u.Block = domain.ClaudeBlockToolInput, nil
	publish()
	before, err := f.service.Store.Get(context.Background(), domain.MessageKind, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The provider's completion would be valid, but the tool's proposal changed.
	// Both writes and the session sequence must roll back in the same transaction.
	tool.Mutation, tool.Delta, tool.Proposal = domain.ClaudeToolProposalComplete, nil, &domain.ClaudeToolProposal{Proposed: `{}`, Applied: delta}
	u.Mutation, u.Block = domain.ClaudeBlockComplete, &domain.ClaudeTextBlock{Kind: domain.ClaudeToolUse, Tool: &ref}
	event := f.event(domain.ExecutionClaudeMessageObserved, sequence+1)
	event.ClaudeMessage = &u
	if _, err := f.call(f.requestEvent(t, event)); err == nil {
		t.Fatal("changed proposal committed")
	}
	after, err := f.service.Store.Get(context.Background(), domain.MessageKind, u.ID)
	if err != nil || after.Revision != before.Revision {
		t.Fatal("tool rejection partially completed provider", err)
	}
	tool.Proposal.Proposed = delta
	publish()
	toolResult := domain.ClaudeToolUpdate{Mutation: domain.ClaudeToolResultObserved, Reference: ref, MessageID: u.ID, NativeMessageID: u.NativeID, Index: index, Result: &domain.ClaudeToolResult{NativeEventID: string(domain.NewID())}}
	resultEvent := func() domain.ExecutionEvent {
		e := f.event(domain.ExecutionClaudeToolObserved, sequence+1)
		e.ClaudeTool = &toolResult
		return e
	}
	if _, err := f.call(f.requestEvent(t, resultEvent())); err == nil {
		t.Fatal("result before original provider closure accepted")
	}
	u.Mutation, u.Block, u.Tool = domain.ClaudeBlockStop, nil, nil
	publish()
	u.Mutation, u.Index = domain.ClaudeMessageStop, nil
	publish()
	for _, change := range []string{"product", "native", "index", "provider", "caller"} {
		bad := toolResult
		switch change {
		case "product":
			bad.Reference.ID = domain.NewID()
		case "native":
			bad.Reference.NativeID = "tool_foreign"
		case "index":
			bad.Index++
		case "provider":
			bad.NativeMessageID = "msg_foreign"
		case "caller":
			caller := domain.ClaudeDirectToolCaller
			bad.Caller = &caller
		}
		e := resultEvent()
		e.ClaudeTool = &bad
		if _, err := f.call(f.requestEvent(t, e)); err == nil {
			t.Fatal("foreign result acquired original tool", change)
		}
	}
	result := "Original native failure"
	failed := true
	toolResult.Result.Text, toolResult.Result.Error = &result, &failed
	request := f.publish(t, resultEvent())
	sequence++
	if response, err := f.call(request); err != nil || !response.Msg.Replayed {
		t.Fatal("result receipt lost", err)
	}
	if _, err := f.call(f.requestEvent(t, resultEvent())); err == nil {
		t.Fatal("result reapplied with new receipt")
	}
	row, err := f.service.Store.Get(context.Background(), domain.MessageKind, ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Decode[domain.ExecutionMessage](row)
	if err != nil || value.State != domain.MessageComplete || value.Role != domain.ToolMessage || value.NativeID != ref.NativeID || value.NativeParentID != u.NativeID || value.Text != "" || value.ClaudeTool == nil || value.ClaudeTool.Proposal.Proposed != delta || *value.ClaudeTool.Result.Text != result || !*value.ClaudeTool.Result.Error {
		t.Fatal("native tool input/result or original identity lost", err)
	}
	row, err = f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Decode[domain.Session](row)
	if err != nil || session.Execution.Outcome != domain.ExecutionRunning || session.Execution.LastSequence != sequence {
		t.Fatal("tool failure changed root outcome or sequence", err)
	}
}
