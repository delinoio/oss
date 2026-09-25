package server

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

func respondApprovalRPC(f *publicationFixture, identity security.Identity, req *pb.RespondApprovalRequest) (*connect.Response[pb.RespondApprovalResponse], error) {
	client := delidevv1connect.NewInteractionServiceClient(f.http.Client(), f.http.URL)
	return client.RespondApproval(context.Background(), ownerRequest(identity, req))
}

func approvalRPCRequest(t *testing.T, id domain.ID, input domain.ApprovalResponseInput) *pb.RespondApprovalRequest {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return &pb.RespondApprovalRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(id), ExpectedRevision: 1}, ResponseJson: raw}
}

func TestApprovalOwnerRPCConcurrentClientsAndCurrentStateReplay(t *testing.T) {
	f, id := approvalResponseFixture(t)
	paired, _ := pairedQuestionClient(t, f)
	_, original := readPublishedInteraction(t, f, id)
	type outcome struct {
		identity security.Identity
		request  *pb.RespondApprovalRequest
		response *connect.Response[pb.RespondApprovalResponse]
		err      error
	}
	results := make(chan outcome, 8)
	var group sync.WaitGroup
	for i := range 8 {
		identity := f.service.Identity
		if i%2 == 0 {
			identity = paired
		}
		request := approvalRPCRequest(t, id, approvalInput())
		group.Go(func() {
			response, err := respondApprovalRPC(f, identity, request)
			results <- outcome{identity, request, response, err}
		})
	}
	group.Wait()
	close(results)
	var accepted outcome
	count := 0
	for result := range results {
		if result.err == nil {
			accepted, count = result, count+1
		} else if connect.CodeOf(result.err) != connect.CodeAborted {
			t.Fatal(result.err)
		}
	}
	if count != 1 || accepted.response.Msg.Replayed || accepted.response.Msg.RequestId != accepted.request.Mutation.RequestId || accepted.response.Header().Get(rpc.CorrelationHeader) == "" {
		t.Fatal("competing clients accepted multiple responses or lost receipt metadata")
	}
	r, value := readPublishedInteraction(t, f, id)
	if r.Revision != 2 || value.ApprovalResponse.ID != domain.ID(accepted.request.Mutation.RequestId) || value.ApprovalResponse.State != domain.ApprovalResponseQueued || !reflect.DeepEqual(value.ApprovalResponse.Input, approvalInput()) || !reflect.DeepEqual(value.Approval, original.Approval) {
		t.Fatal("owner response changed its original approval, identity or exact response")
	}
	changed := proto.Clone(accepted.request).(*pb.RespondApprovalRequest)
	changed.ResponseJson = []byte(`{"decision":{"kind":"cancel"}}`)
	if _, err := respondApprovalRPC(f, accepted.identity, changed); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("changed responses inherited an accepted receipt: %v", err)
	}
	otherActor := f.service.Identity
	if accepted.identity.Token == otherActor.Token {
		otherActor = paired
	}
	if _, err := respondApprovalRPC(f, otherActor, accepted.request); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("a different principal inherited an accepted receipt: %v", err)
	}
	// Closing the original request cancels queued work. An exact accepted
	// receipt remains inspectable even after the execution authority ends.
	e := f.event(domain.ExecutionTurnFinished, 4)
	e.Outcome = domain.ExecutionSucceeded
	f.publish(t, e)
	accepted.request.ResponseJson = []byte("{\n\"decision\":{\"kind\":\"accept\"}}")
	replay, err := respondApprovalRPC(f, accepted.identity, accepted.request)
	if err != nil || !replay.Msg.Replayed || replay.Msg.Interaction.Revision != 3 {
		t.Fatalf("canonical receipt replay failed after native closure: %v", err)
	}
	var current domain.ExecutionInteraction
	if domain.Decode(replay.Msg.Interaction.DocumentJson, &current) != nil || current.ApprovalResponse.State != domain.ApprovalResponseCanceled || current.Closure != domain.InteractionTurnEnded || !reflect.DeepEqual(current.Approval, original.Approval) || !reflect.DeepEqual(current.ApprovalResponse.Input, approvalInput()) {
		t.Fatal("receipt replay returned stale content or requeued a canceled response")
	}
}

func TestApprovalOwnerRPCReceiptContainsOnlyReference(t *testing.T) {
	f, id := approvalResponseFixture(t)
	req := approvalRPCRequest(t, id, approvalInput())
	if _, err := respondApprovalRPC(f, f.service.Identity, req); err != nil {
		t.Fatal(err)
	}
	identity := approvalResponseIdentity{id, 1, approvalInput(), domain.Principal{Type: domain.OwnerDevice}}
	result, err := f.service.Store.Mutate(context.Background(), domain.ID(req.Mutation.RequestId), "approval.respond", identity, func(*store.Tx) (any, error) {
		t.Fatal("accepted response was applied twice")
		return nil, nil
	})
	if err != nil || !result.Replayed || string(result.Data) != `{"interaction_id":"`+string(id)+`"}` {
		t.Fatalf("owner response receipt duplicated original or response content: %v", err)
	}
}

func TestApprovalOwnerRPCRequiresCurrentClientAuthority(t *testing.T) {
	f, id := approvalResponseFixture(t)
	client, device := pairedQuestionClient(t, f)
	req := approvalRPCRequest(t, id, approvalInput())
	for _, actor := range []security.Identity{{}, {Token: f.workerToken}} {
		if _, err := respondApprovalRPC(f, actor, req); err == nil {
			t.Fatal("unauthenticated or Worker principal responded to an owner approval")
		}
	}
	if _, err := f.service.RespondApproval(context.Background(), connect.NewRequest(req)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("direct invocation bypassed explicit principal authorization")
	}
	if _, err := respondApprovalRPC(f, client, req); err != nil {
		t.Fatal(err)
	}
	devices := delidevv1connect.NewDeviceServiceClient(f.http.Client(), f.http.URL)
	_, err := devices.RevokeDevice(context.Background(), ownerRequest(f.service.Identity, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{Id: device.Id, ExpectedRevision: device.Revision, RequestId: string(domain.NewID())}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := respondApprovalRPC(f, client, req); err == nil {
		t.Fatal("revoked client read an accepted response through receipt replay")
	}
	// A principal that was authenticated before revocation must also fail at
	// the transaction boundary, including an already accepted receipt.
	stale := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: domain.ID(device.Id)})
	if _, err := f.service.RespondApproval(stale, connect.NewRequest(req)); err == nil {
		t.Fatal("stale authenticated client bypassed commit-time revocation")
	}
}

func TestApprovalOwnerRPCRejectsInvalidInputAtomically(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"decision":null}`, `{"decision":{"kind":"accept"},"decision":{"kind":"cancel"}}`, `{"decision":{"kind":"decline"}}`, `{"decision":{"kind":"accept"},"answers":{}}`, `{"decision":{"kind":"accept"},"grant":{"permissions":{},"scope":"turn"}}`} {
		t.Run(raw, func(t *testing.T) {
			f, id := approvalResponseFixture(t)
			req := approvalRPCRequest(t, id, approvalInput())
			req.ResponseJson = []byte(raw)
			if _, err := respondApprovalRPC(f, f.service.Identity, req); connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("invalid response returned %v", err)
			}
			r, value := readPublishedInteraction(t, f, id)
			if r.Revision != 1 || value.ApprovalResponse != nil || value.Closure != domain.InteractionOpen {
				t.Fatal("invalid input consumed original request")
			}
		})
	}
}
