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
	baseRef := new(string)
	if query.Operation != domain.WorkspaceRoots {
		repo = f.String("repository-id", "", "prepared repository UUID; omit for General Chat")
		path = f.String("path", ".", "relative slash path within the selected workspace")
	}
	if query.Operation == domain.WorkspaceDirectory {
		page = f.String("page-token", "", "next page from the same directory observation")
	}
	if query.Operation == domain.WorkspaceGitDiff {
		comparison = f.String("comparison", string(domain.DiffWorkingTree), "working-tree, staged, creation, or branch comparison")
		baseRef = f.String("base-ref", "", "closed Reference JSON for an explicit branch base")
	}
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if err := domain.ID(*id).Validate(); err != nil {
		return nil, err
	}
	query.RepositoryID, query.Path, query.PageToken = domain.ID(*repo), *path, *page
	query.Comparison = domain.WorkspaceDiffComparison(*comparison)
	if query.Comparison == domain.DiffBranch {
		optionsQuery := domain.WorkspaceReadQuery{Operation: domain.WorkspaceGitDiffOptions, RepositoryID: query.RepositoryID, Path: query.Path}
		if err := optionsQuery.Validate(); err != nil {
			return nil, err
		}
		optionsRaw, _ := json.Marshal(optionsQuery)
		response, err := c.sessions.ReadSessionWorkspace(ctx, request(c, &pb.ReadSessionWorkspaceRequest{SessionId: *id, QueryJson: optionsRaw}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		var options domain.WorkspaceReadResult
		if len(response.Msg.DocumentJson) > 256<<10 || domain.Decode(response.Msg.DocumentJson, &options) != nil || options.Validate(optionsQuery) != nil {
			return nil, domain.Fail(domain.Unavailable, "The comparison options are invalid.", "Refresh the original repository options.")
		}
		if *baseRef != "" {
			var selected domain.Reference
			if domain.Decode([]byte(*baseRef), &selected) != nil || selected.Validate(false) != nil {
				return nil, domain.Fail(domain.InvalidArgument, "Invalid branch base reference.", "Use closed Reference JSON.")
			}
			query.BaseRef = &selected
		} else if options.DiffOptions.DefaultAvailable {
			query.BaseRef = options.DiffOptions.Default
		} else {
			return nil, domain.Fail(domain.MissingInput, "A branch base must be selected.", "Use an explicit locally available --base-ref; no fallback was performed.")
		}
	} else if *baseRef != "" {
		return nil, domain.Fail(domain.InvalidArgument, "Base references apply only to branch comparisons.", "Select --comparison branch.")
	}
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
