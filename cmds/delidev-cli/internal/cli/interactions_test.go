package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

type questionCLIFixture struct {
	delidevv1connect.UnimplementedResourceServiceHandler
	delidevv1connect.UnimplementedInteractionServiceHandler
	mu       sync.Mutex
	t        *testing.T
	root     string
	token    string
	resource *pb.Resource
	requests []*pb.RespondQuestionRequest
}

func (f *questionCLIFixture) GetResource(_ context.Context, req *connect.Request[pb.GetResourceRequest]) (*connect.Response[pb.GetResourceResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.Header().Get("Authorization") != "Bearer "+f.token || req.Msg.Kind != pb.EntityKind_ENTITY_KIND_INTERACTION || req.Msg.Id != f.resource.Id {
		f.t.Error("CLI read the wrong original interaction or lost authentication")
	}
	return connect.NewResponse(&pb.GetResourceResponse{Resource: proto.Clone(f.resource).(*pb.Resource)}), nil
}

func (f *questionCLIFixture) RespondQuestion(_ context.Context, req *connect.Request[pb.RespondQuestionRequest]) (*connect.Response[pb.RespondQuestionResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.Header().Get("Authorization") != "Bearer "+f.token {
		f.t.Error("CLI response lost authentication")
	}
	replayed := len(f.requests) > 0
	f.requests = append(f.requests, proto.Clone(req.Msg).(*pb.RespondQuestionRequest))
	return connect.NewResponse(&pb.RespondQuestionResponse{Interaction: proto.Clone(f.resource).(*pb.Resource), RequestId: req.Msg.Mutation.RequestId, Replayed: replayed}), nil
}

func newQuestionCLIFixture(t *testing.T) *questionCLIFixture {
	t.Helper()
	token, err := security.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	document := domain.ExecutionInteraction{Type: domain.UserQuestionInteraction, Closure: domain.InteractionOpen, Questions: &domain.QuestionRequest{Blocking: true, Questions: []domain.Question{{ID: "choice", Header: "Choose", Text: "Original question", Options: []domain.QuestionOption{{Label: "First", Description: "First choice"}, {Label: "Second", Description: "Second choice"}}}, {ID: "free", Other: true, Text: "Optional text"}}}}
	raw, _ := json.Marshal(document)
	f := &questionCLIFixture{t: t, token: token, root: filepath.Join(t.TempDir(), "client"), resource: &pb.Resource{Id: string(domain.NewID()), Kind: pb.EntityKind_ENTITY_KIND_INTERACTION, SchemaVersion: 1, Revision: 1, DocumentJson: raw}}
	mux := http.NewServeMux()
	mux.Handle(delidevv1connect.NewResourceServiceHandler(f))
	mux.Handle(delidevv1connect.NewInteractionServiceHandler(f))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	if err := security.PrivateDir(f.root); err != nil {
		t.Fatal(err)
	}
	credential := worker.Credential{Version: 1, Type: domain.ClientDevice, Endpoint: server.URL, ServerID: domain.NewID(), DeviceID: domain.NewID(), PairingID: domain.NewID(), Token: token}
	raw, _ = json.Marshal(credential)
	if err := security.WriteAtomic(filepath.Join(f.root, "device.json"), raw); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestCLIQuestionResponseOriginalIdentityAndReceiptRetry(t *testing.T) {
	f := newQuestionCLIFixture(t)
	id, requestID := f.resource.Id, string(domain.NewID())
	args := []string{"interaction", "respond", "--id", id, "--revision", "1", "--request-id", requestID}
	input := "{\"answers\":{\"free\":[\"Exact 답변\\n\"],\"choice\":[\"Second\",\"First\"]}}"
	code, output := cliRun(t, f.root, args, input)
	if code != 0 || output["request_id"] != requestID || output["result"].(map[string]any)["replayed"] != false {
		t.Fatalf("CLI response failed: %d %v", code, output)
	}
	f.mu.Lock()
	first := proto.Clone(f.requests[0]).(*pb.RespondQuestionRequest)
	var doc domain.ExecutionInteraction
	if err := domain.Decode(f.resource.DocumentJson, &doc); err != nil {
		t.Fatal(err)
	}
	doc.Closure = domain.InteractionTurnEnded
	f.resource.DocumentJson, _ = json.Marshal(doc)
	f.resource.Revision = 3
	f.mu.Unlock()
	file := filepath.Join(t.TempDir(), "answers.json")
	if err := os.WriteFile(file, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	// A closed current document must not hide an earlier acceptance receipt.
	// Authentication stdin remains separate from the answer document.
	code, output = cliRun(t, f.root, append(args, "--input", file, "--token-stdin"), f.token)
	if code != 0 || output["request_id"] != requestID || output["result"].(map[string]any)["replayed"] != true || output["result"].(map[string]any)["interaction"].(map[string]any)["revision"] != float64(3) {
		t.Fatalf("CLI rejected an accepted receipt after closure: %d %v", code, output)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) != 2 || !proto.Equal(first, f.requests[1]) || first.Mutation.Id != id || first.Mutation.ExpectedRevision != 1 || first.Mutation.RequestId != requestID {
		t.Fatal("CLI retry changed its original mutation identity or canonical content")
	}
	var answers domain.QuestionResponseInput
	if domain.Decode(first.ResponseJson, &answers) != nil || !reflect.DeepEqual(answers.Answers, map[string][]string{"choice": {"Second", "First"}, "free": {"Exact 답변\n"}}) {
		t.Fatal("CLI reordered answers or lost exact free text")
	}
}

func TestCLIQuestionResponseRejectsInvalidOriginalAndAnswerBeforeRPC(t *testing.T) {
	for _, invalid := range []string{"missing-id", "missing-revision", "unknown-flag", "unknown-option", "null", "approval", "secret", "wrong-kind", "schema", "credential-stdin"} {
		t.Run(invalid, func(t *testing.T) {
			f := newQuestionCLIFixture(t)
			args := []string{"interaction", "respond", "--id", f.resource.Id, "--revision", "1"}
			input := `{"answers":{"choice":["Second"],"free":[]}}`
			switch invalid {
			case "missing-id":
				args = []string{"interaction", "respond", "--revision", "1"}
			case "missing-revision":
				args = args[:4]
			case "unknown-flag":
				args = append(args, "--answer", "Second")
			case "unknown-option":
				input = `{"answers":{"choice":["Third"],"free":[]}}`
			case "null":
				input = `{"answers":{"choice":null,"free":[]}}`
			case "approval":
				input = `{"answers":{"choice":["Second"],"free":[]},"approved":true}`
			case "credential-stdin":
				args = append(args, "--token-stdin")
				input = f.token
			default:
				f.mu.Lock()
				var doc domain.ExecutionInteraction
				if domain.Decode(f.resource.DocumentJson, &doc) != nil {
					t.Fatal("invalid fixture")
				}
				if invalid == "secret" {
					doc.Questions.Questions[0].Secret = true
				} else if invalid == "wrong-kind" {
					doc.Type = "approval"
				} else {
					f.resource.SchemaVersion++
				}
				f.resource.DocumentJson, _ = json.Marshal(doc)
				f.mu.Unlock()
			}
			code, output := cliRun(t, f.root, args, input)
			if code == 0 || output["error"] == nil {
				t.Fatal("CLI accepted an invalid question response")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.requests) != 0 {
				t.Fatal("invalid/secret answer crossed the response RPC boundary")
			}
		})
	}
}
