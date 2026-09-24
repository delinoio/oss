package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

type approvalCLIFixture struct {
	delidevv1connect.UnimplementedResourceServiceHandler
	delidevv1connect.UnimplementedInteractionServiceHandler
	mu       sync.Mutex
	t        *testing.T
	root     string
	token    string
	resource *pb.Resource
	requests []*pb.RespondApprovalRequest
}

func (f *approvalCLIFixture) GetResource(_ context.Context, req *connect.Request[pb.GetResourceRequest]) (*connect.Response[pb.GetResourceResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.Header().Get("Authorization") != "Bearer "+f.token || req.Msg.Kind != pb.EntityKind_ENTITY_KIND_INTERACTION || req.Msg.Id != f.resource.Id {
		f.t.Error("CLI read the wrong original interaction or lost authentication")
	}
	return connect.NewResponse(&pb.GetResourceResponse{Resource: proto.Clone(f.resource).(*pb.Resource)}), nil
}

func (f *approvalCLIFixture) RespondApproval(_ context.Context, req *connect.Request[pb.RespondApprovalRequest]) (*connect.Response[pb.RespondApprovalResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.Header().Get("Authorization") != "Bearer "+f.token {
		f.t.Error("CLI response lost authentication")
	}
	replayed := len(f.requests) > 0
	f.requests = append(f.requests, proto.Clone(req.Msg).(*pb.RespondApprovalRequest))
	return connect.NewResponse(&pb.RespondApprovalResponse{Interaction: proto.Clone(f.resource).(*pb.Resource), RequestId: req.Msg.Mutation.RequestId, Replayed: replayed}), nil
}

func newApprovalCLIFixture(t *testing.T) *approvalCLIFixture {
	t.Helper()
	token, err := security.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	document := domain.ExecutionInteraction{Type: domain.NativeApprovalInteraction, Closure: domain.InteractionOpen, Approval: &domain.ApprovalRequest{Harness: domain.Codex, Version: domain.CodexProtocolVersion, Codex: &domain.CodexApprovalRequest{Kind: domain.CodexCommandApproval, Command: &domain.CodexCommandApprovalRequest{Kind: domain.CodexExecuteCommandApproval, AvailableDecisions: []domain.CodexApprovalDecision{{Kind: domain.CodexApprovalAccept}, {Kind: domain.CodexApprovalCancel}}}}}}
	raw, _ := json.Marshal(document)
	f := &approvalCLIFixture{t: t, token: token, root: filepath.Join(t.TempDir(), "client"), resource: &pb.Resource{Id: string(domain.NewID()), Kind: pb.EntityKind_ENTITY_KIND_INTERACTION, SchemaVersion: 1, Revision: 1, DocumentJson: raw}}
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

func TestCLIApprovalResponseOriginalIdentityAndReceiptRetry(t *testing.T) {
	f := newApprovalCLIFixture(t)
	id, requestID := f.resource.Id, string(domain.NewID())
	args := []string{"interaction", "approve", "--id", id, "--revision", "1", "--request-id", requestID}
	input := `{"decision":{"kind":"accept"}}`
	code, output := cliRun(t, f.root, args, input)
	if code != 0 || output["request_id"] != requestID || output["result"].(map[string]any)["replayed"] != false {
		t.Fatalf("CLI response failed: %d %v", code, output)
	}
	f.mu.Lock()
	first := proto.Clone(f.requests[0]).(*pb.RespondApprovalRequest)
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
	// Authentication stdin remains separate from the response document.
	code, output = cliRun(t, f.root, append(args, "--input", file, "--token-stdin"), f.token)
	if code != 0 || output["request_id"] != requestID || output["result"].(map[string]any)["replayed"] != true || output["result"].(map[string]any)["interaction"].(map[string]any)["revision"] != float64(3) {
		t.Fatalf("CLI rejected an accepted receipt after closure: %d %v", code, output)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) != 2 || !proto.Equal(first, f.requests[1]) || first.Mutation.Id != id || first.Mutation.ExpectedRevision != 1 || first.Mutation.RequestId != requestID {
		t.Fatal("CLI retry changed its original mutation identity or canonical content")
	}
	var answers domain.ApprovalResponseInput
	if domain.Decode(first.ResponseJson, &answers) != nil || answers.Decision == nil || answers.Decision.Kind != domain.CodexApprovalAccept {
		t.Fatal("CLI changed the selected native decision")
	}
}

func TestCLIApprovalResponseRejectsInvalidOriginalAndResponseBeforeRPC(t *testing.T) {
	for _, invalid := range []string{"missing-id", "missing-revision", "unknown-flag", "unknown-option", "null", "approval", "missing-choices", "wrong-kind", "schema", "credential-stdin"} {
		t.Run(invalid, func(t *testing.T) {
			f := newApprovalCLIFixture(t)
			args := []string{"interaction", "approve", "--id", f.resource.Id, "--revision", "1"}
			input := `{"decision":{"kind":"accept"}}`
			switch invalid {
			case "missing-id":
				args = []string{"interaction", "approve", "--revision", "1"}
			case "missing-revision":
				args = args[:4]
			case "unknown-flag":
				args = append(args, "--answer", "Second")
			case "unknown-option":
				input = `{"decision":{"kind":"decline"}}`
			case "null":
				input = `{"decision":null}`
			case "approval":
				input = `{"decision":{"kind":"accept"},"answers":{}}`
			case "credential-stdin":
				args = append(args, "--token-stdin")
				input = f.token
			default:
				f.mu.Lock()
				var doc domain.ExecutionInteraction
				if domain.Decode(f.resource.DocumentJson, &doc) != nil {
					t.Fatal("invalid fixture")
				}
				if invalid == "missing-choices" {
					doc.Approval.Codex.Command.AvailableDecisions = nil
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
				t.Fatal("CLI accepted an invalid approval response")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.requests) != 0 {
				t.Fatal("invalid approval response crossed the response RPC boundary")
			}
		})
	}
}
