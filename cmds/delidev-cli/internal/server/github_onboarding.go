// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	gh "github.com/delinoio/oss/cmds/delidev-cli/internal/integrations/github"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func (s *Service) authorizeGitHubOnboarding(ctx context.Context, requestID string) error {
	if _, err := integrationActor(ctx); err != nil {
		return err
	}
	if domain.ID(requestID).Validate() != nil {
		return domain.Fail(domain.InvalidArgument, "A valid GitHub request ID is required.", "Start this action again from the current connection.")
	}
	return s.Store.Read(ctx, func(tx *store.Tx) error { return tx.Authorize() })
}

// Inspection has no receipt or native credential generation. Keep its lifetime
// separate from profile IDs while sharing admission and shutdown with all other
// GitHub reads. In particular, close done only after clearing the request token.
func (s *Service) InspectGitHubToken(ctx context.Context, req *connect.Request[pb.InspectGitHubTokenRequest]) (*connect.Response[pb.InspectGitHubTokenResponse], error) {
	defer clear(req.Msg.Token)
	correlation := req.Header().Get(rpc.CorrelationHeader)
	fail := func(err error) (*connect.Response[pb.InspectGitHubTokenResponse], error) {
		return nil, rpc.Error(err, correlation)
	}
	if err := s.authorizeGitHubOnboarding(ctx, req.Msg.RequestId); err != nil {
		return fail(err)
	}
	if err := credentials.ValidatePAT(req.Msg.Token); err != nil {
		return fail(err)
	}
	unlock, err := s.lockIntegrations(ctx)
	if err != nil {
		return fail(err)
	}
	id := domain.ID(req.Msg.RequestId)
	if s.stopping.Load() || s.integrationPreviews[id] != nil || len(s.integrationChecks)+len(s.integrationPreviews) >= 8 {
		unlock()
		return fail(domain.Fail(domain.ResourceExhausted, "GitHub token inspection is currently unavailable.", "Wait for the current GitHub reads to finish and try again."))
	}
	if s.integrationPreviews == nil {
		s.integrationPreviews = map[domain.ID]*integrationCheck{}
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	check := &integrationCheck{cancel: cancel, done: make(chan struct{})}
	s.integrationPreviews[id] = check
	if s.github == nil {
		s.github = gh.New(s.outboundResolver())
	}
	client := s.github
	unlock()
	defer func() {
		clear(req.Msg.Token)
		cancel()
		close(check.done)
		release, _ := s.lockIntegrations(context.Background())
		defer release()
		if s.integrationPreviews[id] == check {
			delete(s.integrationPreviews, id)
		}
	}()
	s.logger.InfoContext(ctx, "github_token_inspection_started", "request_id", id, "correlation_id", correlation)
	observed, err := client.Identity(bounded, req.Msg.Token)
	clear(req.Msg.Token)
	if err == nil {
		err = bounded.Err()
	}
	if err == nil {
		err = s.authorizeGitHubOnboarding(bounded, req.Msg.RequestId)
	}
	if err != nil {
		s.logger.InfoContext(ctx, "github_token_inspection_failed", "request_id", id, "error_code", domain.SafeError(err).Code, "correlation_id", correlation)
		return fail(err)
	}
	state := pb.GitHubTokenIdentityState_GIT_HUB_TOKEN_IDENTITY_STATE_UNAVAILABLE
	switch observed.State {
	case domain.IntegrationIdentityVerified:
		state = pb.GitHubTokenIdentityState_GIT_HUB_TOKEN_IDENTITY_STATE_VERIFIED
	case domain.IntegrationInvalidToken:
		state = pb.GitHubTokenIdentityState_GIT_HUB_TOKEN_IDENTITY_STATE_INVALID_TOKEN
	case domain.IntegrationAccessRestricted:
		state = pb.GitHubTokenIdentityState_GIT_HUB_TOKEN_IDENTITY_STATE_ACCESS_RESTRICTED
	case domain.IntegrationSSORequired:
		state = pb.GitHubTokenIdentityState_GIT_HUB_TOKEN_IDENTITY_STATE_SSO_REQUIRED
	case domain.IntegrationRateLimited:
		state = pb.GitHubTokenIdentityState_GIT_HUB_TOKEN_IDENTITY_STATE_RATE_LIMITED
	}
	verified := state == pb.GitHubTokenIdentityState_GIT_HUB_TOKEN_IDENTITY_STATE_VERIFIED
	if verified && (observed.Identity == nil || observed.Identity.Validate() != nil || observed.Problem != nil) || !verified && observed.Identity != nil {
		return fail(domain.Fail(domain.Unavailable, "GitHub returned an invalid identity observation.", "Reenter the token and try verification again."))
	}
	var identity *pb.GitHubTokenIdentity
	if verified {
		identity = &pb.GitHubTokenIdentity{Id: observed.Identity.ID, NodeId: observed.Identity.NodeID, Login: observed.Identity.Login}
	}
	if !verified && observed.Problem == nil {
		observed.Problem = domain.Fail(domain.Unavailable, "GitHub identity could not be verified.", "Reenter the token and try verification again.")
	}
	var problem error
	if observed.Problem != nil {
		problem = observed.Problem
	}
	s.logger.InfoContext(ctx, "github_token_inspection_finished", "request_id", id, "state", state.String(), "error_code", integrationErrorCode(problem), "correlation_id", correlation)
	response := connect.NewResponse(&pb.InspectGitHubTokenResponse{RequestId: req.Msg.RequestId, State: state, Identity: identity, ProblemJson: integrationProblem(problem, correlation)})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) PrepareGitHubTokenForm(ctx context.Context, req *connect.Request[pb.PrepareGitHubTokenFormRequest]) (*connect.Response[pb.PrepareGitHubTokenFormResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	fail := func(err error) (*connect.Response[pb.PrepareGitHubTokenFormResponse], error) {
		return nil, rpc.Error(err, correlation)
	}
	if err := s.authorizeGitHubOnboarding(ctx, req.Msg.RequestId); err != nil {
		return fail(err)
	}
	var kind domain.PATKind
	switch req.Msg.TokenKind {
	case pb.GitHubTokenKind_GIT_HUB_TOKEN_KIND_FINE_GRAINED:
		kind = domain.FineGrainedPAT
	case pb.GitHubTokenKind_GIT_HUB_TOKEN_KIND_CLASSIC:
		kind = domain.ClassicPAT
	}
	var access domain.GitHubTokenAccess
	switch req.Msg.Access {
	case pb.GitHubTokenAccess_GIT_HUB_TOKEN_ACCESS_SELECTED_REPOSITORIES:
		access = domain.GitHubSelectedRepositories
	case pb.GitHubTokenAccess_GIT_HUB_TOKEN_ACCESS_PUBLIC_REPOSITORIES:
		access = domain.GitHubPublicRepositories
	case pb.GitHubTokenAccess_GIT_HUB_TOKEN_ACCESS_PRIVATE_REPOSITORIES:
		access = domain.GitHubPrivateRepositories
	}
	address, err := domain.GitHubTokenFormURL(kind, req.Msg.ResourceOwner, access)
	if err != nil {
		return fail(err)
	}
	s.logger.InfoContext(ctx, "github_draft_token_form_prepared", "request_id", req.Msg.RequestId, "token_kind", kind, "access", access, "correlation_id", correlation)
	response := connect.NewResponse(&pb.PrepareGitHubTokenFormResponse{RequestId: req.Msg.RequestId, TokenKind: req.Msg.TokenKind, ResourceOwner: req.Msg.ResourceOwner, Access: req.Msg.Access, Url: address})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
