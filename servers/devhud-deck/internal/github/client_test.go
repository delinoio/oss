package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
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
			`{"node_id":"PR_1","state":"open","draft":false,"merged":false,"mergeable":true,"mergeable_state":"clean","updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"},"labels":[{"name":"ready","node_id":"LA_1"}]}`,
			"POST", "/graphql", "removeLabelsFromLabelable"},
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
			`{"node_id":"PR_1","state":"open","draft":false,"merged":false,"mergeable":true,"mergeable_state":"blocked","updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"}}`,
			"POST", "/graphql", "expectedHeadOid"},
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
						`{"allow_merge_commit":true,"allow_squash_merge":true,"allow_rebase_merge":true,"allow_auto_merge":true,"permissions":{"pull":true,"push":true}}`), nil
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
					Metadata: PermissionRead, Contents: PermissionWrite,
					PullRequests: PermissionWrite,
					Checks:       PermissionRead, Members: PermissionRead,
				}, reference, pullRevision(pull), test.mutation)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, request := range requests {
				bodyMatches := test.body == "" ||
					strings.Contains(request.body, test.body)
				if test.mutation.Kind == MutationEnableAutoMerge {
					bodyMatches = bodyMatches &&
						strings.Contains(request.body, `"head":"abc"`)
				}
				if request.method == test.method && request.path == test.path &&
					bodyMatches {
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
		Metadata: PermissionRead, Contents: PermissionWrite,
		PullRequests: PermissionWrite,
		Checks:       PermissionRead,
	}
	if _, err := client.Mutate(context.Background(), "missing-confirmation", 1,
		Credential{AccessToken: "token"}, permissions, reference, revision,
		Mutation{Kind: MutationMerge, MergeMethod: MergeMethodMerge}); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("merge confirmation error = %v", err)
	}
	if _, err := client.Mutate(context.Background(), "no-contents", 1,
		Credential{AccessToken: "token"},
		Permissions{
			Metadata: PermissionRead, PullRequests: PermissionWrite,
		},
		reference, revision, Mutation{
			Kind: MutationMerge, MergeMethod: MergeMethodMerge, Confirmed: true,
		}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("merge contents permission error = %v", err)
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
	conflictingClient := NewClient(&http.Client{Transport: roundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/repos/acme/widget" {
				return jsonResponse(http.StatusOK,
					`{"allow_merge_commit":true,"permissions":{"pull":true,"push":true}}`), nil
			}
			return jsonResponse(http.StatusOK,
				`{"node_id":"PR_1","state":"open","draft":false,"merged":false,`+
					`"mergeable":false,"mergeable_state":"dirty",`+
					`"updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"}}`), nil
		})})
	if _, err := conflictingClient.Mutate(
		context.Background(), "conflicting", 1,
		Credential{AccessToken: "token"}, permissions, reference,
		pullRevision(pullResponse{
			NodeID: "PR_1", State: "open", UpdatedAt: "2026-01-01T00:00:00Z",
			Head: struct {
				SHA string `json:"sha"`
			}{SHA: "abc"},
		}), Mutation{
			Kind: MutationMerge, MergeMethod: MergeMethodMerge, Confirmed: true,
		}); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("merge conflict error = %v", err)
	}
	limiter := newUserRateLimiter(2, time.Minute)
	now := time.Unix(1_800_000_000, 0)
	if !limiter.allow("actor", now) || !limiter.allow("actor", now) ||
		limiter.allow("actor", now) ||
		!limiter.allow("actor", now.Add(time.Minute+time.Second)) {
		t.Fatal("mutation rate limit was not deterministic")
	}
	client.mutations = newUserRateLimiter(0, time.Minute)
	if _, err := client.Mutate(
		context.Background(), "locally-limited", 1,
		Credential{AccessToken: "token"}, permissions, reference, revision,
		Mutation{Kind: MutationClose},
	); !errors.Is(err, ErrMutationRateLimited) ||
		errors.Is(err, ErrRateLimited) {
		t.Fatalf("local mutation rate-limit error = %v", err)
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

func TestGraphQLErrorMapsHTTP200RateLimitHeaders(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0)
	response := jsonResponse(http.StatusOK,
		`{"errors":[{"type":"RATE_LIMITED"}]}`)
	response.Header.Set("X-RateLimit-Remaining", "0")
	response.Header.Set("X-RateLimit-Reset",
		fmt.Sprint(now.Add(17*time.Second).Unix()))
	client := NewClient(&http.Client{Transport: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return response, nil
		})})
	client.now = func() time.Time { return now }

	err := client.graphQL(context.Background(),
		Credential{AccessToken: "token"}, "mutation { fixture }", nil)
	var rateLimit *RateLimitError
	if !errors.As(err, &rateLimit) ||
		rateLimit.RetryAfter != 17*time.Second {
		t.Fatalf("GraphQL provider rate limit = %T %#v", err, err)
	}
}

func TestGraphQLErrorMapsSecondaryRateLimitWithoutHeaders(t *testing.T) {
	t.Parallel()
	client := NewClient(&http.Client{Transport: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{
				"errors":[{
					"type":"FORBIDDEN",
					"message":"You have exceeded a secondary rate limit."
				}]
			}`), nil
		})})

	err := client.graphQL(context.Background(),
		Credential{AccessToken: "token"}, "mutation { fixture }", nil)
	var rateLimit *RateLimitError
	if !errors.As(err, &rateLimit) ||
		rateLimit.RetryAfter != time.Minute {
		t.Fatalf("GraphQL secondary rate limit = %T %#v", err, err)
	}
}

func TestGraphQLErrorMapsTypedRateLimitWithoutHeaders(t *testing.T) {
	t.Parallel()
	client := NewClient(&http.Client{Transport: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK,
				`{"errors":[{"type":"RATE_LIMITED"}]}`), nil
		})})

	err := client.graphQL(context.Background(),
		Credential{AccessToken: "token"}, "mutation { fixture }", nil)
	var rateLimit *RateLimitError
	if !errors.As(err, &rateLimit) ||
		rateLimit.RetryAfter != time.Minute {
		t.Fatalf("typed GraphQL rate limit = %T %#v", err, err)
	}
}

func TestGraphQLValidationAndConflictErrorsRemainProviderFailures(t *testing.T) {
	t.Parallel()
	for _, failureType := range []string{"UNPROCESSABLE", "CONFLICT"} {
		failureType := failureType
		t.Run(failureType, func(t *testing.T) {
			t.Parallel()
			client := NewClient(&http.Client{Transport: roundTripFunc(
				func(*http.Request) (*http.Response, error) {
					return jsonResponse(http.StatusOK, fmt.Sprintf(
						`{"errors":[{"type":%q}]}`, failureType)), nil
				})})

			err := client.graphQL(context.Background(),
				Credential{AccessToken: "token"}, "mutation { fixture }", nil)
			if !errors.Is(err, ErrProvider) ||
				errors.Is(err, ErrBranchProtected) {
				t.Fatalf("GraphQL %s error = %v", failureType, err)
			}
		})
	}
}

func TestGraphQLHTTP403SecondaryLimitWithoutHeadersUsesProviderBackoff(
	t *testing.T,
) {
	t.Parallel()
	client := NewClient(&http.Client{Transport: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusForbidden, `{
				"errors":[{
					"type":"FORBIDDEN",
					"message":"You have exceeded a secondary rate limit."
				}]
			}`), nil
		})})

	err := client.graphQL(context.Background(),
		Credential{AccessToken: "token"}, "mutation { fixture }", nil)
	var rateLimit *RateLimitError
	if !errors.As(err, &rateLimit) ||
		rateLimit.RetryAfter != time.Minute {
		t.Fatalf("GraphQL HTTP 403 secondary rate limit = %T %#v", err, err)
	}
}

func TestSecondaryRESTLimitWithoutQuotaHeadersUsesProviderBackoff(t *testing.T) {
	t.Parallel()
	client := NewClient(&http.Client{Transport: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusForbidden,
				`{"message":"You have exceeded a secondary rate limit."}`), nil
		})})
	_, err := client.CanReadRepository(context.Background(),
		Credential{AccessToken: "token"},
		Repository{Owner: "acme", Name: "widget"})
	var rateLimit *RateLimitError
	if !errors.As(err, &rateLimit) ||
		rateLimit.RetryAfter != time.Minute {
		t.Fatalf("secondary provider rate limit = %T %#v", err, err)
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
				`{"owner":{"type":"Organization"},"permissions":{"pull":true,"push":true}}`), nil
		case "/repos/acme/widget/pulls/7":
			return jsonResponse(http.StatusOK,
				`{"node_id":"PR_1","state":"open","draft":false,"merged":false,"mergeable":true,"mergeable_state":"clean","updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"}}`), nil
		case "/repos/acme/widget/collaborators":
			if request.URL.Query().Get("affiliation") != "all" {
				t.Fatal("reviewer candidates omitted visible collaborators")
			}
			return jsonResponse(http.StatusOK,
				`[{"login":"octo"},{"login":"core-user"}]`), nil
		case "/repos/acme/widget/teams":
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
		if path == "/repos/acme/widget/teams" {
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
			path == "/repos/acme/widget/collaborators" ||
			path == "/repos/acme/widget/teams" {
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

func TestCandidatePaginationAndUserOwnerTeamSkip(t *testing.T) {
	t.Parallel()
	teamRequests := 0
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/repos/acme/widget":
			return jsonResponse(http.StatusOK,
				`{"owner":{"type":"Organization"},"permissions":{"pull":true,"push":true}}`), nil
		case "/repos/octo/widget":
			return jsonResponse(http.StatusOK,
				`{"owner":{"type":"User"},"permissions":{"pull":true,"push":true}}`), nil
		case "/repos/acme/widget/pulls/7", "/repos/octo/widget/pulls/7":
			return jsonResponse(http.StatusOK,
				`{"node_id":"PR_1","state":"open","draft":false,"merged":false,`+
					`"mergeable":true,"mergeable_state":"clean",`+
					`"updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"}}`), nil
		case "/repos/acme/widget/assignees":
			if request.URL.Query().Get("page") == "1" {
				users := make([]map[string]string, 100)
				for index := range users {
					users[index] = map[string]string{
						"login": fmt.Sprintf("user-%03d", index),
					}
				}
				body, _ := json.Marshal(users)
				return jsonResponse(http.StatusOK, string(body)), nil
			}
			return jsonResponse(http.StatusOK,
				`[{"login":"target-user"}]`), nil
		case "/repos/octo/widget/collaborators":
			return jsonResponse(http.StatusOK, `[{"login":"octo"}]`), nil
		case "/orgs/octo/teams":
			teamRequests++
			return jsonResponse(http.StatusNotFound, `{}`), nil
		default:
			return jsonResponse(http.StatusNotFound, `{}`), nil
		}
	})
	client := NewClient(&http.Client{Transport: transport})
	permissions := Permissions{
		Metadata: PermissionRead, PullRequests: PermissionWrite,
		Members: PermissionRead,
	}
	page, err := client.ListMutationCandidates(
		context.Background(), 1, Credential{AccessToken: "token"}, permissions,
		PullRequestRef{
			Repository: Repository{Owner: "acme", Name: "widget"}, Number: 7,
		},
		MutationAssignUsers, "target", Page{},
	)
	if err != nil || len(page.Candidates) != 1 ||
		page.Candidates[0].User.Login != "target-user" {
		t.Fatalf("paginated candidates = %#v err=%v", page, err)
	}
	if _, err := client.ListMutationCandidates(
		context.Background(), 2, Credential{AccessToken: "token"}, permissions,
		PullRequestRef{
			Repository: Repository{Owner: "octo", Name: "widget"}, Number: 7,
		},
		MutationRequestReviewers, "", Page{},
	); err != nil {
		t.Fatal(err)
	}
	if teamRequests != 0 {
		t.Fatalf("user-owned repository issued %d team requests", teamRequests)
	}
}

func TestCandidateFetchOmitsTeamsWithoutRepositoryAdministration(t *testing.T) {
	t.Parallel()
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/repos/acme/widget":
			return jsonResponse(http.StatusOK,
				`{"owner":{"type":"Organization"},"permissions":{"pull":true,"push":true}}`), nil
		case "/repos/acme/widget/pulls/7":
			return jsonResponse(http.StatusOK,
				`{"node_id":"PR_1","state":"open","draft":false,"merged":false,`+
					`"mergeable":true,"mergeable_state":"clean",`+
					`"updated_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"}}`), nil
		case "/repos/acme/widget/collaborators":
			return jsonResponse(http.StatusOK, `[{"login":"octo"}]`), nil
		case "/repos/acme/widget/teams":
			return jsonResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		case "/orgs/acme/teams":
			t.Fatal("organization-wide teams endpoint was used")
			return nil, nil
		default:
			return jsonResponse(http.StatusNotFound, `{}`), nil
		}
	})
	client := NewClient(&http.Client{Transport: transport})
	page, err := client.ListMutationCandidates(
		context.Background(), 1, Credential{AccessToken: "token"},
		Permissions{
			Metadata: PermissionRead, PullRequests: PermissionWrite,
			Members: PermissionRead,
		},
		PullRequestRef{
			Repository: Repository{Owner: "acme", Name: "widget"}, Number: 7,
		},
		MutationRequestReviewers, "", Page{},
	)
	if err != nil || len(page.Candidates) != 1 ||
		page.Candidates[0].Kind != CandidateUser ||
		page.Candidates[0].User.Login != "octo" {
		t.Fatalf("repository-scoped candidates = %#v err=%v", page, err)
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
	redirectCalls := 0
	refusedRedirectClient := NewClient(&http.Client{Transport: roundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			redirectCalls++
			response := jsonResponse(http.StatusFound, `{}`)
			response.Header.Set("Location",
				"https://api.github.com/repos/acme/renamed")
			return response, nil
		})})
	if _, err := refusedRedirectClient.CanReadRepository(
		context.Background(), Credential{AccessToken: "ghu_super_secret"},
		Repository{Owner: "acme", Name: "widget"}); !errors.Is(
		err, ErrProvider) {
		t.Fatalf("redirect refusal error = %v", err)
	}
	if redirectCalls != 1 {
		t.Fatalf("redirect followed with credential: calls = %d", redirectCalls)
	}
}

func TestMutationOperandCountIsBounded(t *testing.T) {
	t.Parallel()
	labels := make([]string, maxMutationOperands+1)
	for index := range labels {
		labels[index] = fmt.Sprintf("label-%d", index)
	}
	if err := validateMutation(PullRequestRef{
		Repository: Repository{Owner: "acme", Name: "widget"}, Number: 7,
	}, Mutation{
		Kind: MutationRemoveLabels, Labels: labels,
	}); !errors.Is(err, ErrUnsupportedAction) {
		t.Fatalf("oversize label mutation error = %v", err)
	}
}

func TestMultiLabelRemovalUsesSingleGraphQLMutation(t *testing.T) {
	t.Parallel()
	calls := 0
	client := NewClient(&http.Client{Transport: roundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			calls++
			if request.Method != http.MethodPost ||
				request.URL.Path != GraphQLPath {
				t.Fatalf("label removal request = %s %s",
					request.Method, request.URL.Path)
			}
			var input struct {
				Query     string `json:"query"`
				Variables struct {
					ID     string   `json:"id"`
					Labels []string `json:"labels"`
				} `json:"variables"`
			}
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(input.Query, "removeLabelsFromLabelable") ||
				input.Variables.ID != "PR_1" ||
				!reflect.DeepEqual(input.Variables.Labels, []string{"LA_1", "LA_2"}) {
				t.Fatalf("label mutation = %#v", input)
			}
			return jsonResponse(http.StatusOK, `{"data":{}}`), nil
		})})
	err := client.applyMutation(
		context.Background(), Credential{AccessToken: "token"},
		PullRequestRef{
			Repository: Repository{Owner: "acme", Name: "widget"},
			Number:     7,
		},
		ActionMetadata{
			NodeID: "PR_1",
			LabelIDs: map[string]string{
				"remove-a": "LA_1",
				"remove-b": "LA_2",
			},
		},
		Mutation{
			Kind: MutationRemoveLabels,
			Labels: []string{
				"remove-a",
				"remove-b",
			},
		})
	if err != nil || calls != 1 {
		t.Fatalf("multi-label removal calls=%d err=%v", calls, err)
	}
}

func TestLabelRemovalRejectsMissingCurrentLabelID(t *testing.T) {
	t.Parallel()
	client := NewClient(&http.Client{Transport: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			t.Fatal("stale label removal dispatched")
			return nil, nil
		})})
	err := client.applyMutation(
		context.Background(), Credential{AccessToken: "token"},
		PullRequestRef{
			Repository: Repository{Owner: "acme", Name: "widget"},
			Number:     7,
		},
		ActionMetadata{
			NodeID:   "PR_1",
			LabelIDs: map[string]string{"remove-a": "LA_1"},
		},
		Mutation{
			Kind:   MutationRemoveLabels,
			Labels: []string{"remove-a", "missing"},
		})
	if !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("missing label ID error = %v", err)
	}
}

func TestMergeConflictMapsToStaleRevision(t *testing.T) {
	t.Parallel()
	client := NewClient(&http.Client{Transport: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusConflict, `{}`), nil
		})})
	reference := PullRequestRef{
		Repository: Repository{Owner: "acme", Name: "widget"}, Number: 7,
	}
	credential := Credential{AccessToken: "token"}
	err := client.applyMutation(
		context.Background(), credential, reference,
		ActionMetadata{HeadSHA: "outdated"},
		Mutation{
			Kind: MutationMerge, MergeMethod: MergeMethodSquash,
			Confirmed: true,
		})
	if !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("merge conflict error = %v", err)
	}
	err = client.applyMutation(
		context.Background(), credential, reference, ActionMetadata{},
		Mutation{Kind: MutationClose})
	if !errors.Is(err, ErrProvider) || errors.Is(err, ErrBranchProtected) {
		t.Fatalf("non-merge conflict error = %v", err)
	}
}

func TestRESTValidationAndMergeProtectionMappingsAreOperationSpecific(
	t *testing.T,
) {
	t.Parallel()
	reference := PullRequestRef{
		Repository: Repository{Owner: "acme", Name: "widget"}, Number: 7,
	}
	credential := Credential{AccessToken: "token"}
	validationClient := NewClient(&http.Client{Transport: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusUnprocessableEntity,
				`{"message":"Validation Failed"}`), nil
		})})
	err := validationClient.applyMutation(
		context.Background(), credential, reference, ActionMetadata{},
		Mutation{Kind: MutationAddLabels, Labels: []string{"stale"}})
	if !errors.Is(err, ErrProvider) || errors.Is(err, ErrBranchProtected) {
		t.Fatalf("label validation error = %v", err)
	}
	err = validationClient.applyMutation(
		context.Background(), credential, reference,
		ActionMetadata{HeadSHA: "current"},
		Mutation{
			Kind: MutationMerge, MergeMethod: MergeMethodSquash,
			Confirmed: true,
		})
	if !errors.Is(err, ErrProvider) || errors.Is(err, ErrStaleRevision) {
		t.Fatalf("merge validation error = %v", err)
	}

	protectedClient := NewClient(&http.Client{Transport: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusMethodNotAllowed, `{}`), nil
		})})
	err = protectedClient.applyMutation(
		context.Background(), credential, reference,
		ActionMetadata{HeadSHA: "current"},
		Mutation{
			Kind: MutationMerge, MergeMethod: MergeMethodSquash,
			Confirmed: true,
		})
	if !errors.Is(err, ErrBranchProtected) {
		t.Fatalf("merge protection error = %v", err)
	}
}

func TestAssigneeMutationsRespectGitHubLimit(t *testing.T) {
	t.Parallel()
	users := make([]User, maxGitHubAssignees+1)
	for index := range users {
		users[index] = User{Login: fmt.Sprintf("user-%02d", index)}
	}
	reference := PullRequestRef{
		Repository: Repository{Owner: "acme", Name: "widget"}, Number: 7,
	}
	if err := validateMutation(
		reference,
		Mutation{Kind: MutationAssignUsers, Users: users},
	); !errors.Is(err, ErrUnsupportedAction) {
		t.Fatalf("oversize assignee mutation error = %v", err)
	}
	if err := validateMutation(
		reference,
		Mutation{
			Kind:  MutationUnassignUsers,
			Users: users[:maxGitHubAssignees],
		},
	); err != nil {
		t.Fatalf("maximum assignee mutation error = %v", err)
	}
	if err := validateMutation(
		PullRequestRef{
			Repository: Repository{Owner: "acme", Name: "widget"}, Number: 7,
		},
		Mutation{Kind: MutationRequestReviewers, Users: users},
	); err != nil {
		t.Fatalf("reviewer mutation inherited assignee cap: %v", err)
	}
}

func TestArchivedRepositoriesAdvertiseNoMutations(t *testing.T) {
	t.Parallel()
	client := NewClient(&http.Client{Transport: roundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/repos/acme/widget" {
				return jsonResponse(http.StatusOK,
					`{"archived":true,"allow_merge_commit":true,`+
						`"allow_auto_merge":true,`+
						`"permissions":{"pull":true,"push":true}}`), nil
			}
			return jsonResponse(http.StatusOK,
				`{"node_id":"PR_1","state":"open","mergeable":true,`+
					`"mergeable_state":"clean",`+
					`"updated_at":"2026-01-01T00:00:00Z",`+
					`"head":{"sha":"abc"}}`), nil
		})})
	metadata, err := client.ActionMetadata(
		context.Background(), 1, Credential{AccessToken: "token"},
		Permissions{
			Metadata: PermissionRead, Contents: PermissionWrite,
			PullRequests: PermissionWrite,
		},
		PullRequestRef{
			Repository: Repository{Owner: "acme", Name: "widget"}, Number: 7,
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.Supported) != 0 {
		t.Fatalf("archived repository mutations = %v", metadata.Supported)
	}
}

func TestAutoMergeRequiresRepositoryAndPullRequestEligibility(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		repository string
		pull       string
		supported  bool
	}{
		{
			name: "eligible",
			repository: `{"allow_merge_commit":true,"allow_auto_merge":true,` +
				`"permissions":{"pull":true,"push":true}}`,
			pull: `{"node_id":"PR_1","state":"open","mergeable":true,` +
				`"mergeable_state":"blocked","updated_at":"2026-01-01T00:00:00Z",` +
				`"head":{"sha":"abc"}}`,
			supported: true,
		},
		{
			name: "repository disabled",
			repository: `{"allow_merge_commit":true,"allow_auto_merge":false,` +
				`"permissions":{"pull":true,"push":true}}`,
			pull: `{"node_id":"PR_1","state":"open","mergeable":true,` +
				`"mergeable_state":"blocked","updated_at":"2026-01-01T00:00:00Z",` +
				`"head":{"sha":"abc"}}`,
		},
		{
			name: "immediately mergeable",
			repository: `{"allow_merge_commit":true,"allow_auto_merge":true,` +
				`"permissions":{"pull":true,"push":true}}`,
			pull: `{"node_id":"PR_1","state":"open","mergeable":true,` +
				`"mergeable_state":"clean","updated_at":"2026-01-01T00:00:00Z",` +
				`"head":{"sha":"abc"}}`,
		},
		{
			name: "mergeability pending",
			repository: `{"allow_merge_commit":true,"allow_auto_merge":true,` +
				`"permissions":{"pull":true,"push":true}}`,
			pull: `{"node_id":"PR_1","state":"open","mergeable":null,` +
				`"mergeable_state":"unknown","updated_at":"2026-01-01T00:00:00Z",` +
				`"head":{"sha":"abc"}}`,
		},
		{
			name: "conflicting",
			repository: `{"allow_merge_commit":true,"allow_auto_merge":true,` +
				`"permissions":{"pull":true,"push":true}}`,
			pull: `{"node_id":"PR_1","state":"open","mergeable":false,` +
				`"mergeable_state":"dirty","updated_at":"2026-01-01T00:00:00Z",` +
				`"head":{"sha":"abc"}}`,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := NewClient(&http.Client{Transport: roundTripFunc(
				func(request *http.Request) (*http.Response, error) {
					if request.URL.Path == "/repos/acme/widget" {
						return jsonResponse(http.StatusOK, test.repository), nil
					}
					return jsonResponse(http.StatusOK, test.pull), nil
				})})
			metadata, err := client.ActionMetadata(
				context.Background(), 1, Credential{AccessToken: "token"},
				Permissions{
					Metadata: PermissionRead, Contents: PermissionWrite,
					PullRequests: PermissionWrite,
				},
				PullRequestRef{
					Repository: Repository{Owner: "acme", Name: "widget"},
					Number:     7,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if metadata.Supported[MutationEnableAutoMerge] != test.supported {
				t.Fatalf("auto-merge supported = %v", metadata.Supported)
			}
		})
	}
}

func TestActionMetadataIncludesCurrentPullRequestOperands(t *testing.T) {
	t.Parallel()
	client := NewClient(&http.Client{Transport: roundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/repos/acme/widget" {
				return jsonResponse(http.StatusOK,
					`{"allow_merge_commit":true,"permissions":{"pull":true,"push":true}}`), nil
			}
			return jsonResponse(http.StatusOK, `{
				"node_id":"PR_1",
				"title":"Current title",
				"user":{"login":"author"},
				"state":"open",
				"draft":false,
				"merged":false,
				"mergeable":true,
				"mergeable_state":"clean",
				"updated_at":"2026-02-03T04:05:06Z",
				"head":{"sha":"abc"},
				"requested_reviewers":[{"login":"reviewer"}],
				"requested_teams":[{"slug":"core"}],
				"assignees":[{"login":"assignee"}],
				"labels":[{"name":"ready"}]
			}`), nil
		})})
	metadata, err := client.ActionMetadata(
		context.Background(), 1, Credential{AccessToken: "token"},
		Permissions{
			Metadata: PermissionRead, PullRequests: PermissionWrite,
		},
		PullRequestRef{
			Repository: Repository{Owner: "acme", Name: "widget"},
			Number:     7,
		})
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Title != "Current title" ||
		metadata.Author.Login != "author" ||
		!metadata.UpdatedAt.Equal(time.Date(
			2026, 2, 3, 4, 5, 6, 0, time.UTC)) ||
		len(metadata.Reviewers) != 1 ||
		metadata.Reviewers[0].Login != "reviewer" ||
		len(metadata.ReviewerTeams) != 1 ||
		metadata.ReviewerTeams[0] != (Team{
			Organization: "acme", Slug: "core",
		}) ||
		len(metadata.Assignees) != 1 ||
		metadata.Assignees[0].Login != "assignee" ||
		len(metadata.Labels) != 1 || metadata.Labels[0] != "ready" {
		t.Fatalf("current action metadata = %#v", metadata)
	}
}

func TestMutationKeepsProviderSlotThroughResultReload(t *testing.T) {
	t.Parallel()
	var client *Client
	mutated := false
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == "/repos/acme/widget" &&
			request.Method == http.MethodGet:
			return jsonResponse(http.StatusOK,
				`{"permissions":{"pull":true,"push":true}}`), nil
		case request.URL.Path == "/repos/acme/widget/pulls/7" &&
			request.Method == http.MethodGet:
			updatedAt := "2026-01-01T00:00:00Z"
			state := "open"
			if mutated {
				updatedAt = "2026-01-01T00:01:00Z"
				state = "closed"
			}
			return jsonResponse(http.StatusOK, fmt.Sprintf(
				`{"node_id":"PR_1","state":%q,"mergeable":true,`+
					`"updated_at":%q,"head":{"sha":"abc"}}`,
				state, updatedAt)), nil
		case request.URL.Path == "/repos/acme/widget/pulls/7" &&
			request.Method == http.MethodPatch:
			mutated = true
			client.concurrency.mu.Lock()
			client.concurrency.limit = 0
			client.concurrency.mu.Unlock()
			return jsonResponse(http.StatusOK, `{}`), nil
		case request.URL.Path == GraphQLPath &&
			request.Method == http.MethodPost:
			return jsonResponse(http.StatusOK, `{
				"data":{"node":{
					"reviewDecision":"APPROVED",
					"statusCheckRollup":{
						"state":"FAILURE",
						"contexts":{
							"totalCount":7,
							"checkRunCountsByState":[
								{"state":"COMPLETED","count":1},
								{"state":"PENDING","count":1},
								{"state":"SUCCESS","count":2}
							],
							"statusContextCountsByState":[
								{"state":"SUCCESS","count":1},
								{"state":"ERROR","count":2}
							]
						}
					}
				}}
			}`), nil
		default:
			t.Fatalf("unexpected mutation request %s %s",
				request.Method, request.URL.Path)
			return nil, nil
		}
	})
	client = NewClient(&http.Client{Transport: transport})
	client.concurrency.limit = 1
	credential := Credential{AccessToken: "ghu_viewer"}
	permissions := Permissions{
		Metadata: PermissionRead, PullRequests: PermissionWrite,
	}
	reference := PullRequestRef{
		Repository: Repository{Owner: "acme", Name: "widget"}, Number: 7,
	}
	initial, err := client.ActionMetadata(
		context.Background(), 42, credential, permissions, reference)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Mutate(
		context.Background(), "viewer", 42, credential, permissions,
		reference, initial.Revision, Mutation{Kind: MutationClose})
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision == initial.Revision || result.Metadata.IsOpen ||
		result.RefreshRequired ||
		result.Metadata.ReviewDecision != ReviewDecisionApproved ||
		result.Metadata.ChecksState != ChecksStateFailure ||
		result.Metadata.PendingChecks != 1 ||
		result.Metadata.SuccessfulChecks != 4 ||
		result.Metadata.FailedChecks != 2 {
		t.Fatalf("mutation result = %#v initial=%d", result, initial.Revision)
	}
}

func TestMutationRequiresRefreshWhenResultReloadFails(t *testing.T) {
	t.Parallel()
	mutated := false
	client := NewClient(&http.Client{Transport: roundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			switch {
			case request.URL.Path == "/repos/acme/widget" &&
				request.Method == http.MethodGet:
				if mutated {
					return jsonResponse(http.StatusServiceUnavailable, `{}`), nil
				}
				return jsonResponse(http.StatusOK,
					`{"permissions":{"pull":true,"push":true}}`), nil
			case request.URL.Path == "/repos/acme/widget/pulls/7" &&
				request.Method == http.MethodGet:
				return jsonResponse(http.StatusOK,
					`{"node_id":"PR_1","state":"open","mergeable":true,`+
						`"updated_at":"2026-01-01T00:00:00Z",`+
						`"head":{"sha":"abc"}}`), nil
			case request.URL.Path == "/repos/acme/widget/pulls/7" &&
				request.Method == http.MethodPatch:
				mutated = true
				return jsonResponse(http.StatusOK, `{}`), nil
			default:
				t.Fatalf("unexpected mutation request %s %s",
					request.Method, request.URL.Path)
				return nil, nil
			}
		})})
	credential := Credential{AccessToken: "ghu_viewer"}
	permissions := Permissions{
		Metadata: PermissionRead, PullRequests: PermissionWrite,
	}
	reference := PullRequestRef{
		Repository: Repository{Owner: "acme", Name: "widget"}, Number: 7,
	}
	initial, err := client.ActionMetadata(
		context.Background(), 42, credential, permissions, reference)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Mutate(
		context.Background(), "viewer", 42, credential, permissions,
		reference, initial.Revision, Mutation{Kind: MutationClose})
	if err != nil || !result.RefreshRequired ||
		result.Kind != MutationClose || result.Revision != 0 {
		t.Fatalf("mutation result = %#v err=%v", result, err)
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
		case "/user/installations/42/repositories":
			return jsonResponse(http.StatusOK,
				`{"repositories":[{"name":"visible","owner":{"login":"acme"},`+
					`"permissions":{"pull":true}}]}`), nil
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

func TestRepositoryAuthorizationIsBoundToSelectedInstallation(t *testing.T) {
	t.Parallel()
	client := NewClient(&http.Client{Transport: roundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != "/user/installations/42/repositories" {
				t.Fatalf("unexpected repository authorization path %q",
					request.URL.Path)
			}
			return jsonResponse(http.StatusOK, `{"repositories":[
				{"name":"selected","owner":{"login":"acme"},
				 "permissions":{"pull":true}},
				{"name":"other","owner":{"login":"elsewhere"},
				 "permissions":{"pull":true}},
				{"name":"hidden","owner":{"login":"acme"},
				 "permissions":{"pull":false}}
			]}`), nil
		})})
	credential := Credential{AccessToken: "ghu_viewer"}
	allowed, err := client.CanReadRepositoryForInstallation(
		context.Background(), 42, credential,
		Repository{Owner: "acme", Name: "selected"})
	if err != nil || !allowed {
		t.Fatalf("selected repository allowed=%v err=%v", allowed, err)
	}
	allowed, err = client.CanReadRepositoryForInstallation(
		context.Background(), 42, credential,
		Repository{Owner: "acme", Name: "installed-elsewhere"})
	if err != nil || allowed {
		t.Fatalf("other installation repository allowed=%v err=%v",
			allowed, err)
	}
	readable, err := client.ListReadableRepositoriesForInstallation(
		context.Background(), 42, credential)
	if err != nil || len(readable) != 2 {
		t.Fatalf("readable repositories = %#v err=%v", readable, err)
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
