package cli

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func prRemediationCommand(ctx context.Context, c client, o *options, args []string) (any, error) {
	if len(args) == 0 {
		return nil, usage()
	}
	action := args[0]
	f := flags("github pr remediation " + action)
	var remote, pr, page, id string
	var limit uint
	var revision uint64
	switch action {
	case "list":
		f.StringVar(&remote, "remote-repository-id", "", "original GitHub repository numeric identity")
		f.StringVar(&pr, "pull-request-id", "", "original GitHub PR numeric identity")
		f.StringVar(&page, "page-token", "", "original remediation history cursor")
		f.UintVar(&limit, "limit", 20, "page size 1 through 50")
	case "resume":
		f.StringVar(&id, "id", "", "retained PR problem set UUID")
		f.Uint64Var(&revision, "revision", 0, "original PR set revision")
	default:
		return nil, usage()
	}
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if action == "resume" {
		if domain.ID(id).Validate() != nil || revision == 0 || revision >= 1<<63 {
			return nil, domain.Fail(domain.MissingInput, "The original PR set and revision are required.", "Inspect remediation history and supply --id and --revision.")
		}
		ensureRequest(o)
		r, err := c.integrations.ResumePullRequestRemediation(ctx, request(c, &pb.ResumePullRequestRemediationRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: id, ExpectedRevision: revision}}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		var set domain.PRProblemSet
		if !prProblemEnvelope(r.Msg.ProblemSet) || r.Msg.ProblemSet.Id != id || r.Msg.RequestId != string(o.requestID) || domain.Decode(r.Msg.ProblemSet.DocumentJson, &set) != nil || set.Validate() != nil || set.Remediation == nil {
			return nil, prProblemResponseError()
		}
		return map[string]any{"problem_set": resourceJSON(r.Msg.ProblemSet), "request_id": r.Msg.RequestId, "replayed": r.Msg.Replayed}, nil
	}
	if !domain.PositiveDecimal(remote) || !domain.PositiveDecimal(pr) || limit < 1 || limit > 50 {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid remediation history selection.", "Provide exact remote numeric identities and a page size from 1 through 50.")
	}
	r, err := c.integrations.ListPullRequestRemediationAttempts(ctx, request(c, &pb.ListPullRequestRemediationAttemptsRequest{RemoteRepositoryId: remote, PullRequestId: pr, PageToken: page, PageSize: uint32(limit)}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	var set domain.PRProblemSet
	if r.Msg.ProblemSet == nil {
		if len(r.Msg.Attempts) != 0 || r.Msg.NextPageToken != "" {
			return nil, prProblemResponseError()
		}
	} else if !prProblemEnvelope(r.Msg.ProblemSet) || domain.Decode(r.Msg.ProblemSet.DocumentJson, &set) != nil || set.Validate() != nil || set.Target.RemoteRepositoryID != remote || set.Target.PullRequestID != pr {
		return nil, prProblemResponseError()
	}
	if len(r.Msg.Attempts) > int(limit) || r.Msg.NextPageToken != "" && len(r.Msg.Attempts) == 0 {
		return nil, prProblemResponseError()
	}
	values := []any{}
	previous := uint32(domain.MaxPRRemediationAttempts + 1)
	seen := map[string]bool{}
	for _, row := range r.Msg.Attempts {
		var attempt domain.PRRemediationAttempt
		if !prProblemEnvelope(row) || domain.Decode(row.DocumentJson, &attempt) != nil || attempt.Validate() != nil || attempt.SetID != domain.ID(r.Msg.ProblemSet.Id) || set.Remediation == nil || attempt.ChainID != set.Remediation.ID || attempt.Sequence > set.Remediation.Sequence || attempt.Sequence >= previous || seen[row.Id] || attempt.State.Active() != (set.Remediation.ActiveAttemptID == domain.ID(row.Id)) {
			return nil, prProblemResponseError()
		}
		seen[row.Id], previous = true, attempt.Sequence
		values = append(values, resourceJSON(row))
	}
	value := map[string]any{"attempts": values, "next_page_token": r.Msg.NextPageToken}
	if r.Msg.ProblemSet != nil {
		value["problem_set"] = resourceJSON(r.Msg.ProblemSet)
	}
	return value, nil
}
