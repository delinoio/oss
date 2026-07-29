package github

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	apiVersion            = "2022-11-28"
	defaultCandidateLimit = 50
	maxCandidateLimit     = 100
)

type Client struct {
	http        *http.Client
	now         func() time.Time
	mutations   *userRateLimiter
	concurrency *installationLimiter
}

func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{
		http: httpClient, now: func() time.Time { return time.Now().UTC() },
		mutations:   newUserRateLimiter(30, time.Minute),
		concurrency: newInstallationLimiter(4),
	}
}

type apiError struct {
	Message string `json:"message"`
}

func (client *Client) do(
	ctx context.Context,
	credential Credential,
	method, path string,
	input any,
	output any,
) (http.Header, error) {
	if client == nil || client.http == nil ||
		credential.Validate(client.now()) != nil {
		return nil, ErrPermissionDenied
	}
	target, err := apiURL(path)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if input != nil {
		encoded, encodeErr := json.Marshal(input)
		if encodeErr != nil {
			return nil, ErrProvider
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, ErrProvider
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	request.Header.Set("X-GitHub-Api-Version", apiVersion)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.http.Do(request)
	if err != nil {
		return nil, ErrProvider
	}
	defer response.Body.Close()
	if response.Request != nil &&
		(response.Request.URL.Scheme != "https" ||
			response.Request.URL.Host != "api.github.com" ||
			response.Request.URL.User != nil) {
		return nil, ErrUnsupportedHost
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, ErrProvider
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.Header, mapStatus(response.StatusCode, response.Header, client.now())
	}
	if output != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, output); err != nil {
			return nil, ErrProvider
		}
	}
	return response.Header, nil
}

type installationResponse struct {
	ID          uint64  `json:"id"`
	SuspendedAt *string `json:"suspended_at"`
	Account     struct {
		ID    uint64 `json:"id"`
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"account"`
	Permissions map[string]string `json:"permissions"`
}

func (client *Client) ListInstallations(
	ctx context.Context,
	credential Credential,
	page Page,
) ([]Installation, string, error) {
	limit := normalizedLimit(page.Limit)
	offset, err := decodeProviderCursor(page.Cursor)
	if err != nil {
		return nil, "", ErrUnsupportedAction
	}
	path := fmt.Sprintf("/user/installations?per_page=%d&page=%d",
		limit, offset/limit+1)
	var response struct {
		Installations []installationResponse `json:"installations"`
	}
	if _, err := client.do(ctx, credential, http.MethodGet, path, nil, &response); err != nil {
		return nil, "", err
	}
	installations := make([]Installation, 0, len(response.Installations))
	for _, item := range response.Installations {
		kind := AccountKindUnknown
		switch strings.ToLower(item.Account.Type) {
		case "user":
			kind = AccountKindUser
		case "organization":
			kind = AccountKindOrganization
		default:
			continue
		}
		installations = append(installations, Installation{
			ID: item.ID, AccountID: item.Account.ID,
			AccountLogin: item.Account.Login, AccountKind: kind,
			Permissions: parsePermissions(item.Permissions),
			Suspended:   item.SuspendedAt != nil,
		})
	}
	next := ""
	if len(response.Installations) == limit {
		next = encodeProviderCursor(offset + limit)
	}
	return installations, next, nil
}

func parsePermissions(values map[string]string) Permissions {
	return Permissions{
		Metadata:     parsePermission(values["metadata"]),
		PullRequests: parsePermission(values["pull_requests"]),
		Checks:       parsePermission(values["checks"]),
		Members:      parsePermission(values["members"]),
	}
}

func parsePermission(value string) PermissionLevel {
	switch strings.ToLower(value) {
	case "read":
		return PermissionRead
	case "write":
		return PermissionWrite
	case "admin":
		return PermissionAdmin
	default:
		return PermissionNone
	}
}

type repositoryResponse struct {
	NodeID           string `json:"node_id"`
	AllowMergeCommit bool   `json:"allow_merge_commit"`
	AllowSquashMerge bool   `json:"allow_squash_merge"`
	AllowRebaseMerge bool   `json:"allow_rebase_merge"`
	Permissions      struct {
		Pull     bool `json:"pull"`
		Triage   bool `json:"triage"`
		Push     bool `json:"push"`
		Maintain bool `json:"maintain"`
		Admin    bool `json:"admin"`
	} `json:"permissions"`
}

type searchResponse struct {
	TotalCount        int  `json:"total_count"`
	IncompleteResults bool `json:"incomplete_results"`
	Items             []struct {
		RepositoryURL string `json:"repository_url"`
		Number        uint64 `json:"number"`
		Title         string `json:"title"`
		UpdatedAt     string `json:"updated_at"`
		User          struct {
			Login string `json:"login"`
		} `json:"user"`
		PullRequest json.RawMessage `json:"pull_request"`
	} `json:"items"`
}

// SearchPullRequests executes a GitHub.com search using the current viewer's
// user authorization token, then rechecks repository access before returning
// any identity-bearing field. GitHub's upstream total_count is deliberately
// decoded but never copied into the result.
func (client *Client) SearchPullRequests(
	ctx context.Context,
	installationID uint64,
	credential Credential,
	query string,
	page Page,
) (SearchPage, error) {
	if query == "" || len(query) > 4096 ||
		strings.ContainsAny(query, "\x00\r\n") {
		return SearchPage{}, ErrUnsupportedAction
	}
	release, err := client.concurrency.acquire(installationID)
	if err != nil {
		return SearchPage{}, err
	}
	defer release()

	limit := normalizedLimit(page.Limit)
	offset, err := decodeProviderCursor(page.Cursor)
	if err != nil {
		return SearchPage{}, ErrUnsupportedAction
	}
	values := url.Values{}
	values.Set("q", query+" is:pr")
	values.Set("per_page", strconv.Itoa(limit))
	values.Set("page", strconv.Itoa(offset/limit+1))
	var response searchResponse
	if _, err := client.do(ctx, credential, http.MethodGet,
		"/search/issues?"+values.Encode(), nil, &response); err != nil {
		return SearchPage{}, err
	}

	result := SearchPage{
		PullRequests: make([]SearchPullRequest, 0, len(response.Items)),
		Truncated:    response.IncompleteResults,
	}
	for _, item := range response.Items {
		// Search issues can theoretically return non-PR items if GitHub changes
		// query handling. They are never part of Deck's result shape.
		if len(item.PullRequest) == 0 || string(item.PullRequest) == "null" {
			continue
		}
		repository, err := repositoryFromAPIURL(item.RepositoryURL)
		if err != nil || item.Number == 0 {
			return SearchPage{}, ErrProvider
		}
		allowed, err := client.CanReadRepository(ctx, credential, repository)
		if err != nil {
			return SearchPage{}, err
		}
		if !allowed {
			continue
		}
		updatedAt, err := time.Parse(time.RFC3339, item.UpdatedAt)
		if err != nil {
			return SearchPage{}, ErrProvider
		}
		result.PullRequests = append(result.PullRequests, SearchPullRequest{
			Repository: repository,
			Number:     item.Number,
			Title:      item.Title,
			Author:     User{Login: item.User.Login},
			UpdatedAt:  updatedAt,
		})
	}
	result.VisibleCount = len(result.PullRequests)
	if len(response.Items) == limit {
		result.NextCursor = encodeProviderCursor(offset + limit)
	}
	return result, nil
}

func repositoryFromAPIURL(value string) (Repository, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" ||
		parsed.Host != "api.github.com" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return Repository{}, ErrUnsupportedHost
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(parts) != 3 || parts[0] != "repos" {
		return Repository{}, ErrUnsupportedHost
	}
	owner, ownerErr := url.PathUnescape(parts[1])
	name, nameErr := url.PathUnescape(parts[2])
	repository := Repository{Owner: owner, Name: name}
	if ownerErr != nil || nameErr != nil || repository.Validate() != nil {
		return Repository{}, ErrUnsupportedHost
	}
	return repository, nil
}

func (repository repositoryResponse) userPermissions() Permissions {
	level := PermissionNone
	switch {
	case repository.Permissions.Admin:
		level = PermissionAdmin
	case repository.Permissions.Push, repository.Permissions.Maintain:
		level = PermissionWrite
	case repository.Permissions.Pull, repository.Permissions.Triage:
		level = PermissionRead
	}
	return Permissions{
		Metadata: level, PullRequests: level, Checks: level, Members: level,
	}
}

func (client *Client) repository(
	ctx context.Context,
	credential Credential,
	repository Repository,
) (repositoryResponse, error) {
	path, err := repositoryPath(repository, "")
	if err != nil {
		return repositoryResponse{}, err
	}
	var result repositoryResponse
	_, err = client.do(ctx, credential, http.MethodGet, path, nil, &result)
	return result, err
}

func (client *Client) CanReadRepository(
	ctx context.Context,
	credential Credential,
	repository Repository,
) (bool, error) {
	result, err := client.repository(ctx, credential, repository)
	if errors.Is(err, ErrPermissionDenied) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return result.userPermissions().Metadata >= PermissionRead, nil
}

func (client *Client) CanReadRepositoryForInstallation(
	ctx context.Context,
	installationID uint64,
	credential Credential,
	repository Repository,
) (bool, error) {
	release, err := client.concurrency.acquire(installationID)
	if err != nil {
		return false, err
	}
	defer release()
	return client.CanReadRepository(ctx, credential, repository)
}

type pullResponse struct {
	NodeID         string `json:"node_id"`
	State          string `json:"state"`
	Draft          bool   `json:"draft"`
	Merged         bool   `json:"merged"`
	Mergeable      *bool  `json:"mergeable"`
	MergeableState string `json:"mergeable_state"`
	UpdatedAt      string `json:"updated_at"`
	Head           struct {
		SHA string `json:"sha"`
	} `json:"head"`
	AutoMerge any `json:"auto_merge"`
}

func (client *Client) ActionMetadata(
	ctx context.Context,
	installationID uint64,
	credential Credential,
	appPermissions Permissions,
	reference PullRequestRef,
) (ActionMetadata, error) {
	if err := reference.Validate(); err != nil {
		return ActionMetadata{}, err
	}
	release, err := client.concurrency.acquire(installationID)
	if err != nil {
		return ActionMetadata{}, err
	}
	defer release()
	repository, err := client.repository(ctx, credential, reference.Repository)
	if err != nil {
		return ActionMetadata{}, err
	}
	path, _ := repositoryPath(reference.Repository,
		fmt.Sprintf("/pulls/%d", reference.Number))
	var pull pullResponse
	if _, err := client.do(ctx, credential, http.MethodGet, path, nil, &pull); err != nil {
		return ActionMetadata{}, err
	}
	effective := IntersectPermissions(appPermissions, repository.userPermissions())
	metadata := ActionMetadata{
		Revision: pullRevision(pull), Permissions: effective,
		NodeID: pull.NodeID, HeadSHA: pull.Head.SHA, IsDraft: pull.Draft,
		IsOpen: strings.EqualFold(pull.State, "open"), IsMerged: pull.Merged,
		AutoMergeEnabled: pull.AutoMerge != nil,
		Mergeable:        pull.Mergeable != nil && *pull.Mergeable,
		MergeBlocked: pull.MergeableState == "blocked" ||
			pull.MergeableState == "dirty",
		Supported: make(map[MutationKind]bool),
		AvailableMethods: map[MergeMethod]bool{
			MergeMethodMerge:  repository.AllowMergeCommit,
			MergeMethodSquash: repository.AllowSquashMerge,
			MergeMethodRebase: repository.AllowRebaseMerge,
		},
	}
	if effective.PullRequests < PermissionWrite || pull.Merged {
		return metadata, nil
	}
	metadata.Supported[MutationAssignUsers] = metadata.IsOpen
	metadata.Supported[MutationUnassignUsers] = metadata.IsOpen
	metadata.Supported[MutationRequestReviewers] = metadata.IsOpen && !metadata.IsDraft
	metadata.Supported[MutationRemoveReviewers] = metadata.IsOpen
	metadata.Supported[MutationAddLabels] = true
	metadata.Supported[MutationRemoveLabels] = true
	metadata.Supported[MutationMarkDraft] = metadata.IsOpen && !metadata.IsDraft
	metadata.Supported[MutationMarkReady] = metadata.IsOpen && metadata.IsDraft
	metadata.Supported[MutationClose] = metadata.IsOpen
	metadata.Supported[MutationReopen] = !metadata.IsOpen
	hasMethod := repository.AllowMergeCommit || repository.AllowSquashMerge ||
		repository.AllowRebaseMerge
	metadata.Supported[MutationMerge] = metadata.IsOpen && !metadata.IsDraft &&
		metadata.Mergeable && !metadata.MergeBlocked && hasMethod
	metadata.Supported[MutationEnableAutoMerge] = metadata.IsOpen &&
		!metadata.IsDraft && !metadata.AutoMergeEnabled && hasMethod
	metadata.Supported[MutationCancelAutoMerge] = metadata.IsOpen &&
		metadata.AutoMergeEnabled
	return metadata, nil
}

func pullRevision(pull pullResponse) uint64 {
	digest := sha256.Sum256([]byte(strings.Join([]string{
		pull.NodeID, pull.Head.SHA, pull.State, strconv.FormatBool(pull.Draft),
		strconv.FormatBool(pull.Merged), pull.UpdatedAt,
	}, "\x00")))
	revision := binary.BigEndian.Uint64(digest[:8])
	if revision == 0 {
		return 1
	}
	return revision
}

func (client *Client) ListMutationCandidates(
	ctx context.Context,
	installationID uint64,
	credential Credential,
	appPermissions Permissions,
	reference PullRequestRef,
	kind MutationKind,
	query string,
	page Page,
) (CandidatePage, error) {
	if kind != MutationAssignUsers && kind != MutationRequestReviewers &&
		kind != MutationAddLabels {
		return CandidatePage{}, ErrUnsupportedAction
	}
	metadata, err := client.ActionMetadata(
		ctx, installationID, credential, appPermissions, reference)
	if err != nil {
		return CandidatePage{}, err
	}
	if !metadata.Supported[kind] {
		return CandidatePage{}, ErrPermissionDenied
	}
	release, err := client.concurrency.acquire(installationID)
	if err != nil {
		return CandidatePage{}, err
	}
	defer release()
	limit := normalizedLimit(page.Limit)
	offset, err := decodeProviderCursor(page.Cursor)
	if err != nil {
		return CandidatePage{}, ErrUnsupportedAction
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	var candidates []Candidate
	switch kind {
	case MutationAssignUsers, MutationRequestReviewers:
		candidates, err = client.userCandidates(ctx, credential, reference.Repository)
		if err == nil && kind == MutationRequestReviewers &&
			metadata.Permissions.Members >= PermissionRead {
			var teams []Candidate
			teams, err = client.teamCandidates(ctx, credential, reference.Repository.Owner)
			candidates = append(candidates, teams...)
		}
	case MutationAddLabels:
		candidates, err = client.labelCandidates(ctx, credential, reference.Repository)
	}
	if err != nil {
		return CandidatePage{}, err
	}
	filtered := candidates[:0]
	for _, candidate := range candidates {
		if needle == "" || strings.Contains(strings.ToLower(candidateSearchText(candidate)), needle) {
			filtered = append(filtered, candidate)
		}
	}
	sort.Slice(filtered, func(left, right int) bool {
		return candidateSearchText(filtered[left]) < candidateSearchText(filtered[right])
	})
	if offset > len(filtered) {
		return CandidatePage{}, ErrUnsupportedAction
	}
	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	next := ""
	if end < len(filtered) {
		next = encodeProviderCursor(end)
	}
	return CandidatePage{
		Candidates: append([]Candidate(nil), filtered[offset:end]...),
		NextCursor: next, Revision: metadata.Revision,
	}, nil
}

func (client *Client) userCandidates(
	ctx context.Context,
	credential Credential,
	repository Repository,
) ([]Candidate, error) {
	path, err := repositoryPath(repository, "/assignees?per_page=100")
	if err != nil {
		return nil, err
	}
	var users []struct {
		Login string `json:"login"`
	}
	if _, err := client.do(ctx, credential, http.MethodGet, path, nil, &users); err != nil {
		return nil, err
	}
	result := make([]Candidate, 0, len(users))
	for _, user := range users {
		if safePathSegment(user.Login) {
			result = append(result, Candidate{
				Kind: CandidateUser, User: User{Login: user.Login},
			})
		}
	}
	return result, nil
}

func (client *Client) teamCandidates(
	ctx context.Context,
	credential Credential,
	organization string,
) ([]Candidate, error) {
	if !safePathSegment(organization) {
		return nil, ErrUnsupportedHost
	}
	var teams []struct {
		Slug string `json:"slug"`
	}
	path := "/orgs/" + url.PathEscape(organization) + "/teams?per_page=100"
	if _, err := client.do(ctx, credential, http.MethodGet, path, nil, &teams); err != nil {
		return nil, err
	}
	result := make([]Candidate, 0, len(teams))
	for _, team := range teams {
		if safePathSegment(team.Slug) {
			result = append(result, Candidate{
				Kind: CandidateTeam,
				Team: Team{Organization: organization, Slug: team.Slug},
			})
		}
	}
	return result, nil
}

func (client *Client) labelCandidates(
	ctx context.Context,
	credential Credential,
	repository Repository,
) ([]Candidate, error) {
	path, err := repositoryPath(repository, "/labels?per_page=100")
	if err != nil {
		return nil, err
	}
	var labels []struct {
		Name string `json:"name"`
	}
	if _, err := client.do(ctx, credential, http.MethodGet, path, nil, &labels); err != nil {
		return nil, err
	}
	result := make([]Candidate, 0, len(labels))
	for _, label := range labels {
		if validOperand(label.Name) {
			result = append(result, Candidate{
				Kind: CandidateLabel, Label: label.Name,
			})
		}
	}
	return result, nil
}

func candidateSearchText(candidate Candidate) string {
	switch candidate.Kind {
	case CandidateUser:
		return candidate.User.Login
	case CandidateTeam:
		return candidate.Team.Organization + "/" + candidate.Team.Slug
	case CandidateLabel:
		return candidate.Label
	default:
		return ""
	}
}

func normalizedLimit(limit int) int {
	if limit <= 0 {
		return defaultCandidateLimit
	}
	if limit > maxCandidateLimit {
		return maxCandidateLimit
	}
	return limit
}

func encodeProviderCursor(offset int) string {
	return url.QueryEscape(strconv.Itoa(offset))
}

func decodeProviderCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	value, err := url.QueryUnescape(cursor)
	if err != nil {
		return 0, err
	}
	offset, err := strconv.Atoi(value)
	if err != nil || offset < 0 {
		return 0, ErrUnsupportedAction
	}
	return offset, nil
}

type userRateLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	requests map[string][]time.Time
}

func newUserRateLimiter(limit int, window time.Duration) *userRateLimiter {
	return &userRateLimiter{
		limit: limit, window: window, requests: make(map[string][]time.Time),
	}
}

func (limiter *userRateLimiter) allow(user string, now time.Time) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	cutoff := now.Add(-limiter.window)
	entries := limiter.requests[user]
	first := 0
	for first < len(entries) && !entries[first].After(cutoff) {
		first++
	}
	entries = entries[first:]
	if len(entries) >= limiter.limit {
		limiter.requests[user] = entries
		return false
	}
	limiter.requests[user] = append(entries, now)
	return true
}

type installationLimiter struct {
	mu     sync.Mutex
	limit  int
	active map[uint64]int
}

func newInstallationLimiter(limit int) *installationLimiter {
	return &installationLimiter{limit: limit, active: make(map[uint64]int)}
}

func (limiter *installationLimiter) acquire(installationID uint64) (func(), error) {
	if installationID == 0 {
		return nil, ErrPermissionDenied
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.active[installationID] >= limiter.limit {
		return nil, ErrConcurrencyLimited
	}
	limiter.active[installationID]++
	return func() {
		limiter.mu.Lock()
		defer limiter.mu.Unlock()
		limiter.active[installationID]--
		if limiter.active[installationID] == 0 {
			delete(limiter.active, installationID)
		}
	}, nil
}
