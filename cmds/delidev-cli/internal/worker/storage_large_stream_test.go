// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type storageLargeStream struct {
	delidevv1connect.UnimplementedWorkerServiceHandler
	resource *pb.Resource
}

func (s *storageLargeStream) WatchWork(ctx context.Context, request *connect.Request[pb.WatchWorkRequest], stream *connect.ServerStream[pb.WatchWorkResponse]) error {
	if request.Header().Get("Authorization") != "Bearer isolated-large-stream" {
		return connect.NewError(connect.CodeUnauthenticated, nil)
	}
	if err := stream.Send(&pb.WatchWorkResponse{Job: s.resource}); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}
func TestLargeStorageStreamDecodesBeforeOriginalOwnershipGuard(t *testing.T) {
	root := t.TempDir()
	original := workspace.StorageRequest{Version: 1, OperationID: domain.NewID(), Action: workspace.StoragePreview, Preparation: workspace.PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), ForkSourcePath: "/" + strings.Repeat("metadata", 80_000)}}
	input := original
	input.Action, input.OperationID = workspace.StorageRecover, domain.NewID()
	input.Recovery = &workspace.StorageRecovery{Original: original, Claims: []workspace.StorageJournalClaim{{JobID: original.OperationID, InstanceID: domain.NewID(), Revision: 2, AssignmentDigest: strings.Repeat("a", 64)}}}
	raw, _ := json.Marshal(input)
	job := domain.Job{Type: domain.WorkspaceStorageJob, State: domain.JobClaimed, MachineID: original.Preparation.MachineID, InstanceID: domain.NewID(), Input: raw, AcceptedAt: time.Now().UTC()}
	session := original.Preparation.SessionID

	document, _ := json.Marshal(job)
	if len(document) <= 1<<20 {
		t.Fatal("fixture did not exceed ordinary envelope")
	}
	service := &storageLargeStream{resource: &pb.Resource{Id: string(domain.NewID()), Kind: pb.EntityKind_ENTITY_KIND_JOB, SchemaVersion: 1, Revision: 2, SessionId: string(session), DocumentJson: document}}
	_, handler := delidevv1connect.NewWorkerServiceHandler(service)
	server := httptest.NewServer(handler)
	defer server.Close()
	client := delidevv1connect.NewWorkerServiceClient(server.Client(), server.URL, connect.WithReadMaxBytes(8<<20))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Scope rejection occurs before input/native use. The storage fixture is
	// byte-bound metadata only; no native path or preparation is authorized.
	err := watch(ctx, Config{Root: root, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, client, Credential{MachineID: domain.NewID(), Token: "isolated-large-stream"}, job.InstanceID)
	if domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("large stream failed before its scope guard", domain.SafeError(err).Code)
	}
}
