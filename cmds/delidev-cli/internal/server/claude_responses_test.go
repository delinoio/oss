package server

import (
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestClaudeReplyEchoPreservesDeliveryWithoutGrantingAcceptance(t *testing.T) {
	for _, question := range []bool{false, true} {
		f, original, sequence := claudeCallbackPublicationFixture(t, question)
		f.registerGrant(t)
		e := f.event(domain.ExecutionInteractionRequested, sequence+1)
		e.Interaction = &original
		f.publish(t, e)
		response, claimID := domain.NewID(), domain.NewID()
		reply := &domain.ClaudePermissionResponse{Behavior: domain.ClaudeReplyAllow}
		meta := &pb.Mutation{RequestId: string(claimID), Id: string(original.ID), ExpectedRevision: 2}
		if question {
			reply.Answers = map[string]string{"Original?": "Two, One"}
			if _, err := acceptFixtureResponse(f, response, original.ID, 1, domain.QuestionResponseInput{Claude: reply}); err != nil {
				t.Fatal(err)
			}
			if _, err := claimQuestion(f, &pb.ClaimQuestionResponseRequest{Mutation: meta, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: string(response)}); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := acceptFixtureApproval(f, response, original.ID, 1, domain.ApprovalResponseInput{Claude: reply}); err != nil {
				t.Fatal(err)
			}
			if _, err := claimApproval(f, &pb.ClaimApprovalResponseRequest{Mutation: meta, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: string(response)}); err != nil {
				t.Fatal(err)
			}
		}
		_, value := readPublishedInteraction(t, f, original.ID)
		digest, err := domain.ClaudeResponseDigest(value, *reply)
		if err != nil {
			t.Fatal(err)
		}
		echo := &domain.ExecutionClaudeReplyEcho{InteractionID: original.ID, ResponseID: response, ClaimID: claimID, ArrivalID: original.Claude.ArrivalID, NativeItemID: original.NativeItemID, BodyDigest: digest}
		e = f.event(domain.ExecutionClaudeReplyEchoObserved, sequence+2)
		e.ClaudeReplyEcho = echo
		if _, err := f.call(f.requestEvent(t, e)); err == nil {
			t.Fatal("echo preceded delivery")
		}
		if question {
			e = f.event(domain.ExecutionQuestionDeliveryObserved, sequence+2)
			e.QuestionResponse = &domain.ExecutionQuestionResponseUpdate{InteractionID: original.ID, ResponseID: response, ClaimID: claimID, NativeItemID: original.NativeItemID, Delivery: domain.QuestionTransmitted}
		} else {
			e = f.event(domain.ExecutionApprovalDeliveryObserved, sequence+2)
			e.ApprovalResponse = &domain.ExecutionApprovalResponseUpdate{InteractionID: original.ID, ResponseID: response, ClaimID: claimID, NativeItemID: original.NativeItemID, Delivery: domain.ApprovalTransmitted}
		}
		f.publish(t, e)
		for _, changed := range []string{"arrival", "digest", "claim", "response", "tool"} {
			copy := *echo
			switch changed {
			case "arrival":
				copy.ArrivalID = domain.NewID()
			case "claim":
				copy.ClaimID = domain.NewID()
			case "response":
				copy.ResponseID = domain.NewID()
			case "tool":
				copy.NativeItemID = "foreign"
			case "digest":
				copy.BodyDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			}
			e = f.event(domain.ExecutionClaudeReplyEchoObserved, sequence+3)
			e.ClaudeReplyEcho = &copy
			if _, err := f.call(f.requestEvent(t, e)); err == nil {
				t.Fatal("foreign echo accepted", changed)
			}
		}
		e.ClaudeReplyEcho = echo
		request := f.publish(t, e)
		if r, err := f.call(request); err != nil || !r.Msg.Replayed {
			t.Fatal("echo lost receipt", err)
		}
		_, value = readPublishedInteraction(t, f, original.ID)
		if value.Closure != domain.InteractionOpen || value.ClaudeCancellation != nil {
			t.Fatal("echo fabricated callback cancellation")
		}
		if question {
			if value.Response.State != domain.QuestionResponseTransmitted || value.Response.Acceptance != nil || value.Response.ClaudeEcho == nil || value.Response.ClaudeEcho.BodyDigest != digest {
				t.Fatal("echo became question acceptance")
			}
		} else if value.ApprovalResponse.State != domain.ApprovalResponseTransmitted || value.ApprovalResponse.Acceptance != nil || value.ApprovalResponse.ClaudeEcho == nil || value.ApprovalResponse.ClaudeEcho.BodyDigest != digest {
			t.Fatal("echo became approval acceptance")
		}
		e.Sequence++
		if _, err := f.call(f.requestEvent(t, e)); err == nil {
			t.Fatal("echo reused under a new receipt")
		}
	}
}
