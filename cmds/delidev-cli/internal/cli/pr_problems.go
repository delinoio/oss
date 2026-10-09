package cli

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func prProblemEnvelope(r *pb.Resource) bool {
	return r != nil && r.Kind == pb.EntityKind_ENTITY_KIND_PROBLEM && r.SchemaVersion == 1 && domain.ID(r.Id).Validate() == nil && r.Revision > 0 && r.Revision < 1<<63 && r.SessionId == "" && r.ProjectId == ""
}
func prProblemResponseError() error {
	return domain.Fail(domain.RecoveryRequired, "The retained PR problem response is inconsistent.", "Inspect the original PR and exact content version before retrying.")
}
func prProblemsCommand(ctx context.Context, c client, o *options, args []string) (any, error) {
	if len(args) == 0 {
		return nil, usage()
	}
	action := args[0]
	f := flags("github pr problems " + action)
	var repository, number, remote, pr, page, id, version, collection string
	var limit uint
	var revision uint64
	switch action {
	case "refresh":
		f.StringVar(&repository, "repository-id", "", "configured repository UUID")
		f.StringVar(&number, "number", "", "exact PR number")
		f.StringVar(&collection, "kind", "feedback", "feedback, ci or conflict")
	case "list":
		f.StringVar(&remote, "remote-repository-id", "", "GitHub repository numeric ID")
		f.StringVar(&pr, "pull-request-id", "", "GitHub PR numeric ID, not its number or issue ID")
		f.StringVar(&page, "page-token", "", "original history cursor")
		f.UintVar(&limit, "limit", 20, "page size 1 through 50")
	case "dismiss":
		f.StringVar(&id, "id", "", "original problem UUID")
		f.Uint64Var(&revision, "revision", 0, "original problem revision")
		f.StringVar(&version, "content-version", "", "original feedback content version")
	default:
		return nil, usage()
	}
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	switch action {
	case "refresh":
		if domain.ID(repository).Validate() != nil || !domain.PositiveDecimal(number) {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid PR selection.", "Provide a configured repository UUID and canonical positive PR number.")
		}
		kind := pb.PullRequestProblemCollectionKind_PULL_REQUEST_PROBLEM_COLLECTION_KIND_FEEDBACK
		switch collection {
		case "feedback":
		case "ci":
			kind = pb.PullRequestProblemCollectionKind_PULL_REQUEST_PROBLEM_COLLECTION_KIND_CI
		case "conflict":
			kind = pb.PullRequestProblemCollectionKind_PULL_REQUEST_PROBLEM_COLLECTION_KIND_CONFLICT
		default:
			return nil, domain.Fail(domain.InvalidArgument, "Unknown PR collection kind.", "Use --kind feedback, ci or conflict.")
		}
		ensureRequest(o)
		r, err := c.integrations.RefreshPullRequestProblems(ctx, request(c, &pb.RefreshPullRequestProblemsRequest{RequestId: string(o.requestID), RepositoryId: repository, Number: number, Kind: kind}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		var value domain.PRProblemSet
		if !prProblemEnvelope(r.Msg.ProblemSet) || domain.Decode(r.Msg.ProblemSet.DocumentJson, &value) != nil || value.Validate() != nil || value.Target.Number != number || r.Msg.RequestId != string(o.requestID) {
			return nil, prProblemResponseError()
		}
		if (kind == pb.PullRequestProblemCollectionKind_PULL_REQUEST_PROBLEM_COLLECTION_KIND_FEEDBACK && value.Feedback == nil) || (kind == pb.PullRequestProblemCollectionKind_PULL_REQUEST_PROBLEM_COLLECTION_KIND_CI && value.CI == nil) || (kind == pb.PullRequestProblemCollectionKind_PULL_REQUEST_PROBLEM_COLLECTION_KIND_CONFLICT && value.Conflict == nil) {
			return nil, prProblemResponseError()
		}
		// Replay returns current shared history, whose latest local alias may differ.
		return map[string]any{"problem_set": resourceJSON(r.Msg.ProblemSet), "request_id": r.Msg.RequestId, "replayed": r.Msg.Replayed}, nil
	case "list":
		if !domain.PositiveDecimal(remote) || !domain.PositiveDecimal(pr) || limit < 1 || limit > 50 {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid retained history selection.", "Provide stable remote numeric IDs and a page size from 1 through 50.")
		}
		r, err := c.integrations.ListPullRequestProblems(ctx, request(c, &pb.ListPullRequestProblemsRequest{RemoteRepositoryId: remote, PullRequestId: pr, PageSize: uint32(limit), PageToken: page}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		var set domain.PRProblemSet
		if r.Msg.ProblemSet == nil {
			if len(r.Msg.Problems) != 0 || r.Msg.NextPageToken != "" {
				return nil, prProblemResponseError()
			}
		} else if !prProblemEnvelope(r.Msg.ProblemSet) || domain.Decode(r.Msg.ProblemSet.DocumentJson, &set) != nil || set.Validate() != nil || set.Target.RemoteRepositoryID != remote || set.Target.PullRequestID != pr {
			return nil, prProblemResponseError()
		}
		if len(r.Msg.Problems) > int(limit) || (r.Msg.NextPageToken != "" && len(r.Msg.Problems) == 0) {
			return nil, prProblemResponseError()
		}
		seen := map[string]bool{}
		for _, row := range r.Msg.Problems {
			var value domain.PRProblem
			if !prProblemEnvelope(row) || domain.Decode(row.DocumentJson, &value) != nil || value.Validate() != nil || value.SetID != domain.ID(r.Msg.ProblemSet.Id) || !set.Target.SamePR(value.Target) || seen[row.Id] {
				return nil, prProblemResponseError()
			}
			seen[row.Id] = true
		}
		result := map[string]any{"problems": resourcesJSON(r.Msg.Problems), "next_page_token": r.Msg.NextPageToken}
		if r.Msg.ProblemSet != nil {
			result["problem_set"] = resourceJSON(r.Msg.ProblemSet)
		}
		return result, nil
	case "dismiss":
		if domain.ID(id).Validate() != nil || revision == 0 || revision >= 1<<63 || len(version) != 64 {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid problem dismissal.", "Provide the original UUID, revision and content version.")
		}
		ensureRequest(o)
		r, err := c.integrations.DismissPullRequestProblem(ctx, request(c, &pb.DismissPullRequestProblemRequest{Mutation: &pb.Mutation{Id: id, ExpectedRevision: revision, RequestId: string(o.requestID)}, ContentVersion: version}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		var value domain.PRProblem
		if !prProblemEnvelope(r.Msg.Problem) || r.Msg.Problem.Id != id || domain.Decode(r.Msg.Problem.DocumentJson, &value) != nil || value.Validate() != nil || value.ContentVersion != version || value.State != domain.PRProblemDismissed || r.Msg.RequestId != string(o.requestID) {
			return nil, prProblemResponseError()
		}
		return map[string]any{"problem": resourceJSON(r.Msg.Problem), "request_id": r.Msg.RequestId, "replayed": r.Msg.Replayed}, nil
	}
	return nil, usage()
}
