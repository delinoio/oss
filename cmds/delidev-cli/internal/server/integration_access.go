package server

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	gh "github.com/delinoio/oss/cmds/delidev-cli/internal/integrations/github"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type githubRepositoryAccess interface {
	InspectRepository(context.Context, []byte, string, string) (gh.RepositoryAccessObservation, error)
}
type repositoryIntegrationSelection struct {
	record     store.Record
	repository domain.Repository
	profile    domain.Integration
}

func repositoryIntegrationFromTx(tx *store.Tx, id domain.ID) (repositoryIntegrationSelection, error) {
	if err := tx.Authorize(); err != nil {
		return repositoryIntegrationSelection{}, err
	}
	record, err := tx.Get(domain.RepositoryKind, id)
	if err != nil {
		return repositoryIntegrationSelection{}, err
	}
	repository, err := store.Decode[domain.Repository](record)
	if err != nil {
		return repositoryIntegrationSelection{}, err
	}
	if err = domain.ValidateGitHubRepository(repository.GitHubOwner, repository.GitHubName); err != nil {
		return repositoryIntegrationSelection{}, err
	}
	if repository.IntegrationID == "" {
		return repositoryIntegrationSelection{}, domain.Fail(domain.MissingInput, "The repository has no selected GitHub profile.", "Choose its profile explicitly in repository settings; no other token or system credential is used.")
	}
	_, profile, err := integrationFromTx(tx, repository.IntegrationID, 0)
	if err != nil {
		return repositoryIntegrationSelection{}, err
	}
	if !profile.AllowsGitHubOwner(repository.GitHubOwner) {
		return repositoryIntegrationSelection{}, domain.Fail(domain.PermissionDenied, "The selected profile targets another resource owner.", "Select a separate profile for this repository's owner.")
	}
	if profile.Connection == nil || profile.Pending != nil {
		return repositoryIntegrationSelection{}, domain.Fail(domain.Conflict, "The selected GitHub profile has no usable token generation.", "Complete its connection or pending cleanup before inspecting repository access.")
	}
	return repositoryIntegrationSelection{record: record, repository: repository, profile: profile}, nil
}
func (s *Service) InspectRepositoryIntegration(ctx context.Context, req *connect.Request[pb.InspectRepositoryIntegrationRequest]) (*connect.Response[pb.InspectRepositoryIntegrationResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, err := integrationActor(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	id := domain.ID(req.Msg.RepositoryId)
	if err := id.Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	unlock, err := s.lockIntegrations(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	var selected repositoryIntegrationSelection
	err = s.Store.Read(ctx, func(tx *store.Tx) error { var e error; selected, e = repositoryIntegrationFromTx(tx, id); return e })
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	profileID := selected.repository.IntegrationID
	if s.integrationChecks[profileID] != nil {
		return nil, rpc.Error(domain.Fail(domain.Conflict, "The selected profile already has an active GitHub inspection.", "Wait for its result before starting another inspection."), correlation)
	}
	if len(s.integrationChecks) >= 8 {
		return nil, rpc.Error(domain.Fail(domain.ResourceExhausted, "The server's GitHub inspection limit is reached.", "Retry after an active inspection completes."), correlation)
	}
	if s.integrationChecks == nil {
		s.integrationChecks = map[domain.ID]*integrationCheck{}
	}
	checkCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	check := &integrationCheck{cancel: cancel, done: make(chan struct{})}
	s.integrationChecks[profileID] = check
	defer func() {
		cancel()
		close(check.done)
		if !locked {
			unlock, _ = s.lockIntegrations(context.Background())
			locked = true
		}
		if s.integrationChecks[profileID] == check {
			delete(s.integrationChecks, profileID)
		}
	}()
	vault, err := s.patSecrets()
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	generation := selected.profile.Connection.GenerationID
	token, err := vault.Get(checkCtx, credentials.PATRef{ProfileID: profileID, GenerationID: generation})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	defer clear(token)
	if s.githubAccess == nil {
		s.githubAccess = gh.New()
	}
	client := s.githubAccess
	unlock()
	locked = false
	s.logger.Info("repository_integration_inspection_started", "repository_id", id, "profile_id", profileID, "correlation_id", correlation)
	observed, err := client.InspectRepository(checkCtx, token, selected.repository.GitHubOwner, selected.repository.GitHubName)
	clear(token)
	if checkCtx.Err() != nil {
		err = domain.SafeError(checkCtx.Err())
	}
	if err != nil {
		s.logger.Warn("repository_integration_inspection_failed", "repository_id", id, "profile_id", profileID, "error_code", domain.SafeError(err).Code, "correlation_id", correlation)
		return nil, rpc.Error(err, correlation)
	}
	unlock, err = s.lockIntegrations(checkCtx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	locked = true
	err = s.Store.Read(checkCtx, func(tx *store.Tx) error {
		current, e := repositoryIntegrationFromTx(tx, id)
		if e != nil {
			return e
		}
		if current.record.Revision != selected.record.Revision || current.repository.IntegrationID != profileID || current.profile.Connection.GenerationID != generation {
			return domain.Fail(domain.Conflict, "The inspected repository or token generation changed.", "Inspect the current repository selection explicitly.")
		}
		return nil
	})
	if err != nil {
		s.logger.Warn("repository_integration_inspection_rejected", "repository_id", id, "profile_id", profileID, "error_code", domain.SafeError(err).Code, "correlation_id", correlation)
		return nil, rpc.Error(err, correlation)
	}
	value := domain.RepositoryIntegrationAccess{RepositoryID: id, RepositoryRevision: strconv.FormatUint(selected.record.Revision, 10), ProfileID: profileID, GenerationID: generation, ObservedAt: time.Now().UTC().Truncate(time.Millisecond), Identity: observed.Identity, Repository: observed.Repository, Features: observed.Features}
	for i := range value.Features {
		if value.Features[i].Problem != nil {
			problem := *value.Features[i].Problem
			problem.CorrelationID = correlation
			value.Features[i].Problem = &problem
		}
	}
	if err := value.Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, rpc.Error(domain.SafeError(err), correlation)
	}
	s.logger.Info("repository_integration_inspection_finished", "repository_id", id, "profile_id", profileID, "feature_count", len(value.Features), "correlation_id", correlation)
	response := connect.NewResponse(&pb.InspectRepositoryIntegrationResponse{SchemaVersion: 1, DocumentJson: raw})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
