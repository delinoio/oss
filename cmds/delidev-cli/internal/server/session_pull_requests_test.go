package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	gh "github.com/delinoio/oss/cmds/delidev-cli/internal/integrations/github"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func sessionPRFixture(t *testing.T) (*integrationFixture, domain.ID, domain.ID, *pb.Resource) {
	t.Helper()
	f := newIntegrationFixture(t)
	profile := f.replace(profileMutation(f.save("PR lookup")), "private-link-fixture")
	repository := f.repository(domain.ID(profile.Profile.Id))
	session, project := domain.NewID(), domain.NewID()
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "test.scope", session, func(tx *store.Tx) (any, error) {
		if _, err := tx.Put(domain.ProjectKind, project, 0, "", "", domain.Project{Name: "PR project", Repositories: []domain.ID{domain.ID(repository.Id)}}); err != nil {
			return nil, err
		}
		return tx.Put(domain.SessionKind, session, 0, session, project, domain.Session{Name: "PR session", ProjectID: project, Archive: domain.NotArchived, Outcome: domain.ExecutionNotStarted, Recovery: domain.NoRecovery, Dispatch: domain.DispatchPaused})
	})
	if err != nil {
		t.Fatal(err)
	}
	return f, session, project, repository
}
func linkObservation(q domain.RepositoryQuery) gh.RepositoryQueryObservation {
	v := queryObservation()
	body, no, now := "Private original body excluded from association", false, time.Now().UTC()
	v.Items = []domain.RepositoryItem{{Provider: domain.GitHubCom, Kind: domain.RepositoryPullRequest, IdentitySource: domain.RepositoryPullRequestIdentity, ID: "9007199254740993", NodeID: "PR_17", Number: q.Number, Title: "Retained PR title", State: domain.RepositoryItemOpen, CreatedAt: now, UpdatedAt: now, URL: domain.RepositoryItemURL(v.Repository.Owner, v.Repository.Name, domain.RepositoryPullRequest, q.Number), Body: &body, Draft: &no, Merged: &no, BaseRef: "main", BaseSHA: strings.Repeat("a", 40), HeadRef: "feature", HeadSHA: strings.Repeat("b", 40)}}
	return v
}
func linkRequest(session domain.ID, repository *pb.Resource, number string) *pb.LinkSessionPullRequestRequest {
	return &pb.LinkSessionPullRequestRequest{RequestId: string(domain.NewID()), SessionId: string(session), RepositoryId: repository.Id, Number: number}
}

func TestSessionPRLinksRetainAcrossArchiveRestartAndUnlink(t *testing.T) {
	f, session, project, repository := sessionPRFixture(t)
	calls := 0
	f.service.githubQueries = queryFunc(func(_ context.Context, token []byte, _, _ string, q domain.RepositoryQuery) (gh.RepositoryQueryObservation, error) {
		calls++
		if string(token) != "private-link-fixture" || q.Operation != domain.RepositoryDetail || q.Kind != domain.RepositoryPullRequest {
			t.Error("lookup selection changed")
		}
		v := linkObservation(q)
		v.Items[0].ID, v.Items[0].NodeID = "90071992547409"+q.Number, "PR_"+q.Number
		return v, nil
	})
	client := delidevv1connect.NewSessionServiceClient(http.DefaultClient, f.url)
	original := linkRequest(session, repository, "17")
	r, err := client.LinkSessionPullRequest(context.Background(), ownerRequest(f.service.Identity, original))
	if err != nil || r.Msg.Association.Id != original.RequestId {
		t.Fatal("link failed", err)
	}
	var retained domain.SessionPullRequest
	if domain.Decode(r.Msg.Association.DocumentJson, &retained) != nil || retained.Validate() != nil || retained.PullRequestID != "9007199254740917" || strings.Contains(string(r.Msg.Association.DocumentJson), "Private original body") {
		t.Fatal("lost or leaked original provenance")
	}
	if _, err := client.LinkSessionPullRequest(context.Background(), ownerRequest(f.service.Identity, linkRequest(session, repository, "18"))); err != nil {
		t.Fatal(err)
	}
	if _, err := client.LinkSessionPullRequest(context.Background(), ownerRequest(f.service.Identity, linkRequest(session, repository, "17"))); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("duplicate stable PR accepted", err)
	}
	archive, err := client.ControlSession(context.Background(), ownerRequest(f.service.Identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{Id: string(session), ExpectedRevision: 1, RequestId: string(domain.NewID())}, Action: pb.SessionAction_SESSION_ACTION_ARCHIVE}))
	if err != nil {
		t.Fatal("link unexpectedly changed session revision or dispatch", err)
	}
	var state domain.Session
	if domain.Decode(archive.Msg.Change.Session.DocumentJson, &state) != nil || state.Archive != domain.Archived || state.Dispatch != domain.DispatchPaused {
		t.Fatal("archive fixture failed")
	}
	f.restart()
	client = delidevv1connect.NewSessionServiceClient(http.DefaultClient, f.url)
	// The restarted service has no scripted query adapter: exact replay must
	// return retained metadata without any GitHub call or credential retrieval.
	replayed, err := client.LinkSessionPullRequest(context.Background(), ownerRequest(f.service.Identity, original))
	if err != nil || !replayed.Msg.Replayed || string(replayed.Msg.Association.DocumentJson) != string(r.Msg.Association.DocumentJson) || calls != 3 {
		t.Fatal("restart replay changed original association", err)
	}
	resources := delidevv1connect.NewResourceServiceClient(http.DefaultClient, f.url)
	listed, err := resources.ListResources(context.Background(), ownerRequest(f.service.Identity, &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_PULL_REQUEST, SessionId: string(session), PageSize: 50}}))
	if err != nil || len(listed.Msg.Resources) != 2 {
		t.Fatal("Archive lost links", err)
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "test.remove.project", project, func(tx *store.Tx) (any, error) { return nil, tx.Delete(domain.ProjectKind, project, 1) })
	if err != nil {
		t.Fatal(err)
	}
	remove := &pb.UnlinkSessionPullRequestRequest{SessionId: string(session), Mutation: &pb.Mutation{Id: r.Msg.Association.Id, ExpectedRevision: r.Msg.Association.Revision, RequestId: string(domain.NewID())}}
	for i := range 2 {
		deleted, err := client.UnlinkSessionPullRequest(context.Background(), ownerRequest(f.service.Identity, remove))
		if err != nil || deleted.Msg.Id != r.Msg.Association.Id || deleted.Msg.Replayed != (i == 1) {
			t.Fatal("unlink/replay after project removal failed", err)
		}
	}
	if _, err := client.LinkSessionPullRequest(context.Background(), ownerRequest(f.service.Identity, original)); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal("old request resurrected link", err)
	}
	changed := &pb.LinkSessionPullRequestRequest{RequestId: original.RequestId, SessionId: original.SessionId, RepositoryId: original.RepositoryId, Number: "19"}
	if _, err := client.LinkSessionPullRequest(context.Background(), ownerRequest(f.service.Identity, changed)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("changed retry accepted", err)
	}
}

func TestSessionPRLinkRechecksProjectScopeAndDeniesForeignActors(t *testing.T) {
	f, session, project, repository := sessionPRFixture(t)
	request := linkRequest(session, repository, "17")
	for _, ctx := range []context.Context{context.Background(), domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: domain.NewID()})} {
		if _, err := f.service.LinkSessionPullRequest(ctx, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodePermissionDenied && connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatal("unauthorized link", err)
		}
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	f.service.githubQueries = queryFunc(func(_ context.Context, _ []byte, _, _ string, q domain.RepositoryQuery) (gh.RepositoryQueryObservation, error) {
		_, err := f.service.Store.Mutate(ctx, domain.NewID(), "test.project.edit", project, func(tx *store.Tx) (any, error) {
			return tx.Put(domain.ProjectKind, project, 1, "", "", domain.Project{Name: "Changed", Repositories: []domain.ID{domain.ID(repository.Id)}})
		})
		if err != nil {
			t.Error(err)
		}
		return linkObservation(q), nil
	})
	client := delidevv1connect.NewSessionServiceClient(http.DefaultClient, f.url)
	if _, err := client.LinkSessionPullRequest(context.Background(), ownerRequest(f.service.Identity, request)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("late changed project accepted", err)
	}
	rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.PullRequestKind, SessionID: session, Limit: 100})
	if err != nil || len(rows) != 0 {
		t.Fatal("partial association persisted", err)
	}
	var configured domain.Repository
	if domain.Decode(repository.DocumentJson, &configured) != nil {
		t.Fatal("repository fixture")
	}
	foreign := f.repository(configured.IntegrationID)
	if _, err := client.LinkSessionPullRequest(context.Background(), ownerRequest(f.service.Identity, linkRequest(session, foreign, "17"))); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("repository outside project accepted", err)
	}
}

func TestSessionPRLinkProfileDeletionJoinsBeforeRejecting(t *testing.T) {
	f, session, _, repository := sessionPRFixture(t)
	started := make(chan struct{})
	var captured []byte
	f.service.githubQueries = queryFunc(func(ctx context.Context, token []byte, _, _ string, _ domain.RepositoryQuery) (gh.RepositoryQueryObservation, error) {
		captured = token
		close(started)
		<-ctx.Done()
		return gh.RepositoryQueryObservation{}, ctx.Err()
	})
	client := delidevv1connect.NewSessionServiceClient(http.DefaultClient, f.url)
	done := make(chan error, 1)
	go func() {
		_, err := client.LinkSessionPullRequest(context.Background(), ownerRequest(f.service.Identity, linkRequest(session, repository, "17")))
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("lookup did not start")
	}
	var repo domain.Repository
	if domain.Decode(repository.DocumentJson, &repo) != nil {
		t.Fatal("repository fixture")
	}
	profile, err := f.service.Store.Get(context.Background(), domain.IntegrationKind, repo.IntegrationID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.client.DeleteIntegrationProfile(context.Background(), ownerRequest(f.service.Identity, &pb.DeleteIntegrationProfileRequest{Mutation: &pb.Mutation{Id: string(profile.ID), ExpectedRevision: profile.Revision, RequestId: string(domain.NewID())}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("deleted generation created link")
	}
	for _, b := range captured {
		if b != 0 {
			t.Fatal("lookup token outlived deletion")
		}
	}
}

func TestSessionPRLinkCapacityIsAtomic(t *testing.T) {
	f, session, project, repository := sessionPRFixture(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "test.links.fill", session, func(tx *store.Tx) (any, error) {
		for n := 1; n <= domain.MaxSessionPullRequests; n++ {
			v := domain.SessionPullRequest{Version: 1, Provider: domain.GitHubCom, RepositoryID: domain.ID(repository.Id), RemoteRepositoryID: "37", RepositoryNodeID: "R_37", Owner: "fixture-owner", Name: "repo", PullRequestID: strconv.Itoa(n), PullRequestNodeID: "PR_" + strconv.Itoa(n), Number: strconv.Itoa(n), Title: "Retained", ObservedAt: time.Now().UTC()}
			if _, err := tx.Put(domain.PullRequestKind, domain.NewID(), 0, session, project, v); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	f.service.githubQueries = queryFunc(func(_ context.Context, _ []byte, _, _ string, q domain.RepositoryQuery) (gh.RepositoryQueryObservation, error) {
		v := linkObservation(q)
		v.Items[0].NodeID = "PR_101"
		return v, nil
	})
	client := delidevv1connect.NewSessionServiceClient(http.DefaultClient, f.url)
	if _, err := client.LinkSessionPullRequest(context.Background(), ownerRequest(f.service.Identity, linkRequest(session, repository, "101"))); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatal("capacity overflow", err)
	}
	rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.PullRequestKind, SessionID: session, Limit: 101})
	if err != nil || len(rows) != 100 {
		t.Fatal("failed create changed retained links", err)
	}
}

func TestSessionPRLinksSpanProjectRepositoriesWithIndependentStableIdentities(t *testing.T) {
	f, session, project, first := sessionPRFixture(t)
	var configured domain.Repository
	if domain.Decode(first.DocumentJson, &configured) != nil {
		t.Fatal("repository fixture")
	}
	second := f.repository(configured.IntegrationID)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "test.multirepo", second.Id, func(tx *store.Tx) (any, error) {
		other := configured
		other.Name = "Second repository"
		other.GitHubName = "other"
		if _, err := tx.Put(domain.RepositoryKind, domain.ID(second.Id), second.Revision, "", "", other); err != nil {
			return nil, err
		}
		return tx.Put(domain.ProjectKind, project, 1, "", "", domain.Project{Name: "Multiple", Repositories: []domain.ID{domain.ID(first.Id), domain.ID(second.Id)}})
	})
	if err != nil {
		t.Fatal(err)
	}
	f.service.githubQueries = queryFunc(func(_ context.Context, _ []byte, _, name string, q domain.RepositoryQuery) (gh.RepositoryQueryObservation, error) {
		v := linkObservation(q)
		if name == "other" {
			v.Repository.ID, v.Repository.NodeID, v.Repository.Name = "38", "R_38", name
			v.Items[0].ID, v.Items[0].NodeID = "9007199254740994", "PR_OTHER_17"
			v.Items[0].URL = domain.RepositoryItemURL(v.Repository.Owner, name, domain.RepositoryPullRequest, q.Number)
		}
		return v, nil
	})
	client := delidevv1connect.NewSessionServiceClient(http.DefaultClient, f.url)
	for _, repo := range []*pb.Resource{first, second} {
		if _, err := client.LinkSessionPullRequest(context.Background(), ownerRequest(f.service.Identity, linkRequest(session, repo, "17"))); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.PullRequestKind, SessionID: session, Limit: 100})
	if err != nil || len(rows) != 2 {
		t.Fatal("lost cross-repository associations", err)
	}
	a, _ := store.Decode[domain.SessionPullRequest](rows[0])
	b, _ := store.Decode[domain.SessionPullRequest](rows[1])
	if a.SamePR(b) || a.Number != b.Number || a.RepositoryID == b.RepositoryID {
		t.Fatal("PR number used as global identity")
	}
}
