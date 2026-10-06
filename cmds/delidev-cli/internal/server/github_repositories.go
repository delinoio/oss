// SPDX-License-Identifier: Apache-2.0
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

type githubRepositoryInventory interface {
	ListRepositories(context.Context, []byte, uint32, uint32) (gh.RepositoryListObservation, error)
}

func usableRepositoryProfile(tx *store.Tx, id domain.ID, revision uint64) (store.Record, domain.Integration, error) {
	if revision == 0 {
		return store.Record{}, domain.Integration{}, domain.Fail(domain.InvalidArgument, "Select a current GitHub profile revision.", "Refresh the profile inventory and choose one explicitly.")
	}
	record, profile, err := integrationFromTx(tx, id, revision)
	if err == nil && (profile.Connection == nil || profile.Pending != nil) {
		err = domain.Fail(domain.Conflict, "The selected GitHub profile has no usable token generation.", "Finish connecting or changing this profile before browsing repositories.")
	}
	return record, profile, err
}
func (s *Service) ListGitHubRepositories(ctx context.Context, req *connect.Request[pb.ListGitHubRepositoriesRequest]) (*connect.Response[pb.ListGitHubRepositoriesResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	result, err := s.readGitHubRepositories(ctx, domain.ID(req.Msg.ProfileId), req.Msg.ExpectedRevision, req.Msg.Page, req.Msg.PageSize, correlation)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > 1<<20 {
		return nil, rpc.Error(domain.Fail(domain.ResourceExhausted, "The GitHub repository page exceeds its response limit.", "Read a smaller page."), correlation)
	}
	response := connect.NewResponse(&pb.ListGitHubRepositoriesResponse{SchemaVersion: 1, DocumentJson: raw})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) readGitHubRepositories(ctx context.Context, id domain.ID, revision uint64, page, size uint32, correlation string) (domain.GitHubRepositoryPage, error) {
	empty := domain.GitHubRepositoryPage{}
	if _, err := integrationActor(ctx); err != nil {
		return empty, err
	}
	if err := id.Validate(); err != nil {
		return empty, err
	}
	if err := domain.ValidateGitHubRepositoryPageInput(page, size); err != nil {
		return empty, err
	}
	unlock, err := s.lockIntegrations(ctx)
	if err != nil {
		return empty, err
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	var record store.Record
	var profile domain.Integration
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		var e error
		record, profile, e = usableRepositoryProfile(tx, id, revision)
		return e
	})
	if err != nil {
		return empty, err
	}
	if s.integrationChecks[id] != nil {
		return empty, domain.Fail(domain.Conflict, "The selected profile already has an active GitHub read.", "Wait for its result before refreshing.")
	}
	if len(s.integrationChecks) >= 8 {
		return empty, domain.Fail(domain.ResourceExhausted, "The server's GitHub read limit is reached.", "Retry after an active read finishes.")
	}
	if s.integrationChecks == nil {
		s.integrationChecks = map[domain.ID]*integrationCheck{}
	}
	readCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	check := &integrationCheck{cancel: cancel, done: make(chan struct{})}
	s.integrationChecks[id] = check
	// Profile mutations cancel and join this exact read before credential retirement.
	// Close the joined signal before reacquiring the gate to avoid their deadlock.
	defer func() {
		cancel()
		close(check.done)
		if !locked {
			unlock, _ = s.lockIntegrations(context.Background())
			locked = true
		}
		if s.integrationChecks[id] == check {
			delete(s.integrationChecks, id)
		}
	}()
	vault, err := s.patSecrets()
	if err != nil {
		return empty, err
	}
	generation := profile.Connection.GenerationID
	token, err := vault.Get(readCtx, credentials.PATRef{ProfileID: id, GenerationID: generation})
	if err != nil {
		return empty, err
	}
	defer clear(token)
	unlock()
	locked = false
	s.logger.InfoContext(ctx, "github_repository_page_started", "profile_id", id, "page", page, "correlation_id", correlation)
	client := s.githubRepositories
	if client == nil {
		client = gh.New(s.outboundResolver())
	}
	observed, err := client.ListRepositories(readCtx, token, page, size)
	clear(token)
	if readCtx.Err() != nil {
		err = domain.SafeError(readCtx.Err())
	}
	if err != nil {
		s.logger.WarnContext(ctx, "github_repository_page_failed", "profile_id", id, "page", page, "code", domain.SafeError(err).Code, "correlation_id", correlation)
		return empty, err
	}
	// Validate the entire upstream page before owner filtering; malformed excluded
	// rows still invalidate a mixed response. The next page retains upstream order.
	result := domain.GitHubRepositoryPage{ProfileID: id, ProfileRevision: strconv.FormatUint(record.Revision, 10), GenerationID: generation, ObservedAt: time.Now().UTC().Truncate(time.Millisecond), Page: page, PageSize: size, NextPage: observed.NextPage, Repositories: observed.Repositories}
	if err := result.Validate(); err != nil {
		return empty, err
	}
	unlock, err = s.lockIntegrations(readCtx)
	if err != nil {
		return empty, err
	}
	locked = true
	err = s.Store.Read(readCtx, func(tx *store.Tx) error {
		_, current, e := usableRepositoryProfile(tx, id, revision)
		if e != nil {
			return e
		}
		if current.Connection.GenerationID != generation {
			return domain.Fail(domain.Conflict, "The selected GitHub credential generation changed.", "Choose the current profile and refresh its page explicitly.")
		}
		return nil
	})
	if err != nil {
		return empty, err
	}
	filtered := make([]domain.GitHubRepositoryListEntry, 0, len(result.Repositories))
	for _, entry := range result.Repositories {
		if profile.AllowsGitHubOwner(entry.Repository.Owner) {
			filtered = append(filtered, entry)
		}
	}
	result.Repositories = filtered
	s.logger.InfoContext(ctx, "github_repository_page_finished", "profile_id", id, "page", page, "count", len(filtered), "correlation_id", correlation)
	return result, nil
}
