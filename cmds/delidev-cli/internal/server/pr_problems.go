package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type prProblemReceipt struct {
	SetID     domain.ID `json:"set_id"`
	ProblemID domain.ID `json:"problem_id,omitempty"`
}

func prObservationConflict() error {
	return domain.Fail(domain.Conflict, "The selected repository, profile or PR inventory changed.", "Refresh the original selection before collecting feedback again.")
}

func (s *Service) readProblemObservation(ctx context.Context, repository domain.ID, number string, operation domain.RepositoryQueryOperation, correlation string) (domain.RepositoryQueryResult, error) {
	query := domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: operation, Number: number}
	raw, _ := json.Marshal(query)
	request := connect.NewRequest(&pb.QueryRepositoryIntegrationRequest{RepositoryId: string(repository), SchemaVersion: 1, QueryJson: raw})
	request.Header().Set(rpc.CorrelationHeader, correlation)
	response, err := s.QueryRepositoryIntegration(ctx, request)
	var value domain.RepositoryQueryResult
	if err != nil {
		return value, rpc.ClientError(err)
	}
	if response.Msg.SchemaVersion != 1 || domain.Decode(response.Msg.DocumentJson, &value) != nil || value.Validate() != nil || value.RepositoryID != repository || value.Query != query {
		return value, prObservationConflict()
	}
	return value, nil
}

func (s *Service) RefreshPullRequestProblems(ctx context.Context, req *connect.Request[pb.RefreshPullRequestProblemsRequest]) (*connect.Response[pb.RefreshPullRequestProblemsResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	fail := func(err error) (*connect.Response[pb.RefreshPullRequestProblemsResponse], error) {
		return nil, rpc.Error(err, correlation)
	}
	actor, err := integrationActor(ctx)
	if err != nil {
		return fail(err)
	}
	repository, requestID := domain.ID(req.Msg.RepositoryId), domain.ID(req.Msg.RequestId)
	if repository.Validate() != nil || requestID.Validate() != nil || !domain.PositiveDecimal(req.Msg.Number) {
		return fail(domain.Fail(domain.InvalidArgument, "Invalid PR collection request.", "Provide a repository UUID, request UUID and exact positive PR number."))
	}
	identity := struct {
		Repository domain.ID
		Number     string
		Actor      domain.Principal
	}{repository, req.Msg.Number, actor}
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	result, replayed, err := s.Store.Replay(ctx, requestID, "pr.problems.refresh", identity)
	if err != nil {
		return fail(err)
	}
	if !replayed {
		detail, err := s.readProblemObservation(ctx, repository, req.Msg.Number, domain.RepositoryDetail, correlation)
		if err != nil {
			return fail(err)
		}
		var epoch uint64
		err = s.Store.Read(ctx, func(tx *store.Tx) error {
			row, _, err := tx.FindPRProblemSet(detail.Repository.Provider, detail.Repository.ID, detail.Items[0].ID)
			if domain.SafeError(err).Code == domain.NotFound {
				return nil
			}
			if err == nil {
				epoch = row.Revision
			}
			return err
		})
		if err != nil {
			return fail(err)
		}
		observed, err := s.readProblemObservation(ctx, repository, req.Msg.Number, domain.RepositoryFeedback, correlation)
		if err != nil {
			return fail(err)
		}
		if detail.RepositoryRevision != observed.RepositoryRevision || detail.ProfileID != observed.ProfileID || detail.GenerationID != observed.GenerationID || detail.Repository.ID != observed.Repository.ID || detail.Repository.NodeID != observed.Repository.NodeID || detail.Items[0].ID != observed.Items[0].ID || detail.Items[0].NodeID != observed.Items[0].NodeID {
			return fail(prObservationConflict())
		}
		result, err = s.Store.Mutate(ctx, requestID, "pr.problems.refresh", identity, func(tx *store.Tx) (any, error) {
			selected, err := repositoryIntegrationFromTx(tx, repository)
			if err != nil {
				return nil, err
			}
			if strconv.FormatUint(selected.record.Revision, 10) != observed.RepositoryRevision || selected.repository.IntegrationID != observed.ProfileID || selected.profile.Connection.GenerationID != observed.GenerationID {
				return nil, prObservationConflict()
			}
			row, _, err := tx.ObservePRFeedback(epoch, observed)
			if err != nil {
				return nil, err
			}
			return prProblemReceipt{SetID: row.ID}, nil
		})
		if err != nil {
			return fail(err)
		}
	}
	var refs prProblemReceipt
	if domain.Decode(result.Data, &refs) != nil || refs.SetID.Validate() != nil || refs.ProblemID != "" {
		return fail(domain.Fail(domain.NotFound, "The original PR collection is no longer retained.", "Inspect current history; an old receipt cannot recreate it."))
	}
	var row store.Record
	err = s.Store.Read(ctx, func(tx *store.Tx) error { var err error; row, _, err = tx.GetPRProblemSet(refs.SetID); return err })
	if err != nil {
		return fail(err)
	}
	s.logger.InfoContext(ctx, "pr_problems_collected", "repository_id", repository, "set_id", row.ID, "replayed", result.Replayed, "correlation_id", correlation)
	response := connect.NewResponse(&pb.RefreshPullRequestProblemsResponse{ProblemSet: rpc.Resource(row), RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) ListPullRequestProblems(ctx context.Context, req *connect.Request[pb.ListPullRequestProblemsRequest]) (*connect.Response[pb.ListPullRequestProblemsResponse], error) {
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
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Invalid PR history request.", "Use stable remote numeric IDs and a page size from 1 through 50."), correlation)
	}
	scope := fmt.Sprintf("pr-problems:%s:%d", key, limit)
	var after domain.ID
	var epoch uint64
	if req.Msg.PageToken != "" {
		cursor, err := s.Identity.DecodeCursor(req.Msg.PageToken, scope)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		if cursor.After.Validate() != nil || cursor.Sequence == 0 {
			return nil, rpc.Error(domain.Fail(domain.CursorExpired, "The PR history cursor is incomplete.", "Restart history pagination."), correlation)
		}
		after, epoch = cursor.After, cursor.Sequence
	}
	response := connect.NewResponse(&pb.ListPullRequestProblemsResponse{})
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		set, _, err := tx.FindPRProblemSet(domain.GitHubCom, req.Msg.RemoteRepositoryId, req.Msg.PullRequestId)
		if domain.SafeError(err).Code == domain.NotFound && epoch == 0 {
			return nil
		}
		if err != nil {
			return err
		}
		if epoch != 0 && epoch != set.Revision {
			return domain.Fail(domain.CursorExpired, "The PR inventory or local handling changed during pagination.", "Restart history pagination.")
		}
		rows, more, err := tx.ListPRProblems(set.ID, after, limit)
		if err != nil {
			return err
		}
		response.Msg.ProblemSet = rpc.Resource(set)
		for _, row := range rows {
			response.Msg.Problems = append(response.Msg.Problems, rpc.Resource(row))
		}
		if more && len(rows) > 0 {
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

func (s *Service) DismissPullRequestProblem(ctx context.Context, req *connect.Request[pb.DismissPullRequestProblemRequest]) (*connect.Response[pb.DismissPullRequestProblemResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := integrationActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if validateSessionMutation(req.Msg.Mutation) != nil || len(req.Msg.ContentVersion) != 64 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Invalid problem dismissal.", "Provide the original problem revision and content version."), correlation)
	}
	meta := req.Msg.Mutation
	identity := struct {
		ID             domain.ID
		Revision       uint64
		ContentVersion string
		Actor          domain.Principal
	}{domain.ID(meta.Id), meta.ExpectedRevision, req.Msg.ContentVersion, actor}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "pr.problem.dismiss", identity, func(tx *store.Tx) (any, error) {
		row, err := tx.DismissPRProblem(identity.ID, identity.Revision, identity.ContentVersion)
		if err != nil {
			return nil, err
		}
		value, err := store.Decode[domain.PRProblem](row)
		if err != nil {
			return nil, err
		}
		return prProblemReceipt{SetID: value.SetID, ProblemID: row.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var refs prProblemReceipt
	if domain.Decode(result.Data, &refs) != nil || refs.SetID.Validate() != nil || refs.ProblemID != identity.ID {
		return nil, rpc.Error(domain.Fail(domain.NotFound, "The original dismissal is no longer retained.", "Inspect the original problem history."), correlation)
	}
	var row store.Record
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		var value domain.PRProblem
		var err error
		row, value, err = tx.GetPRProblem(refs.ProblemID)
		if err == nil && value.SetID != refs.SetID {
			return prObservationConflict()
		}
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "pr_problem_dismissed", "set_id", refs.SetID, "problem_id", row.ID, "replayed", result.Replayed, "correlation_id", correlation)
	response := connect.NewResponse(&pb.DismissPullRequestProblemResponse{Problem: rpc.Resource(row), RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
