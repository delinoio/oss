package server

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func approvalDeliveryEvent(f *publicationFixture, claim *pb.ClaimApprovalResponseRequest, delivery domain.ApprovalDelivery) domain.ExecutionEvent {
	e := f.event(domain.ExecutionApprovalDeliveryObserved, 4)
	e.ApprovalResponse = &domain.ExecutionApprovalResponseUpdate{InteractionID: domain.ID(claim.Mutation.Id), ResponseID: domain.ID(claim.ResponseId), ClaimID: domain.ID(claim.Mutation.RequestId), NativeItemID: "approval-tool", Delivery: delivery}
	return e
}

func TestApprovalDeliveryRetainsTransportSeparatelyFromAcceptance(t *testing.T) {
	for _, delivery := range []domain.ApprovalDelivery{domain.ApprovalNotSent, domain.ApprovalTransmitted, domain.ApprovalDeliveryUncertain} {
		t.Run(string(delivery), func(t *testing.T) {
			f, claim := approvalClaimFixture(t)
			if _, err := claimApproval(f, claim); err != nil {
				t.Fatal(err)
			}
			receipt := f.publish(t, approvalDeliveryEvent(f, claim, delivery))
			if replay, err := f.call(receipt); err != nil || !replay.Msg.Replayed {
				t.Fatal("delivery lost its exact event receipt", err)
			}
			id := domain.ID(claim.Mutation.Id)
			r, value := readPublishedInteraction(t, f, id)
			state := domain.ApprovalResponseTransmitted
			if delivery == domain.ApprovalNotSent {
				state = domain.ApprovalResponseCanceled
			} else if delivery == domain.ApprovalDeliveryUncertain {
				state = domain.ApprovalResponseUncertain
			}
			if r.Revision != 4 || value.ApprovalResponse.State != state || value.Closure != domain.InteractionOpen || value.ApprovalResponse.Delivery.State != delivery || value.ApprovalResponse.Delivery.Sequence != 4 || !reflect.DeepEqual(value.ApprovalResponse.Input, approvalInput()) || value.LastSequence != 4 {
				t.Fatal("delivery changed original response or invented native closure/acceptance")
			}
			duplicate := approvalDeliveryEvent(f, claim, delivery)
			duplicate.Sequence = 5
			if _, err := f.call(f.requestEvent(t, duplicate)); err == nil {
				t.Fatal("another sequence replaced an immutable delivery observation")
			}
			closed := approvalFixtureEvent(f, 5, id, "native-approval")
			closed.Kind, closed.Interaction.Approval, closed.Interaction.Closure = domain.ExecutionInteractionClosed, nil, domain.InteractionNativeClosed
			f.publish(t, closed)
			terminal := f.event(domain.ExecutionTurnFinished, 6)
			terminal.Outcome = domain.ExecutionSucceeded
			f.publish(t, terminal)
			_, value = readPublishedInteraction(t, f, id)
			if value.ApprovalResponse.Delivery.State != delivery || value.ApprovalResponse.State != state || value.Closure != domain.InteractionNativeClosed {
				t.Fatal("native closure replaced the transport observation")
			}
			sr, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.Decode[domain.Session](sr)
			if err != nil || session.Execution.Outcome != domain.ExecutionSucceeded {
				t.Fatal("response gate changed the native outcome")
			}
			if delivery == domain.ApprovalNotSent {
				if session.Execution.UnconfirmedResponses != 0 || session.Recovery != domain.NoRecovery {
					t.Fatal("proven unsent response acquired acceptance uncertainty")
				}
			} else if session.Execution.UnconfirmedResponses != 1 || session.Recovery != domain.NeedsRecovery || session.Dispatch != domain.DispatchPaused || (delivery == domain.ApprovalTransmitted && session.Outcome != domain.ExecutionSucceeded) {
				t.Fatal("pipe transmission or closure falsely cleared native acceptance recovery")
			}
		})
	}
}

func TestApprovalDeliveryRejectsUnclaimedAndMismatchedObservations(t *testing.T) {
	for _, changed := range []string{"unclaimed", "response", "claim", "interaction", "item", "turn", "thread", "delivery", "other-payload", "answers"} {
		t.Run(changed, func(t *testing.T) {
			f, claim := approvalClaimFixture(t)
			if changed != "unclaimed" {
				if _, err := claimApproval(f, claim); err != nil {
					t.Fatal(err)
				}
			}
			before, original := readPublishedInteraction(t, f, domain.ID(claim.Mutation.Id))
			e := approvalDeliveryEvent(f, claim, domain.ApprovalTransmitted)
			switch changed {
			case "response":
				e.ApprovalResponse.ResponseID = domain.NewID()
			case "claim":
				e.ApprovalResponse.ClaimID = domain.NewID()
			case "interaction":
				e.ApprovalResponse.InteractionID = domain.NewID()
			case "item":
				e.ApprovalResponse.NativeItemID = "foreign"
			case "turn":
				e.NativeTurnID = string(domain.NewID())
			case "thread":
				e.NativeThreadID = string(domain.NewID())
			case "delivery":
				e.ApprovalResponse.Delivery = "accepted"
			case "other-payload":
				e.Outcome = domain.ExecutionSucceeded
			}
			req := f.requestEvent(t, e)
			if changed == "answers" {
				var document map[string]any
				_ = json.Unmarshal(req.EventJson, &document)
				document["approval_response"].(map[string]any)["decision"] = approvalInput().Decision
				req.EventJson, _ = json.Marshal(document)
			}
			if _, err := f.call(req); err == nil {
				t.Fatal("invalid native delivery acquired response ownership")
			}
			after, value := readPublishedInteraction(t, f, before.ID)
			if after.Revision != before.Revision || !reflect.DeepEqual(value, original) {
				t.Fatal("rejected observation partially changed response evidence")
			}
			sr, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.Decode[domain.Session](sr)
			if err != nil || session.Execution.LastSequence != 3 || session.Execution.UnconfirmedResponses != 0 {
				t.Fatal("rejected observation consumed sequence or altered response accounting")
			}
		})
	}
}

func TestApprovalCommandCompletionPreservesPinnedSourceTransition(t *testing.T) {
	for _, change := range []string{"valid", "closed", "absent", "wrong-item", "wrong-kind", "stdin", "later-callback", "source", "command", "cwd", "preexisting"} {
		t.Run(change, func(t *testing.T) {
			f := newPublicationFixture(t)
			f.publish(t, f.event(domain.ExecutionThreadBound, 1))
			f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
			id := domain.NewID()
			started := f.toolEvent(domain.ExecutionToolStarted, 3, id, "approval-tool")
			started.Tool.Snapshot = toolCommand()
			approval := approvalFixtureEvent(f, 4, domain.NewID(), "native-approval")
			if change == "preexisting" {
				approval.Sequence, started.Sequence = 3, 4
			}
			switch change {
			case "wrong-item":
				approval.Interaction.NativeItemID = "foreign-tool"
			case "wrong-kind":
				approval.Interaction.Approval.Codex.Kind = domain.CodexFileApproval
				approval.Interaction.Approval.Codex.Command = nil
				approval.Interaction.Approval.Codex.File = &domain.CodexFileApprovalRequest{}
			case "stdin":
				a := "stdin-approval"
				approval.Interaction.Approval.Codex.Command.Kind = domain.CodexWriteStdinApproval
				approval.Interaction.Approval.Codex.Command.ApprovalID = &a
			case "later-callback":
				a := "later-approval"
				approval.Interaction.Approval.Codex.Command.ApprovalID = &a
			}
			if change == "preexisting" {
				f.publish(t, approval)
			}
			f.publish(t, started)
			sequence := uint64(4)
			if change != "absent" && change != "preexisting" {
				f.publish(t, approval)
				sequence++
			}
			if change == "preexisting" {
				sequence = 5
			}
			if change == "closed" {
				approval.Sequence, approval.Kind, approval.Interaction.Approval, approval.Interaction.Closure = sequence, domain.ExecutionInteractionClosed, nil, domain.InteractionNativeClosed
				f.publish(t, approval)
				sequence++
			}
			done := f.toolEvent(domain.ExecutionToolCompleted, sequence, id, "approval-tool")
			done.Tool.Snapshot = toolCommand()
			done.Tool.Snapshot.Status = domain.ToolCompleted
			done.Tool.Snapshot.Command.Source = domain.ExecStartupCommand
			switch change {
			case "source":
				done.Tool.Snapshot.Command.Source = domain.UserShellCommand
			case "command":
				done.Tool.Snapshot.Command.Command = "different command"
			case "cwd":
				done.Tool.Snapshot.Command.Cwd = "/different"
			}
			_, err := f.call(f.requestEvent(t, done))
			want := change == "valid" || change == "closed"
			if (err == nil) != want {
				t.Fatalf("unexpected source transition: %v", err)
			}
			r, err := f.service.Store.Get(context.Background(), domain.MessageKind, id)
			if err != nil {
				t.Fatal(err)
			}
			message, err := store.Decode[domain.ExecutionMessage](r)
			if err != nil || message.Tool.Started.Command.Source != domain.AgentCommand || (message.Tool.Completed != nil) != want {
				t.Fatal("source transition rewrote original or retained invalid completion")
			}
			if want && message.Tool.Completed.Command.Source != domain.ExecStartupCommand {
				t.Fatal("canonical source was erased")
			}
		})
	}
}
