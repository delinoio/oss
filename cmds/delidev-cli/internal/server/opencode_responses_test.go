package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func openCodeResponseFixture(t *testing.T, question bool) (*publicationFixture, domain.ExecutionEvent, domain.ExecutionEvent) {
	return openCodeResponsePolicyFixture(t, question, domain.OpenCodePermissionResponse{Decision: domain.OpenCodePermissionOnce}, false)
}

func openCodeResponsePolicyFixture(t *testing.T, question bool, permission domain.OpenCodePermissionResponse, rejectQuestion bool) (*publicationFixture, domain.ExecutionEvent, domain.ExecutionEvent) {
	t.Helper()
	f, proposal, _ := openCodeInteractionPublicationFixture(t, question)
	if !question && permission.Decision == domain.OpenCodePermissionAlways {
		proposal.Interaction.OpenCode.Permission.Always = []string{"original/*"}
	}
	f.registerGrant(t)
	f.publish(t, proposal)
	id, response, claim := proposal.Interaction.ID, domain.NewID(), domain.NewID()
	qi := domain.QuestionResponseInput{OpenCode: &domain.OpenCodeQuestionResponse{Answers: [][]string{{"Original reply"}}}}
	if rejectQuestion {
		qi.OpenCode = &domain.OpenCodeQuestionResponse{Reject: true}
	}
	ai := domain.ApprovalResponseInput{OpenCode: &permission}
	var err error
	var digest string
	if question {
		_, err = acceptFixtureResponse(f, response, id, 1, qi)
		digest, _ = domain.OpenCodeResponseDigest(qi.OpenCode, nil)
	} else {
		_, err = acceptFixtureApproval(f, response, id, 1, ai)
		digest, _ = domain.OpenCodeResponseDigest(nil, ai.OpenCode)
	}
	if err != nil {
		t.Fatal(err)
	}
	meta := &pb.Mutation{RequestId: string(claim), Id: string(id), ExpectedRevision: 2}
	if question {
		_, err = claimQuestion(f, &pb.ClaimQuestionResponseRequest{Mutation: meta, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: string(response)})
	} else {
		_, err = claimApproval(f, &pb.ClaimApprovalResponseRequest{Mutation: meta, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: string(response)})
	}
	if err != nil {
		t.Fatal(err)
	}
	evidence := &domain.OpenCodeReplyEvidence{NativeEventID: "evt_01960dcbe1fcABCDEFGHIJKLMN", ProposalEventID: proposal.Interaction.OpenCode.NativeEventID, NativeRequestID: proposal.Interaction.NativeRequestID.Text, BodyDigest: digest, HTTPAccepted: true}
	delivery := f.event(domain.ExecutionQuestionDeliveryObserved, 5)
	accepted := f.event(domain.ExecutionQuestionAccepted, 6)
	if question {
		delivery.QuestionResponse = &domain.ExecutionQuestionResponseUpdate{InteractionID: id, ResponseID: response, ClaimID: claim, NativeItemID: proposal.Interaction.NativeItemID, Delivery: domain.QuestionTransmitted}
		accepted.QuestionAcceptance = &domain.ExecutionQuestionAcceptanceUpdate{InteractionID: id, ResponseID: response, ClaimID: claim, NativeItemID: proposal.Interaction.NativeItemID, Evidence: domain.NativeOpenCodeQuestionReply, OpenCode: evidence}
		if rejectQuestion {
			accepted.QuestionAcceptance.Evidence = domain.NativeOpenCodeQuestionRejected
		}
	} else {
		delivery.Kind = domain.ExecutionApprovalDeliveryObserved
		delivery.ApprovalResponse = &domain.ExecutionApprovalResponseUpdate{InteractionID: id, ResponseID: response, ClaimID: claim, NativeItemID: proposal.Interaction.NativeItemID, Delivery: domain.ApprovalTransmitted}
		accepted.Kind = domain.ExecutionApprovalAccepted
		accepted.ApprovalAcceptance = &domain.ExecutionApprovalAcceptanceUpdate{InteractionID: id, ResponseID: response, ClaimID: claim, NativeItemID: proposal.Interaction.NativeItemID, Evidence: domain.NativeOpenCodePermissionReply, OpenCode: evidence}
	}
	f.publish(t, delivery)
	closure := f.event(domain.ExecutionInteractionClosed, 7)
	closure.Interaction = &domain.ExecutionInteractionUpdate{ID: id, NativeRequestID: proposal.Interaction.NativeRequestID, NativeItemID: proposal.Interaction.NativeItemID, Type: proposal.Interaction.Type, Closure: domain.InteractionNativeClosed}
	return f, accepted, closure
}

func TestOpenCodeResponseAcceptanceIsSeparateFromDeliveryClosureAndInboxRead(t *testing.T) {
	for _, question := range []bool{false, true} {
		f, accepted, closure := openCodeResponseFixture(t, question)
		premature := closure
		premature.Sequence = 6
		if _, err := f.call(f.requestEvent(t, premature)); err == nil {
			t.Fatal("HTTP delivery fabricated native closure")
		}
		request := f.publish(t, accepted)
		if result, err := f.call(request); err != nil || !result.Msg.Replayed {
			t.Fatal("original reply receipt was not retained")
		}
		f.publish(t, closure)
		_, value := readPublishedInteraction(t, f, closure.Interaction.ID)
		if value.Closure != domain.InteractionNativeClosed || question && (value.Response == nil || value.Response.State != domain.QuestionResponseAccepted || value.Response.Acceptance.OpenCode == nil) || !question && (value.ApprovalResponse == nil || value.ApprovalResponse.State != domain.ApprovalResponseAccepted || value.ApprovalResponse.Acceptance.OpenCode == nil) {
			t.Fatal("original native acceptance changed")
		}
		sr, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
		session, decodeErr := store.Decode[domain.Session](sr)
		if err != nil || decodeErr != nil || session.Execution.UnconfirmedResponses != 0 || session.Execution.CleanupVerified || session.Execution.Outcome != domain.ExecutionRunning {
			t.Fatal("reply fabricated root completion or cleanup")
		}
		page, err := inboxClient(f).ListInbox(context.Background(), ownerRequest(f.service.Identity, &pb.ListInboxRequest{SessionId: string(f.input.SessionID)}))
		if err != nil || len(page.Msg.Entries) != 1 {
			t.Fatal("native response duplicated or removed inbox")
		}
		var entry domain.InboxEntry
		if domain.Decode(page.Msg.Entries[0].Entry.DocumentJson, &entry) != nil || entry.ReadState != domain.InboxUnread {
			t.Fatal("response implicitly marked inbox read")
		}
	}
}

func TestOpenCodeReplyAcceptanceRejectsChangedEvidenceAtomically(t *testing.T) {
	for _, question := range []bool{false, true} {
		for _, name := range []string{"digest", "proposal", "request", "http", "claim", "legacy", "duplicate-event"} {
			t.Run(string(map[bool]domain.InteractionType{true: domain.UserQuestionInteraction, false: domain.NativeApprovalInteraction}[question])+"/"+name, func(t *testing.T) {
				f, event, closure := openCodeResponseFixture(t, question)
				var evidence *domain.OpenCodeReplyEvidence
				if question {
					evidence = event.QuestionAcceptance.OpenCode
				} else {
					evidence = event.ApprovalAcceptance.OpenCode
				}
				switch name {
				case "digest":
					evidence.BodyDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
				case "proposal":
					evidence.ProposalEventID = "evt_01960dcbe1fdABCDEFGHIJKLMN"
				case "request":
					evidence.NativeRequestID = evidence.NativeRequestID[:len(evidence.NativeRequestID)-1] + "Z"
				case "http":
					evidence.HTTPAccepted = false
				case "claim":
					if question {
						event.QuestionAcceptance.ClaimID = domain.NewID()
					} else {
						event.ApprovalAcceptance.ClaimID = domain.NewID()
					}
				case "legacy":
					if question {
						event.QuestionAcceptance.Evidence = domain.NativeQuestionOutput
						event.QuestionAcceptance.OpenCode = nil
					} else {
						event.ApprovalAcceptance.Evidence = domain.NativePermissionsOutput
						event.ApprovalAcceptance.OpenCode = nil
					}
				case "duplicate-event":
					evidence.NativeEventID = evidence.ProposalEventID
				}
				before, _ := readPublishedInteraction(t, f, closure.Interaction.ID)
				// A malformed native proof can fail local event construction as well as RPC validation.
				if event.Validate() == nil {
					if _, err := f.call(f.requestEvent(t, event)); err == nil {
						t.Fatal("changed native reply proof was accepted")
					}
				}
				after, value := readPublishedInteraction(t, f, closure.Interaction.ID)
				if before.Revision != after.Revision || value.Closure != domain.InteractionOpen {
					t.Fatal("rejected native proof changed original request")
				}
			})
		}
	}
}
