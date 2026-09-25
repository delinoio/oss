package worker

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type steerControllerFixture struct {
	*questionControllerFixture
	steer       *pb.SteerInputControl
	inputID     domain.ID
	inspections int
	observed    domain.ExecutionSteerUpdate
}

func newSteerControllerFixture(t *testing.T, mode string) *steerControllerFixture {
	base := newQuestionControllerFixture(t, mode)
	f := &steerControllerFixture{questionControllerFixture: base, inputID: domain.NewID()}
	f.steer = &pb.SteerInputControl{JobId: base.control.JobId, SteerId: string(domain.NewID()), Revision: 1}
	f.path = filepath.Join(base.mapper.publisher.config.Root, "jobs", f.steer.JobId, "steers", f.steer.SteerId+".json")
	f.mapper.publisher.config.Client = f
	f.mapper.publisher.input.InputID = domain.NewID()
	f.mapper.publisher.input.Input = domain.SessionInput{Prompt: "Primary fixture prompt", Mode: domain.ExecuteMode}
	f.mapper.acceptedInputs = []domain.ExecutionInputBinding{domain.BindExecutionInput(f.mapper.publisher.input.InputID, f.mapper.publisher.input.Input.Prompt)}
	return f
}

func (f *steerControllerFixture) journal(want responseJournalState) steerJournal {
	f.t.Helper()
	raw, err := security.ReadPrivate(f.path, 64<<10)
	var journal steerJournal
	if err != nil || domain.Decode(raw, &journal) != nil || journal.State != want || journal.Control.SteerID != domain.ID(f.steer.SteerId) || journal.ClaimID.Validate() != nil || strings.Contains(string(raw), "Exact private Steer prompt") {
		f.t.Fatal("side effect preceded metadata-only Steer journal", err)
	}
	return journal
}

func (f *steerControllerFixture) ClaimSteerInput(_ context.Context, req *connect.Request[pb.ClaimSteerInputRequest]) (*connect.Response[pb.ClaimSteerInputResponse], error) {
	f.claims++
	j := f.journal(responsePrepared)
	if req.Msg.Mutation.RequestId != string(j.ClaimID) {
		f.t.Fatal("claim changed durable identity")
	}
	if f.mode == "claim-ack-lost" {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("lost claim"))
	}
	if f.mode == "stale-control" {
		return nil, rpc.Error(domain.Fail(domain.Conflict, "Retired fixture control.", ""), "")
	}
	c := f.mapper.publisher.config
	input := domain.QueuedInput{Sequence: 2, ContentRevision: 1, Prompt: "Exact private Steer prompt 한글 🐦", Mode: domain.ExecuteMode, Delivery: domain.InputClaimed, ExecutionID: f.mapper.publisher.execution, NativeRequestID: domain.ID(f.steer.SteerId)}
	attempt := domain.SteerAttempt{Version: 1, JobID: domain.ID(f.steer.JobId), ExecutionID: input.ExecutionID, InputID: f.inputID, ContentRevision: 1, NativeThreadID: f.mapper.thread, NativeTurnID: f.mapper.turn, Mode: input.Mode, PromptDigest: domain.BindExecutionInput(f.inputID, input.Prompt).PromptDigest, State: domain.SteerClaimed, Claim: &domain.SteerClaim{ID: j.ClaimID, MachineID: c.Credential.MachineID, InstanceID: c.Instance, DeviceID: c.Credential.DeviceID}}
	if f.mode == "foreign-claim" {
		attempt.Claim.InstanceID = domain.NewID()
	}
	if f.mode == "changed-prompt" {
		input.Prompt = "Wrong input"
	}
	a, _ := json.Marshal(attempt)
	b, _ := json.Marshal(input)
	return connect.NewResponse(&pb.ClaimSteerInputResponse{Steer: &pb.Resource{Kind: pb.EntityKind_ENTITY_KIND_STEER, SchemaVersion: 1, Id: f.steer.SteerId, SessionId: string(f.mapper.publisher.input.SessionID), Revision: 2, DocumentJson: a}, Input: &pb.Resource{Kind: pb.EntityKind_ENTITY_KIND_QUEUE, SchemaVersion: 1, Id: string(f.inputID), SessionId: string(f.mapper.publisher.input.SessionID), Revision: 2, DocumentJson: b}}), nil
}

func (f *steerControllerFixture) Steer(_ context.Context, request, input, turn domain.ID, value domain.SessionInput) (codex.TurnResult, error) {
	f.sends++
	f.journal(responseSendIntent)
	if request != domain.ID(f.steer.SteerId) || input != f.inputID || turn != f.mapper.turn || value.Prompt != "Exact private Steer prompt 한글 🐦" || value.Mode != domain.ExecuteMode {
		f.t.Fatal("native Steer replaced original claim/input/mode")
	}
	result := codex.TurnResult{RequestID: request, InputID: input, TurnID: turn}
	switch f.mode {
	case "rejected":
		return result, domain.Fail(domain.Conflict, "Fixture stale turn.", "")
	case "history-accepted", "uncertain", "accepted-recovery":
		return result, publicationUncertain()
	}
	return result, nil
}

func (f *steerControllerFixture) InspectSteerAcceptance(_ context.Context, request domain.ID) (codex.SteerObservation, error) {
	f.inspections++
	if request != domain.ID(f.steer.SteerId) {
		f.t.Fatal("inspection replaced original attempt")
	}
	observation := codex.SteerObservation{RequestID: request, InputID: f.inputID, ThreadID: f.mapper.thread, TurnID: f.mapper.turn, Delivery: codex.SteerUncertain}
	switch f.mode {
	case "history-accepted":
		observation.Delivery, observation.Evidence = codex.SteerAccepted, codex.SteerHistory
		return observation, nil
	case "accepted-recovery":
		observation.Delivery, observation.Evidence = codex.SteerAccepted, codex.SteerAcknowledgment
		return observation, publicationUncertain()
	case "rejected":
		observation.Delivery, observation.Evidence = codex.SteerNotSent, codex.SteerRejection
		return observation, nil
	default:
		return observation, publicationUncertain()
	}
}

func (f *steerControllerFixture) PublishExecution(_ context.Context, req *connect.Request[pb.PublishExecutionRequest]) (*connect.Response[pb.PublishExecutionResponse], error) {
	f.publications++
	j := f.journal(responseObserved)
	retained := j.Observation
	if j.Resolution != nil {
		retained = j.Resolution
	}
	var event domain.ExecutionEvent
	if domain.Decode(req.Msg.EventJson, &event) != nil || event.Validate() != nil || event.Kind != domain.ExecutionSteerObserved || event.Steer == nil || *event.Steer != *retained {
		f.t.Fatal("publication preceded original Steer observation journal")
	}
	f.observed = *event.Steer
	if f.mode == "publication-ack-lost" {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("lost delivery acknowledgment"))
	}
	return connect.NewResponse(&pb.PublishExecutionResponse{AcknowledgedSequence: event.Sequence}), nil
}

func TestSteerLateResponseRetainsBothObservationsWithoutResend(t *testing.T) {
	for _, delivery := range []codex.SteerDelivery{codex.SteerAccepted, codex.SteerNotSent} {
		t.Run(string(delivery), func(t *testing.T) {
			f := newSteerControllerFixture(t, "uncertain")
			if err := f.mapper.deliverSteer(context.Background(), context.Background(), f.steer, f); err == nil {
				t.Fatal("uncertain send did not retain recovery")
			}
			original := f.observed
			observed := &codex.SteerObservation{RequestID: original.SteerID, InputID: f.inputID, ThreadID: f.mapper.thread, TurnID: f.mapper.turn, Delivery: delivery, Evidence: codex.SteerAcknowledgment}
			event := codex.Event{Kind: codex.LateTurnResponseEvent, Action: codex.SteerTurnAction, Correlated: true, Late: true, ThreadID: f.mapper.thread, TurnID: f.mapper.turn, InputID: f.inputID, RequestID: original.SteerID, Steer: observed}
			if delivery == codex.SteerNotSent {
				observed.Evidence = codex.SteerRejection
				event.Problem = domain.Fail(domain.Conflict, "Fixture stale turn.", "")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if handled, err := f.mapper.PublishCore(ctx, event); err != nil || !handled {
				t.Fatal("late native fact was discarded after cancellation", err)
			}
			j := f.journal(responseObserved)
			if *j.Observation != original || j.Resolution == nil || j.Resolution.Delivery != domain.SteerDelivery(delivery) || f.publications != 2 || f.sends != 1 || f.inspections != 1 {
				t.Fatal("late resolution replaced original evidence or replayed native input")
			}
			if delivery == codex.SteerAccepted && (f.observed.ProblemCode != domain.RecoveryRequired || len(f.mapper.acceptedInputs) != 2) {
				t.Fatal("late acceptance lost binding or cleared recovery")
			}
			if handled, err := f.mapper.PublishCore(ctx, event); err != nil || !handled || f.publications != 2 {
				t.Fatal("repeated native evidence repeated publication", err)
			}
		})
	}
}

func TestSteerControllerOwnsOneAttemptAndInspectsUncertainty(t *testing.T) {
	for _, mode := range []string{"accepted", "history-accepted", "rejected", "uncertain", "accepted-recovery", "claim-ack-lost", "foreign-claim", "changed-prompt", "stale-control", "publication-ack-lost"} {
		t.Run(mode, func(t *testing.T) {
			f := newSteerControllerFixture(t, mode)
			err := f.mapper.deliverSteer(context.Background(), context.Background(), f.steer, f)
			wantSuccess := mode == "accepted" || mode == "history-accepted" || mode == "rejected" || mode == "stale-control"
			if (err == nil) != wantSuccess {
				t.Fatal("unexpected Steer controller result", err)
			}
			noSend := mode == "claim-ack-lost" || mode == "foreign-claim" || mode == "changed-prompt" || mode == "stale-control"
			if f.claims != 1 || (f.sends == 0) != noSend {
				t.Fatal("ambiguous/foreign claim reached native input")
			}
			if mode == "history-accepted" && (f.inspections != 1 || f.observed.Delivery != domain.SteerNativeAccepted || f.observed.Evidence != domain.SteerNativeHistory || len(f.mapper.acceptedInputs) != 2) {
				t.Fatal("uncertain Steer was not inspected/persisted")
			}
			if mode == "uncertain" && (f.inspections != 1 || f.observed.Delivery != domain.SteerNativeUncertain || len(f.mapper.acceptedInputs) != 1) {
				t.Fatal("missing history fabricated native acceptance")
			}
			if mode == "accepted-recovery" && (f.observed.Delivery != domain.SteerNativeAccepted || f.observed.ProblemCode != domain.RecoveryRequired || len(f.mapper.acceptedInputs) != 2) {
				t.Fatal("acceptance erased independent inspection recovery")
			}
			before := f.sends
			if err := f.mapper.deliverSteer(context.Background(), context.Background(), f.steer, f); err == nil || f.sends != before {
				t.Fatal("duplicate control authorized native resend")
			}
		})
	}
}
