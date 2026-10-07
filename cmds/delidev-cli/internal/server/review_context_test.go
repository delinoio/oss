package server

import (
	"context"
	"encoding/json"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestReviewContextRejectsWorkerAndNonDiffQueries(t *testing.T) {
	f := newFirstDispatchFixture(t)
	query := domain.WorkspaceReadQuery{Operation: domain.WorkspaceGitDiff, RepositoryID: domain.NewID(), Path: ".", Comparison: domain.DiffWorkingTree}
	raw, _ := json.Marshal(query)
	request := &pb.ReadSessionReviewContextRequest{SessionId: f.change.Session.Id, QueryJson: raw}
	if _, err := sessionClient(f.accountFixture).ReadSessionReviewContext(context.Background(), ownerRequest(f.workerIdentity, request)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("Worker accessed client review", err)
	}
	query.Operation, query.RepositoryID, query.Comparison, query.Path = domain.WorkspaceRoots, "", "", ""
	request.QueryJson, _ = json.Marshal(query)
	if _, err := sessionClient(f.accountFixture).ReadSessionReviewContext(context.Background(), ownerRequest(f.identity, request)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("non-diff gained review context", err)
	}
}
