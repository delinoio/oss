package cli

import (
	"context"
	"encoding/json"
	"flag"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// declareGitDiffComparisonFlags registers the comparison selection shared by
// the diff and review-context operations. The command deadline selection parses
// the same declarations so it cannot drift from the command's real syntax.
func declareGitDiffComparisonFlags(f *flag.FlagSet) (comparison, baseType, baseName, baseRemote *string) {
	comparison = f.String("comparison", string(domain.DiffWorkingTree), "branch, working-tree, staged, or Worktree creation comparison")
	baseType = f.String("base-type", "", "branch base: local-branch, remote-branch, or commit")
	baseName = f.String("base-name", "", "exact locally available branch or commit")
	baseRemote = f.String("base-remote", "", "remote for a remote-branch base")
	return comparison, baseType, baseName, baseRemote
}

// branchComparisonRequested reports whether the arguments after "session"
// select an explicit branch comparison for a diff or review-context command,
// including the "session files diff|review-context" aliases. It accepts both
// "--comparison branch" and "--comparison=branch" by using the command's own
// flag parser. Unparseable arguments are not branch comparisons: the command
// itself rejects them before any request is sent.
func branchComparisonRequested(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "diff", "review-context":
		args = args[1:]
	case "files":
		if len(args) < 2 || args[1] != "diff" && args[1] != "review-context" {
			return false
		}
		args = args[2:]
	default:
		return false
	}
	f := flags("session files diff")
	f.String("id", "", "")
	f.String("repository-id", "", "")
	f.String("path", ".", "")
	comparison, _, _, _ := declareGitDiffComparisonFlags(f)
	if err := f.Parse(args); err != nil || f.NArg() != 0 {
		return false
	}
	return domain.WorkspaceDiffComparison(*comparison) == domain.DiffBranch
}

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
	baseType, baseName, baseRemote := new(string), new(string), new(string)
	if query.Operation != domain.WorkspaceRoots {
		repo = f.String("repository-id", "", "prepared repository UUID; omit for General Chat")
		path = f.String("path", ".", "relative slash path within the selected workspace")
	}
	if query.Operation == domain.WorkspaceDirectory {
		page = f.String("page-token", "", "next page from the same directory observation")
	}
	if query.Operation == domain.WorkspaceGitDiff {
		comparison, baseType, baseName, baseRemote = declareGitDiffComparisonFlags(f)
	}
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if err := domain.ID(*id).Validate(); err != nil {
		return nil, err
	}
	query.RepositoryID, query.Path, query.PageToken = domain.ID(*repo), *path, *page
	query.Comparison = domain.WorkspaceDiffComparison(*comparison)
	query.BaseRef = domain.Reference{Type: domain.ReferenceType(*baseType), Name: *baseName, Remote: *baseRemote}
	if err := query.Validate(); err != nil {
		return nil, err
	}
	if query.Comparison == domain.DiffBranch {
		optionsQuery := domain.WorkspaceReadQuery{Operation: domain.WorkspaceGitDiffOptions, RepositoryID: query.RepositoryID, Path: "."}
		optsRaw, _ := json.Marshal(optionsQuery)
		response, err := c.sessions.ReadSessionWorkspace(ctx, request(c, &pb.ReadSessionWorkspaceRequest{SessionId: *id, QueryJson: optsRaw}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		var options domain.WorkspaceReadResult
		if err := domain.Decode(response.Msg.DocumentJson, &options); err != nil {
			return nil, err
		}
		if err := options.Validate(optionsQuery); err != nil {
			return nil, err
		}
		if !branchBaseAvailable(options.DiffOptions.Choices, query.BaseRef) {
			return nil, domain.Fail(domain.Unavailable, "The requested branch base is unavailable locally.", "Choose an available base after updating the original server and Worker.")
		}
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

// branchBaseAvailable decides whether the CLI may send the final branch read.
// The options inventory enumerates local and remote-tracking branches plus one
// configured reference. It is not an inventory of commit objects, so a named
// branch must match an available choice exactly, while a commit that the
// inventory does not mention is left to the original Worker. The Worker
// resolves the full lowercase object ID locally without fetching and rejects
// absent objects, non-commits, unrelated history and ambiguous merge-bases.
// A commit the inventory does list (the configured Base) keeps its labelled
// availability, so a known-missing configured commit never reaches the Worker.
func branchBaseAvailable(choices []domain.WorkspaceDiffChoice, base domain.Reference) bool {
	listed := false
	for _, choice := range choices {
		if choice.Reference != base {
			continue
		}
		if choice.Available {
			return true
		}
		listed = true
	}
	return base.Type == domain.CommitReference && !listed
}
