package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	apiVersion          = "2022-11-28"
	maximumResponseBody = 2 * 1024 * 1024
	reconciliationAge   = 24 * time.Hour
	maxReconcilePages   = 10
)

var (
	ErrAmbiguousCreate  = errors.New("realqa github: issue create result remains ambiguous")
	ErrProviderRejected = errors.New("realqa github: provider rejected the request")
)

type UserToken struct{ value string }

func (UserToken) String() string   { return "[REDACTED]" }
func (UserToken) GoString() string { return "github.UserToken{[REDACTED]}" }

func NewUserToken(value string) (UserToken, error) {
	if len(value) < 20 || len(value) > 1024 ||
		strings.TrimSpace(value) != value || strings.ContainsAny(value, " \t\r\n") {
		return UserToken{}, errors.New("realqa github: user authorization token is invalid")
	}
	return UserToken{value: value}, nil
}

type ClientConfig struct {
	HTTPClient        *http.Client
	APIOrigin         string
	WebOrigin         string
	ProjectPermission ProjectPermission
	Now               func() time.Time
}

type Client struct {
	httpClient        *http.Client
	projectPermission ProjectPermission
	now               func() time.Time
}

func NewClient(config ClientConfig) (*Client, error) {
	if config.APIOrigin == "" {
		config.APIOrigin = APIOrigin
	}
	if config.WebOrigin == "" {
		config.WebOrigin = WebOrigin
	}
	if config.APIOrigin != APIOrigin || config.WebOrigin != WebOrigin {
		return nil, errors.New("realqa github: GHES and custom hosts are not supported")
	}
	if _, err := RequiredPermissions(config.ProjectPermission); err != nil {
		return nil, err
	}
	base := config.HTTPClient
	if base == nil {
		base = &http.Client{Timeout: 15 * time.Second}
	}
	copied := *base
	copied.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return errors.New("realqa github: provider redirects are not allowed")
	}
	if copied.Timeout <= 0 || copied.Timeout > 30*time.Second {
		copied.Timeout = 15 * time.Second
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &Client{
		httpClient: &copied, projectPermission: config.ProjectPermission, now: now,
	}, nil
}

func (client *Client) ListInstallations(
	ctx context.Context,
	token UserToken,
) ([]Installation, error) {
	if token.value == "" {
		return nil, errors.New("realqa github: user authorization is required")
	}
	var result []Installation
	for page := 1; ; page++ {
		var response struct {
			Installations []struct {
				ID          int64          `json:"id"`
				Account     apiAccount     `json:"account"`
				Permissions apiPermissions `json:"permissions"`
			} `json:"installations"`
		}
		endpoint := "/user/installations?per_page=100&page=" + strconv.Itoa(page)
		if err := client.getJSON(ctx, token, endpoint, &response); err != nil {
			return nil, err
		}
		for _, item := range response.Installations {
			installation := Installation{
				ID: item.ID, AccountID: item.Account.ID, AccountLogin: item.Account.Login,
				AccountKind: item.Account.Type, Permissions: item.Permissions.model(),
			}
			if err := installation.Validate(client.projectPermission); err != nil {
				return nil, err
			}
			result = append(result, installation)
		}
		if len(response.Installations) < 100 {
			return result, nil
		}
	}
}

func (client *Client) ListRepositories(
	ctx context.Context,
	token UserToken,
	installationID int64,
) ([]Repository, error) {
	if token.value == "" || installationID <= 0 {
		return nil, errors.New("realqa github: installation repository request is invalid")
	}
	var result []Repository
	for page := 1; ; page++ {
		var response struct {
			Repositories []struct {
				ID        int64      `json:"id"`
				NodeID    string     `json:"node_id"`
				Name      string     `json:"name"`
				Owner     apiAccount `json:"owner"`
				HasIssues bool       `json:"has_issues"`
			} `json:"repositories"`
		}
		endpoint := fmt.Sprintf("/user/installations/%d/repositories?per_page=100&page=%d",
			installationID, page)
		if err := client.getJSON(ctx, token, endpoint, &response); err != nil {
			return nil, err
		}
		for _, item := range response.Repositories {
			repository := Repository{
				ID: item.ID, NodeID: item.NodeID, Owner: item.Owner.Login, Name: item.Name,
				IssuesEnabled: item.HasIssues, CanSubmit: item.HasIssues,
			}
			if err := repository.Validate(); err != nil {
				return nil, err
			}
			result = append(result, repository)
		}
		if len(response.Repositories) < 100 {
			return result, nil
		}
	}
}

func (client *Client) GetRepositoryDefinitions(
	ctx context.Context,
	token UserToken,
	repository Repository,
) (RepositoryDefinitions, error) {
	if token.value == "" {
		return RepositoryDefinitions{}, errors.New(
			"realqa github: user authorization is required")
	}
	if err := repository.Validate(); err != nil {
		return RepositoryDefinitions{}, err
	}
	var files []struct {
		Type string `json:"type"`
		Path string `json:"path"`
		SHA  string `json:"sha"`
	}
	endpoint := fmt.Sprintf("/repos/%s/%s/contents/.github/ISSUE_TEMPLATE",
		repository.Owner, repository.Name)
	status, err := client.requestJSON(ctx, token, http.MethodGet, endpoint, nil, &files)
	if err != nil {
		return RepositoryDefinitions{}, err
	}
	if status == http.StatusNotFound {
		return RepositoryDefinitions{}, nil
	}
	if status != http.StatusOK {
		return RepositoryDefinitions{}, ErrProviderRejected
	}
	result := RepositoryDefinitions{
		Markdown: []MarkdownTemplate{}, Forms: []IssueForm{},
	}
	for _, file := range files {
		if file.Type != "file" || path.Clean(file.Path) != file.Path ||
			!strings.HasPrefix(file.Path, ".github/ISSUE_TEMPLATE/") {
			continue
		}
		extension := strings.ToLower(path.Ext(file.Path))
		if extension != ".md" && extension != ".yml" && extension != ".yaml" {
			continue
		}
		baseName := strings.ToLower(path.Base(file.Path))
		if baseName == "config.yml" || baseName == "config.yaml" {
			continue
		}
		contents, contentErr := client.getRepositoryFile(
			ctx, token, repository, file.Path)
		if contentErr != nil {
			return RepositoryDefinitions{}, contentErr
		}
		switch extension {
		case ".md":
			template, parseErr := ParseMarkdownTemplate(file.Path, file.SHA, contents)
			if parseErr != nil {
				return RepositoryDefinitions{}, parseErr
			}
			result.Markdown = append(result.Markdown, template)
		default:
			form, parseErr := ParseIssueForm(file.Path, file.SHA, contents)
			if parseErr != nil {
				return RepositoryDefinitions{}, parseErr
			}
			result.Forms = append(result.Forms, form)
		}
	}
	return result, nil
}

func (client *Client) getRepositoryFile(
	ctx context.Context,
	token UserToken,
	repository Repository,
	filePath string,
) ([]byte, error) {
	segments := strings.Split(filePath, "/")
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}
	endpoint := fmt.Sprintf("/repos/%s/%s/contents/%s",
		repository.Owner, repository.Name, strings.Join(segments, "/"))
	var response struct {
		Type     string `json:"type"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := client.getJSON(ctx, token, endpoint, &response); err != nil {
		return nil, err
	}
	if response.Type != "file" || response.Encoding != "base64" {
		return nil, errors.New("realqa github: repository definition response is invalid")
	}
	decoded, err := base64.StdEncoding.DecodeString(
		strings.ReplaceAll(response.Content, "\n", ""))
	if err != nil || len(decoded) > maximumDefinitionBytes {
		return nil, errors.New("realqa github: repository definition content is invalid")
	}
	return decoded, nil
}

// CreateIssue performs only a new-issue create. It always reconciles the hidden
// marker before dispatch, and after every ambiguous result, before a bounded
// retry can occur.
func (client *Client) CreateIssue(
	ctx context.Context,
	token UserToken,
	repository Repository,
	input IssueInput,
) (Issue, error) {
	if token.value == "" {
		return Issue{}, errors.New("realqa github: user authorization is required")
	}
	if err := repository.Validate(); err != nil || !repository.IssuesEnabled ||
		!repository.CanSubmit {
		return Issue{}, errors.New("realqa github: repository does not permit issue creation")
	}
	normalized, err := normalizeIssueInput(input)
	if err != nil {
		return Issue{}, err
	}
	if err = client.validateProjects(normalized.Extension.Projects); err != nil {
		return Issue{}, err
	}
	body, err := ComposeBody(normalized)
	if err != nil {
		return Issue{}, err
	}
	marker, _ := SubmissionMarker(normalized.SubmissionID)
	if existing, found, reconcileErr := client.reconcile(
		ctx, token, repository, marker,
	); reconcileErr != nil {
		return Issue{}, reconcileErr
	} else if found {
		return client.attachProjects(ctx, token, existing, normalized.Extension.Projects)
	}

	payload := createIssueRequest{
		Title: normalized.Title, Body: body,
		Labels: names(normalized.Labels), Assignees: logins(normalized.Assignees),
	}
	if normalized.Extension.Milestone != nil {
		payload.Milestone = normalized.Extension.Milestone.Number
	}
	for attempt := 0; attempt < 2; attempt++ {
		created, ambiguous, createErr := client.createOnce(
			ctx, token, repository, payload,
		)
		if createErr == nil {
			return client.attachProjects(ctx, token, created, normalized.Extension.Projects)
		}
		if !ambiguous {
			return Issue{}, createErr
		}
		existing, found, reconcileErr := client.reconcile(
			ctx, token, repository, marker,
		)
		if reconcileErr != nil {
			return Issue{}, ErrAmbiguousCreate
		}
		if found {
			return client.attachProjects(ctx, token, existing, normalized.Extension.Projects)
		}
	}
	return Issue{}, ErrAmbiguousCreate
}

type createIssueRequest struct {
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Labels    []string `json:"labels,omitempty"`
	Assignees []string `json:"assignees,omitempty"`
	Milestone int64    `json:"milestone,omitempty"`
}

func (client *Client) createOnce(
	ctx context.Context,
	token UserToken,
	repository Repository,
	payload createIssueRequest,
) (Issue, bool, error) {
	var response apiIssue
	status, err := client.requestJSON(ctx, token, http.MethodPost,
		fmt.Sprintf("/repos/%s/%s/issues", repository.Owner, repository.Name),
		payload, &response)
	if err != nil {
		return Issue{}, true, ErrAmbiguousCreate
	}
	if status == http.StatusCreated {
		issue, validateErr := response.model(repository)
		if validateErr != nil {
			return Issue{}, true, ErrAmbiguousCreate
		}
		return issue, false, nil
	}
	if status == http.StatusRequestTimeout || status == http.StatusTooManyRequests ||
		status >= 500 {
		return Issue{}, true, ErrAmbiguousCreate
	}
	return Issue{}, false, ErrProviderRejected
}

func (client *Client) reconcile(
	ctx context.Context,
	token UserToken,
	repository Repository,
	marker string,
) (Issue, bool, error) {
	var user apiAccount
	if err := client.getJSON(ctx, token, "/user", &user); err != nil {
		return Issue{}, false, err
	}
	if _, err := cleanName(user.Login); err != nil {
		return Issue{}, false, errors.New("realqa github: provider user identity is invalid")
	}
	needle := "<!-- " + marker + " -->"
	var found *Issue
	complete := false
	for pageNumber := 1; pageNumber <= maxReconcilePages; pageNumber++ {
		query := url.Values{}
		query.Set("state", "all")
		query.Set("creator", user.Login)
		query.Set("sort", "created")
		query.Set("direction", "desc")
		query.Set("since", client.now().UTC().Add(-reconciliationAge).Format(time.RFC3339))
		query.Set("per_page", "100")
		query.Set("page", strconv.Itoa(pageNumber))
		var response []apiIssue
		endpoint := fmt.Sprintf("/repos/%s/%s/issues?%s",
			repository.Owner, repository.Name, query.Encode())
		if err := client.getJSON(ctx, token, endpoint, &response); err != nil {
			return Issue{}, false, err
		}
		for _, candidate := range response {
			if candidate.PullRequest != nil || !strings.Contains(candidate.Body, needle) {
				continue
			}
			issue, err := candidate.model(repository)
			if err != nil {
				return Issue{}, false, err
			}
			if found != nil && found.ID != issue.ID {
				return Issue{}, false, errors.New(
					"realqa github: duplicate submission markers require manual resolution")
			}
			found = &issue
		}
		if len(response) < 100 {
			complete = true
			break
		}
	}
	if !complete {
		return Issue{}, false, errors.New(
			"realqa github: reconciliation result was truncated")
	}
	if found == nil {
		return Issue{}, false, nil
	}
	return *found, true, nil
}

func (client *Client) validateProjects(projects []Project) error {
	for _, project := range projects {
		if project.Permission != client.projectPermission ||
			client.projectPermission == ProjectPermissionNone {
			return errors.New("realqa github: project permission was not configured")
		}
	}
	return nil
}

func (client *Client) attachProjects(
	ctx context.Context,
	token UserToken,
	issue Issue,
	projects []Project,
) (Issue, error) {
	for _, project := range projects {
		payload := map[string]any{
			"query":     `mutation($project:ID!,$content:ID!){addProjectV2ItemById(input:{projectId:$project,contentId:$content}){item{id}}}`,
			"variables": map[string]string{"project": project.NodeID, "content": issue.NodeID},
		}
		var response struct {
			Errors []json.RawMessage `json:"errors"`
		}
		status, err := client.requestJSON(ctx, token, http.MethodPost,
			"/graphql", payload, &response)
		if err != nil || status != http.StatusOK || len(response.Errors) != 0 {
			return Issue{}, errors.New("realqa github: project assignment failed")
		}
	}
	return issue, nil
}

func (client *Client) getJSON(
	ctx context.Context,
	token UserToken,
	endpoint string,
	target any,
) error {
	status, err := client.requestJSON(ctx, token, http.MethodGet, endpoint, nil, target)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return ErrProviderRejected
	}
	return nil
}

func (client *Client) requestJSON(
	ctx context.Context,
	token UserToken,
	method string,
	endpoint string,
	payload any,
	target any,
) (int, error) {
	if !strings.HasPrefix(endpoint, "/") || strings.HasPrefix(endpoint, "//") {
		return 0, errors.New("realqa github: provider endpoint is invalid")
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return 0, errors.New("realqa github: provider request is invalid")
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, APIOrigin+endpoint, body)
	if err != nil {
		return 0, errors.New("realqa github: provider request is invalid")
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+token.value)
	request.Header.Set("X-GitHub-Api-Version", apiVersion)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return 0, errors.New("realqa github: provider request failed")
	}
	defer response.Body.Close()
	if response.Request == nil || response.Request.URL.Scheme != "https" ||
		response.Request.URL.Host != "api.github.com" {
		return 0, errors.New("realqa github: provider response host is invalid")
	}
	limited := io.LimitReader(response.Body, maximumResponseBody+1)
	contents, err := io.ReadAll(limited)
	if err != nil || len(contents) > maximumResponseBody {
		return 0, errors.New("realqa github: provider response is invalid")
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 && target != nil {
		decoder := json.NewDecoder(bytes.NewReader(contents))
		if err = decoder.Decode(target); err != nil {
			return 0, errors.New("realqa github: provider response is invalid")
		}
	}
	return response.StatusCode, nil
}

type apiAccount struct {
	ID    int64       `json:"id"`
	Login string      `json:"login"`
	Type  AccountKind `json:"type"`
}

type apiPermissions struct {
	Issues               PermissionLevel `json:"issues"`
	Metadata             PermissionLevel `json:"metadata"`
	Contents             PermissionLevel `json:"contents"`
	RepositoryProjects   PermissionLevel `json:"repository_projects"`
	OrganizationProjects PermissionLevel `json:"organization_projects"`
}

func (permissions *apiPermissions) UnmarshalJSON(data []byte) error {
	var values map[string]PermissionLevel
	if err := json.Unmarshal(data, &values); err != nil {
		return errors.New("realqa github: installation permissions are invalid")
	}
	for name, level := range values {
		if level != PermissionRead && level != PermissionWrite {
			return errors.New("realqa github: installation permissions are invalid")
		}
		switch name {
		case "issues":
			permissions.Issues = level
		case "metadata":
			permissions.Metadata = level
		case "contents":
			permissions.Contents = level
		case "repository_projects":
			permissions.RepositoryProjects = level
		case "organization_projects":
			permissions.OrganizationProjects = level
		default:
			return errors.New("realqa github: installation has an excess permission")
		}
	}
	return nil
}

func (permissions apiPermissions) model() Permissions {
	return Permissions(permissions)
}

type apiIssue struct {
	ID          int64           `json:"id"`
	NodeID      string          `json:"node_id"`
	Number      int64           `json:"number"`
	HTMLURL     string          `json:"html_url"`
	Body        string          `json:"body"`
	CreatedAt   time.Time       `json:"created_at"`
	PullRequest json.RawMessage `json:"pull_request"`
}

func (issue apiIssue) model(repository Repository) (Issue, error) {
	if issue.ID <= 0 || issue.Number <= 0 || !nodeIDPattern.MatchString(issue.NodeID) ||
		issue.CreatedAt.IsZero() ||
		validateGitHubIssueURL(issue.HTMLURL, repository.Owner, repository.Name, issue.Number) != nil {
		return Issue{}, errors.New("realqa github: provider issue response is invalid")
	}
	return Issue{
		ID: issue.ID, NodeID: issue.NodeID, Number: issue.Number,
		URL: issue.HTMLURL, Body: issue.Body, CreatedAt: issue.CreatedAt,
	}, nil
}

func names(labels []Label) []string {
	result := make([]string, 0, len(labels))
	for _, label := range labels {
		result = append(result, label.Name)
	}
	return result
}

func logins(assignees []Assignee) []string {
	result := make([]string, 0, len(assignees))
	for _, assignee := range assignees {
		result = append(result, assignee.Login)
	}
	return result
}
