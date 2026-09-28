package server

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	gh "github.com/delinoio/oss/cmds/delidev-cli/internal/integrations/github"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type githubRepositoryQueries interface {
	QueryRepository(context.Context, []byte, string, string, domain.RepositoryQuery) (gh.RepositoryQueryObservation, error)
}

func (s *Service) QueryRepositoryIntegration(ctx context.Context, req *connect.Request[pb.QueryRepositoryIntegrationRequest]) (*connect.Response[pb.QueryRepositoryIntegrationResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, err := integrationActor(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if req.Msg.SchemaVersion != 1 {
		return nil, rpc.Error(domain.Fail(domain.Unsupported, "Unsupported repository query schema.", "Use a compatible client and server."), correlation)
	}
	var query domain.RepositoryQuery
	if err := domain.Decode(req.Msg.QueryJson, &query); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if err := query.Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var observed gh.RepositoryQueryObservation
	selected, err := s.withRepositoryIntegration(ctx, domain.ID(req.Msg.RepositoryId), string(query.Operation), correlation, func(readCtx context.Context, token []byte, selected repositoryIntegrationSelection) error {
		client := s.githubQueries
		if client == nil {
			client = gh.New()
		}
		var err error
		observed, err = client.QueryRepository(readCtx, token, selected.repository.GitHubOwner, selected.repository.GitHubName, query)
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if !strings.EqualFold(observed.Repository.Owner, selected.repository.GitHubOwner) || !strings.EqualFold(observed.Repository.Name, selected.repository.GitHubName) {
		return nil, rpc.Error(domain.Fail(domain.RecoveryRequired, "The query returned another repository.", "Refresh the explicitly selected repository."), correlation)
	}

	value := domain.RepositoryQueryResult{RepositoryID: domain.ID(req.Msg.RepositoryId), RepositoryRevision: strconv.FormatUint(selected.record.Revision, 10), ProfileID: selected.repository.IntegrationID, GenerationID: selected.profile.Connection.GenerationID, ObservedAt: time.Now().UTC().Truncate(time.Millisecond), Identity: observed.Identity, Repository: observed.Repository, Query: query, Items: observed.Items, NextPage: observed.NextPage, TotalCount: observed.TotalCount, Incomplete: observed.Incomplete, SearchLimitReached: observed.SearchLimitReached, Diff: observed.Diff, Checks: observed.Checks, Statuses: observed.Statuses}
	if err := value.Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 1<<20 {
		return nil, rpc.Error(domain.Fail(domain.ResourceExhausted, "The repository query result exceeds its response limit.", "Read a smaller page or a single item."), correlation)
	}
	response := connect.NewResponse(&pb.QueryRepositoryIntegrationResponse{SchemaVersion: 1, DocumentJson: raw})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
