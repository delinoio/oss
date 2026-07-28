package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func fixtureHTTPClient(function roundTripFunc) *http.Client {
	return &http.Client{Transport: function, Timeout: 2 * time.Second}
}

func jsonResponse(request *http.Request, status int, value any) *http.Response {
	data, _ := json.Marshal(value)
	return &http.Response{
		StatusCode: status, Body: io.NopCloser(strings.NewReader(string(data))),
		Header: make(http.Header), Request: request,
	}
}

func fixtureToken(t *testing.T) UserToken {
	t.Helper()
	token, err := NewUserToken("ghu_fixture_user_authorization_token_123456")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func fixtureRepository() Repository {
	return Repository{
		ID: 1, NodeID: "R_fixture", Owner: "delinoio", Name: "oss",
		IssuesEnabled: true, CanSubmit: true,
	}
}

func fixtureIssueInput() IssueInput {
	return IssueInput{
		SubmissionID:       fixtureSubmissionID,
		Title:              "Fixture issue",
		RepositoryResponse: "## Response\n\nObserved.",
		Capture: CaptureMetadata{
			Environment: []CaptureField{{Key: "OS", Value: "Linux"}},
		},
		Labels:    []Label{{Name: "bug"}},
		Assignees: []Assignee{{Login: "octocat"}},
	}
}

func TestRefreshUserCredentialRotatesAccessAndRefreshTokens(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	oldRefreshToken := "ghr_fixture_old_refresh_token_123456"
	httpClient := fixtureHTTPClient(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" || request.URL.Host != "github.com" ||
			request.URL.Path != "/login/oauth/access_token" {
			t.Fatalf("unexpected refresh URL %s", request.URL)
		}
		var payload map[string]string
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["client_id"] != "fixture-realqa-client" ||
			payload["client_secret"] != "fixture-realqa-client-secret-value" ||
			payload["grant_type"] != "refresh_token" ||
			payload["refresh_token"] != oldRefreshToken {
			t.Fatalf("unexpected refresh payload %#v", payload)
		}
		return jsonResponse(request, http.StatusOK, map[string]any{
			"access_token":             "ghu_fixture_new_access_token_123456",
			"refresh_token":            "ghr_fixture_new_refresh_token_123456",
			"expires_in":               28800,
			"refresh_token_expires_in": 15897600,
		}), nil
	})
	client, err := NewClient(ClientConfig{
		HTTPClient: httpClient, ProjectPermission: ProjectPermissionNone,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	credential, token, err := client.RefreshUserCredential(
		context.Background(),
		"fixture-realqa-client",
		"fixture-realqa-client-secret-value",
		oldRefreshToken,
	)
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccessToken != "ghu_fixture_new_access_token_123456" ||
		credential.RefreshToken != "ghr_fixture_new_refresh_token_123456" ||
		!credential.ExpiresAt.Equal(now.Add(8*time.Hour)) ||
		!credential.RefreshExpiresAt.Equal(now.Add(184*24*time.Hour)) ||
		token.value != credential.AccessToken {
		t.Fatalf("unexpected refreshed credential %#v", credential)
	}
}

func TestListInstallationsAndRepositoriesUsesUserAuthorization(t *testing.T) {
	t.Parallel()
	var authorizationHeaders []string
	httpClient := fixtureHTTPClient(func(request *http.Request) (*http.Response, error) {
		authorizationHeaders = append(authorizationHeaders,
			request.Header.Get("Authorization"))
		switch request.URL.Path {
		case "/user/installations":
			return jsonResponse(request, http.StatusOK, map[string]any{
				"installations": []any{map[string]any{
					"id": 77,
					"account": map[string]any{
						"id": 9, "login": "delinoio", "type": "Organization",
					},
					"permissions": map[string]any{
						"issues": "write", "metadata": "read", "contents": "read",
					},
				}},
			}), nil
		case "/user/installations/77/repositories":
			return jsonResponse(request, http.StatusOK, map[string]any{
				"repositories": []any{map[string]any{
					"id": 1, "node_id": "R_fixture", "name": "oss",
					"owner":      map[string]any{"id": 9, "login": "delinoio", "type": "Organization"},
					"has_issues": true, "permissions": map[string]any{"push": true},
				}},
			}), nil
		default:
			t.Fatalf("unexpected request %s", request.URL.String())
			return nil, nil
		}
	})
	client, err := NewClient(ClientConfig{
		HTTPClient: httpClient, ProjectPermission: ProjectPermissionNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	installations, err := client.ListInstallations(context.Background(), fixtureToken(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(installations) != 1 || installations[0].ID != 77 ||
		installations[0].AccountKind != AccountKindOrganization {
		t.Fatalf("unexpected installations: %#v", installations)
	}
	repositories, err := client.ListRepositories(
		context.Background(), fixtureToken(t), installations[0].ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(repositories) != 1 || !repositories[0].CanSubmit {
		t.Fatalf("unexpected repositories: %#v", repositories)
	}
	for _, value := range authorizationHeaders {
		if value != "Bearer ghu_fixture_user_authorization_token_123456" {
			t.Fatalf("expected user token, got %q", value)
		}
	}
}

func TestInstallationPermissionValidationRejectsMissingAndExcess(t *testing.T) {
	t.Parallel()
	for _, permissions := range []map[string]any{
		{"issues": "read", "metadata": "read", "contents": "read"},
		{"issues": "write", "metadata": "read", "contents": "write"},
		{"issues": "write", "metadata": "read", "contents": "read", "repository_projects": "write"},
		{"issues": "write", "metadata": "read", "contents": "read", "actions": "read"},
	} {
		permissions := permissions
		httpClient := fixtureHTTPClient(func(request *http.Request) (*http.Response, error) {
			return jsonResponse(request, http.StatusOK, map[string]any{
				"installations": []any{map[string]any{
					"id": 77, "account": map[string]any{
						"id": 9, "login": "delinoio", "type": "Organization",
					},
					"permissions": permissions,
				}},
			}), nil
		})
		client, err := NewClient(ClientConfig{
			HTTPClient: httpClient, ProjectPermission: ProjectPermissionNone,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = client.ListInstallations(
			context.Background(), fixtureToken(t),
		); err == nil {
			t.Fatalf("expected permissions %#v to fail", permissions)
		}
	}
}

func TestGetRepositoryDefinitionsParsesProviderFixtures(t *testing.T) {
	t.Parallel()
	markdown, err := os.ReadFile("testdata/bug.md")
	if err != nil {
		t.Fatal(err)
	}
	form, err := os.ReadFile("testdata/bug.yml")
	if err != nil {
		t.Fatal(err)
	}
	httpClient := fixtureHTTPClient(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/repos/delinoio/oss/contents/.github/ISSUE_TEMPLATE":
			return jsonResponse(request, http.StatusOK, []any{
				map[string]any{
					"type": "file", "path": ".github/ISSUE_TEMPLATE/bug.md",
					"sha": "markdown-etag",
				},
				map[string]any{
					"type": "file", "path": ".github/ISSUE_TEMPLATE/bug.yml",
					"sha": "form-etag",
				},
				map[string]any{
					"type": "file", "path": ".github/ISSUE_TEMPLATE/config.yml",
					"sha": "config-etag",
				},
			}), nil
		case "/repos/delinoio/oss/contents/.github/ISSUE_TEMPLATE/bug.md":
			return jsonResponse(request, http.StatusOK, map[string]any{
				"type": "file", "encoding": "base64",
				"content": base64.StdEncoding.EncodeToString(markdown),
			}), nil
		case "/repos/delinoio/oss/contents/.github/ISSUE_TEMPLATE/bug.yml":
			return jsonResponse(request, http.StatusOK, map[string]any{
				"type": "file", "encoding": "base64",
				"content": base64.StdEncoding.EncodeToString(form),
			}), nil
		default:
			t.Fatalf("unexpected content request %s", request.URL)
			return nil, nil
		}
	})
	client, err := NewClient(ClientConfig{
		HTTPClient: httpClient, ProjectPermission: ProjectPermissionNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := client.GetRepositoryDefinitions(
		context.Background(), fixtureToken(t), fixtureRepository())
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions.Markdown) != 1 || len(definitions.Forms) != 1 ||
		definitions.Markdown[0].Definition.ETag != "markdown-etag" ||
		definitions.Forms[0].Definition.ETag != "form-etag" {
		t.Fatalf("unexpected definitions: %#v", definitions)
	}
}

func TestAmbiguousCreateReconcilesMarkerBeforeRetry(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	var sequence []string
	postCount := 0
	issueListCount := 0
	httpClient := fixtureHTTPClient(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case request.URL.Path == "/repos/delinoio/oss/issues" &&
			request.Method == http.MethodGet:
			sequence = append(sequence, "reconcile")
			if request.URL.Query().Has("creator") {
				t.Fatal("reconciliation was limited to the reauthorized user")
			}
			issueListCount++
			if issueListCount == 1 {
				return jsonResponse(request, http.StatusOK, []any{}), nil
			}
			return jsonResponse(request, http.StatusOK, []any{map[string]any{
				"id": 99, "node_id": "I_fixture", "number": 757,
				"html_url":   "https://github.com/delinoio/oss/issues/757",
				"body":       "<!-- realqa:submission:" + fixtureSubmissionID.String() + " -->",
				"created_at": now,
			}}), nil
		case request.URL.Path == "/repos/delinoio/oss/issues" &&
			request.Method == http.MethodPost:
			sequence = append(sequence, "post")
			postCount++
			return nil, errors.New("fixture timeout containing secret-provider-body")
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.String())
			return nil, nil
		}
	})
	client, err := NewClient(ClientConfig{
		HTTPClient: httpClient, ProjectPermission: ProjectPermissionNone,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	issue, err := client.CreateIssue(
		context.Background(), fixtureToken(t), fixtureRepository(), fixtureIssueInput(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if issue.Number != 757 || postCount != 1 {
		t.Fatalf("expected one reconciled create, got issue=%#v posts=%d", issue, postCount)
	}
	if strings.Join(sequence, ",") != "reconcile,post,reconcile" {
		t.Fatalf("unexpected ordering %v", sequence)
	}
}

func TestAmbiguousCreateRetriesOnlyAfterDefinitiveReconciliation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	var sequence []string
	postCount := 0
	httpClient := fixtureHTTPClient(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == "/repos/delinoio/oss/issues" &&
			request.Method == http.MethodGet:
			sequence = append(sequence, "reconcile")
			return jsonResponse(request, http.StatusOK, []any{}), nil
		case request.URL.Path == "/repos/delinoio/oss/issues" &&
			request.Method == http.MethodPost:
			sequence = append(sequence, "post")
			postCount++
			if postCount == 1 {
				return nil, errors.New("fixture timeout")
			}
			return jsonResponse(request, http.StatusCreated, map[string]any{
				"id": 99, "node_id": "I_fixture", "number": 758,
				"html_url": "https://github.com/delinoio/oss/issues/758",
				"body":     "", "created_at": now,
			}), nil
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.String())
			return nil, nil
		}
	})
	client, err := NewClient(ClientConfig{
		HTTPClient: httpClient, ProjectPermission: ProjectPermissionNone,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	issue, err := client.CreateIssue(
		context.Background(), fixtureToken(t), fixtureRepository(), fixtureIssueInput(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if issue.Number != 758 || postCount != 2 {
		t.Fatalf("unexpected result issue=%#v posts=%d", issue, postCount)
	}
	if strings.Join(sequence, ",") !=
		"reconcile,post,reconcile,post" {
		t.Fatalf("retry occurred without reconciliation: %v", sequence)
	}
}

func TestAmbiguousCreateDoesNotRetryWhenReconciliationIsTruncated(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	postCount := 0
	reconcileCount := 0
	httpClient := fixtureHTTPClient(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == "/repos/delinoio/oss/issues" &&
			request.Method == http.MethodGet:
			reconcileCount++
			if reconcileCount == 1 {
				return jsonResponse(request, http.StatusOK, []any{}), nil
			}
			candidates := make([]any, 100)
			for index := range candidates {
				candidates[index] = map[string]any{
					"id": index + 1, "node_id": "I_unrelated", "number": index + 1,
					"html_url":   "https://github.com/delinoio/oss/issues/1",
					"body":       "unrelated issue",
					"created_at": now,
				}
			}
			return jsonResponse(request, http.StatusOK, candidates), nil
		case request.URL.Path == "/repos/delinoio/oss/issues" &&
			request.Method == http.MethodPost:
			postCount++
			return nil, errors.New("fixture timeout")
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.String())
			return nil, nil
		}
	})
	client, err := NewClient(ClientConfig{
		HTTPClient: httpClient, ProjectPermission: ProjectPermissionNone,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CreateIssue(
		context.Background(), fixtureToken(t), fixtureRepository(), fixtureIssueInput(),
	)
	if !errors.Is(err, ErrAmbiguousCreate) {
		t.Fatalf("expected ambiguous create, got %v", err)
	}
	if postCount != 1 {
		t.Fatalf("unsafe retry occurred after truncated reconciliation: %d posts", postCount)
	}
}

func TestProjectAssignmentFailureDoesNotMaskCreatedIssue(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	postCount := 0
	httpClient := fixtureHTTPClient(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == "/repos/delinoio/oss/issues" &&
			request.Method == http.MethodGet:
			return jsonResponse(request, http.StatusOK, []any{}), nil
		case request.URL.Path == "/repos/delinoio/oss/issues" &&
			request.Method == http.MethodPost:
			postCount++
			return jsonResponse(request, http.StatusCreated, map[string]any{
				"id": 99, "node_id": "I_fixture", "number": 758,
				"html_url": "https://github.com/delinoio/oss/issues/758",
				"body":     "", "created_at": now,
			}), nil
		case request.URL.Path == "/graphql":
			return jsonResponse(request, http.StatusOK, map[string]any{
				"errors": []any{map[string]any{"type": "NOT_FOUND"}},
			}), nil
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.String())
			return nil, nil
		}
	})
	client, err := NewClient(ClientConfig{
		HTTPClient: httpClient, ProjectPermission: ProjectPermissionRepository,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	input := fixtureIssueInput()
	input.Extension.Projects = []Project{{
		NodeID: "PVT_fixture", Permission: ProjectPermissionRepository,
	}}
	issue, err := client.CreateIssue(
		context.Background(), fixtureToken(t), fixtureRepository(), input,
	)
	if err != nil {
		t.Fatalf("created issue was reported as failed: %v", err)
	}
	if issue.Number != 758 || postCount != 1 ||
		len(issue.ProjectAssignments) != 1 ||
		issue.ProjectAssignments[0].Disposition != ProjectAssignmentFailed {
		t.Fatalf("unexpected create disposition: %#v", issue)
	}
}

func TestProviderErrorsRedactTokensAndBodies(t *testing.T) {
	t.Parallel()
	secretToken := "ghu_top_secret_user_token_123456789"
	token, err := NewUserToken(secretToken)
	if err != nil {
		t.Fatal(err)
	}
	httpClient := fixtureHTTPClient(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnprocessableEntity,
			Body: io.NopCloser(strings.NewReader(
				`{"message":"secret-provider-body","token":"` + secretToken + `"}`)),
			Header: make(http.Header), Request: request,
		}, nil
	})
	client, err := NewClient(ClientConfig{
		HTTPClient: httpClient, ProjectPermission: ProjectPermissionNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ListInstallations(context.Background(), token)
	if err == nil {
		t.Fatal("expected provider rejection")
	}
	if strings.Contains(err.Error(), secretToken) ||
		strings.Contains(err.Error(), "secret-provider-body") {
		t.Fatalf("provider secret leaked in %q", err)
	}
}
