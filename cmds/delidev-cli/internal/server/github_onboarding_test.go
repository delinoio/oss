// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	gh "github.com/delinoio/oss/cmds/delidev-cli/internal/integrations/github"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func onboardingOwner() context.Context {
	return domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
}

func TestGitHubOnboardingInspectionIsEphemeral(t *testing.T) {
	f := newIntegrationFixture(t)
	var logs bytes.Buffer
	f.service.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	request := &pb.InspectGitHubTokenRequest{RequestId: string(domain.NewID()), Token: []byte("ephemeral-fixture-pat")}
	result, err := f.service.InspectGitHubToken(onboardingOwner(), connect.NewRequest(request))
	if err != nil || result.Msg.RequestId != request.RequestId || result.Msg.State != pb.GitHubTokenIdentityState_GIT_HUB_TOKEN_IDENTITY_STATE_VERIFIED || result.Msg.Identity.Login != "fixture-user" || result.Msg.Identity.Id != "17" || result.Msg.Identity.NodeId != "U_17" || len(result.Msg.ProblemJson) != 0 {
		t.Fatal("invalid inspection result", result, err)
	}
	if !bytes.Equal(request.Token, make([]byte, len(request.Token))) || strings.Contains(logs.String(), "ephemeral-fixture-pat") || strings.Contains(logs.String(), "fixture-user") {
		t.Fatal("inspection retained secret or identity in logs")
	}
	if puts, deletes, size := f.native.counts(); puts != 0 || deletes != 0 || size != 0 {
		t.Fatal("inspection touched credential storage")
	}
	rows, err := f.service.ListResources(onboardingOwner(), connect.NewRequest(&pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_INTEGRATION}}))
	if err != nil || len(rows.Msg.Resources) != 0 {
		t.Fatal("inspection created metadata", err)
	}
	_, replayed, err := f.service.Store.Replay(onboardingOwner(), domain.ID(request.RequestId), "preview", struct{}{})
	if err != nil || replayed {
		t.Fatal("inspection persisted a receipt", err)
	}
	// Repeating an inspection ID is another read, never receipt replay.
	request.Token = []byte("ephemeral-fixture-pat")
	if _, err = f.service.InspectGitHubToken(onboardingOwner(), connect.NewRequest(request)); err != nil || f.calls.Load() != 2 {
		t.Fatal("inspection did not perform an independent read", err)
	}
}

func TestGitHubOnboardingClosedIdentityStates(t *testing.T) {
	for _, tc := range []struct {
		state domain.IntegrationValidationState
		want  pb.GitHubTokenIdentityState
	}{
		{domain.IntegrationInvalidToken, pb.GitHubTokenIdentityState_GIT_HUB_TOKEN_IDENTITY_STATE_INVALID_TOKEN},
		{domain.IntegrationAccessRestricted, pb.GitHubTokenIdentityState_GIT_HUB_TOKEN_IDENTITY_STATE_ACCESS_RESTRICTED},
		{domain.IntegrationSSORequired, pb.GitHubTokenIdentityState_GIT_HUB_TOKEN_IDENTITY_STATE_SSO_REQUIRED},
		{domain.IntegrationRateLimited, pb.GitHubTokenIdentityState_GIT_HUB_TOKEN_IDENTITY_STATE_RATE_LIMITED},
		{domain.IntegrationUnavailable, pb.GitHubTokenIdentityState_GIT_HUB_TOKEN_IDENTITY_STATE_UNAVAILABLE},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			f := newIntegrationFixture(t)
			f.service.github = identityFunc(func(context.Context, []byte) (gh.IdentityObservation, error) {
				return gh.IdentityObservation{State: tc.state}, nil
			})
			result, err := f.client.InspectGitHubToken(context.Background(), ownerRequest(f.service.Identity, &pb.InspectGitHubTokenRequest{RequestId: string(domain.NewID()), Token: []byte("fixture-pat")}))
			if err != nil || result.Msg.State != tc.want || result.Msg.Identity != nil || len(result.Msg.ProblemJson) == 0 {
				t.Fatal("failure became a verified identity", result, err)
			}
		})
	}
	for _, identity := range []*domain.GitHubIdentity{nil, {ID: "0", NodeID: "U_17", Login: "fixture-user"}, {ID: "17", NodeID: "", Login: "fixture-user"}, {ID: "17", NodeID: "U_17", Login: "bad/owner"}} {
		f := newIntegrationFixture(t)
		f.service.github = identityFunc(func(context.Context, []byte) (gh.IdentityObservation, error) {
			return gh.IdentityObservation{State: domain.IntegrationIdentityVerified, Identity: identity}, nil
		})
		if _, err := f.service.InspectGitHubToken(onboardingOwner(), connect.NewRequest(&pb.InspectGitHubTokenRequest{RequestId: string(domain.NewID()), Token: []byte("fixture-pat")})); connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatal("malformed verified identity accepted", err)
		}
	}
}

func TestGitHubOnboardingAdmissionAndShutdownClearTokens(t *testing.T) {
	f := newIntegrationFixture(t)
	for _, ctx := range []context.Context{context.Background(), domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice})} {
		request := &pb.InspectGitHubTokenRequest{RequestId: string(domain.NewID()), Token: []byte("fixture-pat")}
		if _, err := f.service.InspectGitHubToken(ctx, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodePermissionDenied || !bytes.Equal(request.Token, make([]byte, len(request.Token))) {
			t.Fatal("unauthorized inspection retained token", err)
		}
	}
	for _, req := range []*pb.InspectGitHubTokenRequest{{RequestId: "bad", Token: []byte("fixture-pat")}, {RequestId: string(domain.NewID()), Token: []byte("invalid token")}} {
		if _, err := f.service.InspectGitHubToken(onboardingOwner(), connect.NewRequest(req)); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatal("invalid inspection admitted", err)
		}
	}
	f.service.integrationChecks = map[domain.ID]*integrationCheck{}
	for range 8 {
		done := make(chan struct{})
		close(done)
		f.service.integrationChecks[domain.NewID()] = &integrationCheck{done: done, cancel: func() {}}
	}
	if _, err := f.service.InspectGitHubToken(onboardingOwner(), connect.NewRequest(&pb.InspectGitHubTokenRequest{RequestId: string(domain.NewID()), Token: []byte("fixture-pat")})); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatal("inspection bypassed shared global limit", err)
	}
	f.service.integrationChecks = nil
	started, finished := make(chan struct{}), make(chan error, 1)
	request := &pb.InspectGitHubTokenRequest{RequestId: string(domain.NewID()), Token: []byte("fixture-pat")}
	f.service.github = identityFunc(func(ctx context.Context, _ []byte) (gh.IdentityObservation, error) {
		close(started)
		<-ctx.Done()
		return gh.IdentityObservation{}, ctx.Err()
	})
	go func() {
		_, err := f.service.InspectGitHubToken(onboardingOwner(), connect.NewRequest(request))
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("inspection did not start")
	}
	if err := f.service.closeIntegrationSecrets(); err != nil || !bytes.Equal(request.Token, make([]byte, len(request.Token))) {
		t.Fatal("shutdown did not join secret erasure", err)
	}
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("canceled result published")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("inspection survived shutdown")
	}
}

func TestGitHubOnboardingOfficialForms(t *testing.T) {
	f := newIntegrationFixture(t)
	for _, tc := range []struct {
		kind        pb.GitHubTokenKind
		owner       string
		access      pb.GitHubTokenAccess
		path, scope string
	}{
		{pb.GitHubTokenKind_GIT_HUB_TOKEN_KIND_FINE_GRAINED, "example-org", pb.GitHubTokenAccess_GIT_HUB_TOKEN_ACCESS_SELECTED_REPOSITORIES, "/settings/personal-access-tokens/new", ""},
		{pb.GitHubTokenKind_GIT_HUB_TOKEN_KIND_FINE_GRAINED, "", pb.GitHubTokenAccess_GIT_HUB_TOKEN_ACCESS_SELECTED_REPOSITORIES, "/settings/personal-access-tokens/new", ""},
		{pb.GitHubTokenKind_GIT_HUB_TOKEN_KIND_CLASSIC, "", pb.GitHubTokenAccess_GIT_HUB_TOKEN_ACCESS_PUBLIC_REPOSITORIES, "/settings/tokens/new", ""},
		{pb.GitHubTokenKind_GIT_HUB_TOKEN_KIND_CLASSIC, "explicit-owner", pb.GitHubTokenAccess_GIT_HUB_TOKEN_ACCESS_PRIVATE_REPOSITORIES, "/settings/tokens/new", "repo"},
	} {
		id := string(domain.NewID())
		result, err := f.client.PrepareGitHubTokenForm(context.Background(), ownerRequest(f.service.Identity, &pb.PrepareGitHubTokenFormRequest{RequestId: id, TokenKind: tc.kind, ResourceOwner: tc.owner, Access: tc.access}))
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(result.Msg.Url)
		if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.Path != tc.path || u.Query().Get("scopes") != tc.scope || result.Msg.RequestId != id || result.Msg.TokenKind != tc.kind || result.Msg.ResourceOwner != tc.owner || result.Msg.Access != tc.access {
			t.Fatal("form binding changed", result, err)
		}
		if tc.kind == pb.GitHubTokenKind_GIT_HUB_TOKEN_KIND_FINE_GRAINED {
			q := u.Query()
			count := 8
			if tc.owner != "" {
				count++
			}
			if len(q) != count || q.Has("target_name") != (tc.owner != "") || q.Get("target_name") != tc.owner || q.Get("expires_in") != "30" {
				t.Fatal("owner/expiry changed", q)
			}
			for _, permission := range []string{"metadata", "contents", "pull_requests", "issues", "statuses"} {
				if q.Get(permission) != "read" {
					t.Fatal("permission changed", q)
				}
			}
		}
	}
	for _, tc := range []*pb.PrepareGitHubTokenFormRequest{
		{TokenKind: pb.GitHubTokenKind_GIT_HUB_TOKEN_KIND_FINE_GRAINED, Access: pb.GitHubTokenAccess_GIT_HUB_TOKEN_ACCESS_PUBLIC_REPOSITORIES},
		{TokenKind: pb.GitHubTokenKind(99), ResourceOwner: "owner", Access: pb.GitHubTokenAccess_GIT_HUB_TOKEN_ACCESS_SELECTED_REPOSITORIES},
		{TokenKind: pb.GitHubTokenKind_GIT_HUB_TOKEN_KIND_CLASSIC, Access: pb.GitHubTokenAccess(99)},
		{TokenKind: pb.GitHubTokenKind_GIT_HUB_TOKEN_KIND_FINE_GRAINED, ResourceOwner: "bad/owner", Access: pb.GitHubTokenAccess_GIT_HUB_TOKEN_ACCESS_SELECTED_REPOSITORIES},
	} {
		tc.RequestId = string(domain.NewID())
		if _, err := f.service.PrepareGitHubTokenForm(onboardingOwner(), connect.NewRequest(tc)); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatal("invalid form choice accepted", err)
		}
	}
	if f.calls.Load() != 0 {
		t.Fatal("form preparation contacted GitHub")
	}
}
