package server

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func openCodeStopClosureFixture(t *testing.T, question bool) (*publicationFixture, domain.ExecutionEvent) {
	t.Helper()
	f, proposal, _ := openCodeInteractionPublicationFixture(t, question)
	f.registerGrant(t)
	f.publish(t, proposal)
	input := domain.ExecutionMessageUpdate{ID: domain.NewID(), NativeID: "prt_01960dcbe1faABCDEFGHIJKLMN", NativeParentID: string(f.turn), InputID: f.input.InputID, Role: domain.UserMessage, Text: f.input.Input.Prompt}
	for i, kind := range []domain.ExecutionEventKind{domain.ExecutionMessageStarted, domain.ExecutionMessageCompleted} {
		e := f.event(kind, uint64(5+i))
		e.Message = &input
		f.publish(t, e)
	}
	u := proposal.Interaction
	proof := domain.OpenCodeStopObservation{RequestID: domain.NewID(), InputRequestID: f.input.TurnRequestID, InputPartID: input.NativeID, AssistantID: u.OpenCode.NativeMessageID, HistoryDigest: strings.Repeat("ab", 32), HTTPAccepted: false, InterruptedObserved: true, TerminalObserved: true, IdleObserved: true, PendingCleared: true, CleanupVerified: true}
	e := f.event(domain.ExecutionInteractionClosed, 7)
	e.Interaction = &domain.ExecutionInteractionUpdate{ID: u.ID, NativeItemID: u.NativeItemID, NativeRequestID: u.NativeRequestID, Type: u.Type, Closure: domain.InteractionTurnEnded, OpenCodeStop: &domain.OpenCodeStopClosure{Stop: proof, ProposalEventID: u.OpenCode.NativeEventID, ToolInterrupted: true}}
	return f, e
}

func requestOpenCodeStopFixture(t *testing.T, f *publicationFixture) {
	t.Helper()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.original-stop", nil, func(tx *store.Tx) (any, error) { return nil, tx.RequestJobCancellation(f.job) })
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenCodeStopClosurePreservesQueuedResponsesAndInboxReadState(t *testing.T) {
	for _, question := range []bool{false, true} {
		f, closure := openCodeStopClosureFixture(t, question)
		id := closure.Interaction.ID
		var err error
		if question {
			_, err = acceptFixtureResponse(f, domain.NewID(), id, 1, domain.QuestionResponseInput{OpenCode: &domain.OpenCodeQuestionResponse{Answers: [][]string{{"Original reply"}}}})
		} else {
			_, err = acceptFixtureApproval(f, domain.NewID(), id, 1, domain.ApprovalResponseInput{OpenCode: &domain.OpenCodePermissionResponse{Decision: domain.OpenCodePermissionOnce}})
		}
		if err != nil {
			t.Fatal(err)
		}
		client, ctx := inboxClient(f), context.Background()
		page, err := client.ListInbox(ctx, ownerRequest(f.service.Identity, &pb.ListInboxRequest{SessionId: string(f.input.SessionID)}))
		if err != nil || len(page.Msg.Entries) != 1 {
			t.Fatal(err)
		}
		entry := page.Msg.Entries[0].Entry
		read, err := client.SetInboxReadState(ctx, ownerRequest(f.service.Identity, &pb.SetInboxReadStateRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: entry.Id, ExpectedRevision: entry.Revision}, ReadState: pb.InboxReadState_INBOX_READ_STATE_READ}))
		if err != nil {
			t.Fatal(err)
		}
		requestOpenCodeStopFixture(t, f)
		request := f.publish(t, closure)
		if reply, err := f.call(request); err != nil || !reply.Msg.Replayed {
			t.Fatal("Stop closure lost its exact receipt", err)
		}
		_, value := readPublishedInteraction(t, f, id)
		if value.Closure != domain.InteractionTurnEnded || !reflect.DeepEqual(value.OpenCodeStop, closure.Interaction.OpenCodeStop) || value.OpenCodeClosure != nil {
			t.Fatal("Stop acquired native rejection authority")
		}
		if question && (value.Response.State != domain.QuestionResponseCanceled || value.Response.Claim != nil || value.Response.Acceptance != nil || value.Response.Delivery != nil) || !question && (value.ApprovalResponse.State != domain.ApprovalResponseCanceled || value.ApprovalResponse.Claim != nil || value.ApprovalResponse.Acceptance != nil || value.ApprovalResponse.Delivery != nil) {
			t.Fatal("Stop rewrote queued response as transmitted or accepted")
		}
		page, err = client.ListInbox(ctx, ownerRequest(f.service.Identity, &pb.ListInboxRequest{SessionId: string(f.input.SessionID)}))
		if err != nil || len(page.Msg.Entries) != 1 || page.Msg.Entries[0].Entry.Revision != read.Msg.View.Entry.Revision {
			t.Fatal("Stop changed original inbox read state")
		}
	}
}

func TestOpenCodeStopClosureRejectsMissingOrConflictingAuthorityAtomically(t *testing.T) {
	for _, name := range []string{"no-cancel", "input", "part", "proposal", "unsettled", "unclean", "tool", "unobserved", "claimed-question", "claimed-permission", "changed-proof"} {
		t.Run(name, func(t *testing.T) {
			question := name == "claimed-question"
			f, closure := openCodeStopClosureFixture(t, question)
			p := closure.Interaction.OpenCodeStop
			switch name {
			case "input":
				p.Stop.InputRequestID = domain.NewID()
			case "part":
				p.Stop.InputPartID = closure.Interaction.NativeItemID
			case "proposal":
				p.ProposalEventID = "evt_01960dcbe1ffABCDEFGHIJKLMN"
			case "unsettled":
				p.Stop.TerminalObserved = false
			case "unclean":
				p.Stop.CleanupVerified = false
			case "tool":
				p.ToolInterrupted = false
			case "unobserved":
				p.Stop.InterruptedObserved = false
			case "claimed-question", "claimed-permission":
				response := domain.NewID()
				id := closure.Interaction.ID
				if question {
					if _, err := acceptFixtureResponse(f, response, id, 1, domain.QuestionResponseInput{OpenCode: &domain.OpenCodeQuestionResponse{Answers: [][]string{{"Original reply"}}}}); err != nil {
						t.Fatal(err)
					}
					if _, err := claimQuestion(f, &pb.ClaimQuestionResponseRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(id), ExpectedRevision: 2}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: string(response)}); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err := acceptFixtureApproval(f, response, id, 1, domain.ApprovalResponseInput{OpenCode: &domain.OpenCodePermissionResponse{Decision: domain.OpenCodePermissionOnce}}); err != nil {
						t.Fatal(err)
					}
					if _, err := claimApproval(f, &pb.ClaimApprovalResponseRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(id), ExpectedRevision: 2}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: string(response)}); err != nil {
						t.Fatal(err)
					}
				}
			case "changed-proof":
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.prior-stop-proof", nil, func(tx *store.Tx) (any, error) {
					r, s, err := sessionRecord(tx, f.input.SessionID)
					if err != nil {
						return nil, err
					}
					prior := p.Stop
					prior.RequestID = domain.NewID()
					s.Execution.OpenCodeStop = &prior
					return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, s)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if name != "no-cancel" {
				requestOpenCodeStopFixture(t, f)
			}
			before, _ := readPublishedInteraction(t, f, closure.Interaction.ID)
			if name == "unclean" {
				if _, err := f.call(f.requestEvent(t, closure)); err != nil {
					t.Fatal(err)
				}
				return
			}
			if _, err := f.call(f.requestEvent(t, closure)); err == nil {
				t.Fatal("unproven Stop canceled an original request")
			}
			after, value := readPublishedInteraction(t, f, closure.Interaction.ID)
			if before.Revision != after.Revision || value.Closure != domain.InteractionOpen || value.OpenCodeStop != nil {
				t.Fatal("invalid Stop partially changed original state")
			}
		})
	}
}
