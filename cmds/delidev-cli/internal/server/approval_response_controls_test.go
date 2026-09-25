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

func approvalClaimFixture(t *testing.T) (*publicationFixture, *pb.ClaimApprovalResponseRequest) {
	t.Helper()
	f, id := approvalResponseFixture(t)
	responseID := domain.NewID()
	if _, err := acceptFixtureApproval(f, responseID, id, 1, approvalInput()); err != nil {
		t.Fatal(err)
	}
	return f, &pb.ClaimApprovalResponseRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(id), ExpectedRevision: 2}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: string(responseID)}
}

func claimApproval(f *publicationFixture, message *pb.ClaimApprovalResponseRequest) (*connect.Response[pb.ClaimApprovalResponseResponse], error) {
	req := connect.NewRequest(message)
	req.Header().Set("Authorization", "Bearer "+f.workerToken)
	return f.client.ClaimApprovalResponse(context.Background(), req)
}

func TestApprovalClaimConcurrentRetryRetainsOneOwner(t *testing.T) {
	f, request := approvalClaimFixture(t)
	var group sync.WaitGroup
	results := make(chan bool, 8)
	errors := make(chan error, 8)
	for range 8 {
		group.Go(func() {
			response, err := claimApproval(f, request)
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
	claim := value.ApprovalResponse.Claim
	if fresh != 1 || r.Revision != 3 || value.ApprovalResponse.State != domain.ApprovalResponseClaimed || claim == nil || claim.ID != domain.ID(request.Mutation.RequestId) || claim.JobID != f.job || claim.MachineID != f.input.MachineID || claim.InstanceID != f.instance || claim.DeviceID != f.device || claim.ClaimedAt.IsZero() || !reflect.DeepEqual(value.ApprovalResponse.Input, approvalInput()) {
		t.Fatal("concurrent replay replaced a response claim or changed its content")
	}
	other := proto.Clone(request).(*pb.ClaimApprovalResponseRequest)
	other.Mutation.RequestId, other.Mutation.ExpectedRevision = string(domain.NewID()), r.Revision
	if _, err := claimApproval(f, other); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("new request adopted an already claimed response: %v", err)
	}
	identity := approvalClaimIdentity{r.ID, value.ApprovalResponse.ID, f.job, f.input.MachineID, f.instance, f.device, 2}
	receipt, err := f.service.Store.Mutate(context.Background(), claim.ID, "approval.claim", identity, func(*store.Tx) (any, error) {
		t.Fatal("retained claim was re-applied")
		return nil, nil
	})
	if err != nil || !receipt.Replayed || string(receipt.Data) != `{"interaction_id":"`+string(r.ID)+`"}` {
		t.Fatalf("claim receipt duplicated response content: %v", err)
	}
}

func TestApprovalClaimRejectsForeignAndStaleControl(t *testing.T) {
	for _, changed := range []string{"interaction", "response", "job", "machine", "instance", "revision", "owner"} {
		t.Run(changed, func(t *testing.T) {
			f, original := approvalClaimFixture(t)
			request := proto.Clone(original).(*pb.ClaimApprovalResponseRequest)
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
			if _, err := f.client.ClaimApprovalResponse(context.Background(), req); err == nil {
				t.Fatal("foreign/stale response control was accepted")
			}
			r, value := readPublishedInteraction(t, f, domain.ID(original.Mutation.Id))
			if r.Revision != 2 || value.ApprovalResponse.State != domain.ApprovalResponseQueued || value.ApprovalResponse.Claim != nil {
				t.Fatal("rejected claim consumed the response")
			}
		})
	}
}

func TestApprovalClaimClosureAndLostWorkerRetainUncertainty(t *testing.T) {
	for _, end := range []string{"native-closure", "terminal", "worker-loss", "queued-worker-loss"} {
		t.Run(end, func(t *testing.T) {
			f, request := approvalClaimFixture(t)
			id := domain.ID(request.Mutation.Id)
			if end != "queued-worker-loss" {
				if _, err := claimApproval(f, request); err != nil {
					t.Fatal(err)
				}
			}
			switch end {
			case "native-closure":
				e := approvalFixtureEvent(f, 4, id, "native-approval")
				e.Kind, e.Interaction.Approval, e.Interaction.Closure = domain.ExecutionInteractionClosed, nil, domain.InteractionNativeClosed
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
			if _, err := claimApproval(f, request); err == nil {
				t.Fatal("claim retry recovered response delivery authority after closure/loss")
			}
			_, value := readPublishedInteraction(t, f, id)
			want := domain.ApprovalResponseUncertain
			if end == "queued-worker-loss" {
				want = domain.ApprovalResponseCanceled
			}
			if value.ApprovalResponse.State != want || !reflect.DeepEqual(value.ApprovalResponse.Input, approvalInput()) || value.Approval.Codex.Command.Kind != domain.CodexExecuteCommandApproval {
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

func TestApprovalClaimReplayRechecksAuthorityWithoutReturningContent(t *testing.T) {
	for _, change := range []string{"cancel", "worker", "heartbeat", "device", "account"} {
		t.Run(change, func(t *testing.T) {
			f, request := approvalClaimFixture(t)
			if _, err := claimApproval(f, request); err != nil {
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
			if response, err := claimApproval(f, request); err == nil || response != nil {
				t.Fatal("accepted receipt returned response content after authority loss")
			}
			r, value := readPublishedInteraction(t, f, domain.ID(request.Mutation.Id))
			if r.Revision != 3 || value.ApprovalResponse.Claim.ID != domain.ID(request.Mutation.RequestId) {
				t.Fatal("rejected retry changed the original response claim")
			}
		})
	}
}

func TestApprovalResponseControlBypassesBlockedJobQueue(t *testing.T) {
	f, id := approvalResponseFixture(t)
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
			e := approvalFixtureEvent(f, 4, id, "second-approval")
			f.publish(t, e)
		}
		responseID := domain.NewID()
		if _, err := acceptFixtureApproval(f, responseID, id, 1, approvalInput()); err != nil {
			t.Fatal(err)
		}
		if !stream.Receive() {
			t.Fatal("response control blocked behind running native work", stream.Err())
		}
		message := stream.Msg()
		control := message.ApprovalResponse
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
	if err != nil || !stream.Receive() || stream.Msg().CancelJobId != string(f.job) || stream.Msg().ApprovalResponse != nil {
		t.Fatal("response controls blocked targeted cancellation", err, stream.Err())
	}
}
