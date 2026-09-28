package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func queryFixtureItem(kind domain.RepositoryItemKind, number int, detail, search bool) map[string]any {
	segment, web := "issues", "issues"
	if kind == domain.RepositoryPullRequest {
		web = "pull"
		if !search {
			segment = "pulls"
		}
	}
	value := map[string]any{"id": uint64(9007199254740993), "node_id": "ITEM_stable", "number": number, "title": "Fixture item", "state": "open", "user": map[string]any{"id": 19, "node_id": "U_19", "login": "fixture-author", "type": "User"}, "created_at": "2026-09-01T00:00:00Z", "updated_at": "2026-09-28T00:00:00Z", "url": apiOrigin + "/repos/fixture-owner/repo/" + segment + "/" + strconv.Itoa(number), "html_url": "https://github.com/fixture-owner/repo/" + web + "/" + strconv.Itoa(number), "body": "private raw Markdown <script>inert</script>"}
	if kind == domain.RepositoryPullRequest {
		value["draft"] = false
		if search {
			value["pull_request"] = map[string]any{"url": apiOrigin + "/repos/fixture-owner/repo/pulls/" + strconv.Itoa(number)}
		}
	}
	if detail && kind == domain.RepositoryPullRequest {
		value["merged"] = false
		value["mergeable"] = nil
		value["base"] = map[string]any{"ref": "main", "sha": accessSHA, "repo": map[string]any{"id": 37, "node_id": "R_37"}}
		value["head"] = map[string]any{"ref": "feature", "sha": strings.Repeat("b", 40), "repo": nil}
	}
	return value
}
func queryFixture(t *testing.T, query domain.RepositoryQuery, body any, link string) (*Client, *[]string) {
	t.Helper()
	client, _ := accessFixture(t, nil)
	base := client.http.Transport
	paths := []string{}
	client.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.RequestURI())
		if r.Method != "GET" || r.URL.Host != "api.github.com" || r.URL.Scheme != "https" || r.Header.Get("Authorization") != "Bearer private-fixture-pat" {
			t.Error("unscoped read")
		}
		if r.URL.Path == "/user" || r.URL.Path == "/repos/fixture-owner/repo" {
			return base.RoundTrip(r)
		}
		if query.Operation == domain.RepositorySearch && r.URL.Path != "/search/issues" {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`[]`))}, nil
		}
		raw, _ := json.Marshal(body)
		return &http.Response{StatusCode: 200, Header: http.Header{"Link": {link}}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	return client, &paths
}
func listQuery(kind domain.RepositoryItemKind) domain.RepositoryQuery {
	return domain.RepositoryQuery{Kind: kind, Operation: domain.RepositoryList, State: domain.RepositoryItemOpen, Page: 1, PageSize: 20}
}
func TestRepositoryQueryListsExactStableProjectionAndValidatedPage(t *testing.T) {
	q := listQuery(domain.RepositoryPullRequest)
	path, _ := queryPath(domain.RemoteRepository{Owner: "fixture-owner", Name: "repo"}, q)
	next, _ := url.Parse(apiOrigin + path)
	values := next.Query()
	values.Set("page", "2")
	next.RawQuery = values.Encode()
	client, paths := queryFixture(t, q, []any{queryFixtureItem(q.Kind, 17, false, false)}, "<"+next.String()+">; rel=\"next\"")
	result, err := client.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", q)
	if err != nil || len(result.Items) != 1 || result.Items[0].ID != "9007199254740993" || result.Items[0].Body != nil || result.NextPage != 2 || result.Items[0].IdentitySource != domain.RepositoryPullRequestIdentity || len(*paths) != 3 {
		t.Fatalf("query result: %+v %v", result, err)
	}
	if (*paths)[2] != path {
		t.Fatal("changed query")
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "private raw") || strings.Contains(string(raw), "private-fixture-pat") {
		t.Fatal("unselected private data escaped")
	}
}
func TestRepositoryQueryIssuePageSkipsPRsWithoutInventingCompletion(t *testing.T) {
	q := listQuery(domain.RepositoryIssue)
	path, _ := queryPath(domain.RemoteRepository{Owner: "fixture-owner", Name: "repo"}, q)
	next := strings.Replace(path, "page=1&", "page=2&", 1)
	client, _ := queryFixture(t, q, []any{queryFixtureItem(domain.RepositoryPullRequest, 17, false, true)}, "<"+apiOrigin+next+">; rel=\"next\"")
	result, err := client.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", q)
	if err != nil || len(result.Items) != 0 || result.NextPage != 2 {
		t.Fatal("filtered page declared complete", result, err)
	}
}
func TestRepositoryQuerySearchPinsRepositoryKindAndExposesIncomplete(t *testing.T) {
	q := listQuery(domain.RepositoryPullRequest)
	q.Operation = domain.RepositorySearch
	q.Search = "fix OR issue"
	client, paths := queryFixture(t, q, map[string]any{"total_count": 1001, "incomplete_results": true, "items": []any{queryFixtureItem(q.Kind, 17, false, true)}}, "")
	result, err := client.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", q)
	if err != nil || len(*paths) != 4 || result.Incomplete == nil || !*result.Incomplete || result.TotalCount == nil || *result.TotalCount != "1001" || result.Items[0].IdentitySource != domain.RepositoryIssueIdentity {
		t.Fatal("search lost provenance", result, err)
	}
	request, _ := url.Parse((*paths)[3])
	text := request.Query().Get("q")
	if text != `repo:fixture-owner/repo is:pr in:title,body state:open "fix" "OR" "issue"` {
		t.Fatal("unscoped search", text)
	}
}
func TestRepositoryQueryDetailKeepsUnknownMergeabilityAndDeletedFork(t *testing.T) {
	q := domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryDetail, Number: "17"}
	client, _ := queryFixture(t, q, queryFixtureItem(q.Kind, 17, true, false), "")
	result, err := client.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", q)
	if err != nil || len(result.Items) != 1 || result.Items[0].Mergeable != nil || result.Items[0].Merged == nil || *result.Items[0].Merged || result.Items[0].Body == nil || result.Items[0].HeadSHA != strings.Repeat("b", 40) {
		t.Fatal("detail invented mergeability", result, err)
	}
}
func TestRepositoryQueryRejectsForeignRowsAndPagination(t *testing.T) {
	q := listQuery(domain.RepositoryIssue)
	for _, bad := range []string{"row", "host", "scope", "duplicate-page", "null"} {
		t.Run(bad, func(t *testing.T) {
			row := queryFixtureItem(q.Kind, 17, false, false)
			link := ""
			var body any = []any{row}
			path, _ := queryPath(domain.RemoteRepository{Owner: "fixture-owner", Name: "repo"}, q)
			path = strings.Replace(path, "page=1&", "page=2&", 1)
			switch bad {
			case "row":
				row["url"] = apiOrigin + "/repos/other/repo/issues/17"
			case "host":
				link = "<https://other.invalid" + path + ">; rel=\"next\""
			case "scope":
				link = "<" + apiOrigin + strings.Replace(path, "/repo/", "/foreign/", 1) + ">; rel=\"next\""
			case "duplicate-page":
				link = "<" + apiOrigin + path + "&page=2>; rel=\"next\""
			case "null":
				body = []any{nil}
			}
			client, _ := queryFixture(t, q, body, link)
			if _, err := client.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", q); err == nil {
				t.Fatal("malformed result accepted")
			}
		})
	}
}
func TestRepositoryQuerySearchAccessDenialDoesNotFallback(t *testing.T) {
	q := listQuery(domain.RepositoryIssue)
	q.Operation = domain.RepositorySearch
	q.Search = "fixture"
	client, paths := queryFixture(t, q, nil, "")
	original := client.http.Transport
	client.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/issues") {
			return &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("private-error"))}, nil
		}
		return original.RoundTrip(r)
	})
	_, err := client.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", q)
	if domain.SafeError(err).Code != domain.PermissionDenied || len(*paths) != 2 {
		t.Fatal("query fell back", err, *paths)
	}
}

func TestRepositoryQuerySearchCapAndOversizedResponseRemainExplicit(t *testing.T) {
	q := listQuery(domain.RepositoryIssue)
	q.Operation = domain.RepositorySearch
	q.Search = "fixture"
	q.Page = 50
	path, _ := queryPath(domain.RemoteRepository{Owner: "fixture-owner", Name: "repo"}, q)
	next := strings.Replace(path, "page=50&", "page=51&", 1)
	client, _ := queryFixture(t, q, map[string]any{"total_count": 1001, "incomplete_results": false, "items": []any{}}, "<"+apiOrigin+next+">; rel=\"next\"")
	result, err := client.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", q)
	if err != nil || result.NextPage != 0 || !result.SearchLimitReached {
		t.Fatal("search cap was hidden", result, err)
	}
	q = listQuery(domain.RepositoryIssue)
	row := queryFixtureItem(q.Kind, 17, false, false)
	row["body"] = strings.Repeat("x", 1<<20)
	client, _ = queryFixture(t, q, []any{row}, "")
	if _, err := client.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", q); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("oversized page silently truncated", err)
	}
}

func TestRepositoryQueryRetainsUnknownAuthorForManualInspection(t *testing.T) {
	q := listQuery(domain.RepositoryIssue)
	row := queryFixtureItem(q.Kind, 17, false, false)
	row["user"] = map[string]any{"id": 19, "node_id": "U_19", "login": "imported-author", "type": "Mannequin"}
	client, _ := queryFixture(t, q, []any{row}, "")
	result, err := client.QueryRepository(context.Background(), []byte("private-fixture-pat"), "fixture-owner", "repo", q)
	if err != nil || len(result.Items) != 1 || result.Items[0].Author == nil || result.Items[0].Author.Kind != domain.RepositoryUnknownActor || result.Items[0].Author.ProviderType != "Mannequin" {
		t.Fatal("unknown author discarded", err)
	}
}
