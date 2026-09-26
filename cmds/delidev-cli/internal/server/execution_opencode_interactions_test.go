package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func openCodeInteractionPublicationFixture(t *testing.T, question bool) (*publicationFixture, domain.ExecutionEvent, domain.ExecutionToolUpdate) {
	f := newOpenCodePublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	raw := ""
	snapshot := &domain.ToolSnapshot{Kind: domain.OpenCodeReadTool, Status: domain.ToolPending, Read: &domain.OpenCodeReadObservation{CallID: "original", Raw: &raw}}
	if question {
		snapshot = &domain.ToolSnapshot{Kind: domain.OpenCodeBuiltinTool, Status: domain.ToolPending, Builtin: &domain.OpenCodeBuiltinObservation{Name: domain.OpenCodeQuestionTool, CallID: "original", InputJSON: `{}`, Raw: &raw}}
	}
	tool := domain.ExecutionToolUpdate{ID: domain.NewID(), NativeID: "prt_01960dcbe1fbABCDEFGHIJKLMN", NativeParentID: "msg_01960dcbe1fbABCDEFGHIJKLMN", Snapshot: snapshot}
	e := f.event(domain.ExecutionToolStarted, 3)
	e.Tool = &tool
	f.publish(t, e)
	r := &domain.OpenCodeInteractionRequest{Version: domain.OpenCodeProtocolVersion, NativeEventID: "evt_01960dcbe1fbABCDEFGHIJKLMN", NativeMessageID: tool.NativeParentID, CallID: "original", Permission: &domain.OpenCodePermission{Name: "read", Patterns: []string{"original/*.env"}, Always: []string{}, MetadataJSON: `{"exact":9007199254740993}`}}
	u := &domain.ExecutionInteractionUpdate{ID: domain.NewID(), Type: domain.NativeApprovalInteraction, NativeItemID: tool.NativeID, NativeRequestID: domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: "per_01960dcbe1fbABCDEFGHIJKLMN"}, OpenCode: r}
	if question {
		u.Type, u.NativeRequestID.Text = domain.UserQuestionInteraction, "que_01960dcbe1fbABCDEFGHIJKLMN"
		r.Permission, r.Questions = nil, []domain.OpenCodeQuestion{{Text: "Original question", Header: "", Options: []domain.QuestionOption{}}}
	}
	e = f.event(domain.ExecutionInteractionRequested, 4)
	e.Interaction = u
	return f, e, tool
}

func TestOpenCodeOriginalProposalsReachInboxOnceWithoutResponseAuthority(t *testing.T) {
	for _, question := range []bool{false, true} {
		f, event, tool := openCodeInteractionPublicationFixture(t, question)
		request := f.publish(t, event)
		if response, err := f.call(request); err != nil || !response.Msg.Replayed {
			t.Fatal("original proposal receipt was lost")
		}
		client, ctx := inboxClient(f), context.Background()
		page, err := client.ListInbox(ctx, ownerRequest(f.service.Identity, &pb.ListInboxRequest{SessionId: string(f.input.SessionID)}))
		if err != nil || len(page.Msg.Entries) != 1 || page.Msg.Entries[0].Interaction == nil || page.Msg.Entries[0].Interaction.Id != string(event.Interaction.ID) {
			t.Fatal("original native request did not appear once in inbox")
		}
		var value domain.ExecutionInteraction
		if domain.Decode(page.Msg.Entries[0].Interaction.DocumentJson, &value) != nil || value.OpenCode == nil || value.Questions != nil || value.Approval != nil || value.Closure != domain.InteractionOpen {
			t.Fatal("native matrix/scope was converted into another harness request")
		}
		_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.original-response", nil, func(tx *store.Tx) (any, error) {
			if question {
				return f.service.acceptQuestionResponse(tx, domain.NewID(), event.Interaction.ID, 1, domain.QuestionResponseInput{Answers: map[string][]string{}})
			}
			return f.service.acceptApprovalResponse(tx, domain.NewID(), event.Interaction.ID, 1, domain.ApprovalResponseInput{Decision: &domain.CodexApprovalDecision{Kind: domain.CodexApprovalAccept}})
		})
		if domain.SafeError(err).Code != domain.InvalidArgument {
			t.Fatal("another harness response acquired native authority", err)
		}
		// Even an original failed tool cannot consume the still-open request.
		failure, end := "original failure", uint64(200)
		if question {
			tool.Snapshot = &domain.ToolSnapshot{Kind: domain.OpenCodeBuiltinTool, Status: domain.ToolFailed, Builtin: &domain.OpenCodeBuiltinObservation{Name: domain.OpenCodeQuestionTool, CallID: "original", InputJSON: `{}`, Error: &failure, Timing: &domain.OpenCodeToolTiming{Start: 100, End: &end}}}
		} else {
			tool.Snapshot = &domain.ToolSnapshot{Kind: domain.OpenCodeReadTool, Status: domain.ToolFailed, Read: &domain.OpenCodeReadObservation{CallID: "original", Error: &failure, Timing: &domain.OpenCodeToolTiming{Start: 100, End: &end}}}
		}
		completion := f.event(domain.ExecutionToolCompleted, 5)
		completion.Tool = &tool
		if _, err := f.call(f.requestEvent(t, completion)); err == nil {
			t.Fatal("pending native request was bypassed by tool completion")
		}
		read, err := client.SetInboxReadState(ctx, ownerRequest(f.service.Identity, &pb.SetInboxReadStateRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: page.Msg.Entries[0].Entry.Id, ExpectedRevision: page.Msg.Entries[0].Entry.Revision}, ReadState: pb.InboxReadState_INBOX_READ_STATE_READ}))
		if err != nil || read.Msg.View.Interaction.Revision != 1 {
			t.Fatal("reading native inbox request changed its response/closure", err)
		}
	}
}

func TestOpenCodeProposalRejectsChangedOwnersAndDuplicateNativeEvents(t *testing.T) {
	for _, name := range []string{"part", "parent", "call", "namespace", "mixed", "duplicate-request", "duplicate-event", "foreign-profile"} {
		t.Run(name, func(t *testing.T) {
			f, event, _ := openCodeInteractionPublicationFixture(t, false)
			switch name {
			case "part":
				event.Interaction.NativeItemID = "prt_01960dcbe1fcABCDEFGHIJKLMN"
			case "parent":
				event.Interaction.OpenCode.NativeMessageID = "msg_01960dcbe1fcABCDEFGHIJKLMN"
			case "call":
				event.Interaction.OpenCode.CallID = "changed"
			case "namespace":
				event.Interaction.NativeRequestID.Text = "que_01960dcbe1fbABCDEFGHIJKLMN"
			case "mixed":
				event.Interaction.Questions = &domain.QuestionRequest{Questions: []domain.Question{}}
			case "duplicate-request", "duplicate-event":
				f.publish(t, event)
				event.Sequence++
				event.Interaction.ID = domain.NewID()
				if name == "duplicate-event" {
					event.Interaction.NativeRequestID.Text = "per_01960dcbe1fcABCDEFGHIJKLMN"
				}
			case "foreign-profile":
				input := f.input
				input.Configuration.Harness = domain.Codex
				if validateNativeMessageOrigin(input, event) == nil {
					t.Fatal("Codex acquired an OpenCode request")
				}
				return
			}
			// Malformed documents are sent directly to exercise RPC validation.
			raw, _ := json.Marshal(event)
			request := f.requestEvent(t, f.event(domain.ExecutionNoticeObserved, event.Sequence))
			request.EventJson = raw
			if _, err := f.call(request); err == nil {
				t.Fatal("invalid original proposal was accepted")
			}
		})
	}
}
