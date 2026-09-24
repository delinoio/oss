package server

import (
	"context"
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

func singleUseApprovalFixture(t *testing.T, patch bool, change string, delivery domain.ApprovalDelivery) (*publicationFixture, domain.ID, domain.ExecutionEvent) {
	t.Helper()
	f := newPublicationFixture(t)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	toolID, interactionID, responseID := domain.NewID(), domain.NewID(), domain.NewID()
	start := f.toolEvent(domain.ExecutionToolStarted, 3, toolID, "approval-tool")
	start.Tool.Snapshot = toolCommand()
	if patch {
		start.Tool.Snapshot = &domain.ToolSnapshot{Kind: domain.PatchTool, Status: domain.ToolRunning, Changes: []domain.FileChangeObservation{{Path: "/private/workspace/a", Diff: "+fixture", Kind: domain.AddedFile}}}
	}
	f.publish(t, start)
	request := approvalFixtureEvent(f, 4, interactionID, "original-approval")
	approval := request.Interaction.Approval.Codex
	answer := approvalInput()
	if patch {
		approval.Kind, approval.Command, approval.File = domain.CodexFileApproval, nil, &domain.CodexFileApprovalRequest{}
	} else {
		command, cwd := start.Tool.Snapshot.Command.Command, start.Tool.Snapshot.Command.Cwd
		approval.Command.Command, approval.Command.Cwd = &command, &cwd
	}
	switch change {
	case "session":
		answer.Decision.Kind = domain.CodexApprovalAcceptSession
		if !patch {
			approval.Command.AvailableDecisions = append(approval.Command.AvailableDecisions, *answer.Decision)
		}
	case "amendment":
		answer.Decision = &domain.CodexApprovalDecision{Kind: domain.CodexApprovalExecpolicy, Execpolicy: []string{"printf"}}
		approval.Command.ProposedExecpolicy = []string{"printf"}
		approval.Command.AvailableDecisions = []domain.CodexApprovalDecision{*answer.Decision}
	case "command":
		value := "other"
		approval.Command.Command = &value
	case "cwd":
		value := "/other"
		approval.Command.Cwd = &value
	case "missing-command":
		approval.Command.Command = nil
	case "callback":
		value := "callback"
		approval.Command.ApprovalID = &value
	case "network":
		approval.Command.Network = &domain.CodexNetworkApprovalContext{Host: "fixture.invalid", Protocol: domain.CodexApprovalHTTPS}
	}
	f.publish(t, request)
	if _, err := acceptFixtureApproval(f, responseID, interactionID, 1, answer); err != nil {
		t.Fatal(err)
	}
	claim := &pb.ClaimApprovalResponseRequest{Mutation: &pb.Mutation{Id: string(interactionID), ExpectedRevision: 2, RequestId: string(domain.NewID())}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: string(responseID)}
	if _, err := claimApproval(f, claim); err != nil {
		t.Fatal(err)
	}
	observation := approvalDeliveryEvent(f, claim, delivery)
	observation.Sequence = 5
	f.publish(t, observation)
	sequence := uint64(6)
	if change != "open" {
		closed := approvalFixtureEvent(f, sequence, interactionID, "original-approval")
		closed.Kind, closed.Interaction.Approval, closed.Interaction.Closure = domain.ExecutionInteractionClosed, nil, domain.InteractionNativeClosed
		f.publish(t, closed)
		sequence++
	}
	if change == "ambiguous" {
		other := approvalFixtureEvent(f, sequence, domain.NewID(), "second-approval")
		f.publish(t, other)
		sequence++
	}
	completed := f.toolEvent(domain.ExecutionToolCompleted, sequence, toolID, "approval-tool")
	completed.Tool.Snapshot = start.Tool.Snapshot
	completed.Tool.Snapshot.Status = domain.ToolCompleted
	if !patch {
		zero := int32(0)
		completed.Tool.Snapshot.Command.Source = domain.ExecStartupCommand
		completed.Tool.Snapshot.Command.ExitCode = &zero
	}
	switch change {
	case "source":
		completed.Tool.Snapshot.Command.Source = domain.AgentCommand
	case "no-exit":
		completed.Tool.Snapshot.Command.ExitCode = nil
	case "nonzero":
		exit := int32(1)
		completed.Tool.Snapshot.Command.ExitCode = &exit
	case "failed":
		completed.Tool.Snapshot.Status = domain.ToolFailed
	case "empty-patch":
		completed.Tool.Snapshot.Changes = []domain.FileChangeObservation{}
	}
	return f, interactionID, completed
}

func TestSingleUseApprovalExecutionCommitsAcceptanceWithToolAndReplaysOnce(t *testing.T) {
	for _, patch := range []bool{false, true} {
		for _, delivery := range []domain.ApprovalDelivery{domain.ApprovalTransmitted, domain.ApprovalDeliveryUncertain} {
			t.Run(map[bool]string{false: "command", true: "patch"}[patch]+"/"+string(delivery), func(t *testing.T) {
				f, id, event := singleUseApprovalFixture(t, patch, "", delivery)
				receipt := f.publish(t, event)
				before, value := readPublishedInteraction(t, f, id)
				expected := domain.NativeApprovedCommand
				if patch {
					expected = domain.NativeApprovedPatch
				}
				response := value.ApprovalResponse
				if response.State != domain.ApprovalResponseAccepted || response.Acceptance == nil || response.Acceptance.Evidence != expected || response.Acceptance.Sequence != event.Sequence || response.Delivery.State != delivery || value.Closure != domain.InteractionNativeClosed {
					t.Fatal("single-use execution lost independent approval facts")
				}
				if replay, err := f.call(receipt); err != nil || !replay.Msg.Replayed {
					t.Fatal("tool/acceptance receipt lost", err)
				}
				after, replayed := readPublishedInteraction(t, f, id)
				if before.Revision != after.Revision || !reflect.DeepEqual(value, replayed) {
					t.Fatal("receipt reapplied approval acceptance")
				}
				terminal := f.event(domain.ExecutionTurnFinished, event.Sequence+1)
				terminal.Outcome = domain.ExecutionSucceeded
				f.publish(t, terminal)
				row, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				session, err := store.Decode[domain.Session](row)
				if err != nil || session.Execution.UnconfirmedResponses != 0 || session.Execution.CleanupVerified || session.Execution.Outcome != domain.ExecutionSucceeded {
					t.Fatal("approval acceptance changed outcome/cleanup", err)
				}
				if (session.Recovery == domain.NeedsRecovery) != (delivery == domain.ApprovalDeliveryUncertain) {
					t.Fatal("approval acceptance cleared earlier uncertainty")
				}
			})
		}
	}
}

func TestSingleUseApprovalExecutionCannotConfirmOtherDecisionsOrAmbiguousTools(t *testing.T) {
	for _, change := range []string{"session", "amendment", "command", "cwd", "missing-command", "callback", "network", "open", "ambiguous", "source", "no-exit", "nonzero", "failed", "empty-patch", "not-sent"} {
		t.Run(change, func(t *testing.T) {
			delivery := domain.ApprovalTransmitted
			if change == "not-sent" {
				delivery = domain.ApprovalNotSent
			}
			f, id, event := singleUseApprovalFixture(t, change == "empty-patch", change, delivery)
			if change == "callback" {
				if _, err := f.call(f.requestEvent(t, event)); err == nil {
					t.Fatal("callback source substitution accepted")
				}
			} else {
				f.publish(t, event)
			}
			_, value := readPublishedInteraction(t, f, id)
			if value.ApprovalResponse.Acceptance != nil || value.ApprovalResponse.State == domain.ApprovalResponseAccepted {
				t.Fatal("unproven approval gained acceptance")
			}
		})
	}
}

func TestSingleUseApprovalExecutionRollsBackToolWhenAcceptanceCannotCommit(t *testing.T) {
	f, id, event := singleUseApprovalFixture(t, false, "", domain.ApprovalTransmitted)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.counter", id, func(tx *store.Tx) (any, error) {
		row, err := tx.Get(domain.SessionKind, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		session, err := store.Decode[domain.Session](row)
		if err != nil {
			return nil, err
		}
		session.Execution.UnconfirmedResponses = 0
		_, err = tx.Put(row.Kind, row.ID, row.Revision, row.SessionID, row.ProjectID, session)
		return struct{}{}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.service.Store.Get(context.Background(), domain.MessageKind, event.Tool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.call(f.requestEvent(t, event)); err == nil {
		t.Fatal("inconsistent acceptance counter committed")
	}
	after, err := f.service.Store.Get(context.Background(), domain.MessageKind, event.Tool.ID)
	if err != nil || before.Revision != after.Revision || !reflect.DeepEqual(before.Data, after.Data) {
		t.Fatal("failed approval acceptance partially committed tool completion", err)
	}
	_, value := readPublishedInteraction(t, f, id)
	if value.ApprovalResponse.Acceptance != nil {
		t.Fatal("failed approval acceptance changed response")
	}
}

func TestSingleUseApprovalExecutionOutboxReplaysAtomicAcceptanceAfterLostAck(t *testing.T) {
	f := newPublicationFixture(t)
	f.registerGrant(t)
	cfg := publicationWorkerConfig(t, f)
	path := filepath.Join(cfg.Root, "jobs", string(f.job), "publication.json")
	transport := &losePublicationAck{WorkerServiceClient: f.client, t: t, path: path, dropAt: 7}
	cfg.Client = transport
	publisher, mapper := bindNativeMapper(t, f, cfg)
	command, cwd := "printf fixture", "/private/workspace"
	tool := codex.Tool{ID: "approval-tool", Kind: codex.CommandTool, Status: codex.ToolRunning, Command: &codex.CommandExecution{Command: command, Cwd: cwd, Source: codex.AgentCommand, Actions: []codex.CommandAction{}}}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ToolStartedEvent, ItemID: tool.ID, Tool: &tool})
	id, responseID := domain.NewID(), domain.NewID()
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.InteractionRequestedEvent, ItemID: tool.ID, Interaction: &codex.Interaction{ID: id, Kind: codex.ApprovalInteraction, NativeID: codex.NativeRequestID{Kind: codex.TextRequestID, Text: "original-approval"}, Approval: &codex.ApprovalRequest{Kind: codex.CommandApproval, Command: &codex.CommandApprovalRequest{Kind: codex.ExecuteCommandApproval, Command: &command, Cwd: &cwd, AvailableDecisions: []codex.ApprovalDecision{{Kind: codex.ApprovalAccept}}}}}})
	if _, err := acceptFixtureApproval(f, responseID, id, 1, approvalInput()); err != nil {
		t.Fatal(err)
	}
	claim := &pb.ClaimApprovalResponseRequest{Mutation: &pb.Mutation{Id: string(id), ExpectedRevision: 2, RequestId: string(domain.NewID())}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: string(responseID)}
	if _, err := claimApproval(f, claim); err != nil {
		t.Fatal(err)
	}
	if err := mapper.PublishApprovalDelivery(context.Background(), *approvalDeliveryEvent(f, claim, domain.ApprovalTransmitted).ApprovalResponse); err != nil {
		t.Fatal(err)
	}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.InteractionClosedEvent, ItemID: tool.ID, InteractionState: &codex.InteractionStatus{ID: id, TurnID: f.turn, ItemID: tool.ID, ResponseID: responseID, Delivery: codex.QuestionTransmitted, Closure: codex.InteractionNativeClosed}})
	zero := int32(0)
	tool.Status, tool.Command.Source, tool.Command.ExitCode = codex.ToolCompleted, codex.ExecStartupCommand, &zero
	event := codex.Event{Kind: codex.ToolCompletedEvent, ThreadID: f.thread, TurnID: f.turn, ItemID: tool.ID, Tool: &tool, Correlated: true}
	if handled, err := mapper.PublishCore(context.Background(), event); !handled || err == nil {
		t.Fatal("lost tool/acceptance acknowledgment not retained")
	}
	before, value := readPublishedInteraction(t, f, id)
	if value.ApprovalResponse.Acceptance == nil || value.ApprovalResponse.Acceptance.Sequence != 7 {
		t.Fatal("tool committed without original approval acceptance")
	}
	raw, err := security.ReadPrivate(path, 1<<20)
	if err != nil || strings.Contains(string(raw), "\"decision\"") || !strings.Contains(string(raw), "tool-completed") {
		t.Fatal("pending outbox did not retain exactly the original tool fact", err)
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
	after, replayed := readPublishedInteraction(t, f, id)
	if before.Revision != after.Revision || !reflect.DeepEqual(value, replayed) || len(transport.calls) != 8 || transport.calls[6] != transport.calls[7] {
		t.Fatal("lost-ack replay repeated tool/approval acceptance")
	}
}
