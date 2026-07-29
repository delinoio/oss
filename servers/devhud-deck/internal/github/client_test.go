package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func jsonResponse(status int, value string) *http.Response {
	return &http.Response{
		StatusCode: status, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(value)),
	}
}

type recordedRequest struct {
	method string
	path   string
	body   string
}

func TestEverySupportedMutationUsesOnlyUserAttributedEndpoints(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		mutation Mutation
		pull     string
		method   string
		path     string
		body     string
	}{
		{"assign", Mutation{Kind: MutationAssignUsers, Users: []User{{"octo"}}},
			`{"node_id":"PR_1","state":"open","draft":false,"merged":false,"mergeable":true,"mergeable_state":"clean","updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"}}`,
			"POST", "/repos/acme/widget/issues/7/assignees", `"octo"`},
		{"unassign", Mutation{Kind: MutationUnassignUsers, Users: []User{{"octo"}}},
			`{"node_id":"PR_1","state":"open","draft":false,"merged":false,"mergeable":true,"mergeable_state":"clean","updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"}}`,
			"DELETE", "/repos/acme/widget/issues/7/assignees", `"octo"`},
		{"request reviewers", Mutation{Kind: MutationRequestReviewers,
			Users: []User{{"octo"}}, Teams: []Team{{"acme", "core"}}},
			`{"node_id":"PR_1","state":"open","draft":false,"merged":false,"mergeable":true,"mergeable_state":"clean","updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"}}`,
			"POST", "/repos/acme/widget/pulls/7/requested_reviewers", `"team_reviewers"`},
		{"remove reviewers", Mutation{Kind: MutationRemoveReviewers,
			Users: []User{{"octo"}}, Teams: []Team{{"acme", "core"}}},
			`{"node_id":"PR_1","state":"open","draft":false,"merged":false,"mergeable":true,"mergeable_state":"clean","updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"}}`,
			"DELETE", "/repos/acme/widget/pulls/7/requested_reviewers", `"team_reviewers"`},
		{"add labels", Mutation{Kind: MutationAddLabels, Labels: []string{"ready"}},
			`{"node_id":"PR_1","state":"open","draft":false,"merged":false,"mergeable":true,"mergeable_state":"clean","updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"}}`,
			"POST", "/repos/acme/widget/issues/7/labels", `"ready"`},
		{"remove labels", Mutation{Kind: MutationRemoveLabels, Labels: []string{"ready"}},
			`{"node_id":"PR_1","state":"open","draft":false,"merged":false,"mergeable":true,"mergeable_state":"clean","updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"}}`,
			"DELETE", "/repos/acme/widget/issues/7/labels/ready", ""},
		{"draft", Mutation{Kind: MutationMarkDraft},
			`{"node_id":"PR_1","state":"open","draft":false,"merged":false,"mergeable":true,"mergeable_state":"clean","updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"}}`,
			"POST", "/graphql", "convertPullRequestToDraft"},
		{"ready", Mutation{Kind: MutationMarkReady},
			`{"node_id":"PR_1","state":"open","draft":true,"merged":false,"mergeable":true,"mergeable_state":"clean","updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"}}`,
			"POST", "/graphql", "markPullRequestReadyForReview"},
		{"close", Mutation{Kind: MutationClose},
			`{"node_id":"PR_1","state":"open","draft":false,"merged":false,"mergeable":true,"mergeable_state":"clean","updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"}}`,
			"PATCH", "/repos/acme/widget/pulls/7", `"closed"`},
		{"reopen", Mutation{Kind: MutationReopen},
			`{"node_id":"PR_1","state":"closed","draft":false,"merged":false,"mergeable":true,"mergeable_state":"clean","updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"}}`,
			"PATCH", "/repos/acme/widget/pulls/7", `"open"`},
		{"merge", Mutation{Kind: MutationMerge, MergeMethod: MergeMethodSquash, Confirmed: true},
			`{"node_id":"PR_1","state":"open","draft":false,"merged":false,"mergeable":true,"mergeable_state":"clean","updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"}}`,
			"PUT", "/repos/acme/widget/pulls/7/merge", `"squash"`},
		{"enable auto merge", Mutation{Kind: MutationEnableAutoMerge, MergeMethod: MergeMethodRebase},
			`{"node_id":"PR_1","state":"open","draft":false,"merged":false,"mergeable":true,"mergeable_state":"clean","updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"}}`,
			"POST", "/graphql", "enablePullRequestAutoMerge"},
		{"cancel auto merge", Mutation{Kind: MutationCancelAutoMerge},
			`{"node_id":"PR_1","state":"open","draft":false,"merged":false,"mergeable":true,"mergeable_state":"clean","updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"},"auto_merge":{"enabled_by":{"login":"octo"}}}`,
			"POST", "/graphql", "disablePullRequestAutoMerge"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var mutex sync.Mutex
			var requests []recordedRequest
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				var body []byte
				if request.Body != nil {
					body, _ = io.ReadAll(request.Body)
				}
				mutex.Lock()
				requests = append(requests, recordedRequest{
					method: request.Method, path: request.URL.EscapedPath(),
					body: string(body),
				})
				mutex.Unlock()
				switch {
				case request.Method == http.MethodGet &&
					request.URL.Path == "/repos/acme/widget":
					return jsonResponse(http.StatusOK,
						`{"allow_merge_commit":true,"allow_squash_merge":true,"allow_rebase_merge":true,"permissions":{"pull":true,"push":true}}`), nil
				case request.Method == http.MethodGet &&
					request.URL.Path == "/repos/acme/widget/pulls/7":
					return jsonResponse(http.StatusOK, test.pull), nil
				default:
					return jsonResponse(http.StatusOK, `{"data":{}}`), nil
				}
			})
			client := NewClient(&http.Client{Transport: transport})
			reference := PullRequestRef{
				Repository: Repository{Owner: "acme", Name: "widget"}, Number: 7,
			}
			var pull pullResponse
			if err := json.Unmarshal([]byte(test.pull), &pull); err != nil {
				t.Fatal(err)
			}
			_, err := client.Mutate(context.Background(), "actor-"+test.name,
				42, Credential{AccessToken: "ghu_fixture"},
				Permissions{
					Metadata: PermissionRead, PullRequests: PermissionWrite,
					Checks: PermissionRead, Members: PermissionRead,
				}, reference, pullRevision(pull), test.mutation)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, request := range requests {
				if request.method == test.method && request.path == test.path &&
					(test.body == "" || strings.Contains(request.body, test.body)) {
					found = true
				}
				if strings.Contains(request.body, "ghu_fixture") {
					t.Fatal("user token leaked into request body")
				}
			}
			if !found {
				t.Fatalf("mutation request not found in %#v", requests)
			}
		})
	}
}

func TestMutationRejectionsAndLimits(t *testing.T) {
	t.Parallel()
	pull := `{"node_id":"PR_1","state":"open","draft":false,"merged":false,"mergeable":true,"mergeable_state":"clean","updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"}}`
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/repos/acme/widget" {
			return jsonResponse(http.StatusOK,
				`{"allow_merge_commit":true,"permissions":{"pull":true,"push":true}}`), nil
		}
		return jsonResponse(http.StatusOK, pull), nil
	})
	client := NewClient(&http.Client{Transport: transport})
	reference := PullRequestRef{
		Repository: Repository{Owner: "acme", Name: "widget"}, Number: 7,
	}
	var decoded pullResponse
	_ = json.Unmarshal([]byte(pull), &decoded)
	revision := pullRevision(decoded)
	permissions := Permissions{
		Metadata: PermissionRead, PullRequests: PermissionWrite,
		Checks: PermissionRead,
	}
	if _, err := client.Mutate(context.Background(), "missing-confirmation", 1,
		Credential{AccessToken: "token"}, permissions, reference, revision,
		Mutation{Kind: MutationMerge, MergeMethod: MergeMethodMerge}); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("merge confirmation error = %v", err)
	}
	if _, err := client.Mutate(context.Background(), "stale", 1,
		Credential{AccessToken: "token"}, permissions, reference, revision+1,
		Mutation{Kind: MutationClose}); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale error = %v", err)
	}
	if _, err := client.Mutate(context.Background(), "no-write", 1,
		Credential{AccessToken: "token"},
		Permissions{Metadata: PermissionRead, PullRequests: PermissionRead},
		reference, revision, Mutation{Kind: MutationClose}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("permission error = %v", err)
	}
	if _, err := client.Mutate(context.Background(), "comment", 1,
		Credential{AccessToken: "token"}, permissions, reference, revision,
		Mutation{}); !errors.Is(err, ErrUnsupportedAction) {
		t.Fatalf("unsupported action error = %v", err)
	}
	if _, err := client.Mutate(context.Background(), "method", 1,
		Credential{AccessToken: "token"}, permissions, reference, revision,
		Mutation{
			Kind: MutationMerge, MergeMethod: MergeMethodSquash, Confirmed: true,
		}); !errors.Is(err, ErrBranchProtected) {
		t.Fatalf("unavailable merge method error = %v", err)
	}
	blockedClient := NewClient(&http.Client{Transport: roundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/repos/acme/widget" {
				return jsonResponse(http.StatusOK,
					`{"allow_merge_commit":true,"permissions":{"pull":true,"push":true}}`), nil
			}
			return jsonResponse(http.StatusOK,
				`{"node_id":"PR_1","state":"open","draft":false,"merged":false,`+
					`"mergeable":true,"mergeable_state":"blocked",`+
					`"updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"}}`), nil
		})})
	if _, err := blockedClient.Mutate(context.Background(), "blocked", 1,
		Credential{AccessToken: "token"}, permissions, reference,
		pullRevision(pullResponse{
			NodeID: "PR_1", State: "open", UpdatedAt: "2026-01-01T00:00:00Z",
			Head: struct {
				SHA string `json:"sha"`
			}{SHA: "abc"},
		}), Mutation{
			Kind: MutationMerge, MergeMethod: MergeMethodMerge, Confirmed: true,
		}); !errors.Is(err, ErrBranchProtected) {
		t.Fatalf("branch protection error = %v", err)
	}
	limiter := newUserRateLimiter(2, time.Minute)
	now := time.Unix(1_800_000_000, 0)
	if !limiter.allow("actor", now) || !limiter.allow("actor", now) ||
		limiter.allow("actor", now) ||
		!limiter.allow("actor", now.Add(time.Minute+time.Second)) {
		t.Fatal("mutation rate limit was not deterministic")
	}
	concurrency := newInstallationLimiter(2)
	first, _ := concurrency.acquire(1)
	second, _ := concurrency.acquire(1)
	if _, err := concurrency.acquire(1); !errors.Is(err, ErrConcurrencyLimited) {
		t.Fatalf("concurrency error = %v", err)
	}
	first()
	second()

	rateResponse := jsonResponse(http.StatusTooManyRequests, `{}`)
	rateResponse.Header.Set("Retry-After", "17")
	rateClient := NewClient(&http.Client{Transport: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return rateResponse, nil
		})})
	_, err := rateClient.CanReadRepository(context.Background(),
		Credential{AccessToken: "token"},
		Repository{Owner: "acme", Name: "widget"})
	var rateLimit *RateLimitError
	if !errors.As(err, &rateLimit) || rateLimit.RetryAfter != 17*time.Second {
		t.Fatalf("provider rate limit = %T %#v", err, err)
	}
}

func TestCandidateFetchIsActionSpecificAndPermissionFiltered(t *testing.T) {
	t.Parallel()
	var paths []string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		switch request.URL.Path {
		case "/repos/acme/widget":
			return jsonResponse(http.StatusOK,
				`{"permissions":{"pull":true,"push":true}}`), nil
		case "/repos/acme/widget/pulls/7":
			return jsonResponse(http.StatusOK,
				`{"node_id":"PR_1","state":"open","draft":false,"merged":false,"mergeable":true,"mergeable_state":"clean","updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"}}`), nil
		case "/repos/acme/widget/assignees":
			return jsonResponse(http.StatusOK,
				`[{"login":"octo"},{"login":"core-user"}]`), nil
		case "/orgs/acme/teams":
			return jsonResponse(http.StatusOK,
				`[{"slug":"core"},{"slug":"docs"}]`), nil
		case "/repos/acme/widget/labels":
			return jsonResponse(http.StatusOK,
				`[{"name":"ready"},{"name":"security"}]`), nil
		default:
			return jsonResponse(http.StatusNotFound, `{}`), nil
		}
	})
	client := NewClient(&http.Client{Transport: transport})
	reference := PullRequestRef{
		Repository: Repository{Owner: "acme", Name: "widget"}, Number: 7,
	}
	page, err := client.ListMutationCandidates(context.Background(), 1,
		Credential{AccessToken: "token"}, Permissions{
			Metadata: PermissionRead, PullRequests: PermissionWrite,
			Checks: PermissionRead, Members: PermissionRead,
		}, reference, MutationRequestReviewers, "core", Page{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Candidates) != 2 ||
		page.Candidates[0].Kind != CandidateTeam ||
		page.Candidates[1].Kind != CandidateUser {
		t.Fatalf("filtered candidates = %#v", page.Candidates)
	}
	paths = nil
	_, err = client.ListMutationCandidates(context.Background(), 1,
		Credential{AccessToken: "token"}, Permissions{
			Metadata: PermissionRead, PullRequests: PermissionWrite,
			Checks: PermissionRead,
		}, reference, MutationRequestReviewers, "", Page{})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if path == "/orgs/acme/teams" {
			t.Fatal("team metadata fetched without members permission")
		}
	}
	paths = nil
	labelPage, err := client.ListMutationCandidates(context.Background(), 1,
		Credential{AccessToken: "token"}, Permissions{
			Metadata: PermissionRead, PullRequests: PermissionWrite,
			Checks: PermissionRead,
		}, reference, MutationAddLabels, "security", Page{})
	if err != nil || len(labelPage.Candidates) != 1 ||
		labelPage.Candidates[0].Label != "security" {
		t.Fatalf("label candidates = %#v err=%v", labelPage, err)
	}
	for _, path := range paths {
		if path == "/repos/acme/widget/assignees" ||
			path == "/orgs/acme/teams" {
			t.Fatalf("unneeded identity metadata fetched for label action: %s", path)
		}
	}
	if _, err := client.ListMutationCandidates(context.Background(), 1,
		Credential{AccessToken: "token"}, Permissions{
			Metadata: PermissionRead, PullRequests: PermissionWrite,
		}, reference, MutationRemoveLabels, "", Page{}); !errors.Is(err, ErrUnsupportedAction) {
		t.Fatalf("removal candidate error = %v", err)
	}
}

func TestGitHubHostAndTokenRedactionFailClosed(t *testing.T) {
	t.Parallel()
	client := NewClient(&http.Client{Transport: roundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			return nil, errors.New("transport exposed ghu_super_secret")
		})})
	if _, err := client.CanReadRepository(context.Background(),
		Credential{AccessToken: "ghu_super_secret"},
		Repository{Owner: "https:", Name: "ghe.example"}); !errors.Is(
		err, ErrUnsupportedHost) {
		t.Fatalf("custom host error = %v", err)
	}
	_, err := client.CanReadRepository(context.Background(),
		Credential{AccessToken: "ghu_super_secret"},
		Repository{Owner: "acme", Name: "widget"})
	if err == nil || strings.Contains(err.Error(), "ghu_super_secret") {
		t.Fatalf("token-bearing error = %v", err)
	}
	redirectedClient := NewClient(&http.Client{Transport: roundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			response := jsonResponse(http.StatusOK,
				`{"permissions":{"pull":true}}`)
			redirected := request.Clone(request.Context())
			redirected.URL.Scheme = "https"
			redirected.URL.Host = "ghe.example"
			response.Request = redirected
			return response, nil
		})})
	if _, err := redirectedClient.CanReadRepository(context.Background(),
		Credential{AccessToken: "ghu_super_secret"},
		Repository{Owner: "acme", Name: "widget"}); !errors.Is(
		err, ErrUnsupportedHost) {
		t.Fatalf("redirected custom host error = %v", err)
	}
}

func TestSearchFiltersBeforeReturningIdentityOrCounts(t *testing.T) {
	t.Parallel()
	const secretTitle = "private acquisition codename"
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/search/issues":
			if !strings.Contains(request.URL.Query().Get("q"), "is:pr") {
				t.Fatal("search was not constrained to pull requests")
			}
			return jsonResponse(http.StatusOK, `{
				"total_count":99,
				"incomplete_results":false,
				"items":[
					{
						"repository_url":"https://api.github.com/repos/acme/visible",
						"number":7,
						"title":"visible title",
						"updated_at":"2026-01-01T00:00:00Z",
						"user":{"login":"octo"},
						"pull_request":{}
					},
					{
						"repository_url":"https://api.github.com/repos/secret/hidden",
						"number":8,
						"title":"`+secretTitle+`",
						"updated_at":"2026-01-01T00:00:00Z",
						"user":{"login":"private-user"},
						"pull_request":{}
					}
				]
			}`), nil
		case "/repos/acme/visible":
			return jsonResponse(http.StatusOK,
				`{"permissions":{"pull":true}}`), nil
		case "/repos/secret/hidden":
			return jsonResponse(http.StatusForbidden, `{"message":"forbidden"}`), nil
		default:
			return jsonResponse(http.StatusNotFound, `{}`), nil
		}
	})
	client := NewClient(&http.Client{Transport: transport})
	page, err := client.SearchPullRequests(context.Background(), 42,
		Credential{AccessToken: "ghu_viewer"}, "review-requested:@me",
		Page{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if page.VisibleCount != 1 || len(page.PullRequests) != 1 ||
		page.PullRequests[0].Repository.Name != "visible" ||
		page.PullRequests[0].Title != "visible title" {
		t.Fatalf("filtered page = %#v", page)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	output := string(encoded)
	for _, leaked := range []string{secretTitle, "secret", "hidden",
		"private-user", `"VisibleCount":99`} {
		if strings.Contains(output, leaked) {
			t.Fatalf("unauthorized search data leaked: %q in %s", leaked, output)
		}
	}
}

func TestSearchRejectsCustomRepositoryHosts(t *testing.T) {
	t.Parallel()
	client := NewClient(&http.Client{Transport: roundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{
				"total_count":1,
				"items":[{
					"repository_url":"https://ghe.example/repos/acme/private",
					"number":1,
					"title":"private",
					"updated_at":"2026-01-01T00:00:00Z",
					"user":{"login":"octo"},
					"pull_request":{}
				}]
			}`), nil
		})})
	if _, err := client.SearchPullRequests(context.Background(), 42,
		Credential{AccessToken: "token"}, "is:open", Page{}); !errors.Is(
		err, ErrProvider) {
		t.Fatalf("custom search repository host error = %v", err)
	}
}
