// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type compactionLargeStream struct {
	delidevv1connect.UnimplementedWorkerServiceHandler
	resource *pb.Resource
}

func (s *compactionLargeStream) WatchWork(ctx context.Context, request *connect.Request[pb.WatchWorkRequest], stream *connect.ServerStream[pb.WatchWorkResponse]) error {
	if request.Header().Get("Authorization") != "Bearer isolated-large-stream" {
		return connect.NewError(connect.CodeUnauthenticated, nil)
	}
	if err := stream.Send(&pb.WatchWorkResponse{Job: s.resource}); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}
func TestLargeCompactionStreamDecodesBeforeOriginalOwnershipGuard(t *testing.T) {
	root, input := workerCompactionInput(t)
	input.Assignment.Input.Prompt = strings.Repeat("\"", domain.MaxPromptBytes)
	input.Restore.Input.Prompt = input.Assignment.Input.Prompt
	original, _ := json.Marshal(input.Assignment)
	input.Restore.Continuation.AssignmentInputDigest = executionInputDigest(original)
	input.Restore.Continuation.PromptDigest = executionInputDigest([]byte(input.Assignment.Input.Prompt))
	input.Restore.Continuation.Previous.AcceptedInputs = []domain.ExecutionInputBinding{domain.BindExecutionInput(input.Assignment.InputID, input.Assignment.Input.Prompt)}
	raw, _ := json.Marshal(input)
	job := domain.Job{Type: domain.CompactSessionJob, State: domain.JobClaimed, MachineID: input.Assignment.MachineID, ParentID: input.SourceJobID, AssignedDeviceID: domain.NewID(), InstanceID: domain.NewID(), Input: raw, AcceptedAt: time.Now().UTC()}
	session := input.Assignment.SessionID

	document, _ := json.Marshal(job)
	if len(document) <= 1<<20 {
		t.Fatal("fixture did not exceed ordinary envelope")
	}
	service := &compactionLargeStream{resource: &pb.Resource{Id: string(domain.NewID()), Kind: pb.EntityKind_ENTITY_KIND_JOB, SchemaVersion: 1, Revision: 2, SessionId: string(session), DocumentJson: document}}
	_, handler := delidevv1connect.NewWorkerServiceHandler(service)
	server := httptest.NewServer(handler)
	defer server.Close()
	client := delidevv1connect.NewWorkerServiceClient(server.Client(), server.URL, connect.WithReadMaxBytes(8<<20))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Scope rejection occurs before input/native use. The storage fixture is
	// byte-bound metadata only; no native path or preparation is authorized.
	err := watch(ctx, Config{Root: root, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, client, Credential{MachineID: domain.NewID(), Token: "isolated-large-stream"}, job.InstanceID)
	if domain.SafeError(err).Code != domain.PermissionDenied {
		t.Fatal("large stream failed before its scope guard", domain.SafeError(err).Code)
	}
}
