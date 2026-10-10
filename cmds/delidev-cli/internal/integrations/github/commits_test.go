package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func commitFixtureClient(t *testing.T, mutate func(map[string]any)) *Client {
	t.Helper()
	client, _ := rulesFixtureClient(t, nil)
	original := client.http.Transport
	client.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/graphql" {
			return original.RoundTrip(request)
		}
		var wire map[string]json.RawMessage
		raw, _ := io.ReadAll(request.Body)
		if json.Unmarshal(raw, &wire) != nil {
			t.Fatal("invalid GraphQL request")
		}
		if string(wire["operationName"]) != `"DeliDevPRCommits"` || strings.Contains(string(wire["query"]), "email") || request.Header.Get("Authorization") != "Bearer private-fixture-pat" {
			t.Fatal("unscoped commit document")
		}
		node := feedbackNodeFixture("binding")
		commit := map[string]any{"oid": strings.Repeat("c", 40), "message": "Full headline\n\nOriginal full message", "additions": 0, "deletions": 9, "authoredDate": "2026-09-01T00:00:00Z", "committedDate": "2026-09-02T00:00:00Z", "author": map[string]any{"name": "Original author", "user": nil}, "committer": map[string]any{"name": "Original committer"}, "parents": map[string]any{"nodes": []any{map[string]any{"oid": strings.Repeat("a", 40)}}, "pageInfo": map[string]any{"hasNextPage": false}}}
		if mutate != nil {
			mutate(commit)
		}
		node["commits"] = map[string]any{"nodes": []any{map[string]any{"commit": commit}}, "pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil}}
		reply, _ := json.Marshal(map[string]any{"data": map[string]any{"node": node}})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(reply)))}, nil
	})
	return client
}
func TestPRWorkspaceCommitsRetainFullMessageAndProviderCounts(t *testing.T) {
	client := commitFixtureClient(t, nil)
	page, next, err := client.PullRequestCommits(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", 17, repositorySHA, strings.Repeat("b", 40), "")
	if err != nil || next != "" || len(page.Commits) != 1 {
		t.Fatalf("commit read: %+v %s %v", page, next, err)
	}
	commit := page.Commits[0]
	if commit.Message != "Full headline\n\nOriginal full message" || commit.Author != "Original author" || commit.Counts.Additions != 0 || commit.Counts.Deletions != 9 || len(commit.Parents) != 1 {
		t.Fatalf("changed original commit: %+v", commit)
	}
	if _, _, err = client.PullRequestCommits(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", 17, repositorySHA, strings.Repeat("c", 40), ""); err == nil {
		t.Fatal("changed operands accepted")
	}
}
func TestPRWorkspaceCommitsRejectInvalidCountsAndTruncatedParents(t *testing.T) {
	for _, mutate := range []func(map[string]any){func(commit map[string]any) { commit["additions"] = -1 }, func(commit map[string]any) {
		commit["parents"].(map[string]any)["pageInfo"] = map[string]any{"hasNextPage": true}
	}} {
		client := commitFixtureClient(t, mutate)
		if _, _, err := client.PullRequestCommits(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", 17, repositorySHA, strings.Repeat("b", 40), ""); err == nil {
			t.Fatal("invalid commit observation accepted")
		}
	}
}
