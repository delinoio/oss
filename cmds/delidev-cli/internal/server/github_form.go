package server

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"strconv"
)

func (s *Service) GetGitHubTokenForm(ctx context.Context, req *connect.Request[pb.GetGitHubTokenFormRequest]) (*connect.Response[pb.GetGitHubTokenFormResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	fail := func(err error) (*connect.Response[pb.GetGitHubTokenFormResponse], error) {
		return nil, rpc.Error(err, correlation)
	}
	if _, err := integrationActor(ctx); err != nil {
		return fail(err)
	}
	if domain.ID(req.Msg.ProfileId).Validate() != nil || req.Msg.ExpectedRevision == 0 {
		return fail(domain.Fail(domain.InvalidArgument, "A profile and its current revision are required.", "Refresh the selected GitHub profile."))
	}
	r, profile, err := s.integrationRecord(ctx, domain.ID(req.Msg.ProfileId))
	if err != nil {
		return fail(err)
	}
	if r.Revision != req.Msg.ExpectedRevision || profile.Pending != nil {
		return fail(domain.Fail(domain.Conflict, "The selected profile changed or has a pending operation.", "Finish its pending change and refresh before preparing a token form."))
	}
	access := domain.GitHubTokenAccess("")
	switch req.Msg.Access {
	case pb.GitHubTokenAccess_GIT_HUB_TOKEN_ACCESS_SELECTED_REPOSITORIES:
		access = domain.GitHubSelectedRepositories
	case pb.GitHubTokenAccess_GIT_HUB_TOKEN_ACCESS_PUBLIC_REPOSITORIES:
		access = domain.GitHubPublicRepositories
	case pb.GitHubTokenAccess_GIT_HUB_TOKEN_ACCESS_PRIVATE_REPOSITORIES:
		access = domain.GitHubPrivateRepositories
	}
	address, err := domain.GitHubTokenFormURL(profile.TokenKind, profile.ResourceOwner, access)
	if err != nil {
		return fail(err)
	}
	value := domain.GitHubTokenForm{ProfileID: r.ID, ProfileRevision: strconv.FormatUint(r.Revision, 10), TokenKind: profile.TokenKind, ResourceOwner: profile.ResourceOwner, Access: access, URL: address}
	raw, _ := json.Marshal(value)
	s.logger.InfoContext(ctx, "github_token_form_prepared", "profile_id", r.ID, "access", access, "correlation_id", correlation)
	response := connect.NewResponse(&pb.GetGitHubTokenFormResponse{SchemaVersion: 1, DocumentJson: raw})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
