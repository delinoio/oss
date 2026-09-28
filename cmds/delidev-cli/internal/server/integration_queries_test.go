package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	gh "github.com/delinoio/oss/cmds/delidev-cli/internal/integrations/github"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type queryFunc func(context.Context, []byte, string, string, domain.RepositoryQuery) (gh.RepositoryQueryObservation, error)

func (f queryFunc) QueryRepository(ctx context.Context, token []byte, owner, name string, q domain.RepositoryQuery) (gh.RepositoryQueryObservation, error) {
	return f(ctx, token, owner, name, q)
}
func queryObservation() gh.RepositoryQueryObservation {
	access := accessObservation()
	return gh.RepositoryQueryObservation{Identity: *access.Identity, Repository: *access.Repository, Items: []domain.RepositoryItem{}}
}
func repositoryQueryRequest(id string) *pb.QueryRepositoryIntegrationRequest {
	raw, _ := json.Marshal(domain.RepositoryQuery{Kind: domain.RepositoryIssue, Operation: domain.RepositoryList, State: domain.RepositoryItemsAll, Page: 1, PageSize: 20})
	return &pb.QueryRepositoryIntegrationRequest{RepositoryId: id, SchemaVersion: 1, QueryJson: raw}
}
func TestRepositoryQueriesSelectedProfileReadAndForeignResponseRejection(t *testing.T) {
	f := newIntegrationFixture(t)
	profile := f.replace(profileMutation(f.save("Selected")), "selected-query-token")
	repo := f.repository(domain.ID(profile.Profile.Id))
	foreign := false
	calls := 0
	f.service.githubQueries = queryFunc(func(_ context.Context, token []byte, owner, name string, q domain.RepositoryQuery) (gh.RepositoryQueryObservation, error) {
		calls++
		if string(token) != "selected-query-token" || owner != "fixture-owner" || name != "repo" || q.Kind != domain.RepositoryIssue || q.Operation != domain.RepositoryList {
			t.Error("query selection changed")
		}
		value := queryObservation()
		if foreign {
			value.Repository.Owner = "foreign"
		}
		return value, nil
	})
	for range 2 {
		reply, err := f.client.QueryRepositoryIntegration(context.Background(), ownerRequest(f.service.Identity, repositoryQueryRequest(repo.Id)))
		if err != nil {
			t.Fatal(err)
		}
		var value domain.RepositoryQueryResult
		if domain.Decode(reply.Msg.DocumentJson, &value) != nil || value.Validate() != nil || value.RepositoryRevision != "1" || value.ProfileID != domain.ID(profile.Profile.Id) {
			t.Fatal("unbound query response")
		}
	}
	foreign = true
	if _, err := f.client.QueryRepositoryIntegration(context.Background(), ownerRequest(f.service.Identity, repositoryQueryRequest(repo.Id))); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("foreign response accepted", err)
	}
	request := repositoryQueryRequest(repo.Id)
	request.QueryJson = []byte(`{"kind":"issue","operation":"search","state":"all","search":"repo:foreign/repo","page":1,"page_size":20}`)
	if _, err := f.client.QueryRepositoryIntegration(context.Background(), ownerRequest(f.service.Identity, request)); err == nil || calls != 3 {
		t.Fatal("invalid query reached adapter", err, calls)
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
		row, err := tx.Get(domain.RepositoryKind, domain.ID(repo.Id))
		if err == nil && row.Revision != repo.Revision {
			t.Fatal("read mutated configuration")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
func TestRepositoryQueryDeletionJoinsReadAndClearsToken(t *testing.T) {
	f := newIntegrationFixture(t)
	profile := f.replace(profileMutation(f.save("Selected")), "selected-query-token")
	repo := f.repository(domain.ID(profile.Profile.Id))
	started := make(chan struct{})
	var captured []byte
	f.service.githubQueries = queryFunc(func(ctx context.Context, token []byte, _, _ string, _ domain.RepositoryQuery) (gh.RepositoryQueryObservation, error) {
		captured = token
		close(started)
		<-ctx.Done()
		return gh.RepositoryQueryObservation{}, ctx.Err()
	})
	done := make(chan error, 1)
	go func() {
		_, err := f.client.QueryRepositoryIntegration(context.Background(), ownerRequest(f.service.Identity, repositoryQueryRequest(repo.Id)))
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("query did not start")
	}
	reply, err := f.client.DeleteIntegrationProfile(context.Background(), ownerRequest(f.service.Identity, &pb.DeleteIntegrationProfileRequest{Mutation: profileMutation(profile.Profile)}))
	if err != nil || !reply.Msg.Deleted {
		t.Fatal("delete did not complete", err)
	}
	if err := <-done; err == nil {
		t.Fatal("canceled query published")
	}
	for _, b := range captured {
		if b != 0 {
			t.Fatal("token outlived deletion")
		}
	}
}

func TestRepositoryQueriesExposeOnlyMatchingPRObservationFamily(t *testing.T) {
	f := newIntegrationFixture(t)
	profile := f.replace(profileMutation(f.save("Selected")), "selected-query-token")
	repo := f.repository(domain.ID(profile.Profile.Id))
	invalid := false
	f.service.githubQueries = queryFunc(func(_ context.Context, _ []byte, _, _ string, q domain.RepositoryQuery) (gh.RepositoryQueryObservation, error) {
		value := queryObservation()
		body := "Original PR body"
		no := false
		now := time.Now().UTC()
		item := domain.RepositoryItem{Provider: domain.GitHubCom, Kind: domain.RepositoryPullRequest, IdentitySource: domain.RepositoryPullRequestIdentity, ID: "17", NodeID: "PR_17", Number: "17", Title: "PR fixture", State: domain.RepositoryItemOpen, CreatedAt: now, UpdatedAt: now, URL: domain.RepositoryItemURL("fixture-owner", "repo", domain.RepositoryPullRequest, "17"), Body: &body, Draft: &no, Merged: &no, BaseRef: "main", HeadRef: "feature", BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
		value.Items = []domain.RepositoryItem{item}
		switch q.Operation {
		case domain.RepositoryDiff:
			sum := sha256.Sum256(nil)
			value.Diff = &domain.PullRequestDiff{Patch: "", Digest: hex.EncodeToString(sum[:]), BaseSHA: item.BaseSHA, HeadSHA: item.HeadSHA}
		case domain.RepositoryChecks:
			value.Checks = &domain.PullRequestChecks{HeadSHA: item.HeadSHA, Filter: domain.LatestCheckRuns, TotalCount: "0", Runs: []domain.PullRequestCheck{}}
		case domain.RepositoryRules:
			value.Rules = &domain.PullRequestRules{BaseRef: item.BaseRef, BaseSHA: item.BaseSHA, HeadSHA: item.HeadSHA, Rules: []domain.ActiveRepositoryRule{}, Digest: domain.ActiveRulesDigest(nil)}
		case domain.RepositoryStatuses:
			value.Statuses = &domain.PullRequestCommitStatuses{HeadSHA: item.HeadSHA, State: domain.CommitStatusPending, NativeState: "pending", TotalCount: "0", Contexts: []domain.PullRequestCommitStatus{}}
		}
		if invalid {
			value.Diff = &domain.PullRequestDiff{}
		}
		return value, nil
	})
	for _, operation := range []domain.RepositoryQueryOperation{domain.RepositoryDiff, domain.RepositoryChecks, domain.RepositoryStatuses, domain.RepositoryRules} {
		query := domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: operation, Number: "17"}
		if operation != domain.RepositoryDiff && operation != domain.RepositoryRules {
			query.Page = 1
			query.PageSize = 20
		}
		raw, _ := json.Marshal(query)
		request := &pb.QueryRepositoryIntegrationRequest{RepositoryId: repo.Id, SchemaVersion: 1, QueryJson: raw}
		response, err := f.client.QueryRepositoryIntegration(context.Background(), ownerRequest(f.service.Identity, request))
		if err != nil {
			t.Fatal(operation, err)
		}
		var value domain.RepositoryQueryResult
		if domain.Decode(response.Msg.DocumentJson, &value) != nil || value.Validate() != nil || value.Query != query {
			t.Fatal("PR observation did not preserve query family")
		}
	}
	invalid = true
	query := domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryChecks, Number: "17", Page: 1, PageSize: 20}
	raw, _ := json.Marshal(query)
	if _, err := f.client.QueryRepositoryIntegration(context.Background(), ownerRequest(f.service.Identity, &pb.QueryRepositoryIntegrationRequest{RepositoryId: repo.Id, SchemaVersion: 1, QueryJson: raw})); err == nil {
		t.Fatal("mixed observation families published")
	}
}
