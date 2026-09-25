package server

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/proto"
)

func questionClaimFixture(t *testing.T) (*publicationFixture, *pb.ClaimQuestionResponseRequest) {
	t.Helper()
	f, id := questionResponseFixture(t)
	responseID := domain.NewID()
	if _, err := acceptFixtureResponse(f, responseID, id, 1, responseInput()); err != nil {
		t.Fatal(err)
	}
	return f, &pb.ClaimQuestionResponseRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(id), ExpectedRevision: 2}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: string(responseID)}
}

func claimQuestion(f *publicationFixture, message *pb.ClaimQuestionResponseRequest) (*connect.Response[pb.ClaimQuestionResponseResponse], error) {
	req := connect.NewRequest(message)
	req.Header().Set("Authorization", "Bearer "+f.workerToken)
	return f.client.ClaimQuestionResponse(context.Background(), req)
}

func TestQuestionClaimConcurrentRetryRetainsOneOwner(t *testing.T) {
	f, request := questionClaimFixture(t)
	var group sync.WaitGroup
	results := make(chan bool, 8)
	errors := make(chan error, 8)
	for range 8 {
		group.Go(func() {
			response, err := claimQuestion(f, request)
			errors <- err
			if err == nil {
				results <- response.Msg.Replayed
			}
		})
	}
	group.Wait()
	close(errors)
	close(results)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	fresh := 0
	for replayed := range results {
		if !replayed {
			fresh++
		}
	}
	r, value := readPublishedInteraction(t, f, domain.ID(request.Mutation.Id))
	claim := value.Response.Claim
	if fresh != 1 || r.Revision != 3 || value.Response.State != domain.QuestionResponseClaimed || claim == nil || claim.ID != domain.ID(request.Mutation.RequestId) || claim.JobID != f.job || claim.MachineID != f.input.MachineID || claim.InstanceID != f.instance || claim.DeviceID != f.device || claim.ClaimedAt.IsZero() || !reflect.DeepEqual(value.Response.Input, responseInput()) {
		t.Fatal("concurrent replay replaced a response claim or changed its content")
	}
	other := proto.Clone(request).(*pb.ClaimQuestionResponseRequest)
	other.Mutation.RequestId, other.Mutation.ExpectedRevision = string(domain.NewID()), r.Revision
	if _, err := claimQuestion(f, other); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("new request adopted an already claimed answer: %v", err)
	}
	identity := questionClaimIdentity{r.ID, value.Response.ID, f.job, f.input.MachineID, f.instance, f.device, 2}
	receipt, err := f.service.Store.Mutate(context.Background(), claim.ID, "question.claim", identity, func(*store.Tx) (any, error) {
		t.Fatal("retained claim was re-applied")
		return nil, nil
	})
	if err != nil || !receipt.Replayed || string(receipt.Data) != `{"interaction_id":"`+string(r.ID)+`"}` {
		t.Fatalf("claim receipt duplicated answer content: %v", err)
	}
}

func TestQuestionClaimRejectsForeignAndStaleControl(t *testing.T) {
	for _, changed := range []string{"interaction", "response", "job", "machine", "instance", "revision", "owner"} {
		t.Run(changed, func(t *testing.T) {
			f, original := questionClaimFixture(t)
			request := proto.Clone(original).(*pb.ClaimQuestionResponseRequest)
			switch changed {
			case "interaction":
				request.Mutation.Id = string(domain.NewID())
			case "response":
				request.ResponseId = string(domain.NewID())
			case "job":
				request.JobId = string(domain.NewID())
			case "machine":
				request.MachineId = string(domain.NewID())
			case "instance":
				request.InstanceId = string(domain.NewID())
			case "revision":
				request.Mutation.ExpectedRevision++
			}
			req := connect.NewRequest(request)
			token := f.workerToken
			if changed == "owner" {
				token = f.service.Identity.Token
			}
			req.Header().Set("Authorization", "Bearer "+token)
			if _, err := f.client.ClaimQuestionResponse(context.Background(), req); err == nil {
				t.Fatal("foreign/stale response control was accepted")
			}
			r, value := readPublishedInteraction(t, f, domain.ID(original.Mutation.Id))
			if r.Revision != 2 || value.Response.State != domain.QuestionResponseQueued || value.Response.Claim != nil {
				t.Fatal("rejected claim consumed the response")
			}
		})
	}
}

func TestQuestionClaimClosureAndLostWorkerRetainUncertainty(t *testing.T) {
	for _, end := range []string{"native-closure", "terminal", "worker-loss", "queued-worker-loss"} {
		t.Run(end, func(t *testing.T) {
			f, request := questionClaimFixture(t)
			id := domain.ID(request.Mutation.Id)
			if end != "queued-worker-loss" {
				if _, err := claimQuestion(f, request); err != nil {
					t.Fatal(err)
				}
			}
			switch end {
			case "native-closure":
				e := f.interactionEvent(4, id, "native-question")
				e.Kind, e.Interaction.Questions, e.Interaction.Closure = domain.ExecutionInteractionClosed, nil, domain.InteractionNativeClosed
				f.publish(t, e)
			case "terminal":
				e := f.event(domain.ExecutionTurnFinished, 4)
				e.Outcome = domain.ExecutionSucceeded
				f.publish(t, e)
			default:
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.lost-worker", nil, func(tx *store.Tx) (any, error) {
					r, err := tx.Get(domain.JobKind, f.job)
					if err != nil {
						return nil, err
					}
					job, err := store.Decode[domain.Job](r)
					if err != nil {
						return nil, err
					}
					return nil, finishLostNativeExecution(tx, r, job)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := claimQuestion(f, request); err == nil {
				t.Fatal("claim retry recovered answer delivery authority after closure/loss")
			}
			_, value := readPublishedInteraction(t, f, id)
			want := domain.QuestionResponseUncertain
			if end == "queued-worker-loss" {
				want = domain.QuestionResponseCanceled
			}
			if value.Response.State != want || !reflect.DeepEqual(value.Response.Input, responseInput()) || value.Questions.Questions[0].Text != "Original question" {
				t.Fatal("lost response claimed acceptance or discarded retained evidence")
			}
			if end == "worker-loss" && value.Closure != domain.InteractionOpen {
				t.Fatal("Worker loss invented native request closure")
			}
			r, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.Decode[domain.Session](r)
			if err != nil || session.Recovery != domain.NeedsRecovery || session.Dispatch != domain.DispatchPaused || session.Problem == nil || (end == "terminal" && session.Outcome != domain.ExecutionSucceeded) {
				t.Fatal("response uncertainty lost recovery or changed the observed terminal outcome")
			}
		})
	}
}

func TestQuestionClaimReplayRechecksAuthorityWithoutReturningAnswers(t *testing.T) {
	for _, change := range []string{"cancel", "worker", "heartbeat", "device", "account"} {
		t.Run(change, func(t *testing.T) {
			f, request := questionClaimFixture(t)
			if _, err := claimQuestion(f, request); err != nil {
				t.Fatal(err)
			}
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.revoke-response", change, func(tx *store.Tx) (any, error) {
				switch change {
				case "cancel":
					return nil, tx.RequestJobCancellation(f.job)
				case "worker":
					return nil, tx.SetWorkerInstance(f.input.MachineID, domain.NewID(), time.Now().UTC())
				case "heartbeat":
					return nil, tx.SetWorkerInstance(f.input.MachineID, f.instance, time.Now().Add(-time.Minute))
				}
				kind, id := domain.AccountKind, f.input.AccountID
				if change == "device" {
					kind, id = domain.DeviceKind, f.device
				}
				r, err := tx.Get(kind, id)
				if err != nil {
					return nil, err
				}
				var document map[string]any
				if err := json.Unmarshal(r.Data, &document); err != nil {
					return nil, err
				}
				if change == "device" {
					document["revoked"] = true
				} else {
					document["enabled"] = false
				}
				return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, document)
			})
			if err != nil {
				t.Fatal(err)
			}
			if response, err := claimQuestion(f, request); err == nil || response != nil {
				t.Fatal("accepted receipt returned answers after authority loss")
			}
			r, value := readPublishedInteraction(t, f, domain.ID(request.Mutation.Id))
			if r.Revision != 3 || value.Response.Claim.ID != domain.ID(request.Mutation.RequestId) {
				t.Fatal("rejected retry changed the original response claim")
			}
		})
	}
}

func TestQuestionResponseControlBypassesBlockedJobQueue(t *testing.T) {
	f, id := questionResponseFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request := connect.NewRequest(&pb.WatchWorkRequest{MachineId: string(f.input.MachineID), InstanceId: string(f.instance)})
	request.Header().Set("Authorization", "Bearer "+f.workerToken)
	stream, err := f.client.WatchWork(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if !stream.Receive() || !stream.Msg().Heartbeat || !stream.Receive() || stream.Msg().Job == nil || stream.Msg().Job.Id != string(f.job) {
		t.Fatal("active native assignment was not delivered", stream.Err())
	}
	later := domain.NewID()
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.later-job", nil, func(tx *store.Tx) (any, error) {
		return tx.PutJob(later, 0, "", "", domain.Job{Type: domain.HarnessDiscoveryJob, State: domain.JobQueued, MachineID: f.input.MachineID, Input: json.RawMessage(`{}`), AcceptedAt: time.Now().UTC()})
	})
	if err != nil {
		t.Fatal(err)
	}
	for sequence := uint64(0); sequence < 2; sequence++ {
		if sequence != 0 {
			id = domain.NewID()
			e := f.interactionEvent(4, id, "second-question")
			e.Interaction.Questions.Questions = append(e.Interaction.Questions.Questions, domain.Question{ID: "optional", Header: "Optional", Text: "Optional question", Other: true})
			f.publish(t, e)
		}
		responseID := domain.NewID()
		if _, err := acceptFixtureResponse(f, responseID, id, 1, responseInput()); err != nil {
			t.Fatal(err)
		}
		if !stream.Receive() {
			t.Fatal("response control blocked behind running native work", stream.Err())
		}
		message := stream.Msg()
		control := message.QuestionResponse
		if control == nil || message.Job != nil || message.Heartbeat || message.CancelRequested || message.CancelJobId != "" || control.JobId != string(f.job) || control.InteractionId != string(id) || control.ResponseId != string(responseID) || control.Revision != 2 {
			t.Fatal("control leaked content, duplicated a prior response or delivered a later job")
		}
	}
	r, err := f.service.Store.Get(ctx, domain.JobKind, later)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Decode[domain.Job](r)
	if err != nil || job.State != domain.JobQueued {
		t.Fatal("response control preclaimed a later queued job")
	}
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.cancel", nil, func(tx *store.Tx) (any, error) { return nil, tx.RequestJobCancellation(f.job) })
	if err != nil || !stream.Receive() || stream.Msg().CancelJobId != string(f.job) || stream.Msg().QuestionResponse != nil {
		t.Fatal("response controls blocked targeted cancellation", err, stream.Err())
	}
}
