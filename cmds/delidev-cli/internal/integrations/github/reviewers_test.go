package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func reviewerActorFixture() map[string]any {
	return map[string]any{"id": 199175422, "node_id": "BOT_1", "type": "Bot", "login": "chatgpt-codex-connector[bot]"}
}
func reviewerCommentFixture() map[string]any {
	return map[string]any{"id": 51, "node_id": "conversation-comment_51", "body": "<script>inert feedback</script>", "created_at": "2026-09-28T01:00:00Z", "html_url": "https://github.com/fixture-owner/repo/pull/17#issuecomment-51", "issue_url": "https://api.github.com/repos/fixture-owner/repo/issues/17", "user": reviewerActorFixture(), "performed_via_github_app": map[string]any{"id": 9007199254740993, "node_id": "APP_1", "slug": "reviewer-app"}}
}
func reviewerClientFixture(t *testing.T, mode string) *Client {
	t.Helper()
	c, _ := feedbackClientFixture(t, nil)
	original := c.http.Transport
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		var body any
		status := 200
		switch r.URL.Path {
		case "/user/199175422":
			body = reviewerActorFixture()
			if mode == "identity-foreign" {
				body.(map[string]any)["node_id"] = "OTHER"
			}
			if mode == "identity-missing" {
				status = 404
			}
		case "/repos/fixture-owner/repo/collaborators/chatgpt-codex-connector[bot]/permission":
			body = map[string]any{"permission": "none", "role_name": "", "user": reviewerActorFixture()}
			if mode == "permission-denied" {
				status = 403
			}
			if mode == "permission-malformed" {
				body.(map[string]any)["role_name"] = false
			}
			if mode == "permission-foreign" {
				body.(map[string]any)["user"].(map[string]any)["node_id"] = "OTHER"
			}
			if mode == "permission-custom" {
				body.(map[string]any)["permission"] = "write"
				body.(map[string]any)["role_name"] = "Custom maintainer"
			}
		case "/repos/fixture-owner/repo/issues/17/comments":
			row := reviewerCommentFixture()
			switch mode {
			case "app-missing":
				delete(row, "performed_via_github_app")
			case "app-none":
				row["performed_via_github_app"] = nil
			case "app-edited":
				row["body"] = "another revision"
			case "app-foreign":
				row["issue_url"] = "https://api.github.com/repos/foreign/repo/issues/17"
			case "app-denied":
				status = 403
			}
			body = []any{row}
			if mode == "app-partial" {
				body = []any{}
			}
		default:
			return original.RoundTrip(r)
		}
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer private-fixture-pat" || r.URL.Host != "api.github.com" {
			t.Fatal("unscoped reviewer inspection")
		}
		raw, _ := json.Marshal(body)
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	return c
}
func TestReviewerQueryIndependentlyBindsIdentityPermissionAndApp(t *testing.T) {
	c := reviewerClientFixture(t, "")
	v, err := c.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryReviewers, Number: "17"})
	if err != nil || v.Reviewers == nil || v.Feedback != nil || v.Reviewers.Validate(v.Items[0]) != nil {
		t.Fatal("reviewer observation failed", err)
	}
	actor := v.Reviewers.Actors[0]
	if len(v.Reviewers.Actors) != 1 || actor.IdentityAccess != domain.IntegrationAccessAvailable || actor.Identity.Login != "chatgpt-codex-connector[bot]" || actor.Permission.Minimum != domain.PermissionNone {
		t.Fatal("independent identity/no-permission proof lost")
	}
	app := domain.ReviewerSelector{Kind: domain.ReviewerApp, ID: "9007199254740993", NodeID: "APP_1"}
	if v.Reviewers.Match("conversation-comment_51", []domain.ReviewerSelector{app}) != domain.ReviewerMatches || v.Reviewers.Match("review-comment_53", []domain.ReviewerSelector{app}) != domain.ReviewerUnknown {
		t.Fatal("App attribution crossed feedback families")
	}
}
func TestReviewerUnavailableFeatureDoesNotManufactureAnotherProof(t *testing.T) {
	for _, mode := range []string{"identity-foreign", "identity-missing", "permission-denied", "permission-malformed", "permission-foreign", "permission-custom", "app-missing", "app-none", "app-edited", "app-foreign", "app-denied", "app-partial"} {
		t.Run(mode, func(t *testing.T) {
			c := reviewerClientFixture(t, mode)
			repo, item := ciFixtureItem(t)
			feedback, err := c.readFeedbackInventory(context.Background(), []byte("private-fixture-pat"), repo, item)
			if err != nil {
				t.Fatal(err)
			}
			v, err := c.readReviewerEvidence(context.Background(), []byte("private-fixture-pat"), repo, item, *feedback)
			if err != nil {
				t.Fatal(err)
			}
			bot := domain.ReviewerSelector{Kind: domain.ReviewerBot, ID: "199175422", NodeID: "BOT_1"}
			permission := domain.ReviewerSelector{Kind: domain.ReviewerMinimumPermission, Permission: domain.PermissionWrite}
			app := domain.ReviewerSelector{Kind: domain.ReviewerApp, ID: "9007199254740993", NodeID: "APP_1"}
			if strings.HasPrefix(mode, "identity-") || mode == "permission-foreign" {
				if v.Match("review_9007199254740993", []domain.ReviewerSelector{bot}) != domain.ReviewerUnknown {
					t.Fatal("unverified author allowed")
				}
				return
			}
			if v.Match("review_9007199254740993", []domain.ReviewerSelector{bot}) != domain.ReviewerMatches {
				t.Fatal("unrelated feature failure erased exact bot identity")
			}
			if mode == "permission-custom" {
				if v.Match("review_9007199254740993", []domain.ReviewerSelector{permission}) != domain.ReviewerMatches {
					t.Fatal("proved custom base lost")
				}
				return
			}
			if strings.HasPrefix(mode, "permission-") {
				if v.Match("review_9007199254740993", []domain.ReviewerSelector{permission}) != domain.ReviewerUnknown {
					t.Fatal("unverified permission allowed")
				}
				return
			}
			want := domain.ReviewerUnknown
			if mode == "app-none" {
				want = domain.ReviewerDoesNotMatch
			}
			if v.Match("conversation-comment_51", []domain.ReviewerSelector{app}) != want {
				t.Fatal("App absence/unknown conflated")
			}
		})
	}
}
func TestReviewerIdentityPathRejectsArbitraryOperands(t *testing.T) {
	c := New()
	c.http.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid identity path reached transport")
		return nil, nil
	})
	for _, path := range []string{"/user/0", "/user/17?scope=other", "/user/../17", "/user/017", "/user/name", "/users/17"} {
		if c.readRepositoryJSON(context.Background(), []byte("private-fixture-pat"), path).state != domain.IntegrationAccessUnavailable {
			t.Fatal("unbounded identity route accepted")
		}
	}
}
func TestReviewerPublicCommentAppAndCurrentBotIdentityProjection(t *testing.T) {
	prefix := os.Getenv("DELIDEV_GITHUB_FEEDBACK_FIXTURE_PREFIX")
	if prefix == "" {
		t.Skip("explicit public captures only")
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
	raw, err := os.ReadFile(prefix + "DeliDevPRConversation.json")
	if err != nil {
		t.Fatal(err)
	}
	envelope, _ := jsonObject(raw)
	data, _ := jsonObject(envelope["data"])
	node, _ := jsonObject(data["node"])
	conn, _ := jsonObject(node["comments"])
	var rows []map[string]json.RawMessage
	if domain.Decode(conn["nodes"], &rows) != nil {
		t.Fatal("capture rows")
	}
	rest, err := os.ReadFile(prefix + "comments.json")
	if err != nil {
		t.Fatal(err)
	}
	var originals []json.RawMessage
	if domain.Decode(rest, &originals) != nil {
		t.Fatal("REST rows")
	}
	for i, row := range rows {
		entry, draft, err := parseFeedback(row, domain.PRConversationComment, "", repo, item)
		if err != nil || draft {
			t.Fatal("public feedback", err)
		}
		app, err := parseFeedbackApplication(originals[i], entry, repo, item)
		if err != nil || app.State != domain.FeedbackAppAttributed || app.Application == nil {
			t.Fatal("actual App attribution lost", err)
		}
	}
}

func TestReviewerCancellationJoinsBothIdentityReaders(t *testing.T) {
	c := reviewerClientFixture(t, "")
	repo, item := ciFixtureItem(t)
	feedback, err := c.readFeedbackInventory(context.Background(), []byte("private-fixture-pat"), repo, item)
	if err != nil {
		t.Fatal(err)
	}
	other := *feedback.Entries[0].Author
	other.ID = "199175423"
	other.NodeID = "BOT_2"
	feedback.Entries[0].Author = &other
	started, finished := make(chan struct{}, 2), make(chan struct{}, 2)
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		started <- struct{}{}
		<-r.Context().Done()
		finished <- struct{}{}
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := c.readReviewerEvidence(ctx, []byte("private-fixture-pat"), repo, item, *feedback)
		done <- err
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("both bounded readers did not start")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled inspection succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("inspection did not join")
	}
	if len(finished) != 2 {
		t.Fatal("credential owner returned before both readers joined")
	}
}

func TestReviewerAppInventoryNeverKeepsAnEarlierPageAfterFailure(t *testing.T) {
	for _, failed := range []bool{false, true} {
		c := reviewerClientFixture(t, "")
		repo, item := ciFixtureItem(t)
		feedback, err := c.readFeedbackInventory(context.Background(), []byte("private-fixture-pat"), repo, item)
		if err != nil {
			t.Fatal(err)
		}
		entry, draft, err := parseFeedback(feedbackFixtureJSON(t, domain.PRConversationComment, "55"), domain.PRConversationComment, "", repo, item)
		if err != nil || draft {
			t.Fatal("extra comment", err)
		}
		feedback.Entries = append(feedback.Entries, entry)
		c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/repos/fixture-owner/repo/issues/17/comments" {
				t.Fatal("unexpected App endpoint")
			}
			status := 200
			headers := http.Header{}
			row := reviewerCommentFixture()
			if r.URL.Query().Get("page") == "1" {
				headers.Set("Link", `<https://api.github.com/repositories/37/issues/17/comments?page=2&per_page=100>; rel="next"`)
			} else {
				row["id"], row["node_id"], row["html_url"], row["performed_via_github_app"] = 55, "conversation-comment_55", "https://github.com/fixture-owner/repo/pull/17#issuecomment-55", nil
				if failed {
					status = 403
				}
			}
			raw, _ := json.Marshal([]any{row})
			return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
		})
		values := c.readFeedbackApplications(context.Background(), []byte("private-fixture-pat"), repo, item, *feedback)
		for _, app := range values {
			if app.FeedbackNodeID == "conversation-comment_51" {
				if failed && app.State != domain.FeedbackAppUnknown || !failed && app.State != domain.FeedbackAppAttributed {
					t.Fatal("partial App proof retained or complete proof discarded")
				}
			}
		}
	}
}

func feedbackFixtureJSON(t *testing.T, kind domain.PRFeedbackKind, id string) map[string]json.RawMessage {
	t.Helper()
	raw, _ := json.Marshal(feedbackFixtureRow(kind, id))
	fields, ok := jsonObject(raw)
	if !ok {
		t.Fatal("fixture decode")
	}
	return fields
}

func TestReviewerPublicIdentityAndPermissionShape(t *testing.T) {
	prefix := os.Getenv("DELIDEV_GITHUB_REVIEWER_FIXTURE_PREFIX")
	if prefix == "" {
		t.Skip("explicit public reviewer captures only")
	}
	feedbackPrefix := os.Getenv("DELIDEV_GITHUB_FEEDBACK_FIXTURE_PREFIX")
	if feedbackPrefix == "" {
		t.Fatal("original feedback capture required")
	}
	raw, err := os.ReadFile(feedbackPrefix + "DeliDevPRReviews.json")
	if err != nil {
		t.Fatal(err)
	}
	envelope, _ := jsonObject(raw)
	data, _ := jsonObject(envelope["data"])
	node, _ := jsonObject(data["node"])
	reviews, _ := jsonObject(node["reviews"])
	var rows []map[string]json.RawMessage
	if domain.Decode(reviews["nodes"], &rows) != nil || len(rows) != 1 {
		t.Fatal("original review missing")
	}
	author, err := parseFeedbackAuthor(rows[0]["author"])
	if err != nil || author == nil {
		t.Fatal("original Bot missing", err)
	}
	c := New()
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		name := "bot-identity.json"
		switch r.URL.Path {
		case "/user/199175422":
		case "/repos/delinoio/oss/collaborators/chatgpt-codex-connector[bot]/permission":
			name = "bot-permission.json"
		default:
			t.Fatal("unexpected original identity lookup")
		}
		raw, err := os.ReadFile(prefix + name)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	v := c.readReviewerIdentity(context.Background(), []byte("private-fixture-pat"), domain.RemoteRepository{Owner: "delinoio", Name: "oss"}, *author)
	if v.Validate() != nil || v.IdentityAccess != domain.IntegrationAccessAvailable || v.Permission.Access != domain.IntegrationAccessAvailable || v.Permission.Minimum != domain.PermissionNone {
		t.Fatal("public Bot identity/none permission rejected")
	}
}
