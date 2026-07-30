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
	maxCandidatePages     = 100
)

type Client struct {
	http        *http.Client
	now         func() time.Time
	mutations   *userRateLimiter
	mutationPRs *pullRequestLocker
	concurrency *installationLimiter
}

func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	safeHTTPClient := *httpClient
	safeHTTPClient.CheckRedirect = func(
		_ *http.Request,
		_ []*http.Request,
	) error {
		return http.ErrUseLastResponse
	}
	return &Client{
		http: &safeHTTPClient, now: func() time.Time { return time.Now().UTC() },
		mutations:   newUserRateLimiter(30, time.Minute),
		mutationPRs: newPullRequestLocker(),
		concurrency: newInstallationLimiter(4),
	}
}

func (client *Client) do(
	ctx context.Context,
	credential Credential,
	method, path string,
	input any,
	output any,
) (http.Header, error) {
	return client.doWithConflictError(
		ctx, credential, method, path, input, output, nil)
}

func (client *Client) doWithConflictError(
	ctx context.Context,
	credential Credential,
	method, path string,
	input any,
	output any,
	conflictError error,
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
	if err := notifyDispatch(ctx); err != nil {
		return nil, err
	}
	response, err := client.http.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrTimeout
		}
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
		if conflictError != nil {
			switch response.StatusCode {
			case http.StatusConflict:
				return response.Header, conflictError
			case http.StatusMethodNotAllowed:
				return response.Header, ErrBranchProtected
			}
		}
		return response.Header, mapStatus(
			response.StatusCode, response.Header, payload, client.now())
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

func (client *Client) AuthenticatedUser(
	ctx context.Context,
	credential Credential,
) (AuthenticatedUser, error) {
	var response struct {
		ID    uint64 `json:"id"`
		Login string `json:"login"`
	}
	if _, err := client.do(
		ctx, credential, http.MethodGet, "/user", nil, &response); err != nil {
		return AuthenticatedUser{}, err
	}
	if response.ID == 0 || !safePathSegment(response.Login) {
		return AuthenticatedUser{}, ErrProvider
	}
	return AuthenticatedUser{ID: response.ID, Login: response.Login}, nil
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
		Metadata:       parsePermission(values["metadata"]),
		Administration: parsePermission(values["administration"]),
		Contents:       parsePermission(values["contents"]),
		PullRequests:   parsePermission(values["pull_requests"]),
		Checks:         parsePermission(values["checks"]),
		Members:        parsePermission(values["members"]),
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
	Name             string `json:"name"`
	NodeID           string `json:"node_id"`
	Archived         bool   `json:"archived"`
	AllowMergeCommit bool   `json:"allow_merge_commit"`
	AllowSquashMerge bool   `json:"allow_squash_merge"`
	AllowRebaseMerge bool   `json:"allow_rebase_merge"`
	AllowAutoMerge   bool   `json:"allow_auto_merge"`
	Owner            struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"owner"`
	Permissions struct {
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
		State         string `json:"state"`
		Draft         bool   `json:"draft"`
		User          struct {
			Login string `json:"login"`
		} `json:"user"`
		Assignees []struct {
			Login string `json:"login"`
		} `json:"assignees"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
		PullRequest *struct {
			MergedAt *string `json:"merged_at"`
		} `json:"pull_request"`
	} `json:"items"`
}

// SearchPullRequests executes a GitHub.com search using the current viewer's
// user authorization token, then rechecks repository access within the
// selected installation before returning any identity-bearing field. GitHub's
// upstream total_count is deliberately decoded but never copied into the
// result.
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
	installed, err := client.installationRepositories(
		ctx, installationID, credential)
	if err != nil {
		return SearchPage{}, err
	}

	result := SearchPage{
		PullRequests: make([]SearchPullRequest, 0, len(response.Items)),
		Truncated:    response.IncompleteResults,
	}
	for _, item := range response.Items {
		// Search issues can theoretically return non-PR items if GitHub changes
		// query handling. They are never part of Deck's result shape.
		if item.PullRequest == nil {
			continue
		}
		repository, err := repositoryFromAPIURL(item.RepositoryURL)
		if err != nil || item.Number == 0 {
			return SearchPage{}, ErrProvider
		}
		installedRepository, allowed := installed[repositoryKey(repository)]
		if !allowed ||
			installedRepository.userPermissions().Metadata < PermissionRead {
			continue
		}
		updatedAt, err := time.Parse(time.RFC3339, item.UpdatedAt)
		if err != nil {
			return SearchPage{}, ErrProvider
		}
		pullRequest := SearchPullRequest{
			Repository: repository,
			Number:     item.Number,
			Title:      item.Title,
			Author:     User{Login: item.User.Login},
			UpdatedAt:  updatedAt,
			IsDraft:    item.Draft,
			IsOpen:     strings.EqualFold(item.State, "open"),
			IsMerged:   item.PullRequest.MergedAt != nil,
		}
		for _, assignee := range item.Assignees {
			if safePathSegment(assignee.Login) {
				pullRequest.Assignees = append(
					pullRequest.Assignees, User{Login: assignee.Login})
			}
		}
		for _, label := range item.Labels {
			if validOperand(label.Name) {
				pullRequest.Labels = append(pullRequest.Labels, label.Name)
			}
		}
		result.PullRequests = append(result.PullRequests, pullRequest)
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
		Metadata: level, Administration: level, Contents: level,
		PullRequests: level, Checks: level, Members: level,
	}
}

func (repository repositoryResponse) ownerKind() AccountKind {
	switch strings.ToLower(repository.Owner.Type) {
	case "user":
		return AccountKindUser
	case "organization":
		return AccountKindOrganization
	default:
		return AccountKindUnknown
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
	repositories, err := client.installationRepositories(
		ctx, installationID, credential)
	if errors.Is(err, ErrPermissionDenied) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	result, ok := repositories[repositoryKey(repository)]
	return ok && result.userPermissions().Metadata >= PermissionRead, nil
}

func (client *Client) ListReadableRepositoriesForInstallation(
	ctx context.Context,
	installationID uint64,
	credential Credential,
) ([]Repository, error) {
	release, err := client.concurrency.acquire(installationID)
	if err != nil {
		return nil, err
	}
	defer release()
	result := make([]Repository, 0)
	err = client.visitInstallationRepositories(
		ctx, installationID, credential, func(repository repositoryResponse) bool {
			if repository.userPermissions().Metadata < PermissionRead {
				return true
			}
			result = append(result, Repository{
				Owner: repository.Owner.Login,
				Name:  repository.Name,
			})
			return true
		})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// VisitReadableRepositoriesForInstallation visits readable repositories until
// visit returns false. A nil visitor validates the credential and installation
// with one provider page without enumerating the complete installation.
func (client *Client) VisitReadableRepositoriesForInstallation(
	ctx context.Context,
	installationID uint64,
	credential Credential,
	visit func(Repository) bool,
) error {
	release, err := client.concurrency.acquire(installationID)
	if err != nil {
		return err
	}
	defer release()
	return client.visitInstallationRepositories(
		ctx, installationID, credential, func(repository repositoryResponse) bool {
			if visit == nil {
				return false
			}
			if repository.userPermissions().Metadata < PermissionRead {
				return true
			}
			return visit(Repository{
				Owner: repository.Owner.Login,
				Name:  repository.Name,
			})
		})
}

func (client *Client) installationRepositories(
	ctx context.Context,
	installationID uint64,
	credential Credential,
) (map[string]repositoryResponse, error) {
	result := make(map[string]repositoryResponse)
	err := client.visitInstallationRepositories(
		ctx, installationID, credential, func(repository repositoryResponse) bool {
			reference := Repository{
				Owner: repository.Owner.Login,
				Name:  repository.Name,
			}
			result[repositoryKey(reference)] = repository
			return true
		})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (client *Client) visitInstallationRepositories(
	ctx context.Context,
	installationID uint64,
	credential Credential,
	visit func(repositoryResponse) bool,
) error {
	if installationID == 0 {
		return ErrPermissionDenied
	}
	visited := 0
	for page := 1; ; page++ {
		path := fmt.Sprintf(
			"/user/installations/%d/repositories?per_page=100&page=%d",
			installationID, page)
		var response struct {
			TotalCount   int                  `json:"total_count"`
			Repositories []repositoryResponse `json:"repositories"`
		}
		if _, err := client.do(
			ctx, credential, http.MethodGet, path, nil, &response); err != nil {
			return err
		}
		for _, repository := range response.Repositories {
			reference := Repository{
				Owner: repository.Owner.Login,
				Name:  repository.Name,
			}
			if reference.Validate() != nil {
				return ErrProvider
			}
			visited++
			if visit != nil && !visit(repository) {
				return nil
			}
		}
		if len(response.Repositories) < maxCandidateLimit ||
			(response.TotalCount > 0 && visited >= response.TotalCount) {
			return nil
		}
		if response.TotalCount <= visited {
			return ErrProvider
		}
	}
}

func repositoryKey(repository Repository) string {
	return strings.ToLower(repository.Owner) + "/" +
		strings.ToLower(repository.Name)
}

type pullResponse struct {
	NodeID         string `json:"node_id"`
	Title          string `json:"title"`
	State          string `json:"state"`
	Draft          bool   `json:"draft"`
	Merged         bool   `json:"merged"`
	Mergeable      *bool  `json:"mergeable"`
	MergeableState string `json:"mergeable_state"`
	UpdatedAt      string `json:"updated_at"`
	Head           struct {
		SHA string `json:"sha"`
	} `json:"head"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	RequestedReviewers []struct {
		Login string `json:"login"`
	} `json:"requested_reviewers"`
	RequestedTeams []struct {
		Slug string `json:"slug"`
	} `json:"requested_teams"`
	Assignees []struct {
		Login string `json:"login"`
	} `json:"assignees"`
	Labels []struct {
		Name   string `json:"name"`
		NodeID string `json:"node_id"`
	} `json:"labels"`
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
	return client.actionMetadata(ctx, credential, appPermissions, reference)
}

func (client *Client) actionMetadata(
	ctx context.Context,
	credential Credential,
	appPermissions Permissions,
	reference PullRequestRef,
) (ActionMetadata, error) {
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
	updatedAt, err := time.Parse(time.RFC3339, pull.UpdatedAt)
	if err != nil {
		return ActionMetadata{}, ErrProvider
	}
	effective := IntersectPermissions(appPermissions, repository.userPermissions())
	metadata := ActionMetadata{
		Revision: pullRevision(pull), Permissions: effective,
		RepositoryOwner: repository.ownerKind(),
		Title:           pull.Title,
		Author:          User{Login: pull.User.Login},
		UpdatedAt:       updatedAt,
		NodeID:          pull.NodeID, HeadSHA: pull.Head.SHA, IsDraft: pull.Draft,
		IsOpen: strings.EqualFold(pull.State, "open"), IsMerged: pull.Merged,
		AutoMergeEnabled: pull.AutoMerge != nil,
		Mergeable:        pull.Mergeable != nil && *pull.Mergeable,
		MergeBlocked:     pull.MergeableState == "blocked",
		MergeConflicting: pull.MergeableState == "dirty",
		LabelIDs:         make(map[string]string, len(pull.Labels)),
		Supported:        make(map[MutationKind]bool),
		AvailableMethods: map[MergeMethod]bool{
			MergeMethodMerge:  repository.AllowMergeCommit,
			MergeMethodSquash: repository.AllowSquashMerge,
			MergeMethodRebase: repository.AllowRebaseMerge,
		},
	}
	for _, reviewer := range pull.RequestedReviewers {
		metadata.Reviewers = append(
			metadata.Reviewers, User{Login: reviewer.Login})
	}
	for _, team := range pull.RequestedTeams {
		metadata.ReviewerTeams = append(metadata.ReviewerTeams, Team{
			Organization: reference.Repository.Owner,
			Slug:         team.Slug,
		})
	}
	for _, assignee := range pull.Assignees {
		metadata.Assignees = append(
			metadata.Assignees, User{Login: assignee.Login})
	}
	for _, label := range pull.Labels {
		metadata.Labels = append(metadata.Labels, label.Name)
		if label.NodeID != "" {
			metadata.LabelIDs[strings.ToLower(label.Name)] = label.NodeID
		}
	}
	if effective.PullRequests < PermissionWrite || pull.Merged ||
		repository.Archived {
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
	canMerge := effective.Contents >= PermissionWrite
	metadata.Supported[MutationMerge] = metadata.IsOpen && !metadata.IsDraft &&
		metadata.Mergeable && !metadata.MergeBlocked &&
		!metadata.MergeConflicting && hasMethod && canMerge
	mergeRequirementsUnmet := pull.Mergeable != nil &&
		!metadata.MergeConflicting &&
		(!metadata.Mergeable || metadata.MergeBlocked)
	metadata.Supported[MutationEnableAutoMerge] = metadata.IsOpen &&
		!metadata.IsDraft && !metadata.AutoMergeEnabled &&
		repository.AllowAutoMerge && mergeRequirementsUnmet &&
		hasMethod && canMerge
	metadata.Supported[MutationCancelAutoMerge] = metadata.IsOpen &&
		metadata.AutoMergeEnabled && canMerge
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
	case MutationAssignUsers:
		candidates, err = client.userCandidates(ctx, credential, reference.Repository)
	case MutationRequestReviewers:
		candidates, err = client.reviewerCandidates(
			ctx, credential, reference.Repository)
		if err == nil &&
			metadata.Permissions.Administration >= PermissionRead &&
			metadata.RepositoryOwner == AccountKindOrganization {
			var teams []Candidate
			teams, err = client.teamCandidates(ctx, credential, reference.Repository)
			if errors.Is(err, ErrPermissionDenied) {
				// GitHub requires repository-administration read permission to
				// enumerate repository teams. A viewer can still request user
				// reviewers without that optional team visibility.
				err = nil
			}
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
	return client.userCandidatesAtPath(
		ctx, credential, repository, "/assignees")
}

func (client *Client) reviewerCandidates(
	ctx context.Context,
	credential Credential,
	repository Repository,
) ([]Candidate, error) {
	return client.userCandidatesAtPath(
		ctx, credential, repository, "/collaborators?affiliation=all")
}

func (client *Client) userCandidatesAtPath(
	ctx context.Context,
	credential Credential,
	repository Repository,
	suffix string,
) ([]Candidate, error) {
	result := make([]Candidate, 0, maxCandidateLimit)
	for page := 1; page <= maxCandidatePages; page++ {
		separator := "?"
		if strings.Contains(suffix, "?") {
			separator = "&"
		}
		path, err := repositoryPath(repository,
			fmt.Sprintf("%s%sper_page=%d&page=%d",
				suffix, separator, maxCandidateLimit, page))
		if err != nil {
			return nil, err
		}
		var users []struct {
			Login string `json:"login"`
		}
		if _, err := client.do(
			ctx, credential, http.MethodGet, path, nil, &users); err != nil {
			return nil, err
		}
		for _, user := range users {
			if safePathSegment(user.Login) {
				result = append(result, Candidate{
					Kind: CandidateUser, User: User{Login: user.Login},
				})
			}
		}
		if len(users) < maxCandidateLimit {
			return result, nil
		}
	}
	return nil, ErrProvider
}

func (client *Client) teamCandidates(
	ctx context.Context,
	credential Credential,
	repository Repository,
) ([]Candidate, error) {
	result := make([]Candidate, 0, maxCandidateLimit)
	for page := 1; page <= maxCandidatePages; page++ {
		var teams []struct {
			Slug string `json:"slug"`
		}
		path, err := repositoryPath(repository,
			fmt.Sprintf("/teams?per_page=%d&page=%d", maxCandidateLimit, page))
		if err != nil {
			return nil, err
		}
		if _, err := client.do(
			ctx, credential, http.MethodGet, path, nil, &teams); err != nil {
			return nil, err
		}
		for _, team := range teams {
			if safePathSegment(team.Slug) {
				result = append(result, Candidate{
					Kind: CandidateTeam,
					Team: Team{Organization: repository.Owner, Slug: team.Slug},
				})
			}
		}
		if len(teams) < maxCandidateLimit {
			return result, nil
		}
	}
	return nil, ErrProvider
}

func (client *Client) labelCandidates(
	ctx context.Context,
	credential Credential,
	repository Repository,
) ([]Candidate, error) {
	result := make([]Candidate, 0, maxCandidateLimit)
	for page := 1; page <= maxCandidatePages; page++ {
		path, err := repositoryPath(repository,
			fmt.Sprintf("/labels?per_page=%d&page=%d", maxCandidateLimit, page))
		if err != nil {
			return nil, err
		}
		var labels []struct {
			Name string `json:"name"`
		}
		if _, err := client.do(
			ctx, credential, http.MethodGet, path, nil, &labels); err != nil {
			return nil, err
		}
		for _, label := range labels {
			if validOperand(label.Name) {
				result = append(result, Candidate{
					Kind: CandidateLabel, Label: label.Name,
				})
			}
		}
		if len(labels) < maxCandidateLimit {
			return result, nil
		}
	}
	return nil, ErrProvider
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

type pullRequestLocker struct {
	mu      sync.Mutex
	entries map[string]*pullRequestLock
}

type pullRequestLock struct {
	mu   sync.Mutex
	refs int
}

func newPullRequestLocker() *pullRequestLocker {
	return &pullRequestLocker{entries: make(map[string]*pullRequestLock)}
}

func (locker *pullRequestLocker) acquire(reference PullRequestRef) func() {
	key := fmt.Sprintf("%s#%d",
		repositoryKey(reference.Repository), reference.Number)
	locker.mu.Lock()
	entry := locker.entries[key]
	if entry == nil {
		entry = &pullRequestLock{}
		locker.entries[key] = entry
	}
	entry.refs++
	locker.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		locker.mu.Lock()
		defer locker.mu.Unlock()
		entry.refs--
		if entry.refs == 0 {
			delete(locker.entries, key)
		}
	}
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
