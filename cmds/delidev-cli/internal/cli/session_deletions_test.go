package cli

import (
	"context"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

type deletionCLIService struct {
	delidevv1connect.UnimplementedSessionServiceHandler
	request *pb.DeleteSessionRequest
	job     *pb.SessionDeletionJob
	reads   int
}

func (s *deletionCLIService) DeleteSession(ctx context.Context, r *connect.Request[pb.DeleteSessionRequest]) (*connect.Response[pb.DeleteSessionResponse], error) {
	s.request = r.Msg
	return connect.NewResponse(&pb.DeleteSessionResponse{Job: s.job, RequestId: r.Msg.Mutation.RequestId}), nil
}
func (s *deletionCLIService) GetSessionDeletion(ctx context.Context, r *connect.Request[pb.GetSessionDeletionRequest]) (*connect.Response[pb.GetSessionDeletionResponse], error) {
	s.reads++
	copy := proto.Clone(s.job).(*pb.SessionDeletionJob)
	copy.State = pb.SessionDeletionState_SESSION_DELETION_STATE_SUCCEEDED
	return connect.NewResponse(&pb.GetSessionDeletionResponse{Job: copy}), nil
}

func TestCLISessionDeletionRequiresConfirmationAndObservesWithoutReplay(t *testing.T) {
	s := &deletionCLIService{job: &pb.SessionDeletionJob{Id: string(domain.NewID()), SessionId: string(domain.NewID()), Revision: 9007199254740993, State: pb.SessionDeletionState_SESSION_DELETION_STATE_PENDING}}
	_, handler := delidevv1connect.NewSessionServiceHandler(s)
	server := httptest.NewServer(handler)
	defer server.Close()
	c := client{sessions: delidevv1connect.NewSessionServiceClient(server.Client(), server.URL), token: "fixture"}
	o := options{requestID: domain.NewID()}
	if _, e := sessionDeletionCommand(context.Background(), c, o, []string{"delete", "--id", s.job.SessionId, "--revision", "1"}); e == nil || s.request != nil {
		t.Fatal("unconfirmed deletion dispatched", e)
	}
	result, e := sessionDeletionCommand(context.Background(), c, o, []string{"delete", "--id", s.job.SessionId, "--revision", "7", "--confirm", "--wait"})
	if e != nil {
		t.Fatal(e)
	}
	if s.request.Mutation.RequestId != string(o.requestID) || s.request.Mutation.ExpectedRevision != 7 || s.reads != 1 {
		t.Fatal("lost exact deletion identity or replayed mutation")
	}
	if result.(map[string]any)["revision"] != "9007199254740993" {
		t.Fatal("uint64 revision lost precision", result)
	}
}
