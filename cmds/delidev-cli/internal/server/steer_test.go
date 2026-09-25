package server

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

func newSteerFixture(t *testing.T) (*publicationFixture, *pb.SteerQueuedInputRequest) {
	t.Helper()
	f := newPublicationFixture(t)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.steer-queue-sequence", nil, func(tx *store.Tx) (any, error) {
		r, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		session.LastInputSequence = 1
		return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, session)
	})
	if err != nil {
		t.Fatal(err)
	}
	client := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
	raw, _ := json.Marshal(domain.SessionInput{Prompt: "Original selected Steer 한글 🐦", Mode: f.input.Input.Mode})
	r, err := client.EnqueueInput(context.Background(), ownerRequest(f.service.Identity, &pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: string(f.input.SessionID), DocumentJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	return f, &pb.SteerQueuedInputRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: r.Msg.Change.Input.Id, ExpectedRevision: r.Msg.Change.Input.Revision}, SessionId: string(f.input.SessionID), ExpectedExecutionId: string(f.input.ExecutionID), ExpectedTurnId: string(f.turn)}
}

func callSteer(f *publicationFixture, req *pb.SteerQueuedInputRequest) (*connect.Response[pb.SteerQueuedInputResponse], error) {
	return delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL).SteerQueuedInput(context.Background(), ownerRequest(f.service.Identity, req))
}

func claimSteer(t *testing.T, f *publicationFixture, r *pb.Resource) (*pb.ClaimSteerInputRequest, *pb.ClaimSteerInputResponse) {
	t.Helper()
	req := &pb.ClaimSteerInputRequest{Mutation: acctMutation(r, domain.NewID()), MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job)}
	request := connect.NewRequest(req)
	request.Header().Set("Authorization", "Bearer "+f.workerToken)
	response, err := f.client.ClaimSteerInput(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return req, response.Msg
}

func TestSteerAtomicAcceptanceConcurrentClaimsAndQueueExclusion(t *testing.T) {
	f, request := newSteerFixture(t)
	var wg sync.WaitGroup
	results := make(chan *pb.SteerQueuedInputResponse, 8)
	for range 8 {
		wg.Go(func() {
			r, err := callSteer(f, request)
			if err != nil {
				t.Error(err)
				return
			}
			results <- r.Msg
		})
	}
	wg.Wait()
	close(results)
	fresh := 0
	var accepted *pb.Resource
	for result := range results {
		if !result.Replayed {
			fresh++
		}
		accepted = result.Steer
		var input domain.QueuedInput
		if domain.Decode(result.Change.Input.DocumentJson, &input) != nil || input.Delivery != domain.InputClaimed || input.NativeRequestID != domain.ID(request.Mutation.RequestId) || input.ExecutionID != f.input.ExecutionID {
			t.Fatal("Steer did not atomically claim the selected input")
		}
	}
	if fresh != 1 {
		t.Fatal("Steer accepted more than once", fresh)
	}
	client := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
	if _, err := client.EditQueuedInput(context.Background(), ownerRequest(f.service.Identity, &pb.EditQueuedInputRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: request.Mutation.Id, ExpectedRevision: 2}, SessionId: request.SessionId, Prompt: "Changed"})); err == nil {
		t.Fatal("claimed Steer input was edited")
	}
	other := proto.Clone(request).(*pb.SteerQueuedInputRequest)
	other.Mutation.RequestId = string(domain.NewID())
	if _, err := callSteer(f, other); err == nil {
		t.Fatal("another request claimed the same input")
	}
	claim, response := claimSteer(t, f, accepted)
	var attempt domain.SteerAttempt
	if domain.Decode(response.Steer.DocumentJson, &attempt) != nil || attempt.State != domain.SteerClaimed || attempt.Claim == nil || attempt.Claim.ID != domain.ID(claim.Mutation.RequestId) {
		t.Fatal("Worker lost exact Steer claim")
	}
	changedClaim := proto.Clone(claim).(*pb.ClaimSteerInputRequest)
	changedClaim.Mutation.RequestId = string(domain.NewID())
	changedClaim.Mutation.ExpectedRevision = response.Steer.Revision
	r := connect.NewRequest(changedClaim)
	r.Header().Set("Authorization", "Bearer "+f.workerToken)
	if _, err := f.client.ClaimSteerInput(context.Background(), r); err == nil {
		t.Fatal("another claim adopted Steer delivery")
	}
}

func TestSteerDeliveryPersistsAcceptanceRejectionAndUncertainty(t *testing.T) {
	for _, delivery := range []domain.SteerDelivery{domain.SteerNativeAccepted, domain.SteerNotSent, domain.SteerNativeUncertain} {
		t.Run(string(delivery), func(t *testing.T) {
			f, request := newSteerFixture(t)
			accepted, err := callSteer(f, request)
			if err != nil {
				t.Fatal(err)
			}
			claim, _ := claimSteer(t, f, accepted.Msg.Steer)
			event := f.event(domain.ExecutionSteerObserved, 3)
			event.Steer = &domain.ExecutionSteerUpdate{SteerID: domain.ID(request.Mutation.RequestId), InputID: domain.ID(request.Mutation.Id), ClaimID: domain.ID(claim.Mutation.RequestId), Delivery: delivery}
			switch delivery {
			case domain.SteerNativeAccepted:
				event.Steer.Evidence = domain.SteerNativeHistory
			case domain.SteerNotSent:
				event.Steer.Evidence, event.Steer.ProblemCode = domain.SteerNativeRejection, domain.Conflict
			case domain.SteerNativeUncertain:
				event.Steer.ProblemCode = domain.RecoveryRequired
			}
			published := f.publish(t, event)
			if r, err := f.call(published); err != nil || !r.Msg.Replayed {
				t.Fatal("delivery receipt replay failed", err)
			}
			sr, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			session, _ := store.Decode[domain.Session](sr)
			ir, _ := f.service.Store.Get(context.Background(), domain.QueueKind, domain.ID(request.Mutation.Id))
			input, _ := store.Decode[domain.QueuedInput](ir)
			if delivery == domain.SteerNativeAccepted {
				if input.Delivery != domain.InputAccepted || session.PendingInputs != 0 || session.PendingInputBytes != 0 || session.PendingSteerID != "" || len(session.Execution.AcceptedInputs) != 2 {
					t.Fatal("accepted Steer retained FIFO eligibility or lost history")
				}
				message := f.event(domain.ExecutionMessageStarted, 4)
				message.Message = &domain.ExecutionMessageUpdate{ID: domain.NewID(), NativeID: "steered-user-message", Role: domain.UserMessage, InputID: ir.ID, Text: input.Prompt}
				f.publish(t, message)
			} else if delivery == domain.SteerNotSent {
				if input.Delivery != domain.InputQueued || input.NativeRequestID != "" || input.ExecutionID != "" || session.PendingSteerID != "" || session.PendingInputs != 1 || session.Recovery != domain.NoRecovery {
					t.Fatal("definite rejection consumed input or paused execution")
				}
			} else {
				if input.Delivery != domain.InputUncertain || session.PendingInputs != 1 || session.PendingSteerID == "" || session.Recovery != domain.NeedsRecovery || session.Dispatch != domain.DispatchPaused {
					t.Fatal("uncertainty authorized another send")
				}
			}
			// Accepted owner retries return current state and cannot requeue or
			// repeat either the claim or its already retained delivery accounting.
			if r, err := callSteer(f, request); err != nil || !r.Msg.Replayed {
				t.Fatal("owner receipt replay failed", err)
			}
			retry := connect.NewRequest(claim)
			retry.Header().Set("Authorization", "Bearer "+f.workerToken)
			if _, err := f.client.ClaimSteerInput(context.Background(), retry); err == nil {
				t.Fatal("old claim returned send authority after delivery")
			}
		})
	}
}

func TestSteerLateResolutionPreservesUncertaintyAndCapacity(t *testing.T) {
	for _, delivery := range []domain.SteerDelivery{domain.SteerNativeAccepted, domain.SteerNotSent} {
		t.Run(string(delivery), func(t *testing.T) {
			f, request := newSteerFixture(t)
			r, err := callSteer(f, request)
			if err != nil {
				t.Fatal(err)
			}
			claim, _ := claimSteer(t, f, r.Msg.Steer)
			original := domain.ExecutionSteerUpdate{SteerID: domain.ID(request.Mutation.RequestId), InputID: domain.ID(request.Mutation.Id), ClaimID: domain.ID(claim.Mutation.RequestId), Delivery: domain.SteerNativeUncertain, ProblemCode: domain.RecoveryRequired}
			event := f.event(domain.ExecutionSteerObserved, 3)
			event.Steer = &original
			f.publish(t, event)
			resolution := original
			resolution.Delivery, resolution.Evidence = delivery, domain.SteerNativeAcknowledgment
			if delivery == domain.SteerNotSent {
				resolution.Evidence, resolution.ProblemCode = domain.SteerNativeRejection, domain.Conflict
			}
			event.Sequence, event.Steer = 4, &resolution
			receipt := f.publish(t, event)
			if retried, err := f.call(receipt); err != nil || !retried.Msg.Replayed {
				t.Fatal("late resolution replay failed", err)
			}
			sr, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			session, _ := store.Decode[domain.Session](sr)
			ar, _ := f.service.Store.Get(context.Background(), domain.SteerKind, original.SteerID)
			attempt, _ := store.Decode[domain.SteerAttempt](ar)
			if attempt.Observation == nil || *attempt.Observation != original || attempt.Resolution == nil || *attempt.Resolution != resolution || attempt.Sequence != 3 || attempt.ResolutionSequence != 4 || session.PendingSteerID != "" || session.Recovery != domain.NeedsRecovery || session.Dispatch != domain.DispatchPaused {
				t.Fatal("late fact erased original uncertainty or recovery")
			}
			if delivery == domain.SteerNativeAccepted && (session.PendingInputs != 0 || session.PendingInputBytes != 0 || len(session.Execution.AcceptedInputs) != 2) {
				t.Fatal("late acceptance lost queue accounting")
			}
			if delivery == domain.SteerNotSent && session.PendingInputs != 1 {
				t.Fatal("late rejection consumed queued input")
			}
			event.Sequence = 5
			if _, err := f.call(f.requestEvent(t, event)); err == nil {
				t.Fatal("a second resolution overwrote retained proof")
			}
		})
	}
}

func TestSteerStopsQuestionsAndTerminalPreserveDeliveryOwnership(t *testing.T) {
	for _, action := range []string{"stop", "archive", "question", "terminal", "disconnect"} {
		for _, claimed := range []bool{false, true} {
			t.Run(action+"/"+map[bool]string{false: "queued", true: "claimed"}[claimed], func(t *testing.T) {
				f, request := newSteerFixture(t)
				accepted, err := callSteer(f, request)
				if err != nil {
					t.Fatal(err)
				}
				var claim *pb.ClaimSteerInputRequest
				if claimed {
					claim, _ = claimSteer(t, f, accepted.Msg.Steer)
				}
				switch action {
				case "terminal":
					event := f.event(domain.ExecutionTurnFinished, 3)
					event.Outcome = domain.ExecutionSucceeded
					f.publish(t, event)
				case "question":
					event := f.event(domain.ExecutionWaitingChanged, 3)
					event.Waiting = &domain.NativeWaiting{UserInput: true}
					f.publish(t, event)
				case "disconnect":
					accounts := delidevv1connect.NewAccountServiceClient(f.http.Client(), f.http.URL)
					_, err = accounts.DisconnectAccount(context.Background(), ownerRequest(f.service.Identity, &pb.DisconnectAccountRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: 1}}))
				default:
					sr, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
					control := pb.SessionAction_SESSION_ACTION_STOP
					if action == "archive" {
						control = pb.SessionAction_SESSION_ACTION_ARCHIVE
					}
					_, err = delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL).ControlSession(context.Background(), ownerRequest(f.service.Identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(sr.ID), ExpectedRevision: sr.Revision}, Action: control}))
				}
				if err != nil {
					t.Fatal(err)
				}
				ar, _ := f.service.Store.Get(context.Background(), domain.SteerKind, domain.ID(request.Mutation.RequestId))
				attempt, _ := store.Decode[domain.SteerAttempt](ar)
				ir, _ := f.service.Store.Get(context.Background(), domain.QueueKind, domain.ID(request.Mutation.Id))
				input, _ := store.Decode[domain.QueuedInput](ir)
				if !claimed && (attempt.State != domain.SteerCanceled || input.Delivery != domain.InputQueued || input.ExecutionID != "" || input.NativeRequestID != "") {
					t.Fatal("unsent control did not release exact queue entry")
				}
				if claimed && action == "terminal" && (attempt.State != domain.SteerUncertain || input.Delivery != domain.InputUncertain) {
					t.Fatal("terminal claimed native Steer was not retained uncertain")
				}
				if claimed && action != "terminal" && (attempt.State != domain.SteerClaimed || input.Delivery != domain.InputClaimed) {
					t.Fatal("control pretended to know claimed native delivery")
				}
				if claim == nil {
					claim = &pb.ClaimSteerInputRequest{Mutation: acctMutation(accepted.Msg.Steer, domain.NewID()), MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job)}
				}
				r := connect.NewRequest(claim)
				r.Header().Set("Authorization", "Bearer "+f.workerToken)
				if _, err := f.client.ClaimSteerInput(context.Background(), r); err == nil {
					t.Fatal("retired/currently blocked control returned a native send payload")
				}
				if claimed && action != "terminal" {
					// A committed claim can already have reached native code. Even
					// after Stop/account loss its exact acceptance remains a fact.
					sequence := uint64(3)
					if action == "question" {
						sequence = 4
					}
					event := f.event(domain.ExecutionSteerObserved, sequence)
					event.Steer = &domain.ExecutionSteerUpdate{SteerID: ar.ID, InputID: ir.ID, ClaimID: domain.ID(claim.Mutation.RequestId), Delivery: domain.SteerNativeAccepted, Evidence: domain.SteerNativeAcknowledgment}
					f.publish(t, event)
				}
			})
		}
	}
}

func TestSteerRequiresFreshScopeAndOriginalMode(t *testing.T) {
	for _, mismatch := range []string{"turn", "execution", "revision", "session", "mode", "waiting", "response", "recovery", "paused", "capacity", "worker-actor"} {
		t.Run(mismatch, func(t *testing.T) {
			f, request := newSteerFixture(t)
			switch mismatch {
			case "turn":
				request.ExpectedTurnId = string(domain.NewID())
			case "execution":
				request.ExpectedExecutionId = string(domain.NewID())
			case "revision":
				request.Mutation.ExpectedRevision++
			case "session":
				request.SessionId = string(domain.NewID())
			case "mode":
				client := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
				raw, _ := json.Marshal(domain.SessionInput{Prompt: "Separate Plan input", Mode: domain.PlanMode})
				res, err := client.EnqueueInput(context.Background(), ownerRequest(f.service.Identity, &pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: request.SessionId, DocumentJson: raw}))
				if err != nil {
					t.Fatal(err)
				}
				request.Mutation.Id, request.Mutation.ExpectedRevision = res.Msg.Change.Input.Id, res.Msg.Change.Input.Revision
			case "worker-actor":
			default:
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.steer-scope", nil, func(tx *store.Tx) (any, error) {
					r, session, err := sessionRecord(tx, f.input.SessionID)
					if err != nil {
						return nil, err
					}
					switch mismatch {
					case "waiting":
						session.Execution.Waiting.Approval = true
					case "response":
						session.Execution.UnconfirmedResponses = 1
					case "recovery":
						session.Recovery = domain.NeedsRecovery
					case "paused":
						session.Dispatch = domain.DispatchPaused
					case "capacity":
						session.Execution.SteerAttempts = domain.MaxExecutionSteers
					}
					return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, session)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			before, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			var err error
			if mismatch == "worker-actor" {
				req := connect.NewRequest(request)
				req.Header().Set("Authorization", "Bearer "+f.workerToken)
				_, err = delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL).SteerQueuedInput(context.Background(), req)
			} else {
				_, err = callSteer(f, request)
			}
			if err == nil {
				t.Fatal("invalid scope granted Steer")
			}
			after, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if before.Revision != after.Revision {
				t.Fatal("rejected Steer partially mutated the session")
			}
			if _, err := f.service.Store.Get(context.Background(), domain.SteerKind, domain.ID(request.Mutation.RequestId)); domain.SafeError(err).Code != domain.NotFound {
				t.Fatal("rejected Steer retained a claim")
			}
		})
	}
}

func TestSteerSelectsExplicitEntryWithoutChangingFIFOOrder(t *testing.T) {
	f, earlier := newSteerFixture(t)
	client := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
	raw, _ := json.Marshal(domain.SessionInput{Prompt: "Explicitly selected later entry", Mode: f.input.Input.Mode})
	queued, err := client.EnqueueInput(context.Background(), ownerRequest(f.service.Identity, &pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: earlier.SessionId, DocumentJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	selected := proto.Clone(earlier).(*pb.SteerQueuedInputRequest)
	selected.Mutation = acctMutation(queued.Msg.Change.Input, domain.NewID())
	accepted, err := callSteer(f, selected)
	if err != nil {
		t.Fatal(err)
	}
	claim, _ := claimSteer(t, f, accepted.Msg.Steer)
	event := f.event(domain.ExecutionSteerObserved, 3)
	event.Steer = &domain.ExecutionSteerUpdate{SteerID: domain.ID(selected.Mutation.RequestId), InputID: domain.ID(selected.Mutation.Id), ClaimID: domain.ID(claim.Mutation.RequestId), Delivery: domain.SteerNativeAccepted, Evidence: domain.SteerNativeAcknowledgment}
	f.publish(t, event)
	r, _ := f.service.Store.Get(context.Background(), domain.QueueKind, domain.ID(earlier.Mutation.Id))
	input, _ := store.Decode[domain.QueuedInput](r)
	if r.Revision != earlier.Mutation.ExpectedRevision || input.Delivery != domain.InputQueued || input.Sequence != 2 || input.NativeRequestID != "" {
		t.Fatal("explicit Steer claimed or reordered the earlier FIFO input")
	}
}

func TestSteerPublicationRejectsForeignEvidenceAtomically(t *testing.T) {
	for _, field := range []string{"claim", "input", "thread", "turn", "attempt", "mixed-payload"} {
		t.Run(field, func(t *testing.T) {
			f, req := newSteerFixture(t)
			r, err := callSteer(f, req)
			if err != nil {
				t.Fatal(err)
			}
			claim, _ := claimSteer(t, f, r.Msg.Steer)
			event := f.event(domain.ExecutionSteerObserved, 3)
			event.Steer = &domain.ExecutionSteerUpdate{SteerID: domain.ID(req.Mutation.RequestId), InputID: domain.ID(req.Mutation.Id), ClaimID: domain.ID(claim.Mutation.RequestId), Delivery: domain.SteerNativeAccepted, Evidence: domain.SteerNativeAcknowledgment}
			switch field {
			case "claim":
				event.Steer.ClaimID = domain.NewID()
			case "input":
				event.Steer.InputID = domain.NewID()
			case "thread":
				event.NativeThreadID = string(domain.NewID())
			case "turn":
				event.NativeTurnID = string(domain.NewID())
			case "attempt":
				event.Steer.SteerID = domain.NewID()
			case "mixed-payload":
				event.Waiting = &domain.NativeWaiting{}
			}
			if _, err := f.call(f.requestEvent(t, event)); err == nil {
				t.Fatal("foreign/mixed Steer observation was accepted")
			}
			sr, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			session, _ := store.Decode[domain.Session](sr)
			if session.Execution.LastSequence != 2 || session.PendingInputs != 1 || len(session.Execution.AcceptedInputs) != 1 || session.PendingSteerID != domain.ID(req.Mutation.RequestId) {
				t.Fatal("rejected evidence consumed sequence or input capacity")
			}
		})
	}
}

func TestSteerRecoveryKeepsEarlierDiagnostic(t *testing.T) {
	for _, delivery := range []domain.SteerDelivery{domain.SteerNativeUncertain, domain.SteerNativeAccepted} {
		t.Run(string(delivery), func(t *testing.T) {
			f, req := newSteerFixture(t)
			r, err := callSteer(f, req)
			if err != nil {
				t.Fatal(err)
			}
			claim, _ := claimSteer(t, f, r.Msg.Steer)
			earliest := domain.Fail(domain.Unavailable, "Earlier independent native failure.", "Keep its original evidence.")
			_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.prior-steer-problem", nil, func(tx *store.Tx) (any, error) {
				r, s, err := sessionRecord(tx, f.input.SessionID)
				if err != nil {
					return nil, err
				}
				s.Recovery, s.Dispatch, s.Problem = domain.NeedsRecovery, domain.DispatchPaused, earliest
				return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, s)
			})
			if err != nil {
				t.Fatal(err)
			}
			event := f.event(domain.ExecutionSteerObserved, 3)
			event.Steer = &domain.ExecutionSteerUpdate{SteerID: domain.ID(req.Mutation.RequestId), InputID: domain.ID(req.Mutation.Id), ClaimID: domain.ID(claim.Mutation.RequestId), Delivery: delivery, ProblemCode: domain.RecoveryRequired}
			if delivery == domain.SteerNativeAccepted {
				event.Steer.Evidence = domain.SteerNativeAcknowledgment
			}
			f.publish(t, event)
			sr, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			session, _ := store.Decode[domain.Session](sr)
			if session.Problem == nil || session.Problem.Code != earliest.Code || session.Problem.Message != earliest.Message || session.Recovery != domain.NeedsRecovery || session.Dispatch != domain.DispatchPaused {
				t.Fatal("Steer overwrote an unrelated failure/recovery")
			}
		})
	}
}
