package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func feedbackConnection(rows ...any) map[string]any {
	return map[string]any{"totalCount": len(rows), "nodes": rows, "pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil}}
}
func feedbackFixtureRow(kind domain.PRFeedbackKind, id string) map[string]any {
	fragment, state := "issuecomment-", ""
	if kind == domain.PRReviewBody {
		fragment, state = "pullrequestreview-", "APPROVED"
	}
	if kind == domain.PRReviewComment {
		fragment, state = "discussion_r", "SUBMITTED"
	}
	row := map[string]any{"id": string(kind) + "_" + id, "fullDatabaseId": id, "body": "<script>inert feedback</script>", "publishedAt": "2026-09-28T01:00:00Z", "lastEditedAt": nil, "url": "https://github.com/fixture-owner/repo/pull/17#" + fragment + id, "pullRequest": map[string]any{"id": "ITEM_stable"}, "repository": map[string]any{"id": "R_37"}, "author": map[string]any{"__typename": "Bot", "id": "BOT_1", "databaseId": 199175422, "login": "chatgpt-codex-connector"}}
	if state != "" {
		row["state"] = state
	}
	if kind == domain.PRReviewBody {
		row["submittedAt"] = "2026-09-28T01:00:00Z"
	}
	if kind == domain.PRReviewComment {
		row["pullRequestReview"] = map[string]any{"id": "review_9007199254740993", "state": "APPROVED", "submittedAt": "2026-09-28T01:00:00Z"}
		row["path"], row["diffHunk"], row["line"], row["originalLine"] = "file.go", "@@ -1 +1 @@\n-old\n+new", nil, 1
	}
	return row
}
func feedbackNodeFixture(operation string) map[string]any {
	node := ciNodeFixture()
	delete(node, "potentialMergeCommit")
	delete(node, "statusCheckRollup")
	delete(node, "mergeable")
	delete(node, "isInMergeQueue")
	switch operation {
	case "DeliDevPRReviews":
		node["reviews"] = feedbackConnection(feedbackFixtureRow(domain.PRReviewBody, "9007199254740993"))
	case "DeliDevPRConversation":
		node["comments"] = feedbackConnection(feedbackFixtureRow(domain.PRConversationComment, "51"))
	case "DeliDevPRThreads":
		thread := map[string]any{"id": "THREAD_1", "isResolved": false, "isOutdated": false, "pullRequest": feedbackNodeFixture("binding"), "repository": map[string]any{"id": "R_37"}, "comments": feedbackConnection(feedbackFixtureRow(domain.PRReviewComment, "53"))}
		node["reviewThreads"] = feedbackConnection(thread)
	}
	return node
}
func feedbackClientFixture(t *testing.T, mutate func(string, int, map[string]any, map[string]json.RawMessage)) (*Client, *int) {
	t.Helper()
	c, _ := rulesFixtureClient(t, nil)
	original := c.http.Transport
	reads := 0
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/graphql" {
			return original.RoundTrip(r)
		}
		if r.Method != "POST" || r.URL.Host != "api.github.com" || r.Header.Get("Authorization") != "Bearer private-fixture-pat" {
			t.Fatal("unscoped feedback request")
		}
		var request struct {
			Query         string                     `json:"query"`
			OperationName string                     `json:"operationName"`
			Variables     map[string]json.RawMessage `json:"variables"`
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil || domain.Decode(raw, &request) != nil {
			t.Fatal("bad request", err)
		}
		allowed := map[string]string{"DeliDevPRReviews": feedbackReviewsGraphQL, "DeliDevPRConversation": feedbackConversationGraphQL, "DeliDevPRThreads": feedbackThreadsGraphQL, "DeliDevPRThreadComments": feedbackThreadCommentsGraphQL}
		if allowed[request.OperationName] == "" || request.Query != allowed[request.OperationName] {
			t.Fatal("arbitrary GraphQL document")
		}
		id := "ITEM_stable"
		if request.OperationName == "DeliDevPRThreadComments" {
			id = "THREAD_1"
		}
		if stringField(request.Variables, "id") != id {
			t.Fatal("foreign query node")
		}
		reads++
		node := feedbackNodeFixture(request.OperationName)
		if mutate != nil {
			mutate(request.OperationName, reads, node, request.Variables)
		}
		raw, _ = json.Marshal(map[string]any{"data": map[string]any{"node": node}})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	return c, &reads
}
func TestFeedbackQueryRetainsApprovedBodiesCommentsAndThreads(t *testing.T) {
	c, reads := feedbackClientFixture(t, nil)
	v, err := c.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryFeedback, Number: "17"})
	if err != nil || *reads != 6 || v.Feedback == nil || len(v.Feedback.Entries) != 3 || v.Feedback.Validate(v.Items[0]) != nil {
		t.Fatal("incomplete feedback", err, *reads)
	}
	for _, entry := range v.Feedback.Entries {
		if entry.Author.Kind != domain.RepositoryBot || entry.Author.ID != "199175422" || entry.ContentVersion != entry.Version() {
			t.Fatal("lost original author/version")
		}
	}
}
func TestFeedbackExcludesDraftContentAndRetainsDismissedPublishedFeedback(t *testing.T) {
	c, _ := feedbackClientFixture(t, func(op string, _ int, node map[string]any, _ map[string]json.RawMessage) {
		switch op {
		case "DeliDevPRReviews":
			review := feedbackFixtureRow(domain.PRReviewBody, "9007199254740993")
			review["state"] = "DISMISSED"
			draft := feedbackFixtureRow(domain.PRReviewBody, "59")
			draft["state"], draft["submittedAt"], draft["publishedAt"], draft["body"] = "PENDING", nil, nil, "private draft never returned"
			node["reviews"] = feedbackConnection(review, draft)
		case "DeliDevPRThreads":
			thread := node["reviewThreads"].(map[string]any)["nodes"].([]any)[0].(map[string]any)
			comment := feedbackFixtureRow(domain.PRReviewComment, "53")
			comment["pullRequestReview"].(map[string]any)["state"] = "DISMISSED"
			draft := feedbackFixtureRow(domain.PRReviewComment, "61")
			draft["state"], draft["publishedAt"], draft["body"] = "PENDING", nil, "private draft never returned"
			draft["pullRequestReview"] = map[string]any{"id": "review_59", "state": "PENDING", "submittedAt": nil}
			thread["isResolved"] = true
			thread["comments"] = feedbackConnection(comment, draft)
		}
	})
	repo, item := ciFixtureItem(t)
	v, err := c.readFeedbackInventory(context.Background(), []byte("private-fixture-pat"), repo, item)
	if err != nil || v.ExcludedDraftReviews != 1 || v.Threads[0].ExcludedDrafts != 1 || !v.Threads[0].Resolved || len(v.Entries) != 3 {
		t.Fatal("draft filtering failed", err)
	}
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), "private draft") {
		t.Fatal("unsubmitted content published")
	}
}
func TestFeedbackRejectsChangedEditedPartialAndForeignInventory(t *testing.T) {
	for _, mode := range []string{"edit", "state-change", "foreign-head", "foreign-item", "null-node", "missing-body", "nonstring-body", "partial", "duplicate", "capacity", "unknown-state", "foreign-review"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := feedbackClientFixture(t, func(op string, n int, node map[string]any, _ map[string]json.RawMessage) {
				if op != "DeliDevPRReviews" {
					return
				}
				conn := node["reviews"].(map[string]any)
				row := conn["nodes"].([]any)[0].(map[string]any)
				switch mode {
				case "edit":
					if n > 3 {
						row["lastEditedAt"] = "2026-09-28T01:01:00Z"
					}
				case "state-change":
					if n > 3 {
						row["state"] = "DISMISSED"
					}
				case "foreign-head":
					node["headRefOid"] = repositorySHA
				case "foreign-item":
					row["pullRequest"] = map[string]any{"id": "OTHER"}
				case "null-node":
					conn["nodes"] = []any{nil}
				case "missing-body":
					delete(row, "body")
				case "nonstring-body":
					row["body"] = false
				case "partial":
					conn["totalCount"] = 2
				case "duplicate":
					conn["totalCount"] = 2
					conn["nodes"] = []any{row, row}
				case "capacity":
					conn["totalCount"] = 501
				case "unknown-state":
					row["state"] = "FUTURE_STATE"
				case "foreign-review":
					row["id"] = "OTHER_REVIEW"
				}
			})
			_, err := c.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryFeedback, Number: "17"})
			if err == nil {
				t.Fatal("mixed/partial feedback published")
			}
		})
	}
}
func TestFeedbackNestedPaginationAndCursorChecks(t *testing.T) {
	c, _ := feedbackClientFixture(t, func(op string, _ int, node map[string]any, vars map[string]json.RawMessage) {
		if op == "DeliDevPRThreads" {
			conn := node["reviewThreads"].(map[string]any)["nodes"].([]any)[0].(map[string]any)["comments"].(map[string]any)
			conn["totalCount"], conn["pageInfo"] = 2, map[string]any{"hasNextPage": true, "endCursor": "comments-next"}
		}
		if op == "DeliDevPRThreadComments" {
			if stringField(vars, "after") != "comments-next" {
				t.Fatal("lost nested cursor")
			}
			for key := range node {
				delete(node, key)
			}
			for key, value := range map[string]any{"id": "THREAD_1", "isResolved": false, "isOutdated": false, "pullRequest": feedbackNodeFixture("binding"), "repository": map[string]any{"id": "R_37"}, "comments": feedbackConnection(feedbackFixtureRow(domain.PRReviewComment, "55"))} {
				node[key] = value
			}
			node["comments"].(map[string]any)["totalCount"] = 2
		}
	})
	repo, item := ciFixtureItem(t)
	v, err := c.readFeedbackInventory(context.Background(), []byte("private-fixture-pat"), repo, item)
	if err != nil || len(v.Entries) != 4 || len(v.Threads[0].CommentNodes) != 2 {
		t.Fatal("nested comments truncated", err)
	}
	pages := feedbackPages{}
	for i := 0; i < 2; i++ {
		conn := feedbackConnection(map[string]any{"id": "NODE_" + strconv.Itoa(i)})
		conn["totalCount"] = 3
		conn["pageInfo"] = map[string]any{"hasNextPage": true, "endCursor": "same"}
		raw, _ := json.Marshal(conn)
		_, _, err := pages.append(raw)
		if (i == 0) != (err == nil) {
			t.Fatal("repeated cursor handling failed", err)
		}
	}
}
func TestFeedbackCancellationJoinsRequest(t *testing.T) {
	c, _ := feedbackClientFixture(t, nil)
	started, finished := make(chan struct{}), make(chan struct{})
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		close(finished)
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	repo, item := ciFixtureItem(t)
	go func() { _, err := c.readFeedbackInventory(ctx, []byte("private-fixture-pat"), repo, item); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("request not joined")
	}
	select {
	case <-finished:
	default:
		t.Fatal("reader still running")
	}
}
func TestFeedbackPublicCaptureProjection(t *testing.T) {
	prefix := os.Getenv("DELIDEV_GITHUB_FEEDBACK_FIXTURE_PREFIX")
	if prefix == "" {
		t.Skip("explicit read-only public captures only")
	}
	detail, err := os.ReadFile(prefix + "detail.json")
	if err != nil {
		t.Fatal(err)
	}
	repo := domain.RemoteRepository{Provider: domain.GitHubCom, ID: "1158679171", NodeID: "R_kgDORRAKgw", Owner: "delinoio", Name: "oss"}
	item, _, err := parseItem(detail, repo, domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryDetail, Number: "1016"})
	if err != nil {
		t.Fatal(err)
	}
	c := New()
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		var request struct {
			OperationName string `json:"operationName"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Fatal("request decode")
		}
		raw, err := os.ReadFile(prefix + request.OperationName + ".json")
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	v, err := c.readFeedbackInventory(context.Background(), []byte("private-fixture-pat"), repo, item)
	if err != nil || len(v.Entries) != 4 || len(v.Threads) != 1 {
		t.Fatal("public projection failed", err)
	}
}
