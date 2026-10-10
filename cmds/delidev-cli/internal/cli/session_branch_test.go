package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type branchCLIClient struct {
	delidevv1connect.SessionServiceClient
	queries []domain.WorkspaceReadQuery
	old     bool
}

func (c *branchCLIClient) diff(q domain.WorkspaceReadQuery) domain.WorkspaceDiff {
	value := domain.WorkspaceDiff{RepositoryID: q.RepositoryID, Comparison: q.Comparison, Path: q.Path, Base: domain.DiffCommit, BaseObject: strings.Repeat("a", 40), HeadCommit: strings.Repeat("a", 40), Patch: "", Untracked: []string{}}
	if q.Comparison == domain.DiffBranch {
		value.BaseRef = q.BaseRef
		value.BaseCommit = value.BaseObject
		value.MergeBase = value.BaseObject
	}
	value.Revision = value.Digest()
	return value
}
func (c *branchCLIClient) ReadSessionWorkspace(_ context.Context, r *connect.Request[pb.ReadSessionWorkspaceRequest]) (*connect.Response[pb.ReadSessionWorkspaceResponse], error) {
	var q domain.WorkspaceReadQuery
	if err := domain.Decode(r.Msg.QueryJson, &q); err != nil {
		return nil, err
	}
	c.queries = append(c.queries, q)
	if q.Operation == domain.WorkspaceGitDiffOptions {
		if c.old {
			return nil, connect.NewError(connect.CodeInvalidArgument, nil)
		}
		ref := domain.Reference{Type: domain.LocalBranch, Name: "saved"}
		raw, _ := json.Marshal(domain.WorkspaceReadResult{DiffOptions: &domain.WorkspaceDiffOptions{Version: 1, RepositoryID: q.RepositoryID, Path: q.Path, Choices: []domain.DiffBaseChoice{{Reference: ref, Configured: true}}, Default: &ref, DefaultAvailable: true}})
		return connect.NewResponse(&pb.ReadSessionWorkspaceResponse{DocumentJson: raw}), nil
	}
	diff := c.diff(q)
	raw, _ := json.Marshal(domain.WorkspaceReadResult{Diff: &diff})
	return connect.NewResponse(&pb.ReadSessionWorkspaceResponse{DocumentJson: raw}), nil
}
func (c *branchCLIClient) ReadSessionReviewContext(_ context.Context, r *connect.Request[pb.ReadSessionReviewContextRequest]) (*connect.Response[pb.ReadSessionReviewContextResponse], error) {
	var q domain.WorkspaceReadQuery
	if err := domain.Decode(r.Msg.QueryJson, &q); err != nil {
		return nil, err
	}
	c.queries = append(c.queries, q)
	raw, _ := json.Marshal(domain.ReviewContext{Diff: c.diff(q), Files: []domain.ReviewFile{}})
	return connect.NewResponse(&pb.ReadSessionReviewContextResponse{DocumentJson: raw}), nil
}
func TestCLIBranchCommandsNegotiateBeforeExplicitBaseAndKeepDefault(t *testing.T) {
	for _, operation := range []string{"diff", "review-context"} {
		t.Run(operation, func(t *testing.T) {
			fake := &branchCLIClient{}
			args := []string{operation, "--id", string(domain.NewID()), "--repository-id", string(domain.NewID()), "--comparison", "branch", "--base-ref", `{"type":"local-branch","name":"original-A"}`}
			if _, err := sessionFiles(context.Background(), client{sessions: fake}, args); err != nil {
				t.Fatal(err)
			}
			if len(fake.queries) != 2 || fake.queries[0].Operation != domain.WorkspaceGitDiffOptions || fake.queries[0].Comparison != "" || fake.queries[0].BaseRef != nil || fake.queries[1].BaseRef.Name != "original-A" {
				t.Fatal("speculative or lost original CLI base")
			}
		})
	}
	fake := &branchCLIClient{}
	if _, err := sessionFiles(context.Background(), client{sessions: fake}, []string{"diff", "--id", string(domain.NewID()), "--repository-id", string(domain.NewID())}); err != nil || len(fake.queries) != 1 || fake.queries[0].Comparison != domain.DiffWorkingTree || fake.queries[0].BaseRef != nil {
		t.Fatal("CLI default changed", err)
	}
	fake = &branchCLIClient{old: true}
	if _, err := sessionFiles(context.Background(), client{sessions: fake}, []string{"diff", "--id", string(domain.NewID()), "--repository-id", string(domain.NewID()), "--comparison", "branch"}); err == nil || len(fake.queries) != 1 || fake.queries[0].Operation != domain.WorkspaceGitDiffOptions {
		t.Fatal("old server received branch fields")
	}
}
