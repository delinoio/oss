package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func claudeInteractionPublicationFixture(t *testing.T) (*publicationFixture, domain.ExecutionInteractionUpdate, uint64) {
	return claudeCallbackPublicationFixture(t, false)
}
func claudeCallbackPublicationFixture(t *testing.T, question bool) (*publicationFixture, domain.ExecutionInteractionUpdate, uint64) {
	t.Helper()
	f := newClaudePublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	sequence := uint64(2)
	u := domain.ClaudeMessageUpdate{ID: domain.NewID(), NativeID: "msg_callback_owner", Model: f.input.Configuration.NativeModel, Mutation: domain.ClaudeMessageStart}
	publish := func() {
		t.Helper()
		sequence++
		e := f.event(domain.ExecutionClaudeMessageObserved, sequence)
		e.ClaudeMessage = &u
		f.publish(t, e)
	}
	publish()
	index, input := uint32(0), `{"command":"printf original","exact":9007199254740993}`
	ref := domain.ClaudeToolReference{ID: domain.NewID(), NativeID: "tool_original", Name: "Bash"}
	if question {
		ref.Name = "AskUserQuestion"
		input = `{"questions":[{"question":"Original?","header":"Choice","options":[{"label":"One","description":"First"},{"label":"Two","description":"Second"}],"multiSelect":true}]}`
	}
	tool := domain.ClaudeToolUpdate{Mutation: domain.ClaudeToolStart, Reference: ref, MessageID: u.ID, NativeMessageID: u.NativeID, Index: index, InitialInput: &input}
	u.Mutation, u.Index, u.Block, u.Tool = domain.ClaudeBlockStart, &index, &domain.ClaudeTextBlock{Kind: domain.ClaudeToolUse, Tool: &ref}, &tool
	publish()
	tool.Mutation, tool.InitialInput, tool.Proposal = domain.ClaudeToolProposalComplete, nil, &domain.ClaudeToolProposal{Proposed: input, Applied: input}
	u.Mutation = domain.ClaudeBlockComplete
	publish()
	u.Mutation, u.Block, u.Tool = domain.ClaudeBlockStop, nil, nil
	publish()
	u.Mutation, u.Index = domain.ClaudeMessageStop, nil
	publish()
	request := domain.ExecutionInteractionUpdate{ID: domain.NewID(), NativeItemID: ref.NativeID, NativeRequestID: domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: "request_original"}, Type: domain.NativeApprovalInteraction, Claude: &domain.ClaudeInteractionRequest{Version: domain.ClaudeProtocolVersion, Kind: domain.ClaudeToolPermission, ArrivalID: domain.NewID(), Tool: ref, MessageID: u.ID, NativeMessageID: u.NativeID, Index: index, InputJSON: input}}
	if question {
		request.Type, request.Claude.Kind = domain.UserQuestionInteraction, domain.ClaudeUserQuestion
	}
	return f, request, sequence
}
func TestClaudeCallbackPublicationKeepsOriginalRequestCancellationAndInbox(t *testing.T) {
	f, u, sequence := claudeInteractionPublicationFixture(t)
	publish := func(kind domain.ExecutionEventKind) {
		t.Helper()
		sequence++
		e := f.event(kind, sequence)
		e.Interaction = &u
		request := f.publish(t, e)
		if response, err := f.call(request); err != nil || !response.Msg.Replayed {
			t.Fatal("callback receipt not replayed", err)
		}
	}
	original := *u.Claude
	publish(domain.ExecutionInteractionRequested)
	// Distinct arrivals cannot create overlapping responses for one original call.
	second := u
	second.ID = domain.NewID()
	r := original
	r.ArrivalID = domain.NewID()
	second.Claude = &r
	second.NativeRequestID.Text = "second-request"
	e := f.event(domain.ExecutionInteractionRequested, sequence+1)
	e.Interaction = &second
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("same tool acquired overlapping callback requests")
	}
	u.Claude = nil
	u.Closure = domain.InteractionNativeClosed
	u.ClaudeCancellation = &domain.ClaudeInteractionCancellation{ArrivalID: domain.NewID()}
	e = f.event(domain.ExecutionInteractionClosed, sequence+1)
	e.Interaction = &u
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("foreign callback canceled original request")
	}
	u.ClaudeCancellation.ArrivalID = original.ArrivalID
	publish(domain.ExecutionInteractionClosed)
	row, err := f.service.Store.Get(context.Background(), domain.InteractionKind, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Decode[domain.ExecutionInteraction](row)
	if err != nil || value.Claude == nil || value.Claude.InputJSON != original.InputJSON || value.ClaudeCancellation == nil || value.ClaudeCancellation.ArrivalID != original.ArrivalID || value.Closure != domain.InteractionNativeClosed || value.Response != nil || value.ApprovalResponse != nil {
		t.Fatal("callback cancellation lost original request or fabricated response", err)
	}
	entries, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.InboxKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(entries) != 1 {
		t.Fatal("original callback inbox changed", len(entries), err)
	}
	// A new receipt cannot reuse the original arrival after its cancellation.
	second.Claude = &original
	e = f.event(domain.ExecutionInteractionRequested, sequence+1)
	e.Interaction = &second
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("canceled arrival reused")
	}
}
func TestClaudeCallbackPublicationRejectsForeignToolAndAppliedInput(t *testing.T) {
	for _, change := range []string{"tool", "provider", "index", "caller", "applied", "family"} {
		t.Run(change, func(t *testing.T) {
			f, u, sequence := claudeInteractionPublicationFixture(t)
			switch change {
			case "tool":
				u.Claude.Tool.ID = domain.NewID()
			case "provider":
				u.Claude.MessageID = domain.NewID()
			case "index":
				u.Claude.Index++
			case "caller":
				v := domain.ClaudeDirectToolCaller
				u.Claude.Caller = &v
			case "applied":
				u.Claude.InputJSON = `{"command":"printf foreign"}`
			case "family":
				u.Claude.Tool.Name = "Read"
			}
			e := f.event(domain.ExecutionInteractionRequested, sequence+1)
			e.Interaction = &u
			if _, err := f.call(f.requestEvent(t, e)); err == nil {
				t.Fatal("foreign callback acquired original tool")
			}
			rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.InteractionKind, SessionID: f.input.SessionID, Limit: 10})
			if err != nil || len(rows) != 0 {
				t.Fatal("rejected callback partially persisted", err)
			}
		})
	}
}
