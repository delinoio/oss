// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestPRWorkspaceAuthorizationPrecedesReads(t *testing.T) {
	s := &Service{}
	if _, e := s.GetPullRequestWorkspace(context.Background(), connect.NewRequest(&pb.GetPullRequestWorkspaceRequest{})); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("workspace authorization absent", e)
	}
	if _, e := s.ListPullRequestCommits(context.Background(), connect.NewRequest(&pb.ListPullRequestCommitsRequest{})); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("commits authorization absent", e)
	}
	if _, e := s.ReadPullRequestAvatar(context.Background(), connect.NewRequest(&pb.ReadPullRequestAvatarRequest{})); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("avatar authorization absent", e)
	}
}
func TestPRAvatarReferencesBoundActorGenerationAndCacheCapacity(t *testing.T) {
	s := &Service{}
	actor := domain.Principal{Type: domain.OwnerDevice, DeviceID: domain.NewID()}
	ctx := domain.WithPrincipal(context.Background(), actor)
	id, profile, generation := domain.NewID(), domain.NewID(), domain.NewID()
	selected := repositoryIntegrationSelection{record: store.Record{Revision: 2}, repository: domain.Repository{IntegrationID: profile}, profile: domain.Integration{Connection: &domain.IntegrationConnection{GenerationID: generation}}}
	first := s.avatarReference(ctx, id, selected, "19", "https://avatars.githubusercontent.com/u/19?v=4")
	if first == "" || s.avatarReference(ctx, id, selected, "19", "https://avatars.githubusercontent.com/u/19?v=4") != first {
		t.Fatal("same original observation lost stable reference")
	}
	other := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice, DeviceID: domain.NewID()})
	if s.avatarReference(other, id, selected, "19", "https://avatars.githubusercontent.com/u/19?v=4") == first {
		t.Fatal("foreign actor reused reference")
	}
	selected.profile.Connection.GenerationID = domain.NewID()
	if s.avatarReference(ctx, id, selected, "19", "https://avatars.githubusercontent.com/u/19?v=4") == first {
		t.Fatal("replacement token generation reused authority")
	}
	for range 70 {
		selected.record.Revision++
		s.avatarReference(ctx, id, selected, "19", "https://avatars.githubusercontent.com/u/19?v=4")
	}
	if len(s.prAvatars) != 64 || s.prAvatars[first] != nil {
		t.Fatal("avatar cache unbounded or oldest scope retained")
	}
}
func TestForegroundReadDisplacesAndJoinsWorkspace(t *testing.T) {
	f := newIntegrationFixture(t)
	profile := f.replace(profileMutation(f.save("Selected")), "selected-query-token")
	repo := f.repository(domain.ID(profile.Profile.Id))
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	started, joined := make(chan struct{}), make(chan struct{})
	background := make(chan error, 1)
	go func() {
		_, e := f.service.withRepositoryIntegration(ctx, domain.ID(repo.Id), "workspace", "fixture", func(ctx context.Context, _ []byte, _ repositoryIntegrationSelection) error {
			close(started)
			<-ctx.Done()
			close(joined)
			return ctx.Err()
		})
		background <- e
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("background did not start")
	}
	_, e := f.service.withRepositoryIntegration(ctx, domain.ID(repo.Id), "detail", "fixture", func(_ context.Context, _ []byte, _ repositoryIntegrationSelection) error {
		select {
		case <-joined:
		default:
			t.Error("foreground started before displaced owner joined")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if <-background == nil {
		t.Fatal("interrupted enrichment published")
	}
}
