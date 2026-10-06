// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	gh "github.com/delinoio/oss/cmds/delidev-cli/internal/integrations/github"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type repositoryInventoryFunc func(context.Context, []byte, uint32, uint32) (gh.RepositoryListObservation, error)

func (f repositoryInventoryFunc) ListRepositories(ctx context.Context, token []byte, page, size uint32) (gh.RepositoryListObservation, error) {
	return f(ctx, token, page, size)
}
func inventoryEntry(owner, name, id string) domain.GitHubRepositoryListEntry {
	return domain.GitHubRepositoryListEntry{Repository: domain.RemoteRepository{Provider: domain.GitHubCom, ID: id, NodeID: "R_" + id, Owner: owner, Name: name, Private: true, DefaultBranch: "main"}, HTTPSURL: "https://github.com/" + owner + "/" + name + ".git", SSHURL: "git@github.com:" + owner + "/" + name + ".git"}
}
func inventoryRequest(profile *pb.Resource) *pb.ListGitHubRepositoriesRequest {
	return &pb.ListGitHubRepositoriesRequest{ProfileId: profile.Id, ExpectedRevision: profile.Revision, Page: 1, PageSize: 50}
}
func TestGitHubRepositoryPageBindsProfileGenerationAndOwner(t *testing.T) {
	f := newIntegrationFixture(t)
	profile := f.replace(profileMutation(f.save("Selected")), "fixture-selected-token").Profile
	calls := 0
	f.service.githubRepositories = repositoryInventoryFunc(func(_ context.Context, token []byte, page, size uint32) (gh.RepositoryListObservation, error) {
		calls++
		if string(token) != "fixture-selected-token" || page != 1 || size != 50 {
			t.Error("selection changed")
		}
		return gh.RepositoryListObservation{Repositories: []domain.GitHubRepositoryListEntry{inventoryEntry("fixture-owner", "repo", "12"), inventoryEntry("foreign-owner", "other", "13")}, NextPage: 2}, nil
	})
	response, err := f.client.ListGitHubRepositories(context.Background(), ownerRequest(f.service.Identity, inventoryRequest(profile)))
	if err != nil {
		t.Fatal(err)
	}
	var result domain.GitHubRepositoryPage
	if domain.Decode(response.Msg.DocumentJson, &result) != nil || result.Validate() != nil || len(result.Repositories) != 1 || result.NextPage != 2 || result.ProfileID != domain.ID(profile.Id) || result.GenerationID != integrationValue(t, profile).Connection.GenerationID {
		t.Fatal("unbound or unfiltered result")
	}
	request := inventoryRequest(profile)
	request.ExpectedRevision = 0
	if _, err := f.client.ListGitHubRepositories(context.Background(), ownerRequest(f.service.Identity, request)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("missing revision admitted", err)
	}
	request = inventoryRequest(profile)
	request.ExpectedRevision++
	if _, err := f.client.ListGitHubRepositories(context.Background(), ownerRequest(f.service.Identity, request)); connect.CodeOf(err) != connect.CodeAborted || calls != 1 {
		t.Fatal("stale revision reached GitHub", err)
	}
	f.service.githubRepositories = repositoryInventoryFunc(func(context.Context, []byte, uint32, uint32) (gh.RepositoryListObservation, error) {
		entry := inventoryEntry("foreign-owner", "other", "13")
		entry.HTTPSURL = "ext::unexpected"
		return gh.RepositoryListObservation{Repositories: []domain.GitHubRepositoryListEntry{entry}}, nil
	})
	if _, err := f.client.ListGitHubRepositories(context.Background(), ownerRequest(f.service.Identity, inventoryRequest(profile))); err == nil {
		t.Fatal("malformed excluded row admitted as empty")
	}
}
func TestGitHubRepositoryPageReplacementJoinsAndClearsOriginalToken(t *testing.T) {
	f := newIntegrationFixture(t)
	profile := f.replace(profileMutation(f.save("Selected")), "fixture-original-token").Profile
	started := make(chan struct{})
	var captured []byte
	f.service.githubRepositories = repositoryInventoryFunc(func(ctx context.Context, token []byte, _ uint32, _ uint32) (gh.RepositoryListObservation, error) {
		captured = token
		close(started)
		<-ctx.Done()
		return gh.RepositoryListObservation{}, ctx.Err()
	})
	done := make(chan error, 1)
	go func() {
		_, err := f.client.ListGitHubRepositories(context.Background(), ownerRequest(f.service.Identity, inventoryRequest(profile)))
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("read did not start")
	}
	updated := f.replace(profileMutation(profile), "fixture-replacement-token").Profile
	if updated.Revision <= profile.Revision {
		t.Fatal("replacement did not finish")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("replaced generation published")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("replacement did not join")
	}
	if !bytes.Equal(captured, make([]byte, len(captured))) {
		t.Fatal("original token survived joined completion")
	}
}
func TestGitHubRepositoryPageDeletionJoinsRead(t *testing.T) {
	f := newIntegrationFixture(t)
	profile := f.replace(profileMutation(f.save("Selected")), "fixture-original-token").Profile
	started := make(chan struct{})
	f.service.githubRepositories = repositoryInventoryFunc(func(ctx context.Context, _ []byte, _ uint32, _ uint32) (gh.RepositoryListObservation, error) {
		close(started)
		<-ctx.Done()
		return gh.RepositoryListObservation{}, ctx.Err()
	})
	done := make(chan error, 1)
	go func() {
		_, err := f.client.ListGitHubRepositories(context.Background(), ownerRequest(f.service.Identity, inventoryRequest(profile)))
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("read did not start")
	}
	response, err := f.client.DeleteIntegrationProfile(context.Background(), ownerRequest(f.service.Identity, &pb.DeleteIntegrationProfileRequest{Mutation: profileMutation(profile)}))
	if err != nil || !response.Msg.Deleted {
		t.Fatal("delete did not settle", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("deleted profile published a page")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("delete did not join")
	}
}
