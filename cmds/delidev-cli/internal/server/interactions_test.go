package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

func respondQuestionRPC(f *publicationFixture, identity security.Identity, req *pb.RespondQuestionRequest) (*connect.Response[pb.RespondQuestionResponse], error) {
	client := delidevv1connect.NewInteractionServiceClient(f.http.Client(), f.http.URL)
	return client.RespondQuestion(context.Background(), ownerRequest(identity, req))
}

func questionRPCRequest(t *testing.T, id domain.ID, input domain.QuestionResponseInput) *pb.RespondQuestionRequest {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return &pb.RespondQuestionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(id), ExpectedRevision: 1}, ResponseJson: raw}
}

func pairedQuestionClient(t *testing.T, f *publicationFixture) (security.Identity, *pb.Resource) {
	t.Helper()
	client := delidevv1connect.NewDeviceServiceClient(f.http.Client(), f.http.URL)
	code, token := randomCode(), randomCode()
	codeDigest, tokenDigest := sha256.Sum256([]byte(code)), sha256.Sum256([]byte(token))
	ctx := context.Background()
	grant, err := client.CreatePairing(ctx, ownerRequest(f.service.Identity, &pb.CreatePairingRequest{RequestId: string(domain.NewID()), Name: "question client", Type: pb.DeviceType_DEVICE_TYPE_CLIENT, CodeDigest: codeDigest[:]}))
	if err != nil {
		t.Fatal(err)
	}
	paired, err := client.PairDevice(ctx, connect.NewRequest(&pb.PairDeviceRequest{RequestId: string(domain.NewID()), PairingId: grant.Msg.Pairing.Id, Code: code, DeviceId: string(domain.NewID()), CredentialDigest: tokenDigest[:]}))
	if err != nil {
		t.Fatal(err)
	}
	return security.Identity{Token: token}, paired.Msg.Device
}

func TestQuestionOwnerRPCConcurrentClientsAndCurrentStateReplay(t *testing.T) {
	f, id := questionResponseFixture(t)
	paired, _ := pairedQuestionClient(t, f)
	_, original := readPublishedInteraction(t, f, id)
	type outcome struct {
		identity security.Identity
		request  *pb.RespondQuestionRequest
		response *connect.Response[pb.RespondQuestionResponse]
		err      error
	}
	results := make(chan outcome, 8)
	var group sync.WaitGroup
	for i := range 8 {
		identity := f.service.Identity
		if i%2 == 0 {
			identity = paired
		}
		request := questionRPCRequest(t, id, responseInput())
		group.Go(func() {
			response, err := respondQuestionRPC(f, identity, request)
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
	if r.Revision != 2 || value.Response.ID != domain.ID(accepted.request.Mutation.RequestId) || value.Response.State != domain.QuestionResponseQueued || !reflect.DeepEqual(value.Response.Input, responseInput()) || !reflect.DeepEqual(value.Questions, original.Questions) {
		t.Fatal("owner response changed its original question, identity or exact answers")
	}
	changed := proto.Clone(accepted.request).(*pb.RespondQuestionRequest)
	changed.ResponseJson = []byte(`{"answers":{"choice":["First"],"optional":[]}}`)
	if _, err := respondQuestionRPC(f, accepted.identity, changed); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("changed answers inherited an accepted receipt: %v", err)
	}
	otherActor := f.service.Identity
	if accepted.identity.Token == otherActor.Token {
		otherActor = paired
	}
	if _, err := respondQuestionRPC(f, otherActor, accepted.request); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("a different principal inherited an accepted receipt: %v", err)
	}
	// Closing the original request cancels queued work. An exact accepted
	// receipt remains inspectable even after the execution authority ends.
	e := f.event(domain.ExecutionTurnFinished, 4)
	e.Outcome = domain.ExecutionSucceeded
	f.publish(t, e)
	accepted.request.ResponseJson = []byte("{\n\"answers\":{\"optional\":[],\"choice\":[\"Second\",\"First\",\"Exact 답변\\n\"]}}")
	replay, err := respondQuestionRPC(f, accepted.identity, accepted.request)
	if err != nil || !replay.Msg.Replayed || replay.Msg.Interaction.Revision != 3 {
		t.Fatalf("canonical receipt replay failed after native closure: %v", err)
	}
	var current domain.ExecutionInteraction
	if domain.Decode(replay.Msg.Interaction.DocumentJson, &current) != nil || current.Response.State != domain.QuestionResponseCanceled || current.Closure != domain.InteractionTurnEnded || !reflect.DeepEqual(current.Questions, original.Questions) || !reflect.DeepEqual(current.Response.Input, responseInput()) {
		t.Fatal("receipt replay returned stale content or requeued a canceled response")
	}
}

func TestQuestionOwnerRPCReceiptContainsOnlyReference(t *testing.T) {
	f, id := questionResponseFixture(t)
	req := questionRPCRequest(t, id, responseInput())
	if _, err := respondQuestionRPC(f, f.service.Identity, req); err != nil {
		t.Fatal(err)
	}
	identity := questionResponseIdentity{id, 1, responseInput(), domain.Principal{Type: domain.OwnerDevice}}
	result, err := f.service.Store.Mutate(context.Background(), domain.ID(req.Mutation.RequestId), "question.respond", identity, func(*store.Tx) (any, error) {
		t.Fatal("accepted response was applied twice")
		return nil, nil
	})
	if err != nil || !result.Replayed || string(result.Data) != `{"interaction_id":"`+string(id)+`"}` {
		t.Fatalf("owner response receipt duplicated original or answer content: %v", err)
	}
}

func TestQuestionOwnerRPCRequiresCurrentClientAuthority(t *testing.T) {
	f, id := questionResponseFixture(t)
	client, device := pairedQuestionClient(t, f)
	req := questionRPCRequest(t, id, responseInput())
	for _, actor := range []security.Identity{{}, {Token: f.workerToken}} {
		if _, err := respondQuestionRPC(f, actor, req); err == nil {
			t.Fatal("unauthenticated or Worker principal answered an owner question")
		}
	}
	if _, err := f.service.RespondQuestion(context.Background(), connect.NewRequest(req)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("direct invocation bypassed explicit principal authorization")
	}
	if _, err := respondQuestionRPC(f, client, req); err != nil {
		t.Fatal(err)
	}
	devices := delidevv1connect.NewDeviceServiceClient(f.http.Client(), f.http.URL)
	_, err := devices.RevokeDevice(context.Background(), ownerRequest(f.service.Identity, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{Id: device.Id, ExpectedRevision: device.Revision, RequestId: string(domain.NewID())}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := respondQuestionRPC(f, client, req); err == nil {
		t.Fatal("revoked client read an accepted response through receipt replay")
	}
	// A principal that was authenticated before revocation must also fail at
	// the transaction boundary, including an already accepted receipt.
	stale := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: domain.ID(device.Id)})
	if _, err := f.service.RespondQuestion(stale, connect.NewRequest(req)); err == nil {
		t.Fatal("stale authenticated client bypassed commit-time revocation")
	}
}

func TestQuestionOwnerRPCRejectsInvalidAndOversizedNativeAnswersAtomically(t *testing.T) {
	for _, change := range []string{"null", "duplicate", "approval", "missing-question", "revision", "missing-mutation", "invalid-id", "native-size"} {
		t.Run(change, func(t *testing.T) {
			f, id := questionResponseFixture(t)
			req := questionRPCRequest(t, id, responseInput())
			want := connect.CodeInvalidArgument
			switch change {
			case "null":
				req.ResponseJson = []byte(`{"answers":{"choice":null,"optional":[]}}`)
			case "duplicate":
				req.ResponseJson = []byte(`{"answers":{"choice":["First"],"choice":["Second"],"optional":[]}}`)
			case "approval":
				req.ResponseJson = []byte(`{"answers":{"choice":["First"],"optional":[]},"approved":true}`)
			case "missing-question":
				req.ResponseJson = []byte(`{"answers":{"choice":["First"]}}`)
			case "revision":
				req.Mutation.ExpectedRevision++
				want = connect.CodeAborted
			case "missing-mutation":
				req.Mutation = nil
			case "invalid-id":
				req.Mutation.Id = "invalid"
			case "native-size":
				// The native protocol adds a wrapper around each answer array.
				// A domain-sized document may therefore exceed the native bound.
				input := domain.QuestionResponseInput{Answers: map[string][]string{"choice": {""}, "optional": {}}}
				raw, _ := json.Marshal(input)
				input.Answers["choice"][0] = strings.Repeat("x", domain.MaxQuestionResponseBytes-len(raw))
				_, original := readPublishedInteraction(t, f, id)
				if input.Validate(original.Questions) != nil || codex.ValidateQuestionResponseSize(codex.QuestionAnswers{Answers: input.Answers}) == nil {
					t.Fatal("fixture did not isolate native serialization overhead")
				}
				req.ResponseJson, _ = json.Marshal(input)
				want = connect.CodeResourceExhausted
			}
			if _, err := respondQuestionRPC(f, f.service.Identity, req); connect.CodeOf(err) != want {
				t.Fatalf("invalid response returned %v, want %s", err, want)
			}
			r, value := readPublishedInteraction(t, f, id)
			if r.Revision != 1 || value.Response != nil || value.Closure != domain.InteractionOpen {
				t.Fatal("rejected owner response consumed the original request")
			}
		})
	}
}
