// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"slices"
)

func prFixCommand(ctx context.Context, c client, o options, args []string, streams IO) (any, error) {
	f := flags("github pr remediation fix")
	path := f.String("input", "-", "version-1 JSON selection file or stdin")
	if err := parse(f, args); err != nil {
		return nil, err
	}
	if *path == "-" && o.tokenStdin {
		return nil, domain.Fail(domain.InvalidArgument, "Server authentication and a fix document cannot share stdin.", "Use an input file or the saved authenticated client.")
	}
	raw, err := readDocument(*path, streams.In)
	if err != nil {
		return nil, err
	}
	var input domain.PRFixRequest
	if domain.Decode(raw, &input) != nil || input.Validate() != nil {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid manual fix selection.", "Supply exact set/problem revisions and explicit project/repository IDs.")
	}
	ensureRequest(&o)
	r, err := c.prFixes.RequestPullRequestFix(ctx, request(c, &pb.RequestPullRequestFixRequest{RequestId: string(o.requestID), SchemaVersion: 1, DocumentJson: raw}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	var attempt domain.PRRemediationAttempt
	var session domain.Session
	var set domain.PRProblemSet
	if r.Msg.RequestId != string(o.requestID) || !prProblemEnvelope(r.Msg.Attempt) || domain.Decode(r.Msg.Attempt.DocumentJson, &attempt) != nil || attempt.Validate() != nil || attempt.SetID != input.SetID || attempt.GitTarget == nil || attempt.ProjectID != input.ProjectID || r.Msg.Session == nil || r.Msg.Session.Kind != pb.EntityKind_ENTITY_KIND_SESSION || r.Msg.Session.Id != string(attempt.SessionID) || r.Msg.Session.SessionId != r.Msg.Session.Id || r.Msg.Session.ProjectId != string(input.ProjectID) || r.Msg.Session.SchemaVersion != 1 || r.Msg.Session.Revision == 0 || domain.Decode(r.Msg.Session.DocumentJson, &session) != nil || session.ProjectID != input.ProjectID {
		return nil, prProblemResponseError()
	}
	if attempt.Reserved.RequestID != o.requestID || !prProblemEnvelope(r.Msg.ProblemSet) || r.Msg.ProblemSet.Id != string(input.SetID) || domain.Decode(r.Msg.ProblemSet.DocumentJson, &set) != nil || set.Validate() != nil || set.Remediation == nil || set.Remediation.ID != attempt.ChainID || attempt.State.Active() != (set.Remediation.ActiveAttemptID == domain.ID(r.Msg.Attempt.Id)) || !set.Target.SamePR(attempt.GitTarget.Target) || set.Target.RepositoryNodeID != attempt.GitTarget.Target.RepositoryNodeID || set.Target.PullRequestNodeID != attempt.GitTarget.Target.PullRequestNodeID {
		return nil, prProblemResponseError()
	}
	if attempt.GitTarget.Target.RepositoryID != input.RepositoryID || len(attempt.Problems) != len(input.Problems) {
		return nil, prProblemResponseError()
	}
	expected := make([]domain.PRRemediationProblemRef, 0, len(input.Problems))
	for _, ref := range input.Problems {
		expected = append(expected, domain.PRRemediationProblemRef{ID: ref.ID, ContentVersion: ref.ContentVersion})
	}
	if !slices.Equal(expected, attempt.Problems) {
		return nil, prProblemResponseError()
	}
	return map[string]any{"attempt": resourceJSON(r.Msg.Attempt), "session": resourceJSON(r.Msg.Session), "request_id": r.Msg.RequestId, "replayed": r.Msg.Replayed}, nil
}

func prFixCapabilities(ctx context.Context, c client) (any, error) {
	r, err := c.prFixes.GetPullRequestFixCapabilities(ctx, request(c, &pb.GetPullRequestFixCapabilitiesRequest{}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	if !slices.Contains(r.Msg.Profiles, pb.PullRequestFixProfile_PULL_REQUEST_FIX_PROFILE_CODEX_GIT_V1) {
		return nil, prProblemResponseError()
	}
	return map[string]any{"profiles": []string{"codex-git-v1"}}, nil
}
