package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func observationQuery(operation domain.RepositoryQueryOperation) domain.RepositoryQuery {
	q := domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: operation, Number: "17"}
	if operation != domain.RepositoryDiff {
		q.Page = 1
		q.PageSize = 20
	}
	return q
}
func checkFixture() map[string]any {
	return map[string]any{"total_count": 1, "check_runs": []any{map[string]any{"id": 53, "node_id": "CHECK_53", "url": apiOrigin + "/repos/fixture-owner/repo/check-runs/53", "head_sha": strings.Repeat("b", 40), "name": "Fixture Check", "status": "completed", "conclusion": "success", "started_at": "2026-09-28T00:00:00Z", "completed_at": "2026-09-28T00:01:00Z", "app": map[string]any{"id": 15368, "node_id": "APP_15368", "slug": "github-actions"}}}}
}
func statusFixture() map[string]any {
	return map[string]any{"sha": strings.Repeat("b", 40), "state": "pending", "total_count": 0, "statuses": []any{}, "repository": map[string]any{"id": 37, "node_id": "R_37"}}
}
func observationFixture(t *testing.T, q domain.RepositoryQuery, body any, changed bool) (*Client, *[]string) {
	t.Helper()
	client, _ := accessFixture(t, nil)
	base := client.http.Transport
	paths := []string{}
	details := 0
	client.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.RequestURI())
		if r.Method != "GET" || r.URL.Host != "api.github.com" || r.Header.Get("Authorization") != "Bearer private-fixture-pat" {
			t.Error("unscoped request")
		}
		if r.URL.Path == "/user" || r.URL.Path == "/repos/fixture-owner/repo" {
			return base.RoundTrip(r)
		}
		var value []byte
		header := http.Header{}
		if r.URL.Path == "/repos/fixture-owner/repo/pulls/17" {
			details++
			item := queryFixtureItem(domain.RepositoryPullRequest, 17, true, false)
			if changed && details == 2 {
				item["head"].(map[string]any)["sha"] = strings.Repeat("c", 40)
			}
			value, _ = json.Marshal(item)
		} else if q.Operation == domain.RepositoryDiff {
			if r.URL.Path != "/repos/fixture-owner/repo/compare/"+accessSHA+"..."+strings.Repeat("b", 40) || r.Header.Get("Accept") != "application/vnd.github.diff" {
				t.Error("diff did not pin immutable operands")
			}
			value = []byte(body.(string))
			header.Set("Content-Type", "application/vnd.github.diff; charset=utf-8")
		} else {
			value, _ = json.Marshal(body)
		}
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(string(value)))}, nil
	})
	return client, &paths
}
func TestPRObservationsKeepCheckResultsSeparateFromEmptyPendingStatuses(t *testing.T) {
	for _, operation := range []domain.RepositoryQueryOperation{domain.RepositoryChecks, domain.RepositoryStatuses} {
		q := observationQuery(operation)
		body := checkFixture()
		if operation == domain.RepositoryStatuses {
			body = statusFixture()
		}
		client, paths := observationFixture(t, q, body, false)
		result, err := client.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", q)
		if err != nil || len(*paths) != 5 || len(result.Items) != 1 {
			t.Fatal("PR observation failed", err, *paths)
		}
		if operation == domain.RepositoryChecks {
			if result.Checks == nil || len(result.Checks.Runs) != 1 || *result.Checks.Runs[0].Conclusion != domain.CheckSuccess || result.Statuses != nil {
				t.Fatal("checks projection changed")
			}
		} else {
			if result.Statuses == nil || result.Statuses.State != domain.CommitStatusPending || len(result.Statuses.Contexts) != 0 || result.Checks != nil {
				t.Fatal("missing statuses became passing")
			}
		}
	}
}
func TestPRDiffUsesImmutableOperandsAndPreservesOriginalBytes(t *testing.T) {
	q := observationQuery(domain.RepositoryDiff)
	patch := "diff --git a/file b/file\n--- a/file\n+++ b/file\n@@ -1 +1 @@\n-before\n+after 🙂\n"
	client, _ := observationFixture(t, q, patch, false)
	result, err := client.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", q)
	sum := sha256.Sum256([]byte(patch))
	if err != nil || result.Diff == nil || result.Diff.Patch != patch || result.Diff.Digest != hex.EncodeToString(sum[:]) || result.Diff.HeadSHA != strings.Repeat("b", 40) {
		t.Fatal("diff changed", err)
	}
}
func TestPRObservationRejectsHeadChangeForeignResultsAndOversizedDiff(t *testing.T) {
	for _, operation := range []domain.RepositoryQueryOperation{domain.RepositoryChecks, domain.RepositoryStatuses, domain.RepositoryDiff} {
		q := observationQuery(operation)
		var body any = checkFixture()
		if operation == domain.RepositoryStatuses {
			body = statusFixture()
		}
		if operation == domain.RepositoryDiff {
			body = "diff --git a/file b/file\n"
		}
		client, _ := observationFixture(t, q, body, true)
		if _, err := client.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", q); domain.SafeError(err).Code != domain.Conflict {
			t.Fatal("changed head published", operation, err)
		}
	}
	q := observationQuery(domain.RepositoryChecks)
	body := checkFixture()
	body["check_runs"].([]any)[0].(map[string]any)["head_sha"] = accessSHA
	client, _ := observationFixture(t, q, body, false)
	if _, err := client.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", q); err == nil {
		t.Fatal("foreign check head accepted")
	}
	q = observationQuery(domain.RepositoryDiff)
	client, _ = observationFixture(t, q, strings.Repeat("x", (512<<10)+1), false)
	if _, err := client.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", q); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("diff truncated", err)
	}
}
func TestPRCheckUnknownStateAndConclusionRemainOriginalUnknown(t *testing.T) {
	q := observationQuery(domain.RepositoryChecks)
	body := checkFixture()
	row := body["check_runs"].([]any)[0].(map[string]any)
	row["status"] = "future-status"
	row["conclusion"] = "future-conclusion"
	row["app"] = nil
	client, _ := observationFixture(t, q, body, false)
	result, err := client.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", q)
	if err != nil || result.Checks.Runs[0].Status != domain.CheckUnknown || *result.Checks.Runs[0].Conclusion != domain.CheckConclusionUnknown || result.Checks.Runs[0].NativeStatus != "future-status" || result.Checks.Runs[0].Application != nil {
		t.Fatal("unknown check lost", err)
	}
}
func TestPRCommitStatusPreservesFailedContextAndUnknownAuthor(t *testing.T) {
	q := observationQuery(domain.RepositoryStatuses)
	body := statusFixture()
	body["state"] = "failure"
	body["total_count"] = 1
	body["statuses"] = []any{map[string]any{"id": 83, "node_id": "STATUS_83", "url": apiOrigin + "/repos/fixture-owner/repo/statuses/" + strings.Repeat("b", 40), "context": "external-ci", "state": "error", "description": "Original failure", "creator": map[string]any{"id": 19, "node_id": "U_19", "login": "imported-author", "type": "Mannequin"}, "created_at": "2026-09-28T00:00:00Z", "updated_at": "2026-09-28T00:01:00Z"}}
	client, _ := observationFixture(t, q, body, false)
	result, err := client.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", q)
	if err != nil || result.Statuses.State != domain.CommitStatusFailure || len(result.Statuses.Contexts) != 1 || result.Statuses.Contexts[0].State != domain.CommitStatusError || result.Statuses.Contexts[0].Creator.Kind != domain.RepositoryUnknownActor {
		t.Fatal("context provenance lost", err)
	}
}

func TestPRDiffRejectsWrongRepresentationAndCancelsOwnedRead(t *testing.T) {
	q := observationQuery(domain.RepositoryDiff)
	client, _ := observationFixture(t, q, "diff --git a/file b/file\n", false)
	original := client.http.Transport
	client.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		reply, err := original.RoundTrip(r)
		if strings.Contains(r.URL.Path, "/compare/") {
			reply.Header.Set("Content-Type", "application/json")
		}
		return reply, err
	})
	if _, err := client.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", q); err == nil {
		t.Fatal("JSON masqueraded as diff")
	}
	client, _ = observationFixture(t, q, "", false)
	original = client.http.Transport
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	stopped := make(chan struct{})
	client.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/compare/") {
			close(entered)
			<-r.Context().Done()
			close(stopped)
			return nil, r.Context().Err()
		}
		return original.RoundTrip(r)
	})
	done := make(chan error, 1)
	go func() {
		_, err := client.QueryRepository(ctx, []byte("private-fixture-pat"), "fixture-owner", "repo", q)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("read not started")
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("canceled diff published")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("read survived cancellation")
	}
}
