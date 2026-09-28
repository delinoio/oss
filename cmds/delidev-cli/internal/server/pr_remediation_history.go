package server

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func (s *Service) ListPullRequestRemediationAttempts(ctx context.Context, req *connect.Request[pb.ListPullRequestRemediationAttemptsRequest]) (*connect.Response[pb.ListPullRequestRemediationAttemptsResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, err := integrationActor(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	key := domain.PRProblemKey(domain.GitHubCom, req.Msg.RemoteRepositoryId, req.Msg.PullRequestId)
	limit := int(req.Msg.PageSize)
	if limit == 0 {
		limit = 20
	}
	if key == "" || limit < 1 || limit > 50 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Invalid remediation history request.", "Use exact remote repository/PR numeric identities and a page size from 1 through 50."), correlation)
	}
	response := connect.NewResponse(&pb.ListPullRequestRemediationAttemptsResponse{})
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		set, _, err := tx.FindPRProblemSet(domain.GitHubCom, req.Msg.RemoteRepositoryId, req.Msg.PullRequestId)
		if domain.SafeError(err).Code == domain.NotFound && req.Msg.PageToken == "" {
			return nil
		}
		if err != nil {
			return err
		}
		fingerprint, err := tx.PRRemediationHistoryFingerprint(set.ID)
		if err != nil {
			return err
		}
		scope := fmt.Sprintf("pr-remediation:%s:%d:%s", key, limit, fingerprint)
		var after domain.ID
		if req.Msg.PageToken != "" {
			cursor, err := s.Identity.DecodeCursor(req.Msg.PageToken, scope)
			if err != nil {
				return err
			}
			if cursor.After.Validate() != nil || cursor.Sequence != set.Revision {
				return domain.Fail(domain.CursorExpired, "The original remediation history changed.", "Restart pagination from the first page.")
			}
			after = cursor.After
		}
		rows, more, err := tx.ListPRRemediationAttempts(set.ID, after, limit)
		if err != nil {
			return err
		}
		response.Msg.ProblemSet = rpc.Resource(set)
		for _, row := range rows {
			response.Msg.Attempts = append(response.Msg.Attempts, rpc.Resource(row))
		}
		if more {
			response.Msg.NextPageToken, err = s.Identity.EncodeCursor(security.Cursor{Scope: scope, After: rows[len(rows)-1].ID, Sequence: set.Revision})
		}
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) ResumePullRequestRemediation(ctx context.Context, req *connect.Request[pb.ResumePullRequestRemediationRequest]) (*connect.Response[pb.ResumePullRequestRemediationResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := integrationActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if err := validateSessionMutation(req.Msg.Mutation); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	meta := req.Msg.Mutation
	identity := struct {
		SetID    domain.ID
		Revision uint64
		Actor    domain.Principal
	}{domain.ID(meta.Id), meta.ExpectedRevision, actor}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "pr.remediation.resume", identity, func(tx *store.Tx) (any, error) {
		row, err := tx.ResumePRRemediation(identity.SetID, identity.Revision)
		if err != nil {
			return nil, err
		}
		return prProblemReceipt{SetID: row.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var receipt prProblemReceipt
	if err := domain.Decode(result.Data, &receipt); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var row store.Record
	err = s.Store.Read(ctx, func(tx *store.Tx) error { var err error; row, _, err = tx.GetPRProblemSet(receipt.SetID); return err })
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "pr_remediation_allowance_resumed", "set_id", row.ID, "request_id", meta.RequestId, "replayed", result.Replayed, "correlation_id", correlation)
	response := connect.NewResponse(&pb.ResumePullRequestRemediationResponse{ProblemSet: rpc.Resource(row), RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
