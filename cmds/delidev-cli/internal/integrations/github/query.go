package github

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type RepositoryQueryObservation struct {
	Reviewers          *domain.PullRequestReviewers
	Feedback           *domain.PullRequestFeedback
	CI                 *domain.PullRequestCI
	Rules              *domain.PullRequestRules
	Diff               *domain.PullRequestDiff
	Checks             *domain.PullRequestChecks
	Statuses           *domain.PullRequestCommitStatuses
	Identity           domain.GitHubIdentity
	Repository         domain.RemoteRepository
	Items              []domain.RepositoryItem
	NextPage           uint32
	TotalCount         *string
	Incomplete         *bool
	SearchLimitReached bool
}

func queryUnavailable() error {
	return domain.Fail(domain.Unavailable, "The GitHub query could not be verified.", "Retry the selected repository query; malformed or partial data does not establish an empty or complete result.")
}
func readProblem(read readResult) error {
	if read.problem != nil {
		return read.problem
	}
	return queryUnavailable()
}
func nullableBool(fields map[string]json.RawMessage, key string) (*bool, bool) {
	raw, exists := fields[key]
	if !exists {
		return nil, false
	}
	var value *bool
	err := json.Unmarshal(raw, &value)
	return value, err == nil
}
func parseActor(raw json.RawMessage) (*domain.RepositoryActor, bool) {
	if string(raw) == "null" {
		return nil, true
	}
	fields, ok := jsonObject(raw)
	if !ok {
		return nil, false
	}
	id, ok := exactUnsigned(fields["id"])
	if !ok {
		return nil, false
	}
	kinds := map[string]domain.RepositoryActorKind{"User": domain.RepositoryUser, "Bot": domain.RepositoryBot, "Organization": domain.RepositoryOrganization}
	providerType := stringField(fields, "type")
	kind := kinds[providerType]
	if kind == "" {
		kind = domain.RepositoryUnknownActor
	}
	actor := &domain.RepositoryActor{ID: strconv.FormatUint(id, 10), NodeID: stringField(fields, "node_id"), Login: stringField(fields, "login"), Kind: kind, ProviderType: providerType}
	return actor, actor.Validate() == nil
}
func parseItem(raw []byte, repository domain.RemoteRepository, query domain.RepositoryQuery) (domain.RepositoryItem, bool, error) {
	fields, ok := jsonObject(raw)
	if !ok {
		return domain.RepositoryItem{}, false, queryUnavailable()
	}
	marker, hasMarker := fields["pull_request"]
	if hasMarker {
		if _, ok := jsonObject(marker); !ok {
			return domain.RepositoryItem{}, false, queryUnavailable()
		}
	}
	issueAPI := query.Kind == domain.RepositoryIssue || query.Operation == domain.RepositorySearch
	// Repository issues include PRs. Preserve original API pagination while
	// excluding PR rows, including pages that contain no actual issues.
	if query.Operation == domain.RepositorySearch && (query.Kind == domain.RepositoryPullRequest) != hasMarker {
		return domain.RepositoryItem{}, false, queryUnavailable()
	}
	id, idOK := exactUnsigned(fields["id"])
	number, numberOK := exactUnsigned(fields["number"])
	if !idOK || !numberOK || id == 0 || number == 0 {
		return domain.RepositoryItem{}, false, queryUnavailable()
	}
	item := domain.RepositoryItem{Provider: domain.GitHubCom, Kind: query.Kind, ID: strconv.FormatUint(id, 10), Number: strconv.FormatUint(number, 10), NodeID: stringField(fields, "node_id"), Title: stringField(fields, "title"), State: domain.RepositoryItemState(stringField(fields, "state"))}
	if issueAPI && hasMarker {
		item.Kind = domain.RepositoryPullRequest
	}
	segment := "pulls"
	item.IdentitySource = domain.RepositoryPullRequestIdentity
	if issueAPI {
		segment = "issues"
		item.IdentitySource = domain.RepositoryIssueIdentity
	}
	apiURL := apiOrigin + "/repos/" + repository.Owner + "/" + repository.Name + "/" + segment + "/" + item.Number
	item.URL = domain.RepositoryItemURL(repository.Owner, repository.Name, item.Kind, item.Number)
	if !strings.EqualFold(stringField(fields, "url"), apiURL) || !strings.EqualFold(stringField(fields, "html_url"), item.URL) {
		return domain.RepositoryItem{}, false, queryUnavailable()
	}
	var err error
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, stringField(fields, "created_at"))
	if err != nil {
		return domain.RepositoryItem{}, false, queryUnavailable()
	}
	item.UpdatedAt, err = time.Parse(time.RFC3339Nano, stringField(fields, "updated_at"))
	if err != nil {
		return domain.RepositoryItem{}, false, queryUnavailable()
	}
	item.Author, ok = parseActor(fields["user"])
	if !ok {
		return domain.RepositoryItem{}, false, queryUnavailable()
	}
	if item.Kind == domain.RepositoryPullRequest {
		if _, exists := fields["draft"]; exists {
			item.Draft, ok = nullableBool(fields, "draft")
			if !ok {
				return domain.RepositoryItem{}, false, queryUnavailable()
			}
		} else if !issueAPI {
			return domain.RepositoryItem{}, false, queryUnavailable()
		}
		if !issueAPI && item.Draft == nil {
			return domain.RepositoryItem{}, false, queryUnavailable()
		}
	}
	if issueAPI && hasMarker && query.Kind == domain.RepositoryIssue {
		summary := query
		summary.Kind = domain.RepositoryPullRequest
		summary.Operation = domain.RepositorySearch
		if item.Validate(repository, summary) != nil {
			return domain.RepositoryItem{}, false, queryUnavailable()
		}
		return domain.RepositoryItem{}, true, nil
	}
	if query.Operation == domain.RepositoryDetail {
		bodyRaw, exists := fields["body"]
		if !exists {
			return domain.RepositoryItem{}, false, queryUnavailable()
		}
		var body *string
		if json.Unmarshal(bodyRaw, &body) != nil {
			return domain.RepositoryItem{}, false, queryUnavailable()
		}
		if body == nil {
			empty := ""
			body = &empty
		}
		item.Body = body
		if query.Kind == domain.RepositoryPullRequest {
			item.Merged, ok = nullableBool(fields, "merged")
			if !ok || item.Merged == nil {
				return domain.RepositoryItem{}, false, queryUnavailable()
			}
			item.Mergeable, ok = nullableBool(fields, "mergeable")
			if !ok {
				return domain.RepositoryItem{}, false, queryUnavailable()
			}
			base, baseOK := jsonObject(fields["base"])
			head, headOK := jsonObject(fields["head"])
			baseRepo, repoOK := jsonObject(base["repo"])
			baseID, idOK := exactUnsigned(baseRepo["id"])
			if !baseOK || !headOK || !repoOK || !idOK || strconv.FormatUint(baseID, 10) != repository.ID || stringField(baseRepo, "node_id") != repository.NodeID {
				return domain.RepositoryItem{}, false, queryUnavailable()
			}
			item.BaseRef, item.BaseSHA = stringField(base, "ref"), stringField(base, "sha")
			item.HeadRef, item.HeadSHA = stringField(head, "ref"), stringField(head, "sha")
		}
	}
	if item.Validate(repository, query) != nil {
		return domain.RepositoryItem{}, false, queryUnavailable()
	}
	return item, false, nil
}

// Pagination links are metadata only. A next-page URL must preserve every
// original parameter and authority; callers construct the next request afresh.
func queryNextPage(header, path string, page uint32, repository domain.RemoteRepository) (uint32, error) {
	if header == "" {
		return 0, nil
	}
	expected, err := url.Parse(apiOrigin + path)
	if err != nil {
		return 0, queryUnavailable()
	}
	remaining := strings.TrimSpace(header)
	var next uint32
	for remaining != "" {
		if !strings.HasPrefix(remaining, "<") {
			return 0, queryUnavailable()
		}
		end := strings.IndexByte(remaining, '>')
		if end < 0 {
			return 0, queryUnavailable()
		}
		address := remaining[1:end]
		remaining = strings.TrimSpace(remaining[end+1:])
		comma := strings.IndexByte(remaining, ',')
		parameters := remaining
		if comma >= 0 {
			parameters = remaining[:comma]
			remaining = strings.TrimSpace(remaining[comma+1:])
		} else {
			remaining = ""
		}
		if !strings.HasPrefix(parameters, ";") {
			return 0, queryUnavailable()
		}
		var relation string
		for _, parameter := range strings.Split(parameters, ";")[1:] {
			pair := strings.SplitN(strings.TrimSpace(parameter), "=", 2)
			if len(pair) != 2 || pair[0] != "rel" || relation != "" {
				return 0, queryUnavailable()
			}
			relation = strings.Trim(pair[1], `"`)
		}
		if relation != "next" {
			continue
		}
		if next != 0 {
			return 0, queryUnavailable()
		}
		observed, err := url.Parse(address)
		// GitHub emits numeric repository URLs in Link headers. Only the stable
		// repository identity read in this same query may supply that alias; the
		// next request is still constructed independently from configured names.
		pathMatches := err == nil && observed.EscapedPath() == expected.EscapedPath()
		prefix := repositoryPath(repository) + "/"
		if err == nil && domain.PositiveDecimal(repository.ID) && strings.HasPrefix(expected.EscapedPath(), prefix) {
			pathMatches = pathMatches || observed.EscapedPath() == "/repositories/"+repository.ID+"/"+strings.TrimPrefix(expected.EscapedPath(), prefix)
		}
		if err != nil || observed.Scheme != expected.Scheme || observed.Host != expected.Host || observed.User != nil || observed.Fragment != "" || !pathMatches {
			return 0, queryUnavailable()
		}
		values, err := url.ParseQuery(observed.RawQuery)
		if err != nil || len(values["page"]) != 1 || values.Get("page") != strconv.FormatUint(uint64(page)+1, 10) {
			return 0, queryUnavailable()
		}
		original := expected.Query()
		values.Del("page")
		original.Del("page")
		if values.Encode() != original.Encode() {
			return 0, queryUnavailable()
		}
		next = page + 1
	}
	return next, nil
}
func queryPath(repository domain.RemoteRepository, q domain.RepositoryQuery) (string, error) {
	base := "/repos/" + url.PathEscape(repository.Owner) + "/" + url.PathEscape(repository.Name)
	segment := "issues"
	if q.Kind == domain.RepositoryPullRequest {
		segment = "pulls"
	}
	if q.Operation == domain.RepositoryDetail {
		return base + "/" + segment + "/" + q.Number, nil
	}
	values := url.Values{"per_page": {strconv.FormatUint(uint64(q.PageSize), 10)}, "page": {strconv.FormatUint(uint64(q.Page), 10)}, "sort": {"updated"}}
	if q.Operation == domain.RepositoryList {
		values.Set("state", string(q.State))
		values.Set("direction", "desc")
		return base + "/" + segment + "?" + values.Encode(), nil
	}
	kind := "issue"
	if q.Kind == domain.RepositoryPullRequest {
		kind = "pr"
	}
	terms := []string{"repo:" + repository.Owner + "/" + repository.Name, "is:" + kind, "in:title,body"}
	if q.State != domain.RepositoryItemsAll {
		terms = append(terms, "state:"+string(q.State))
	}
	for _, term := range strings.Fields(q.Search) {
		terms = append(terms, `"`+term+`"`)
	}
	text := strings.Join(terms, " ")
	if len(text) > 256 {
		return "", domain.Fail(domain.InvalidArgument, "The scoped GitHub search is too long.", "Use fewer or shorter plain search terms.")
	}
	values.Set("q", text)
	values.Set("order", "desc")
	return "/search/issues?" + values.Encode(), nil
}
func (c *Client) QueryRepository(ctx context.Context, token []byte, owner, name string, q domain.RepositoryQuery) (RepositoryQueryObservation, error) {
	if err := q.Validate(); err != nil {
		return RepositoryQueryObservation{}, err
	}
	if err := domain.ValidateGitHubRepository(owner, name); err != nil {
		return RepositoryQueryObservation{}, err
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	identity, err := c.Identity(bounded, token)
	if err != nil {
		return RepositoryQueryObservation{}, err
	}
	if identity.Identity == nil {
		return RepositoryQueryObservation{}, identity.Problem
	}
	metadata := c.readRepositoryJSON(bounded, token, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name))
	if metadata.state != domain.IntegrationAccessAvailable {
		return RepositoryQueryObservation{}, readProblem(metadata)
	}
	repository, ok := parseRepository(metadata.raw, owner, name)
	if !ok {
		return RepositoryQueryObservation{}, queryUnavailable()
	}
	result := RepositoryQueryObservation{Identity: *identity.Identity, Repository: *repository, Items: []domain.RepositoryItem{}}
	if q.IsPRObservation() {
		return c.queryPRObservation(bounded, token, *repository, q, result)
	}
	path, err := queryPath(*repository, q)
	if err != nil {
		return result, err
	}
	if q.Operation == domain.RepositorySearch {
		// Search itself requires no fine-grained permission. Verify the matching
		// selected-repository feature independently, never borrowing other access.
		probe := q
		probe.Operation = domain.RepositoryList
		probe.Search = ""
		probe.Page = 1
		probe.PageSize = 1
		probePath, _ := queryPath(*repository, probe)
		read := c.readRepositoryJSON(bounded, token, probePath)
		if read.state != domain.IntegrationAccessAvailable {
			return result, readProblem(read)
		}
		if !boundedArray(read.raw, 1) {
			return result, queryUnavailable()
		}
	}
	read := c.readRepositoryJSON(bounded, token, path)
	if read.state != domain.IntegrationAccessAvailable {
		return result, readProblem(read)
	}
	var rows []json.RawMessage
	switch q.Operation {
	case domain.RepositoryDetail:
		rows = []json.RawMessage{read.raw}
	case domain.RepositoryList:
		if domain.Decode(read.raw, &rows) != nil || rows == nil || len(rows) > int(q.PageSize) {
			return result, queryUnavailable()
		}
	case domain.RepositorySearch:
		fields, ok := jsonObject(read.raw)
		total, totalOK := exactUnsigned(fields["total_count"])
		incomplete, incompleteOK := nullableBool(fields, "incomplete_results")
		if !ok || !totalOK || !incompleteOK || incomplete == nil || domain.Decode(fields["items"], &rows) != nil || rows == nil || len(rows) > int(q.PageSize) || uint64(len(rows)) > total {
			return result, queryUnavailable()
		}
		count := strconv.FormatUint(total, 10)
		result.TotalCount = &count
		result.Incomplete = incomplete
	}
	seen := map[string]bool{}
	for _, raw := range rows {
		item, skip, err := parseItem(raw, *repository, q)
		if err != nil {
			return result, err
		}
		if skip {
			if q.Operation == domain.RepositoryDetail {
				return result, domain.Fail(domain.NotFound, "The selected number is a pull request, not an issue.", "Query it as a pull request explicitly.")
			}
			continue
		}
		if seen[item.ID] {
			return result, queryUnavailable()
		}
		seen[item.ID] = true
		result.Items = append(result.Items, item)
	}
	if q.Operation != domain.RepositoryDetail {
		result.NextPage, err = queryNextPage(read.link, path, q.Page, *repository)
		if err != nil {
			return result, err
		}
		if result.NextPage > 10000 {
			return result, domain.Fail(domain.ResourceExhausted, "The repository listing page limit is reached.", "Narrow the query with search.")
		}
		if q.Operation == domain.RepositorySearch {
			total, _ := strconv.ParseUint(*result.TotalCount, 10, 64)
			result.SearchLimitReached = uint64(q.Page)*uint64(q.PageSize) >= 1000 && (total > 1000 || result.NextPage != 0)
			if result.NextPage != 0 && uint64(result.NextPage-1)*uint64(q.PageSize) >= 1000 {
				result.NextPage = 0
			}
		}
	}
	if bounded.Err() != nil {
		return result, domain.SafeError(bounded.Err())
	}
	return result, nil
}
