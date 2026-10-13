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
	if _, err := sessionClient(f.accountFixture).ReadSessionReviewContext(context.Background(), ownerRequest(f.workerIdentity, request)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker accessed client review", err)
	}
	query.Operation, query.RepositoryID, query.Comparison, query.Path = domain.WorkspaceRoots, "", "", ""
	request.QueryJson, _ = json.Marshal(query)
	if _, err := sessionClient(f.accountFixture).ReadSessionReviewContext(context.Background(), ownerRequest(f.identity, request)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("non-diff gained review context", err)
	}
}

func TestReviewContextReturnsStructuredFilesForBranchObservation(t *testing.T) {
	for name, base := range map[string]domain.Reference{
		"local branch":  {Type: domain.LocalBranch, Name: "main"},
		"remote branch": {Type: domain.RemoteBranch, Remote: "origin", Name: "main"},
		"commit":        {Type: domain.CommitReference, Name: "0123456789abcdef0123456789abcdef01234567"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newLocalReviewFixture(t)
			useBranchObservation(f, base)
			query := domain.WorkspaceReadQuery{Operation: domain.WorkspaceGitDiff, RepositoryID: f.diff.RepositoryID, Path: f.diff.Path, Comparison: domain.DiffBranch, BaseRef: base}
			raw, _ := json.Marshal(query)
			response, err := reviewWithObservation(t, f, func() (*connect.Response[pb.ReadSessionReviewContextResponse], error) {
				return sessionClient(f.f.accountFixture).ReadSessionReviewContext(f.ctx, ownerRequest(f.f.identity, &pb.ReadSessionReviewContextRequest{SessionId: f.f.change.Session.Id, QueryJson: raw}))
			}, nil)
			if err != nil {
				t.Fatal("valid branch observation rejected", err)
			}
			var context domain.ReviewContext
			if err := domain.Decode(response.Msg.DocumentJson, &context); err != nil {
				t.Fatal(err)
			}
			if context.Diff.BaseRef != base || len(context.Files) != 1 || context.Files[0].Path != "file.txt" || context.Files[0].Kind != domain.ReviewTextFile || len(context.Files[0].Lines) != 2 || context.Files[0].Lines[1].New != 1 {
				t.Fatal("unexpected review context", context)
			}
		})
	}
}
