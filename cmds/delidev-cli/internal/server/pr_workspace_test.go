// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"strconv"
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

type avatarReadHarness struct {
	f         *integrationFixture
	ctx       context.Context
	repoID    domain.ID
	selected  repositoryIntegrationSelection
	reference string
}

func newAvatarReadHarness(t *testing.T) *avatarReadHarness {
	t.Helper()
	f := newIntegrationFixture(t)
	profile := f.replace(profileMutation(f.save("Selected")), "selected-query-token")
	repo := f.repository(domain.ID(profile.Profile.Id))
	h := &avatarReadHarness{f: f, ctx: domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), repoID: domain.ID(repo.Id)}
	if _, e := f.service.withRepositoryIntegration(h.ctx, h.repoID, "avatar", "fixture", func(_ context.Context, _ []byte, selected repositoryIntegrationSelection) error {
		h.selected = selected
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	h.reference = f.service.avatarReference(h.ctx, h.repoID, h.selected, "19", "https://avatars.githubusercontent.com/u/19?v=4")
	if h.reference == "" {
		t.Fatal("avatar reference not issued")
	}
	return h
}

// evict fills the 64-entry cache with references from other observations so the
// oldest entry, including the harness reference, is dropped.
func (h *avatarReadHarness) evict(t *testing.T) {
	t.Helper()
	other := h.selected
	for range 70 {
		other.record.Revision++
		h.f.service.avatarReference(h.ctx, h.repoID, other, "19", "https://avatars.githubusercontent.com/u/19?v=4")
	}
	h.f.service.prAvatarMu.Lock()
	defer h.f.service.prAvatarMu.Unlock()
	if h.f.service.prAvatars[h.reference] != nil {
		t.Fatal("reference was not evicted")
	}
}

func (h *avatarReadHarness) read() (*connect.Response[pb.ReadPullRequestAvatarResponse], error) {
	return h.f.service.ReadPullRequestAvatar(h.ctx, connect.NewRequest(&pb.ReadPullRequestAvatarRequest{RepositoryId: string(h.repoID), ExpectedRevision: h.selected.record.Revision, Reference: h.reference}))
}

func TestPRAvatarReadFetchesOnceAndServesSecondReadFromCache(t *testing.T) {
	h := newAvatarReadHarness(t)
	fetches := 0
	h.f.service.githubAvatars = func(_ context.Context, locator, actorID string) ([]byte, error) {
		fetches++
		if locator != "https://avatars.githubusercontent.com/u/19?v=4" || actorID != "19" {
			t.Errorf("unexpected fetch %s %s", locator, actorID)
		}
		return []byte("png-bytes"), nil
	}
	for range 2 {
		response, e := h.read()
		if e != nil || string(response.Msg.Png) != "png-bytes" {
			t.Fatal("avatar read failed", e)
		}
	}
	if fetches != 1 {
		t.Fatalf("second read was not served from cache: %d fetches", fetches)
	}
}

func TestPRAvatarReadDeniesReferenceEvictedBeforeFetch(t *testing.T) {
	h := newAvatarReadHarness(t)
	fetches := 0
	h.f.service.githubAvatars = func(context.Context, string, string) ([]byte, error) {
		fetches++
		return []byte("png-bytes"), nil
	}
	h.evict(t)
	response, e := h.read()
	if connect.CodeOf(e) != connect.CodePermissionDenied || response != nil || fetches != 0 {
		t.Fatal("pre-evicted reference was not denied before fetch", e, fetches)
	}
}

func TestPRAvatarReadDeniesReferenceEvictedDuringFetch(t *testing.T) {
	h := newAvatarReadHarness(t)
	started, release := make(chan struct{}), make(chan struct{})
	h.f.service.githubAvatars = func(context.Context, string, string) ([]byte, error) {
		close(started)
		<-release
		return []byte("png-bytes"), nil
	}
	type result struct {
		response *connect.Response[pb.ReadPullRequestAvatarResponse]
		err      error
	}
	done := make(chan result, 1)
	go func() {
		response, e := h.read()
		done <- result{response, e}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("avatar fetch did not start")
	}
	h.evict(t)
	close(release)
	select {
	case r := <-done:
		if connect.CodeOf(r.err) != connect.CodePermissionDenied || r.response != nil {
			t.Fatal("evicted reference returned bytes", r.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("avatar read did not finish")
	}
	h.f.service.prAvatarMu.Lock()
	defer h.f.service.prAvatarMu.Unlock()
	for _, entry := range h.f.service.prAvatars {
		if len(entry.png) != 0 {
			t.Fatal("evicted fetch result was published to the cache")
		}
	}
}

func TestPRWorkspaceSeedCeilingIsFifty(t *testing.T) {
	s := &Service{}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice, DeviceID: domain.NewID()})
	seeds := func(count int) []string {
		out := make([]string, 0, count)
		for n := 1; n <= count; n++ {
			out = append(out, strconv.Itoa(n))
		}
		return out
	}
	for _, bad := range [][]string{nil, seeds(51), {"1", "1"}, {"01"}} {
		_, e := s.GetPullRequestWorkspace(ctx, connect.NewRequest(&pb.GetPullRequestWorkspaceRequest{RepositoryId: string(domain.NewID()), SeedNumbers: bad}))
		if connect.CodeOf(e) != connect.CodeInvalidArgument {
			t.Fatalf("seed list of %d was not rejected as invalid: %v", len(bad), e)
		}
	}
}
