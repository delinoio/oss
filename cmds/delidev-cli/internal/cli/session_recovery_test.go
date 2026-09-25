package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

type recoveryCLIFixture struct {
	delidevv1connect.UnimplementedSessionServiceHandler
	delidevv1connect.UnimplementedResourceServiceHandler
	mu          sync.Mutex
	token, root string
	t           *testing.T
	change      *pb.SessionChange
	requests    []*pb.RecoverSessionExecutionRequest
	reads       int
	uncertain   bool
}

func (f *recoveryCLIFixture) RecoverSessionExecution(_ context.Context, req *connect.Request[pb.RecoverSessionExecutionRequest]) (*connect.Response[pb.RecoverSessionExecutionResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.Header().Get("Authorization") != "Bearer "+f.token {
		f.t.Error("CLI lost authentication")
	}
	f.requests = append(f.requests, proto.Clone(req.Msg).(*pb.RecoverSessionExecutionRequest))
	change := proto.Clone(f.change).(*pb.SessionChange)
	change.Replayed = len(f.requests) > 1
	return connect.NewResponse(&pb.RecoverSessionExecutionResponse{Change: change}), nil
}
func (f *recoveryCLIFixture) GetResource(_ context.Context, req *connect.Request[pb.GetResourceRequest]) (*connect.Response[pb.GetResourceResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.Header().Get("Authorization") != "Bearer "+f.token {
		f.t.Error("CLI lost authentication")
	}
	f.reads++
	for _, r := range []*pb.Resource{f.change.Session, f.change.ExecutionJob, f.change.ExecutionRecoveryJob} {
		if r.Id == req.Msg.Id && r.Kind == req.Msg.Kind {
			current := proto.Clone(r).(*pb.Resource)
			current.Revision++
			if current.Id == f.change.ExecutionRecoveryJob.Id {
				job := domain.Job{Type: domain.RecoverExecutionJob, State: domain.JobSucceeded}
				if f.uncertain {
					job.State, job.Problem = domain.JobUncertain, domain.ExecutionRecoveryUncertain()
				}
				current.DocumentJson, _ = json.Marshal(job)
			}
			return connect.NewResponse(&pb.GetResourceResponse{Resource: current}), nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, nil)
}
func newRecoveryCLIFixture(t *testing.T) *recoveryCLIFixture {
	t.Helper()
	token, err := security.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	resource := func(kind pb.EntityKind, doc any) *pb.Resource {
		raw, _ := json.Marshal(doc)
		return &pb.Resource{Id: string(domain.NewID()), Revision: 7, Kind: kind, DocumentJson: raw}
	}
	f := &recoveryCLIFixture{t: t, root: filepath.Join(t.TempDir(), "client"), token: token, change: &pb.SessionChange{Session: resource(pb.EntityKind_ENTITY_KIND_SESSION, domain.Session{Recovery: domain.Reconciling, Dispatch: domain.DispatchPaused}), ExecutionJob: resource(pb.EntityKind_ENTITY_KIND_JOB, domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobUncertain}), ExecutionRecoveryJob: resource(pb.EntityKind_ENTITY_KIND_JOB, domain.Job{Type: domain.RecoverExecutionJob, State: domain.JobQueued})}}
	mux := http.NewServeMux()
	mux.Handle(delidevv1connect.NewSessionServiceHandler(f))
	mux.Handle(delidevv1connect.NewResourceServiceHandler(f))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	if err := security.PrivateDir(f.root); err != nil {
		t.Fatal(err)
	}
	credential := worker.Credential{Version: 1, Type: domain.ClientDevice, Endpoint: server.URL, ServerID: domain.NewID(), DeviceID: domain.NewID(), PairingID: domain.NewID(), Token: token}
	raw, _ := json.Marshal(credential)
	if err := security.WriteAtomic(filepath.Join(f.root, "device.json"), raw); err != nil {
		t.Fatal(err)
	}
	return f
}
func TestCLIExecutionRecoveryIdentityWaitAndUncertainty(t *testing.T) {
	for _, scenario := range []string{"accepted", "wait", "uncertain", "missing-execution"} {
		t.Run(scenario, func(t *testing.T) {
			f := newRecoveryCLIFixture(t)
			execution, requestID := string(domain.NewID()), string(domain.NewID())
			args := []string{"session", "recover-execution", "--id", f.change.Session.Id, "--revision", "7", "--request-id", requestID}
			if scenario != "missing-execution" {
				args = append(args, "--execution-id", execution)
			}
			if scenario == "wait" || scenario == "uncertain" {
				args = append(args, "--wait")
			}
			f.uncertain = scenario == "uncertain"
			code, output := cliRun(t, f.root, args, "")
			if scenario == "missing-execution" {
				if code == 0 || len(f.requests) != 0 {
					t.Fatal("invalid execution reached RPC")
				}
				return
			}
			if (code != 0) != (scenario == "uncertain") {
				t.Fatalf("unexpected CLI result: %d %v", code, output)
			}
			f.mu.Lock()
			if len(f.requests) != 1 || f.requests[0].ExpectedExecutionId != execution || f.requests[0].Mutation.Id != f.change.Session.Id || f.requests[0].Mutation.RequestId != requestID || f.requests[0].Mutation.ExpectedRevision != 7 {
				t.Fatal("CLI changed exact recovery identity")
			}
			if scenario == "accepted" && f.reads != 0 || scenario != "accepted" && f.reads != 3 {
				t.Fatal("CLI wait did not refresh recovery/session/original job")
			}
			f.mu.Unlock()
			if scenario == "accepted" {
				code, output = cliRun(t, f.root, args, "")
				if code != 0 || output["result"].(map[string]any)["replayed"] != true {
					t.Fatal("CLI retry lost current receipt", output)
				}
			}
		})
	}
}
