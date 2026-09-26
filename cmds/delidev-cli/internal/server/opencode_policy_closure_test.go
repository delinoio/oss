package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type nativePolicySourceStage uint8

const (
	nativePolicyDelivered nativePolicySourceStage = iota
	nativePolicyAccepted
	nativePolicyClosed
)

func openCodePolicyClosureFixture(t *testing.T, decision domain.OpenCodePermissionDecision, stage nativePolicySourceStage) (*publicationFixture, domain.ExecutionEvent) {
	t.Helper()
	f, accepted, directClosure := openCodeResponsePolicyFixture(t, false, domain.OpenCodePermissionResponse{Decision: decision}, false)
	sequence := accepted.Sequence
	if stage >= nativePolicyAccepted {
		f.publish(t, accepted)
		sequence++
	}
	if stage == nativePolicyClosed {
		f.publish(t, directClosure)
		sequence++
	}
	raw := ""
	tool := domain.ExecutionToolUpdate{ID: domain.NewID(), NativeID: "prt_01960dcbe1fcABCDEFGHIJKLMN", NativeParentID: "msg_01960dcbe1fbABCDEFGHIJKLMN", Snapshot: &domain.ToolSnapshot{Kind: domain.OpenCodeReadTool, Status: domain.ToolPending, Read: &domain.OpenCodeReadObservation{CallID: "automatic", Raw: &raw}}}
	event := f.event(domain.ExecutionToolStarted, sequence)
	event.Tool = &tool
	f.publish(t, event)
	request := domain.ExecutionInteractionUpdate{ID: domain.NewID(), Type: domain.NativeApprovalInteraction, NativeItemID: tool.NativeID, NativeRequestID: domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: "per_01960dcbe1fcABCDEFGHIJKLMN"}, OpenCode: &domain.OpenCodeInteractionRequest{Version: domain.OpenCodeProtocolVersion, NativeEventID: "evt_01960dcbe1fdABCDEFGHIJKLMN", NativeMessageID: tool.NativeParentID, CallID: "automatic", Permission: &domain.OpenCodePermission{Name: "read", Patterns: []string{"original/another.env"}, Always: []string{}, MetadataJSON: `{}`}}}
	event = f.event(domain.ExecutionInteractionRequested, sequence+1)
	event.Interaction = &request
	f.publish(t, event)
	policy := decision
	if policy == domain.OpenCodePermissionOnce {
		policy = domain.OpenCodePermissionAlways
	}
	closure := f.event(domain.ExecutionInteractionClosed, sequence+2)
	closure.Interaction = &domain.ExecutionInteractionUpdate{ID: request.ID, Type: request.Type, NativeRequestID: request.NativeRequestID, NativeItemID: request.NativeItemID, Closure: domain.InteractionNativeClosed, OpenCodeClosure: &domain.OpenCodePolicyClosure{NativeEventID: "evt_01960dcbe1feABCDEFGHIJKLMN", ProposalEventID: request.OpenCode.NativeEventID, Decision: policy, Sources: []domain.OpenCodePolicySource{{InteractionID: directClosure.Interaction.ID, NativeRequestID: directClosure.Interaction.NativeRequestID.Text}}}}
	return f, closure
}

func TestOpenCodePolicyClosurePreservesOnlyNativeContextAndCancelsQueuedResponse(t *testing.T) {
	for _, decision := range []domain.OpenCodePermissionDecision{domain.OpenCodePermissionAlways, domain.OpenCodePermissionReject} {
		for _, queued := range []bool{false, true} {
			f, closure := openCodePolicyClosureFixture(t, decision, nativePolicyClosed)
			id := closure.Interaction.ID
			if queued {
				if _, err := acceptFixtureApproval(f, domain.NewID(), id, 1, domain.ApprovalResponseInput{OpenCode: &domain.OpenCodePermissionResponse{Decision: domain.OpenCodePermissionOnce}}); err != nil {
					t.Fatal(err)
				}
			}
			request := f.publish(t, closure)
			if result, err := f.call(request); err != nil || !result.Msg.Replayed {
				t.Fatal("original native policy receipt changed")
			}
			_, value := readPublishedInteraction(t, f, id)
			if value.Closure != domain.InteractionNativeClosed || value.OpenCodeClosure == nil || value.OpenCodeClosure.Decision != decision || value.Response != nil {
				t.Fatal("original native automatic closure was lost")
			}
			if !queued && value.ApprovalResponse != nil || queued && (value.ApprovalResponse == nil || value.ApprovalResponse.State != domain.ApprovalResponseCanceled || value.ApprovalResponse.Claim != nil || value.ApprovalResponse.Delivery != nil || value.ApprovalResponse.Acceptance != nil) {
				t.Fatal("native cascade fabricated a claimed/delivered/accepted response")
			}
			page, err := inboxClient(f).ListInbox(context.Background(), ownerRequest(f.service.Identity, &pb.ListInboxRequest{SessionId: string(f.input.SessionID)}))
			if err != nil || len(page.Msg.Entries) != 2 {
				t.Fatal("native closure lost or duplicated independent inbox requests")
			}
		}
	}
}

func TestOpenCodePolicyClosureRefusesUnprovenOrClaimedContextAtomically(t *testing.T) {
	for _, name := range []string{"source-delivered", "source-accepted", "source-once", "proposal", "duplicate-event", "source-request", "source-self", "decision", "target-claimed"} {
		t.Run(name, func(t *testing.T) {
			stage, decision := nativePolicyClosed, domain.OpenCodePermissionAlways
			switch name {
			case "source-delivered":
				stage = nativePolicyDelivered
			case "source-accepted":
				stage = nativePolicyAccepted
			case "source-once":
				decision = domain.OpenCodePermissionOnce
			}
			f, closure := openCodePolicyClosureFixture(t, decision, stage)
			id, proof := closure.Interaction.ID, closure.Interaction.OpenCodeClosure
			switch name {
			case "proposal":
				proof.ProposalEventID = "evt_01960dcbe1ffABCDEFGHIJKLMN"
			case "duplicate-event":
				proof.NativeEventID = "evt_01960dcbe1fcABCDEFGHIJKLMN"
			case "source-request":
				proof.Sources[0].NativeRequestID = "per_01960dcbe1ffABCDEFGHIJKLMN"
			case "source-self":
				proof.Sources[0].InteractionID, proof.Sources[0].NativeRequestID = id, closure.Interaction.NativeRequestID.Text
			case "decision":
				proof.Decision = domain.OpenCodePermissionReject
			case "target-claimed":
				response := domain.NewID()
				if _, err := acceptFixtureApproval(f, response, id, 1, domain.ApprovalResponseInput{OpenCode: &domain.OpenCodePermissionResponse{Decision: domain.OpenCodePermissionOnce}}); err != nil {
					t.Fatal(err)
				}
				_, err := claimApproval(f, &pb.ClaimApprovalResponseRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(id), ExpectedRevision: 2}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: string(response)})
				if err != nil {
					t.Fatal(err)
				}
			}
			before, _ := readPublishedInteraction(t, f, id)
			if _, err := f.call(f.requestEvent(t, closure)); err == nil {
				t.Fatal("unproven policy acquired native closure authority")
			}
			after, value := readPublishedInteraction(t, f, id)
			if after.Revision != before.Revision || value.Closure != domain.InteractionOpen || value.OpenCodeClosure != nil {
				t.Fatal("invalid native policy changed original request")
			}
		})
	}
}

func TestOpenCodeQuestionAcceptanceKeepsRejectionDistinctFromAnswers(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		f, accepted, closure := openCodeResponsePolicyFixture(t, true, domain.OpenCodePermissionResponse{}, rejected)
		wrong := accepted
		update := *accepted.QuestionAcceptance
		wrong.QuestionAcceptance = &update
		if rejected {
			update.Evidence = domain.NativeOpenCodeQuestionReply
		} else {
			update.Evidence = domain.NativeOpenCodeQuestionRejected
		}
		if _, err := f.call(f.requestEvent(t, wrong)); err == nil {
			t.Fatal("native question rejection was confused with an answer")
		}
		f.publish(t, accepted)
		f.publish(t, closure)
		_, value := readPublishedInteraction(t, f, closure.Interaction.ID)
		if value.Response == nil || value.Response.Input.OpenCode.Reject != rejected || value.Response.Acceptance.Evidence != accepted.QuestionAcceptance.Evidence || value.Closure != domain.InteractionNativeClosed {
			t.Fatal("native question response lost its separate rejection evidence")
		}
	}
}
