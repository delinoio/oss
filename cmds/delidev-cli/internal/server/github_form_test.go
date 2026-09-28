package server

import (
	"connectrpc.com/connect"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"testing"
)

func TestGitHubTokenFormNeedsCurrentAuthorizedProfileWithoutCredentialRead(t *testing.T) {
	f := newIntegrationFixture(t)
	profile := f.save("Creation form")
	req := &pb.GetGitHubTokenFormRequest{ProfileId: profile.Id, ExpectedRevision: profile.Revision, Access: pb.GitHubTokenAccess_GIT_HUB_TOKEN_ACCESS_SELECTED_REPOSITORIES}
	reply, err := f.client.GetGitHubTokenForm(context.Background(), ownerRequest(f.service.Identity, req))
	if err != nil {
		t.Fatal(err)
	}
	var value domain.GitHubTokenForm
	if reply.Msg.SchemaVersion != 1 || domain.Decode(reply.Msg.DocumentJson, &value) != nil || value.Validate() != nil || value.ProfileID != domain.ID(profile.Id) {
		t.Fatal("invalid form")
	}
	// The profile has never had a token. Preparing its form is a metadata read.
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	r, state, err := f.service.integrationRecord(ctx, domain.ID(profile.Id))
	if err != nil || r.Revision != profile.Revision || state.Connection != nil || state.Pending != nil {
		t.Fatal("form changed profile", err)
	}
	_, err = f.client.GetGitHubTokenForm(context.Background(), connect.NewRequest(req))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("unauthenticated form", err)
	}
	_, err = f.service.GetGitHubTokenForm(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice}), connect.NewRequest(req))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker form", err)
	}
	req.ExpectedRevision++
	_, err = f.client.GetGitHubTokenForm(context.Background(), ownerRequest(f.service.Identity, req))
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("stale form", err)
	}
	req.ExpectedRevision = profile.Revision
	req.Access = pb.GitHubTokenAccess_GIT_HUB_TOKEN_ACCESS_PRIVATE_REPOSITORIES
	_, err = f.client.GetGitHubTokenForm(context.Background(), ownerRequest(f.service.Identity, req))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("wrong kind", err)
	}
}
