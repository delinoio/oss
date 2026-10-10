package server

import (
	"context"
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

type prReferenceKind uint8

const (
	prAvatarReference prReferenceKind = iota + 1
	prCommitReference
)

type prWorkspaceReference struct {
	Kind                                     prReferenceKind
	Actor                                    domain.ID
	Repository                               domain.ID
	Revision                                 uint64
	Profile                                  domain.ID
	Generation                               domain.ID
	Number                                   uint32
	Base, Head, Cursor, Locator, AvatarActor string
	Expires                                  time.Time
}

func (s *Service) rememberPRReference(ref prWorkspaceReference) string {
	s.prWorkspaceMu.Lock()
	defer s.prWorkspaceMu.Unlock()
	if s.prWorkspaceRefs == nil {
		s.prWorkspaceRefs = map[string]prWorkspaceReference{}
	}
	for key, value := range s.prWorkspaceRefs {
		if time.Now().After(value.Expires) {
			delete(s.prWorkspaceRefs, key)
		}
	}
	if len(s.prWorkspaceRefs) >= 256 {
		var oldest string
		var expiry time.Time
		for key, value := range s.prWorkspaceRefs {
			if oldest == "" || value.Expires.Before(expiry) {
				oldest = key
				expiry = value.Expires
			}
		}
		delete(s.prWorkspaceRefs, oldest)
	}
	ref.Expires = time.Now().Add(10 * time.Minute)
	key := string(domain.NewID())
	s.prWorkspaceRefs[key] = ref
	return key
}
func (s *Service) lookupPRReference(key string, actor domain.ID, kind prReferenceKind) (prWorkspaceReference, error) {
	s.prWorkspaceMu.Lock()
	defer s.prWorkspaceMu.Unlock()
	r, ok := s.prWorkspaceRefs[key]
	if !ok || r.Kind != kind || r.Actor != actor || time.Now().After(r.Expires) {
		return r, domain.Fail(domain.CursorExpired, "The PR observation reference is unavailable.", "Reload the original PR observation.")
	}
	return r, nil
}
func prReferenceMatches(ref prWorkspaceReference, selected repositoryIntegrationSelection) bool {
	return ref.Repository == selected.record.ID && ref.Revision == selected.record.Revision && ref.Profile == selected.repository.IntegrationID && ref.Generation == selected.profile.Connection.GenerationID
}
func prScopedReference(actor domain.ID, selected repositoryIntegrationSelection) prWorkspaceReference {
	return prWorkspaceReference{Actor: actor, Repository: selected.record.ID, Revision: selected.record.Revision, Profile: selected.repository.IntegrationID, Generation: selected.profile.Connection.GenerationID}
}
func workspaceFailure() error {
	return domain.Fail(domain.InvalidArgument, "The PR workspace scope is invalid.", "Select the original repository revision and a bounded page of PRs.")
}

func (s *Service) GetPullRequestWorkspace(ctx context.Context, req *connect.Request[pb.GetPullRequestWorkspaceRequest]) (*connect.Response[pb.GetPullRequestWorkspaceResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := integrationActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if len(req.Msg.Seeds) == 0 || len(req.Msg.Seeds) > 20 || req.Msg.ExpectedRevision == 0 {
		return nil, rpc.Error(workspaceFailure(), correlation)
	}
	seen := map[uint32]bool{}
	for _, n := range req.Msg.Seeds {
		if n == 0 || seen[n] {
			return nil, rpc.Error(workspaceFailure(), correlation)
		}
		seen[n] = true
	}
	var value domain.PRWorkspace
	selected, err := s.withRepositoryIntegration(ctx, domain.ID(req.Msg.RepositoryId), "pr-workspace", correlation, func(readCtx context.Context, token []byte, selected repositoryIntegrationSelection) error {
		if selected.record.Revision != req.Msg.ExpectedRevision {
			return workspaceFailure()
		}
		var e error
		value, e = gh.New(s.outboundResolver()).PullRequestWorkspace(readCtx, token, selected.repository.GitHubOwner, selected.repository.GitHubName, req.Msg.Seeds)
		if e != nil {
			return e
		}
		return safePRWorkspaceObservation(value, token)
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	value.RepositoryID = selected.record.ID
	value.RepositoryRevision = strconv.FormatUint(selected.record.Revision, 10)
	value.ProfileID = selected.repository.IntegrationID
	value.GenerationID = selected.profile.Connection.GenerationID
	value.ObservedAt = time.Now().UTC()
	for index := range value.Rows {
		row := &value.Rows[index]
		if row.AvatarLocator != "" && row.Item.Author != nil {
			ref := prScopedReference(actor.DeviceID, selected)
			ref.Kind = prAvatarReference
			n, _ := strconv.ParseUint(row.Item.Number, 10, 32)
			ref.Number = uint32(n)
			ref.Base = row.Item.BaseSHA
			ref.Head = row.Item.HeadSHA
			ref.Locator = row.AvatarLocator
			ref.AvatarActor = row.Item.Author.ID
			row.AvatarReference = s.rememberPRReference(ref)
		}
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 1<<20 {
		return nil, rpc.Error(domain.Fail(domain.ResourceExhausted, "The PR workspace exceeds its complete response limit.", "Read fewer seed PRs."), correlation)
	}
	s.logger.Info("pr_workspace_observed", "repository_id", value.RepositoryID, "row_count", len(value.Rows), "state", value.State, "correlation_id", correlation)
	response := connect.NewResponse(&pb.GetPullRequestWorkspaceResponse{SchemaVersion: 1, DocumentJson: raw})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) ListPullRequestCommits(ctx context.Context, req *connect.Request[pb.ListPullRequestCommitsRequest]) (*connect.Response[pb.ListPullRequestCommitsResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := integrationActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if req.Msg.Number == 0 || !domain.PRWorkspaceSHA(req.Msg.BaseSha) || !domain.PRWorkspaceSHA(req.Msg.HeadSha) || req.Msg.ExpectedRevision == 0 {
		return nil, rpc.Error(workspaceFailure(), correlation)
	}
	var value domain.PRCommitPage
	var next string
	selected, err := s.withRepositoryIntegration(ctx, domain.ID(req.Msg.RepositoryId), "pr-commits", correlation, func(readCtx context.Context, token []byte, selected repositoryIntegrationSelection) error {
		if selected.record.Revision != req.Msg.ExpectedRevision {
			return workspaceFailure()
		}
		after := ""
		if req.Msg.PageToken != "" {
			ref, e := s.lookupPRReference(req.Msg.PageToken, actor.DeviceID, prCommitReference)
			if e != nil {
				return e
			}
			if !prReferenceMatches(ref, selected) || ref.Number != req.Msg.Number || ref.Base != req.Msg.BaseSha || ref.Head != req.Msg.HeadSha {
				return workspaceFailure()
			}
			after = ref.Cursor
		}
		var e error
		value, next, e = gh.New(s.outboundResolver()).PullRequestCommits(readCtx, token, selected.repository.GitHubOwner, selected.repository.GitHubName, req.Msg.Number, req.Msg.BaseSha, req.Msg.HeadSha, after)
		if e != nil {
			return e
		}
		return safePRWorkspaceObservation(value, token)
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	value.RepositoryID = selected.record.ID
	value.RepositoryRevision = strconv.FormatUint(selected.record.Revision, 10)
	value.ProfileID = selected.repository.IntegrationID
	value.GenerationID = selected.profile.Connection.GenerationID
	if next != "" {
		ref := prScopedReference(actor.DeviceID, selected)
		ref.Kind = prCommitReference
		ref.Number = req.Msg.Number
		ref.Base = req.Msg.BaseSha
		ref.Head = req.Msg.HeadSha
		ref.Cursor = next
		value.NextPageToken = s.rememberPRReference(ref)
	}
	for index := range value.Commits {
		commit := &value.Commits[index]
		if commit.AvatarLocator != "" {
			ref := prScopedReference(actor.DeviceID, selected)
			ref.Kind = prAvatarReference
			ref.Number = req.Msg.Number
			ref.Base = req.Msg.BaseSha
			ref.Head = req.Msg.HeadSha
			ref.Locator = commit.AvatarLocator
			ref.AvatarActor = commit.AvatarActor
			commit.AvatarReference = s.rememberPRReference(ref)
		}
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 1<<20 {
		return nil, rpc.Error(domain.Fail(domain.ResourceExhausted, "The PR commits exceed their complete response limit.", "Inspect the complete commits on GitHub."), correlation)
	}
	s.logger.Info("pr_commits_observed", "repository_id", value.RepositoryID, "count", len(value.Commits), "correlation_id", correlation)
	response := connect.NewResponse(&pb.ListPullRequestCommitsResponse{SchemaVersion: 1, DocumentJson: raw})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) ReadPullRequestAvatar(ctx context.Context, req *connect.Request[pb.ReadPullRequestAvatarRequest]) (*connect.Response[pb.ReadPullRequestAvatarResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := integrationActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	ref, err := s.lookupPRReference(req.Msg.Reference, actor.DeviceID, prAvatarReference)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var raster []byte
	_, err = s.withRepositoryIntegration(ctx, ref.Repository, "pr-avatar", correlation, func(readCtx context.Context, _ []byte, selected repositoryIntegrationSelection) error {
		if !prReferenceMatches(ref, selected) {
			return workspaceFailure()
		}
		var e error
		raster, e = gh.New(s.outboundResolver()).ReadAvatar(readCtx, ref.Locator, ref.AvatarActor)
		return e
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(&pb.ReadPullRequestAvatarResponse{Reference: req.Msg.Reference, MediaType: "image/png", Raster: raster})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

// Check only retained public fields while the original selected credential is
// still available. Provider content never becomes authority to disclose it.
func safePRWorkspaceObservation(value any, token []byte) error {
	raw, err := json.Marshal(value)
	if err != nil || !security.NewProtectedJSON([]string{string(token)}).Safe(raw) {
		return domain.Fail(domain.RecoveryRequired, "The GitHub observation contains protected or invalid content.", "Retry the original read; protected content cannot be published.")
	}
	return nil
}
