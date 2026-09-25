package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func approvalAcceptanceEvent(f *publicationFixture, claim *pb.ClaimApprovalResponseRequest, sequence uint64) domain.ExecutionEvent {
	e := f.event(domain.ExecutionApprovalAccepted, sequence)
	e.ApprovalAcceptance = &domain.ExecutionApprovalAcceptanceUpdate{InteractionID: domain.ID(claim.Mutation.Id), ResponseID: domain.ID(claim.ResponseId), ClaimID: domain.ID(claim.Mutation.RequestId), NativeItemID: "approval-tool", Evidence: domain.NativePermissionsOutput}
	return e
}

func TestApprovalAcceptanceReconcilesExactlyOnceWithoutClearingPriorRecovery(t *testing.T) {
	for _, delivery := range []domain.ApprovalDelivery{domain.ApprovalTransmitted, domain.ApprovalDeliveryUncertain} {
		for _, closedFirst := range []bool{false, true} {
			t.Run(string(delivery)+map[bool]string{false: "/open", true: "/closed"}[closedFirst], func(t *testing.T) {
				f, claim := permissionClaimFixture(t)
				if _, err := claimApproval(f, claim); err != nil {
					t.Fatal(err)
				}
				f.publish(t, approvalDeliveryEvent(f, claim, delivery))
				sequence := uint64(5)
				id := domain.ID(claim.Mutation.Id)
				if closedFirst {
					closed := approvalFixtureEvent(f, sequence, id, "native-approval")
					closed.Kind, closed.Interaction.Approval, closed.Interaction.Closure = domain.ExecutionInteractionClosed, nil, domain.InteractionNativeClosed
					f.publish(t, closed)
					sequence++
				}
				receipt := f.publish(t, approvalAcceptanceEvent(f, claim, sequence))
				if replay, err := f.call(receipt); err != nil || !replay.Msg.Replayed {
					t.Fatal("acceptance receipt lost", err)
				}
				before, accepted := readPublishedInteraction(t, f, id)
				if accepted.ApprovalResponse.State != domain.ApprovalResponseAccepted || accepted.ApprovalResponse.Acceptance == nil || accepted.ApprovalResponse.Acceptance.Sequence != sequence || accepted.ApprovalResponse.Acceptance.Evidence != domain.NativePermissionsOutput || accepted.ApprovalResponse.Delivery.State != delivery || !reflect.DeepEqual(accepted.ApprovalResponse.Input, permissionResponseInput()) {
					t.Fatal("native acceptance changed original transport/content evidence")
				}
				if _, err := f.call(f.requestEvent(t, approvalAcceptanceEvent(f, claim, sequence+1))); err == nil {
					t.Fatal("acceptance was counted twice")
				}
				after, value := readPublishedInteraction(t, f, id)
				if after.Revision != before.Revision || !reflect.DeepEqual(accepted, value) {
					t.Fatal("duplicate partially replaced acceptance")
				}
				terminal := f.event(domain.ExecutionTurnFinished, sequence+1)
				terminal.Outcome = domain.ExecutionSucceeded
				f.publish(t, terminal)
				sr, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				session, err := store.Decode[domain.Session](sr)
				if err != nil || session.Execution.UnconfirmedResponses != 0 || session.Execution.Outcome != domain.ExecutionSucceeded || session.Execution.CleanupVerified {
					t.Fatal("acceptance invented native cleanup or lost terminal facts")
				}
				if (delivery == domain.ApprovalTransmitted && session.Recovery != domain.NoRecovery) || (delivery == domain.ApprovalDeliveryUncertain && session.Recovery != domain.NeedsRecovery) {
					t.Fatal("acceptance failed to distinguish healthy execution from prior recovery")
				}
				_, value = readPublishedInteraction(t, f, id)
				if value.ApprovalResponse.State != domain.ApprovalResponseAccepted || value.Closure == domain.InteractionOpen {
					t.Fatal("terminal erased native acceptance")
				}
			})
		}
	}
}

func TestApprovalAcceptanceRejectsUnownedOrContentBearingEvidenceAtomically(t *testing.T) {
	for _, change := range []string{"command", "no-delivery", "not-sent", "claim", "response", "interaction", "item", "turn", "thread", "evidence", "delivery-field", "grant"} {
		t.Run(change, func(t *testing.T) {
			f, claim := permissionClaimFixture(t)
			if change == "command" {
				f, claim = approvalClaimFixture(t)
			}
			if _, err := claimApproval(f, claim); err != nil {
				t.Fatal(err)
			}
			sequence := uint64(4)
			if change != "no-delivery" {
				delivery := domain.ApprovalTransmitted
				if change == "not-sent" {
					delivery = domain.ApprovalNotSent
				}
				f.publish(t, approvalDeliveryEvent(f, claim, delivery))
				sequence++
			}
			before, original := readPublishedInteraction(t, f, domain.ID(claim.Mutation.Id))
			e := approvalAcceptanceEvent(f, claim, sequence)
			switch change {
			case "claim":
				e.ApprovalAcceptance.ClaimID = domain.NewID()
			case "response":
				e.ApprovalAcceptance.ResponseID = domain.NewID()
			case "interaction":
				e.ApprovalAcceptance.InteractionID = domain.NewID()
			case "item":
				e.ApprovalAcceptance.NativeItemID = "foreign"
			case "turn":
				e.NativeTurnID = string(domain.NewID())
			case "thread":
				e.NativeThreadID = string(domain.NewID())
			case "evidence":
				e.ApprovalAcceptance.Evidence = "native-closed"
			case "delivery-field":
				e.ApprovalResponse = approvalDeliveryEvent(f, claim, domain.ApprovalTransmitted).ApprovalResponse
			}
			req := f.requestEvent(t, e)
			if change == "grant" {
				var raw map[string]any
				_ = json.Unmarshal(req.EventJson, &raw)
				raw["approval_acceptance"].(map[string]any)["grant"] = permissionResponseInput().Grant
				req.EventJson, _ = json.Marshal(raw)
			}
			if _, err := f.call(req); err == nil {
				t.Fatal("unowned or content-bearing acceptance entered persistence")
			}
			after, value := readPublishedInteraction(t, f, before.ID)
			if after.Revision != before.Revision || !reflect.DeepEqual(value, original) {
				t.Fatal("invalid acceptance partially changed the response")
			}
		})
	}
}

func TestApprovalAcceptanceOutboxReplaysLostAcknowledgmentWithoutGrantContent(t *testing.T) {
	for _, closedFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "open", true: "closed"}[closedFirst], func(t *testing.T) { approvalAcceptanceOutbox(t, closedFirst) })
	}
}

func approvalAcceptanceOutbox(t *testing.T, closedFirst bool) {
	t.Helper()
	dropAt := 5
	if closedFirst {
		dropAt++
	}
	f := newPublicationFixture(t)
	f.registerGrant(t)
	cfg := publicationWorkerConfig(t, f)
	path := filepath.Join(cfg.Root, "jobs", string(f.job), "publication.json")
	transport := &losePublicationAck{WorkerServiceClient: f.client, t: t, path: path, dropAt: dropAt}
	cfg.Client = transport
	publisher, mapper := bindNativeMapper(t, f, cfg)
	id, responseID := domain.NewID(), domain.NewID()
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.InteractionRequestedEvent, ItemID: "approval-tool", Interaction: &codex.Interaction{ID: id, Kind: codex.ApprovalInteraction, NativeID: codex.NativeRequestID{Kind: codex.TextRequestID, Text: "native-approval"}, Approval: &codex.ApprovalRequest{Kind: codex.PermissionsApproval, Permissions: &codex.PermissionsApprovalRequest{Cwd: "/fixture", Permissions: codex.PermissionProfile{FileSystem: &codex.AdditionalFilePermissions{Write: []string{"/private-fixture-grant"}}}}}}})
	answer := permissionResponseInput()

	if _, err := acceptFixtureApproval(f, responseID, id, 1, answer); err != nil {
		t.Fatal(err)
	}
	claim := &pb.ClaimApprovalResponseRequest{Mutation: &pb.Mutation{Id: string(id), ExpectedRevision: 2, RequestId: string(domain.NewID())}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: string(responseID)}
	if _, err := claimApproval(f, claim); err != nil {
		t.Fatal(err)
	}
	if err := mapper.PublishApprovalDelivery(context.Background(), *approvalDeliveryEvent(f, claim, domain.ApprovalTransmitted).ApprovalResponse); err != nil {
		t.Fatal(err)
	}
	if closedFirst {
		publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.InteractionClosedEvent, ItemID: "approval-tool", InteractionState: &codex.InteractionStatus{ID: id, TurnID: f.turn, ItemID: "approval-tool", ResponseID: responseID, Delivery: codex.QuestionTransmitted, Closure: codex.InteractionNativeClosed}})
	}
	e := codex.Event{Kind: codex.ApprovalAcceptedEvent, ThreadID: f.thread, TurnID: f.turn, ItemID: "approval-tool", Correlated: true, InteractionState: &codex.InteractionStatus{ID: id, TurnID: f.turn, ItemID: "approval-tool", ResponseID: responseID, Delivery: codex.QuestionTransmitted, Closure: codex.InteractionOpen, Accepted: true, ApprovalEvidence: codex.PermissionOutputEvidence}}
	if handled, err := mapper.PublishCore(context.Background(), e); !handled || err == nil {
		t.Fatal("lost acceptance acknowledgment was not retained")
	}
	raw, err := security.ReadPrivate(path, 1<<20)
	if err != nil || strings.Contains(string(raw), "/private-fixture-grant") || strings.Contains(string(raw), "\"fileSystem\"") || !strings.Contains(string(raw), "approval-accepted") {
		t.Fatal("acceptance outbox lost evidence or retained content")
	}
	before, accepted := readPublishedInteraction(t, f, id)
	if accepted.ApprovalResponse.Acceptance == nil {
		t.Fatal("accepted publication was lost")
	}
	if err := publisher.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := worker.OpenExecutionPublisher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, value := readPublishedInteraction(t, f, id)
	if after.Revision != before.Revision || !reflect.DeepEqual(value, accepted) || len(transport.calls) != dropAt+1 || transport.calls[dropAt-1] != transport.calls[dropAt] {
		t.Fatal("replay changed native acceptance or its exact receipt")
	}
}

func permissionResponseInput() domain.ApprovalResponseInput {
	return domain.ApprovalResponseInput{Grant: &domain.CodexPermissionGrant{Scope: domain.CodexPermissionTurn, Permissions: domain.CodexPermissionProfile{FileSystem: &domain.CodexAdditionalFilePermissions{Write: []string{"/private-fixture-grant"}}}}}
}
func permissionClaimFixture(t *testing.T) (*publicationFixture, *pb.ClaimApprovalResponseRequest) {
	t.Helper()
	f := newPublicationFixture(t)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	id, response := domain.NewID(), domain.NewID()
	e := approvalFixtureEvent(f, 3, id, "native-approval")
	e.Interaction.Approval.Codex.Kind = domain.CodexPermissionsApproval
	e.Interaction.Approval.Codex.Command = nil
	e.Interaction.Approval.Codex.Permissions = &domain.CodexPermissionsApprovalRequest{Cwd: "/fixture", Permissions: permissionResponseInput().Grant.Permissions}
	f.publish(t, e)
	if _, err := acceptFixtureApproval(f, response, id, 1, permissionResponseInput()); err != nil {
		t.Fatal(err)
	}
	return f, &pb.ClaimApprovalResponseRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(id), ExpectedRevision: 2}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: string(response)}
}
