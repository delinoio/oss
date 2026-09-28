package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	gh "github.com/delinoio/oss/cmds/delidev-cli/internal/integrations/github"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type accessFunc func(context.Context, []byte, string, string) (gh.RepositoryAccessObservation, error)

func (f accessFunc) InspectRepository(ctx context.Context, token []byte, owner, name string) (gh.RepositoryAccessObservation, error) {
	return f(ctx, token, owner, name)
}
func accessObservation() gh.RepositoryAccessObservation {
	value := gh.RepositoryAccessObservation{Identity: &domain.GitHubIdentity{ID: "17", NodeID: "U_17", Login: "fixture-user"}, Repository: &domain.RemoteRepository{Provider: domain.GitHubCom, ID: "37", NodeID: "R_37", Owner: "fixture-owner", Name: "repo", Private: true, DefaultBranch: "main", HeadCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
	for _, feature := range domain.IntegrationFeatures() {
		value.Features = append(value.Features, domain.IntegrationFeatureAccess{Feature: feature, State: domain.IntegrationAccessAvailable})
	}
	return value
}
func (f *integrationFixture) repository(profile domain.ID) *pb.Resource {
	f.t.Helper()
	id := domain.NewID()
	var record store.Record
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	// This fixture supplies stored configuration only; it makes no Worker or
	// native checkout preparation claim and never touches the placeholder path.
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "test.repository", id, func(tx *store.Tx) (any, error) {
		var e error
		record, e = tx.Put(domain.RepositoryKind, id, 0, "", "", domain.Repository{Name: "Fixture repository", Checkouts: []domain.Checkout{{MachineID: domain.NewID(), Path: "/fixture/unused"}}, GitHubOwner: "fixture-owner", GitHubName: "repo", IntegrationID: profile})
		return record, e
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return rpc.Resource(record)
}
func TestRepositoryIntegrationUsesOnlySelectedGenerationAndDoesNotPersistAccess(t *testing.T) {
	f := newIntegrationFixture(t)
	profile := f.replace(profileMutation(f.save("Selected")), "selected-token")
	other := f.replace(profileMutation(f.save("Unrelated")), "other-token")
	_ = other
	repo := f.repository(domain.ID(profile.Profile.Id))
	calls := 0
	f.service.githubAccess = accessFunc(func(_ context.Context, token []byte, owner, name string) (gh.RepositoryAccessObservation, error) {
		calls++
		if string(token) != "selected-token" || owner != "fixture-owner" || name != "repo" {
			t.Fatal("selection changed")
		}
		return accessObservation(), nil
	})
	for range 2 {
		reply, err := f.client.InspectRepositoryIntegration(context.Background(), ownerRequest(f.service.Identity, &pb.InspectRepositoryIntegrationRequest{RepositoryId: repo.Id}))
		if err != nil {
			t.Fatal(err)
		}
		var observed domain.RepositoryIntegrationAccess
		if err := domain.Decode(reply.Msg.DocumentJson, &observed); err != nil {
			t.Fatal(err)
		}
		if observed.Validate() != nil || observed.RepositoryID != domain.ID(repo.Id) || observed.ProfileID != domain.ID(profile.Profile.Id) || observed.GenerationID != integrationValue(t, profile.Profile).Connection.GenerationID {
			t.Fatal("foreign observation")
		}
	}
	if calls != 2 {
		t.Fatal("read-only inspection reused a stale receipt")
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	record, _, err := f.service.integrationRecord(ctx, domain.ID(profile.Profile.Id))
	if err != nil || record.Revision != profile.Profile.Revision {
		t.Fatal("read changed profile", err)
	}
	_, err = f.client.DeleteIntegrationProfile(context.Background(), ownerRequest(f.service.Identity, &pb.DeleteIntegrationProfileRequest{Mutation: profileMutation(profile.Profile)}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.client.InspectRepositoryIntegration(context.Background(), ownerRequest(f.service.Identity, &pb.InspectRepositoryIntegrationRequest{RepositoryId: repo.Id}))
	if connect.CodeOf(err) != connect.CodeNotFound || calls != 2 {
		t.Fatal("deleted profile fell back", err)
	}
	err = f.service.Store.Read(ctx, func(tx *store.Tx) error {
		row, e := tx.Get(domain.RepositoryKind, domain.ID(repo.Id))
		if e != nil {
			return e
		}
		value, e := store.Decode[domain.Repository](row)
		if e == nil && (value.IntegrationID != domain.ID(profile.Profile.Id) || row.Revision != repo.Revision) {
			t.Fatal("association silently rewritten")
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestRepositoryIntegrationLateResultsCannotCrossConfigurationRevision(t *testing.T) {
	f := newIntegrationFixture(t)
	profile := f.replace(profileMutation(f.save("Selected")), "selected-token")
	repo := f.repository(domain.ID(profile.Profile.Id))
	started, release := make(chan struct{}), make(chan struct{})
	f.service.githubAccess = accessFunc(func(ctx context.Context, _ []byte, _, _ string) (gh.RepositoryAccessObservation, error) {
		close(started)
		select {
		case <-release:
			return accessObservation(), nil
		case <-ctx.Done():
			return gh.RepositoryAccessObservation{}, ctx.Err()
		}
	})
	done := make(chan error, 1)
	go func() {
		_, err := f.client.InspectRepositoryIntegration(context.Background(), ownerRequest(f.service.Identity, &pb.InspectRepositoryIntegrationRequest{RepositoryId: repo.Id}))
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("inspection not started")
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "test.rename", repo.Id, func(tx *store.Tx) (any, error) {
		row, e := tx.Get(domain.RepositoryKind, domain.ID(repo.Id))
		if e != nil {
			return nil, e
		}
		value, e := store.Decode[domain.Repository](row)
		if e != nil {
			return nil, e
		}
		value.Name = "Changed"
		return tx.Put(domain.RepositoryKind, row.ID, row.Revision, "", "", value)
	})
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("late observation accepted", err)
	}
}
func TestRepositoryIntegrationDeletionJoinsTheOriginalLookup(t *testing.T) {
	f := newIntegrationFixture(t)
	profile := f.replace(profileMutation(f.save("Selected")), "selected-token")
	repo := f.repository(domain.ID(profile.Profile.Id))
	started, stopped := make(chan struct{}), make(chan struct{})
	f.service.githubAccess = accessFunc(func(ctx context.Context, _ []byte, _, _ string) (gh.RepositoryAccessObservation, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return gh.RepositoryAccessObservation{}, ctx.Err()
	})
	done := make(chan error, 1)
	go func() {
		_, err := f.client.InspectRepositoryIntegration(context.Background(), ownerRequest(f.service.Identity, &pb.InspectRepositoryIntegrationRequest{RepositoryId: repo.Id}))
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("inspection not started")
	}
	reply, err := f.client.DeleteIntegrationProfile(context.Background(), ownerRequest(f.service.Identity, &pb.DeleteIntegrationProfileRequest{Mutation: profileMutation(profile.Profile)}))
	if err != nil || !reply.Msg.Deleted {
		t.Fatal("delete failed", err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("deletion did not join lookup")
	}
	if err := <-done; err == nil {
		t.Fatal("canceled read published")
	}
}
func TestRepositoryIntegrationRejectsMissingOwnerOrProfileBeforeNativeLookup(t *testing.T) {
	f := newIntegrationFixture(t)
	profile := f.replace(profileMutation(f.save("Selected")), "selected-token")
	repo := f.repository(domain.ID(profile.Profile.Id))
	var value domain.Repository
	if err := json.Unmarshal(repo.DocumentJson, &value); err != nil {
		t.Fatal(err)
	}
	value.GitHubOwner = "other-owner"
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "test.owner", repo.Id, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.RepositoryKind, domain.ID(repo.Id), repo.Revision, "", "", value)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.service.githubAccess = accessFunc(func(context.Context, []byte, string, string) (gh.RepositoryAccessObservation, error) {
		t.Error("foreign owner requested")
		return gh.RepositoryAccessObservation{}, nil
	})
	_, err = f.client.InspectRepositoryIntegration(context.Background(), ownerRequest(f.service.Identity, &pb.InspectRepositoryIntegrationRequest{RepositoryId: repo.Id}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("foreign owner accepted", err)
	}
}
