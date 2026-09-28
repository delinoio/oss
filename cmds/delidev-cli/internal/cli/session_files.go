package cli

import (
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func sessionFiles(ctx context.Context, c client, args []string) (any, error) {
	if len(args) == 0 {
		return nil, domain.Fail(domain.MissingInput, "Select a workspace file operation.", "Use session files roots, list or read.")
	}
	query := domain.WorkspaceReadQuery{}
	switch args[0] {
	case "roots":
		query.Operation = domain.WorkspaceRoots
	case "list":
		query.Operation = domain.WorkspaceDirectory
	case "read":
		query.Operation = domain.WorkspaceFile
	case "diff", "review-context":
		query.Operation = domain.WorkspaceGitDiff
	default:
		return nil, domain.Fail(domain.InvalidArgument, "Unknown workspace file operation.", "Use roots, list or read.")
	}
	f := flags("session files " + args[0])
	id := f.String("id", "", "session UUID")
	repo, path, page := new(string), new(string), new(string)
	comparison := new(string)
	if query.Operation != domain.WorkspaceRoots {
		repo = f.String("repository-id", "", "prepared repository UUID; omit for General Chat")
		path = f.String("path", ".", "relative slash path within the selected workspace")
	}
	if query.Operation == domain.WorkspaceDirectory {
		page = f.String("page-token", "", "next page from the same directory observation")
	}
	if query.Operation == domain.WorkspaceGitDiff {
		comparison = f.String("comparison", string(domain.DiffWorkingTree), "working-tree, staged, or Worktree creation comparison")
	}
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if err := domain.ID(*id).Validate(); err != nil {
		return nil, err
	}
	query.RepositoryID, query.Path, query.PageToken = domain.ID(*repo), *path, *page
	query.Comparison = domain.WorkspaceDiffComparison(*comparison)
	if err := query.Validate(); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(query)
	if args[0] == "review-context" {
		response, err := c.sessions.ReadSessionReviewContext(ctx, request(c, &pb.ReadSessionReviewContextRequest{SessionId: *id, QueryJson: raw}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		var result domain.ReviewContext
		if len(response.Msg.DocumentJson) > domain.MaxReviewContextBytes {
			return nil, domain.Fail(domain.ResourceExhausted, "The review context exceeds its limit.", "Select a narrower relative path.")
		}
		if err := domain.Decode(response.Msg.DocumentJson, &result); err != nil {
			return nil, err
		}
		if err := result.Diff.Validate(query); err != nil {
			return nil, err
		}
		// Derive coordinates from the validated patch rather than trusting a
		// second independently editable representation in the response.
		result.Files, err = result.Diff.ReviewFiles()
		return result, err
	}
	response, err := c.sessions.ReadSessionWorkspace(ctx, request(c, &pb.ReadSessionWorkspaceRequest{SessionId: *id, QueryJson: raw}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	var result domain.WorkspaceReadResult
	if err := domain.Decode(response.Msg.DocumentJson, &result); err != nil {
		return nil, err
	}
	if query.Operation != domain.WorkspaceRoots {
		if err := result.Validate(query); err != nil {
			return nil, err
		}
	}
	return result, nil
}
