// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	gh "github.com/delinoio/oss/cmds/delidev-cli/internal/integrations/github"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type prAvatarEntry struct {
	actor               domain.Principal
	repository          domain.ID
	revision            uint64
	profile, generation domain.ID
	actorID, locator    string
	created             time.Time
	png                 []byte
}

func (s *Service) prScope(id domain.ID, selected repositoryIntegrationSelection, remote domain.RemoteRepository) domain.PRReadScope {
	return domain.PRReadScope{RepositoryID: id, RepositoryRevision: strconv.FormatUint(selected.record.Revision, 10), ProfileID: selected.repository.IntegrationID, GenerationID: selected.profile.Connection.GenerationID, ObservedAt: time.Now().UTC().Truncate(time.Millisecond), Repository: remote}
}
func prRevision(selected repositoryIntegrationSelection, revision uint64) error {
	if revision == 0 || selected.record.Revision != revision {
		return domain.Fail(domain.Conflict, "The selected repository revision changed.", "Reload the current repository before reading this observation.")
	}
	return nil
}
func prInvalid() error {
	return domain.Fail(domain.InvalidArgument, "The pull request read scope is invalid.", "Select the original repository revision and bounded observation.")
}
func (s *Service) avatarReference(ctx context.Context, id domain.ID, selected repositoryIntegrationSelection, actorID, locator string) string {
	if !gh.AvatarLocator(locator, actorID) {
		return ""
	}
	actor, _ := integrationActor(ctx)
	s.prAvatarMu.Lock()
	defer s.prAvatarMu.Unlock()
	if s.prAvatars == nil {
		s.prAvatars = map[string]*prAvatarEntry{}
	}
	for ref, e := range s.prAvatars {
		if e.actor == actor && e.repository == id && e.revision == selected.record.Revision && e.profile == selected.repository.IntegrationID && e.generation == selected.profile.Connection.GenerationID && e.actorID == actorID && e.locator == locator {
			return ref
		}
	}
	if len(s.prAvatars) >= 64 {
		var oldest string
		var at time.Time
		for ref, e := range s.prAvatars {
			if oldest == "" || e.created.Before(at) {
				oldest, at = ref, e.created
			}
		}
		delete(s.prAvatars, oldest)
	}
	ref := string(domain.NewID())
	s.prAvatars[ref] = &prAvatarEntry{actor: actor, repository: id, revision: selected.record.Revision, profile: selected.repository.IntegrationID, generation: selected.profile.Connection.GenerationID, actorID: actorID, locator: locator, created: time.Now()}
	return ref
}
func (s *Service) GetPullRequestWorkspace(ctx context.Context, req *connect.Request[pb.GetPullRequestWorkspaceRequest]) (*connect.Response[pb.GetPullRequestWorkspaceResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, e := integrationActor(ctx); e != nil {
		return nil, rpc.Error(e, correlation)
	}
	if len(req.Msg.SeedNumbers) == 0 || len(req.Msg.SeedNumbers) > 20 {
		return nil, rpc.Error(prInvalid(), correlation)
	}
	seen := map[string]bool{}
	for _, n := range req.Msg.SeedNumbers {
		if !domain.PositiveDecimal(n) || seen[n] {
			return nil, rpc.Error(prInvalid(), correlation)
		}
		seen[n] = true
	}
	var observed gh.WorkspaceObservation
	id := domain.ID(req.Msg.RepositoryId)
	selected, e := s.withRepositoryIntegration(ctx, id, "workspace", correlation, func(ctx context.Context, token []byte, sel repositoryIntegrationSelection) error {
		if e := prRevision(sel, req.Msg.ExpectedRevision); e != nil {
			return e
		}
		var e error
		observed, e = gh.New(s.outboundResolver()).Workspace(ctx, token, sel.repository.GitHubOwner, sel.repository.GitHubName, req.Msg.SeedNumbers)
		return e
	})
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	value := domain.PRWorkspace{PRReadScope: s.prScope(id, selected, observed.Repository), State: observed.State, Reasons: observed.Reasons, Seeds: append([]string{}, req.Msg.SeedNumbers...), Nodes: observed.Nodes, Edges: observed.Edges}
	for i := range value.Nodes {
		n := &value.Nodes[i]
		if n.Item.Author != nil {
			n.AvatarReference = s.avatarReference(ctx, id, selected, n.Item.Author.ID, observed.Avatars[n.Item.Author.ID])
		}
	}
	if !value.Validate() {
		return nil, rpc.Error(domain.Fail(domain.RecoveryRequired, "The workspace observation is inconsistent.", "Retry the original repository workspace explicitly."), correlation)
	}
	raw, e := json.Marshal(value)
	if e != nil || len(raw) > 1<<20 {
		return nil, rpc.Error(domain.Fail(domain.ResourceExhausted, "The complete workspace exceeds its response limit.", "Use fewer accepted seeds; no truncated graph is returned."), correlation)
	}
	s.logger.Info("pull_request_workspace_observed", "repository_id", id, "node_count", len(value.Nodes), "state", value.State, "correlation_id", correlation)
	response := connect.NewResponse(&pb.GetPullRequestWorkspaceResponse{SchemaVersion: 1, DocumentJson: raw})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) ListPullRequestCommits(ctx context.Context, req *connect.Request[pb.ListPullRequestCommitsRequest]) (*connect.Response[pb.ListPullRequestCommitsResponse], error) {
	m := req.Msg
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, e := integrationActor(ctx); e != nil {
		return nil, rpc.Error(e, correlation)
	}
	if !domain.PositiveDecimal(m.Number) || !domain.PositiveDecimal(m.PullRequestId) || !domain.PositiveDecimal(m.RemoteRepositoryId) || !validSHA(m.BaseSha) || !validSHA(m.HeadSha) || domain.Text(m.PageToken, "page token", 2048, false) != nil {
		return nil, rpc.Error(prInvalid(), correlation)
	}
	var observed gh.CommitsObservation
	var cursorScope string
	id := domain.ID(m.RepositoryId)
	selected, e := s.withRepositoryIntegration(ctx, id, "commits", correlation, func(ctx context.Context, token []byte, sel repositoryIntegrationSelection) error {
		if e := prRevision(sel, m.ExpectedRevision); e != nil {
			return e
		}
		var e error
		actor, _ := integrationActor(ctx)
		scopeBytes, _ := json.Marshal([]any{actor.Type, actor.DeviceID, id, m.ExpectedRevision, sel.repository.IntegrationID, sel.profile.Connection.GenerationID, m.RemoteRepositoryId, m.PullRequestId, m.Number, m.BaseSha, m.HeadSha})
		digest := sha256.Sum256(scopeBytes)
		cursorScope = "pr-commits:" + hex.EncodeToString(digest[:])
		cursor := ""
		if m.PageToken != "" {
			value, err := s.Identity.DecodeCursor(m.PageToken, cursorScope)
			if err != nil {
				return err
			}
			cursor = string(value.After)
		}
		observed, e = gh.New(s.outboundResolver()).Commits(ctx, token, sel.repository.GitHubOwner, sel.repository.GitHubName, m.Number, m.PullRequestId, m.RemoteRepositoryId, m.BaseSha, m.HeadSha, cursor)
		return e
	})
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	value := observed.Value
	value.PageToken = m.PageToken
	if value.NextPageToken != "" {
		if domain.Text(value.NextPageToken, "provider cursor", 512, true) != nil {
			return nil, rpc.Error(prInvalid(), correlation)
		}
		encoded, err := s.Identity.EncodeCursor(security.Cursor{Scope: cursorScope, After: domain.ID(value.NextPageToken)})
		if err != nil || len(encoded) > 2048 {
			return nil, rpc.Error(prInvalid(), correlation)
		}
		value.NextPageToken = encoded
	}
	value.PRReadScope = s.prScope(id, selected, observed.Repository)
	for i := range value.Commits {
		person := &value.Commits[i].Author
		if person.Actor != nil {
			person.AvatarReference = s.avatarReference(ctx, id, selected, person.Actor.ID, observed.Avatars[person.Actor.ID])
		}
	}
	if !value.Validate() {
		return nil, rpc.Error(prInvalid(), correlation)
	}
	raw, e := json.Marshal(value)
	if e != nil || len(raw) > 1<<20 {
		return nil, rpc.Error(domain.Fail(domain.ResourceExhausted, "The complete commit page exceeds its response limit.", "Inspect the complete message on GitHub; no truncated page is returned."), correlation)
	}
	s.logger.Info("pull_request_commits_observed", "repository_id", id, "commit_count", len(value.Commits), "correlation_id", correlation)
	response := connect.NewResponse(&pb.ListPullRequestCommitsResponse{SchemaVersion: 1, DocumentJson: raw})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func validSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func (s *Service) ReadPullRequestAvatar(ctx context.Context, req *connect.Request[pb.ReadPullRequestAvatarRequest]) (*connect.Response[pb.ReadPullRequestAvatarResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, e := integrationActor(ctx)
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	if domain.ID(req.Msg.Reference).Validate() != nil {
		return nil, rpc.Error(prInvalid(), correlation)
	}
	id := domain.ID(req.Msg.RepositoryId)
	var png []byte
	_, e = s.withRepositoryIntegration(ctx, id, "avatar", correlation, func(ctx context.Context, _ []byte, selected repositoryIntegrationSelection) error {
		if e := prRevision(selected, req.Msg.ExpectedRevision); e != nil {
			return e
		}
		s.prAvatarMu.Lock()
		entry := s.prAvatars[req.Msg.Reference]
		if entry == nil || entry.actor != actor || entry.repository != id || entry.revision != selected.record.Revision || entry.profile != selected.repository.IntegrationID || entry.generation != selected.profile.Connection.GenerationID {
			s.prAvatarMu.Unlock()
			return domain.Fail(domain.PermissionDenied, "The avatar reference no longer belongs to this observation.", "Reload the selected workspace; no locator supplied by a client is fetched.")
		}
		cached := append([]byte{}, entry.png...)
		locator, actorID := entry.locator, entry.actorID
		s.prAvatarMu.Unlock()
		if len(cached) > 0 {
			png = cached
			return nil
		}
		var e error
		png, e = gh.New(s.outboundResolver()).Avatar(ctx, locator, actorID)
		if e != nil {
			return e
		}
		s.prAvatarMu.Lock()
		if s.prAvatars[req.Msg.Reference] == entry {
			entry.png = append([]byte{}, png...)
		}
		s.prAvatarMu.Unlock()
		return nil
	})
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	response := connect.NewResponse(&pb.ReadPullRequestAvatarResponse{Png: png})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
