package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func claudeSettlementFixture(t *testing.T, kind domain.ClaudeInteractionKind, deny, uncertain, echo bool) (*publicationFixture, domain.ExecutionEvent, domain.ExecutionEvent) {
	t.Helper()
	return claudeSettlementResponseFixture(t, kind, deny, uncertain, echo, false)
}

func claudeSettlementResponseFixture(t *testing.T, kind domain.ClaudeInteractionKind, deny, uncertain, echo, interrupt bool) (*publicationFixture, domain.ExecutionEvent, domain.ExecutionEvent) {
	t.Helper()
	question := kind == domain.ClaudeUserQuestion
	f, original, sequence := claudeNamedCallbackPublicationFixture(t, kind)
	f.registerGrant(t)
	e := f.event(domain.ExecutionInteractionRequested, sequence+1)
	e.Interaction = &original
	f.publish(t, e)
	reply := &domain.ClaudePermissionResponse{Behavior: domain.ClaudeReplyAllow}
	if question {
		reply.Answers = map[string]string{"Original?": "Two, One"}
	}
	if deny {
		message := "Original denial"
		reply = &domain.ClaudePermissionResponse{Behavior: domain.ClaudeReplyDeny, Message: &message}
		if interrupt {
			reply.Interrupt = &interrupt
		}
	}
	response, claim := domain.NewID(), domain.NewID()
	meta := &pb.Mutation{RequestId: string(claim), Id: string(original.ID), ExpectedRevision: 2}
	if question {
		if _, err := acceptFixtureResponse(f, response, original.ID, 1, domain.QuestionResponseInput{Claude: reply}); err != nil {
			t.Fatal(err)
		}
		if _, err := claimQuestion(f, &pb.ClaimQuestionResponseRequest{Mutation: meta, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: string(response)}); err != nil {
			t.Fatal(err)
		}
		e = f.event(domain.ExecutionQuestionDeliveryObserved, sequence+2)
		delivery := domain.QuestionTransmitted
		if uncertain {
			delivery = domain.QuestionDeliveryUncertain
		}
		e.QuestionResponse = &domain.ExecutionQuestionResponseUpdate{InteractionID: original.ID, ResponseID: response, ClaimID: claim, NativeItemID: original.NativeItemID, Delivery: delivery}
	} else {
		if _, err := acceptFixtureApproval(f, response, original.ID, 1, domain.ApprovalResponseInput{Claude: reply}); err != nil {
			t.Fatal(err)
		}
		if _, err := claimApproval(f, &pb.ClaimApprovalResponseRequest{Mutation: meta, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: string(response)}); err != nil {
			t.Fatal(err)
		}
		e = f.event(domain.ExecutionApprovalDeliveryObserved, sequence+2)
		delivery := domain.ApprovalTransmitted
		if uncertain {
			delivery = domain.ApprovalDeliveryUncertain
		}
		e.ApprovalResponse = &domain.ExecutionApprovalResponseUpdate{InteractionID: original.ID, ResponseID: response, ClaimID: claim, NativeItemID: original.NativeItemID, Delivery: delivery}
	}
	f.publish(t, e)
	sequence += 2
	_, value := readPublishedInteraction(t, f, original.ID)
	digest, err := domain.ClaudeResponseDigest(value, *reply)
	if err != nil {
		t.Fatal(err)
	}
	identity := domain.ExecutionClaudeReplyEcho{InteractionID: original.ID, ResponseID: response, ClaimID: claim, ArrivalID: original.Claude.ArrivalID, NativeItemID: original.NativeItemID, BodyDigest: digest}
	if echo {
		sequence++
		e = f.event(domain.ExecutionClaudeReplyEchoObserved, sequence)
		e.ClaudeReplyEcho = &identity
		f.publish(t, e)
	}
	result := &domain.ClaudeToolResult{NativeEventID: string(domain.NewID())}
	evidence := domain.ClaudeToolProcessed
	if question {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal([]byte(original.Claude.InputJSON), &fields)
		fields["answers"], _ = json.Marshal(reply.Answers)
		raw, _ := json.Marshal(fields)
		output := string(raw)
		result.Structured = &output
		evidence = domain.ClaudeAnswersProcessed
	}
	if kind == domain.ClaudePlanApproval {
		output := `{"plan":"# Original plan","isAgent":false,"filePath":"/private/original-plan.md"}`
		result.Structured, evidence = &output, domain.ClaudePlanProcessed
	}
	if deny {
		failed := true
		result.Error, result.Structured = &failed, nil
		result.NonExecution = &domain.ClaudeToolNonExecution{NativeID: original.NativeItemID, Kind: domain.ClaudePermissionRuleNonExecution}
		evidence = domain.ClaudeDenialProcessed
		if interrupt {
			result.NonExecution.Kind, evidence = domain.ClaudeUserRejectedNonExecution, domain.ClaudeInterruptedDenialProcessed
		}
	}
	r := original.Claude
	tool := f.event(domain.ExecutionClaudeToolObserved, sequence+1)
	tool.ClaudeTool = &domain.ClaudeToolUpdate{Mutation: domain.ClaudeToolResultObserved, Reference: r.Tool, MessageID: r.MessageID, NativeMessageID: r.NativeMessageID, Index: r.Index, Caller: r.Caller, Result: result}
	settlement := f.event(domain.ExecutionClaudeCallbackSettled, sequence+2)
	settlement.ClaudeSettlement = &domain.ExecutionClaudeCallbackSettlement{ExecutionClaudeReplyEcho: identity, ToolMessageID: r.Tool.ID, ResultNativeID: result.NativeEventID, Evidence: evidence}
	return f, tool, settlement
}

func TestClaudeCallbackSettlementIsAtomicOnceOnlyAndKeepsPriorRecovery(t *testing.T) {
	for _, kind := range []domain.ClaudeInteractionKind{domain.ClaudeToolPermission, domain.ClaudeUserQuestion, domain.ClaudePlanApproval} {
		question := kind == domain.ClaudeUserQuestion
		for _, deny := range []bool{false, true} {
			for _, uncertain := range []bool{false, true} {
				f, tool, settlement := claudeSettlementFixture(t, kind, deny, uncertain, true)
				f.publish(t, tool)
				id := settlement.ClaudeSettlement.InteractionID
				_, before := readPublishedInteraction(t, f, id)
				if before.Closure != domain.InteractionOpen || before.ClaudeSettlement != nil {
					t.Fatal("tool result alone settled callback")
				}
				receipt := f.publish(t, settlement)
				if replay, err := f.call(receipt); err != nil || !replay.Msg.Replayed {
					t.Fatal("settlement receipt lost", err)
				}
				row, value := readPublishedInteraction(t, f, id)
				if value.Closure != domain.InteractionNativeClosed || value.ClaudeCancellation != nil || value.ClaudeSettlement == nil || value.ClaudeSettlement.Evidence != settlement.ClaudeSettlement.Evidence {
					t.Fatal("original settlement lost")
				}
				if question {
					if value.Response.State != domain.QuestionResponseAccepted || value.Response.Acceptance == nil {
						t.Fatal("question was not processed")
					}
				} else if value.ApprovalResponse.State != domain.ApprovalResponseAccepted || value.ApprovalResponse.Acceptance == nil {
					t.Fatal("approval was not processed")
				}
				settlement.Sequence++
				if _, err := f.call(f.requestEvent(t, settlement)); err == nil {
					t.Fatal("settlement counted twice")
				}
				after, _ := readPublishedInteraction(t, f, id)
				if after.Revision != row.Revision {
					t.Fatal("rejected settlement partially committed")
				}
				sr, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				session, err := store.Decode[domain.Session](sr)
				if err != nil || session.Execution.UnconfirmedResponses != 0 || session.Execution.Outcome != domain.ExecutionRunning || session.Execution.CleanupVerified || (session.Recovery == domain.NeedsRecovery) != uncertain {
					t.Fatal("settlement changed root outcome/recovery", err)
				}
				rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.InboxKind, SessionID: f.input.SessionID, Limit: 10})
				if err != nil || len(rows) != 1 {
					t.Fatal("settlement changed inbox", err)
				}
				inbox, err := store.Decode[domain.InboxEntry](rows[0])
				if err != nil || inbox.ReadState != domain.InboxUnread {
					t.Fatal("settlement marked inbox read", err)
				}
			}
		}
	}
}

func TestClaudeCallbackSettlementRejectsMissingAndForeignEvidence(t *testing.T) {
	for _, change := range []string{"no-echo", "no-result", "arrival", "response", "claim", "tool", "native-result", "digest", "kind", "canceled", "wrong-answer"} {
		t.Run(change, func(t *testing.T) {
			f, tool, e := claudeSettlementFixture(t, domain.ClaudeUserQuestion, false, false, change != "no-echo")
			u := e.ClaudeSettlement
			if change == "wrong-answer" {
				bad := `{"questions":[],"answers":{"Original?":"foreign"}}`
				tool.ClaudeTool.Result.Structured = &bad
			}
			if change != "no-result" {
				f.publish(t, tool)
			} else {
				e.Sequence--
			}
			switch change {
			case "arrival":
				u.ArrivalID = domain.NewID()
			case "response":
				u.ResponseID = domain.NewID()
			case "claim":
				u.ClaimID = domain.NewID()
			case "tool":
				u.ToolMessageID = domain.NewID()
			case "native-result":
				u.ResultNativeID = string(domain.NewID())
			case "digest":
				u.BodyDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			case "kind":
				u.Evidence = domain.ClaudeToolProcessed
			case "canceled":
				_, v := readPublishedInteraction(t, f, u.InteractionID)
				closed := f.event(domain.ExecutionInteractionClosed, e.Sequence)
				closed.Interaction = &domain.ExecutionInteractionUpdate{ID: u.InteractionID, NativeItemID: v.NativeItemID, NativeRequestID: v.NativeRequestID, Type: v.Type, Closure: domain.InteractionNativeClosed, ClaudeCancellation: &domain.ClaudeInteractionCancellation{ArrivalID: u.ArrivalID}}
				f.publish(t, closed)
				e.Sequence++
			}
			before, _ := readPublishedInteraction(t, f, u.InteractionID)
			if _, err := f.call(f.requestEvent(t, e)); err == nil {
				t.Fatal("unverified settlement accepted")
			}
			after, v := readPublishedInteraction(t, f, u.InteractionID)
			if before.Revision != after.Revision || v.ClaudeSettlement != nil || v.Response.State == domain.QuestionResponseAccepted {
				t.Fatal("rejection partially committed")
			}
		})
	}
}
