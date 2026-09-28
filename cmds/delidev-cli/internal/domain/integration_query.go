package domain

import (
	"strconv"
	"strings"
	"time"
	"unicode"
)

type RepositoryItemKind string

const (
	RepositoryPullRequest RepositoryItemKind = "pull-request"
	RepositoryIssue       RepositoryItemKind = "issue"
)

type RepositoryQueryOperation string

const (
	RepositoryList      RepositoryQueryOperation = "list"
	RepositorySearch    RepositoryQueryOperation = "search"
	RepositoryDetail    RepositoryQueryOperation = "detail"
	RepositoryDiff      RepositoryQueryOperation = "diff"
	RepositoryChecks    RepositoryQueryOperation = "checks"
	RepositoryStatuses  RepositoryQueryOperation = "statuses"
	RepositoryRules     RepositoryQueryOperation = "rules"
	RepositoryCI        RepositoryQueryOperation = "ci"
	RepositoryFeedback  RepositoryQueryOperation = "feedback"
	RepositoryReviewers RepositoryQueryOperation = "reviewers"
)

type RepositoryItemState string

const (
	RepositoryItemOpen   RepositoryItemState = "open"
	RepositoryItemClosed RepositoryItemState = "closed"
	RepositoryItemsAll   RepositoryItemState = "all"
)

type RepositoryQuery struct {
	Kind      RepositoryItemKind       `json:"kind"`
	Operation RepositoryQueryOperation `json:"operation"`
	State     RepositoryItemState      `json:"state,omitempty"`
	Search    string                   `json:"search,omitempty"`
	Number    string                   `json:"number,omitempty"`
	Page      uint32                   `json:"page,omitempty"`
	PageSize  uint32                   `json:"page_size,omitempty"`
}

func PositiveDecimal(value string) bool {
	n, err := strconv.ParseUint(value, 10, 64)
	return err == nil && n > 0 && strconv.FormatUint(n, 10) == value
}
func (q RepositoryQuery) Validate() error {
	invalid := func() error {
		return Fail(InvalidArgument, "The repository query is invalid.", "Select an issue or pull request, an explicit operation and bounded page or item number.")
	}
	if q.Kind != RepositoryPullRequest && q.Kind != RepositoryIssue {
		return invalid()
	}
	switch q.Operation {
	case RepositoryDiff, RepositoryDetail, RepositoryRules, RepositoryCI, RepositoryFeedback, RepositoryReviewers:
		if (q.Operation == RepositoryDiff || q.Operation == RepositoryRules || q.Operation == RepositoryCI || q.Operation == RepositoryFeedback || q.Operation == RepositoryReviewers) && q.Kind != RepositoryPullRequest {
			return invalid()
		}
		if !PositiveDecimal(q.Number) || q.State != "" || q.Search != "" || q.Page != 0 || q.PageSize != 0 {
			return invalid()
		}
	case RepositoryChecks, RepositoryStatuses:
		if q.Kind != RepositoryPullRequest || !PositiveDecimal(q.Number) || q.State != "" || q.Search != "" || q.Page < 1 || q.Page > 10000 || q.PageSize < 1 || q.PageSize > 20 {
			return invalid()
		}
	case RepositoryList, RepositorySearch:
		if q.Number != "" || q.Page < 1 || q.Page > 10000 || q.PageSize < 1 || q.PageSize > 20 || (q.State != RepositoryItemOpen && q.State != RepositoryItemClosed && q.State != RepositoryItemsAll) {
			return invalid()
		}
		if q.Operation == RepositoryList && q.Search != "" {
			return invalid()
		}
		if q.Operation == RepositorySearch {
			if Text(q.Search, "search text", 120, true) != nil || strings.TrimSpace(q.Search) != q.Search || uint64(q.Page-1)*uint64(q.PageSize) >= 1000 {
				return invalid()
			}
			// Search is plain terms, never an arbitrary GitHub query that can override
			// the repository/kind scope. The adapter quotes each validated term.
			for _, r := range q.Search {
				if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != ' ' && r != '-' && r != '_' && r != '.' {
					return invalid()
				}
			}
		}
	default:
		return invalid()
	}
	return nil
}

type RepositoryActorKind string

const (
	RepositoryUser         RepositoryActorKind = "user"
	RepositoryBot          RepositoryActorKind = "bot"
	RepositoryOrganization RepositoryActorKind = "organization"
	RepositoryUnknownActor RepositoryActorKind = "unknown"
)

type RepositoryActor struct {
	ProviderType string              `json:"provider_type"`
	ID           string              `json:"id"`
	NodeID       string              `json:"node_id"`
	Login        string              `json:"login"`
	Kind         RepositoryActorKind `json:"kind"`
}

func (a RepositoryActor) Validate() error {
	invalid := func() error {
		return Fail(RecoveryRequired, "The repository actor is invalid.", "Refresh the repository observation.")
	}
	if !PositiveDecimal(a.ID) || Text(a.NodeID, "actor node identity", 256, true) != nil || Text(a.ProviderType, "provider actor type", 64, true) != nil || Text(a.Login, "actor login", 100, true) != nil || strings.ContainsAny(a.Login+a.ProviderType, "\r\n") {
		return invalid()
	}
	native := map[RepositoryActorKind]string{RepositoryUser: "User", RepositoryBot: "Bot", RepositoryOrganization: "Organization"}
	if a.Kind == RepositoryUnknownActor {
		if a.ProviderType == "User" || a.ProviderType == "Bot" || a.ProviderType == "Organization" {
			return invalid()
		}
		return nil
	}
	login := a.Login
	if a.Kind == RepositoryBot {
		login = strings.TrimSuffix(login, "[bot]")
	}
	if native[a.Kind] == "" || native[a.Kind] != a.ProviderType || !GitHubOwner(login) {
		return invalid()
	}
	return nil
}

// Actor kind is original GitHub metadata, not verified App identity or effective
// collaborator permission. It never grants review-remediation authority.
type RepositoryItemIdentitySource string

const (
	RepositoryIssueIdentity       RepositoryItemIdentitySource = "issue-api"
	RepositoryPullRequestIdentity RepositoryItemIdentitySource = "pull-request-api"
)

type RepositoryItem struct {
	IdentitySource RepositoryItemIdentitySource `json:"identity_source"`
	Provider       IntegrationProvider          `json:"provider"`
	Kind           RepositoryItemKind           `json:"kind"`
	ID             string                       `json:"id"`
	NodeID         string                       `json:"node_id"`
	Number         string                       `json:"number"`
	Title          string                       `json:"title"`
	State          RepositoryItemState          `json:"state"`
	Author         *RepositoryActor             `json:"author,omitempty"`
	CreatedAt      time.Time                    `json:"created_at"`
	UpdatedAt      time.Time                    `json:"updated_at"`
	URL            string                       `json:"url"`
	Body           *string                      `json:"body,omitempty"`
	Draft          *bool                        `json:"draft,omitempty"`
	Merged         *bool                        `json:"merged,omitempty"`
	Mergeable      *bool                        `json:"mergeable,omitempty"`
	BaseRef        string                       `json:"base_ref,omitempty"`
	BaseSHA        string                       `json:"base_sha,omitempty"`
	HeadRef        string                       `json:"head_ref,omitempty"`
	HeadSHA        string                       `json:"head_sha,omitempty"`
	HeadRepository *PRHeadRepositoryObservation `json:"head_repository,omitempty"`
}

func RepositoryItemURL(owner, name string, kind RepositoryItemKind, number string) string {
	segment := "issues"
	if kind == RepositoryPullRequest {
		segment = "pull"
	}
	return "https://github.com/" + owner + "/" + name + "/" + segment + "/" + number
}
func (r RepositoryItem) Validate(repository RemoteRepository, query RepositoryQuery) error {
	invalid := func() error {
		return Fail(RecoveryRequired, "The repository item is inconsistent.", "Refresh the selected repository; no partial or foreign item is accepted.")
	}
	if r.Provider != GitHubCom || r.Kind != query.Kind || !PositiveDecimal(r.ID) || !PositiveDecimal(r.Number) || Text(r.NodeID, "item node identity", 256, true) != nil || Text(r.Title, "item title", 4096, true) != nil || (r.State != RepositoryItemOpen && r.State != RepositoryItemClosed) || r.CreatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) || r.URL != RepositoryItemURL(repository.Owner, repository.Name, r.Kind, r.Number) {
		return invalid()
	}
	source := RepositoryIssueIdentity
	if query.Kind == RepositoryPullRequest && query.Operation != RepositorySearch {
		source = RepositoryPullRequestIdentity
	}
	if r.IdentitySource != source {
		return invalid()
	}
	if r.Author != nil && r.Author.Validate() != nil {
		return invalid()
	}
	if r.HeadRepository != nil && (r.Kind != RepositoryPullRequest || query.Operation != RepositoryDetail || r.HeadRepository.Validate(repository) != nil) {
		return invalid()
	}
	if query.Operation != RepositoryDetail {
		if r.Body != nil || r.Merged != nil || r.Mergeable != nil || r.BaseRef != "" || r.BaseSHA != "" || r.HeadRef != "" || r.HeadSHA != "" {
			return invalid()
		}
	} else {
		if r.Number != query.Number || r.Body == nil || Text(*r.Body, "item body", 128<<10, false) != nil {
			return invalid()
		}
		if r.Kind == RepositoryPullRequest {
			if r.Draft == nil || r.Merged == nil || Text(r.BaseRef, "base ref", 1024, true) != nil || Text(r.HeadRef, "head ref", 1024, true) != nil || strings.ContainsAny(r.BaseRef+r.HeadRef, "\r\n") || !repositorySHA(r.BaseSHA) || !repositorySHA(r.HeadSHA) {
				return invalid()
			}
		}
	}
	if r.Kind == RepositoryIssue && (r.Draft != nil || r.Merged != nil || r.Mergeable != nil || r.BaseRef != "" || r.BaseSHA != "" || r.HeadRef != "" || r.HeadSHA != "") {
		return invalid()
	}
	return nil
}
func repositorySHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'f' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func (q RepositoryQuery) IsPRObservation() bool {
	return q.Operation == RepositoryDiff || q.Operation == RepositoryChecks || q.Operation == RepositoryStatuses || q.Operation == RepositoryRules || q.Operation == RepositoryCI || q.Operation == RepositoryFeedback || q.Operation == RepositoryReviewers
}

type RepositoryQueryResult struct {
	Reviewers          *PullRequestReviewers      `json:"reviewers,omitempty"`
	Feedback           *PullRequestFeedback       `json:"feedback,omitempty"`
	CI                 *PullRequestCI             `json:"ci,omitempty"`
	Rules              *PullRequestRules          `json:"rules,omitempty"`
	Diff               *PullRequestDiff           `json:"diff,omitempty"`
	Checks             *PullRequestChecks         `json:"checks,omitempty"`
	Statuses           *PullRequestCommitStatuses `json:"statuses,omitempty"`
	RepositoryID       ID                         `json:"repository_id"`
	RepositoryRevision string                     `json:"repository_revision"`
	ProfileID          ID                         `json:"profile_id"`
	GenerationID       ID                         `json:"generation_id"`
	ObservedAt         time.Time                  `json:"observed_at"`
	Identity           GitHubIdentity             `json:"identity"`
	Repository         RemoteRepository           `json:"repository"`
	Query              RepositoryQuery            `json:"query"`
	Items              []RepositoryItem           `json:"items"`
	NextPage           uint32                     `json:"next_page,omitempty"`
	TotalCount         *string                    `json:"total_count,omitempty"`
	Incomplete         *bool                      `json:"incomplete,omitempty"`
	SearchLimitReached bool                       `json:"search_limit_reached,omitempty"`
}

func (r RepositoryQueryResult) Validate() error {
	invalid := func() error {
		return Fail(RecoveryRequired, "The repository query response is inconsistent.", "Refresh the exact repository query; no complete result is inferred.")
	}
	if r.RepositoryID.Validate() != nil || r.ProfileID.Validate() != nil || r.GenerationID.Validate() != nil || !PositiveDecimal(r.RepositoryRevision) || r.ObservedAt.IsZero() || r.Identity.Validate() != nil || r.Query.Validate() != nil || r.Repository.Provider != GitHubCom || !PositiveDecimal(r.Repository.ID) || Text(r.Repository.NodeID, "repository node identity", 256, true) != nil || ValidateGitHubRepository(r.Repository.Owner, r.Repository.Name) != nil || r.Items == nil {
		return invalid()
	}
	q := r.Query
	if q.Operation == RepositoryDetail || q.Operation == RepositoryDiff || q.Operation == RepositoryRules || q.Operation == RepositoryCI || q.Operation == RepositoryFeedback || q.Operation == RepositoryReviewers {
		if len(r.Items) != 1 || r.NextPage != 0 {
			return invalid()
		}
	} else if len(r.Items) > int(q.PageSize) || (r.NextPage != 0 && (r.NextPage != q.Page+1 || r.NextPage > 10000)) {
		return invalid()
	}
	if q.Operation == RepositorySearch {
		if r.TotalCount == nil || r.Incomplete == nil {
			return invalid()
		}
		n, err := strconv.ParseUint(*r.TotalCount, 10, 64)
		if err != nil || strconv.FormatUint(n, 10) != *r.TotalCount || n < uint64(len(r.Items)) {
			return invalid()
		}
		if r.NextPage > 0 && uint64(r.NextPage-1)*uint64(q.PageSize) >= 1000 {
			return invalid()
		}
	} else if r.TotalCount != nil || r.Incomplete != nil || r.SearchLimitReached {
		return invalid()
	}
	itemQuery := q
	if q.IsPRObservation() {
		if len(r.Items) != 1 {
			return invalid()
		}
		itemQuery = RepositoryQuery{Kind: RepositoryPullRequest, Operation: RepositoryDetail, Number: q.Number}
	}
	if q.Operation != RepositoryReviewers && r.Reviewers != nil {
		return invalid()
	}
	if q.Operation != RepositoryFeedback && r.Feedback != nil {
		return invalid()
	}
	if q.Operation != RepositoryCI && r.CI != nil {
		return invalid()
	}
	if q.Operation != RepositoryRules && r.Rules != nil {
		return invalid()
	}
	switch q.Operation {
	case RepositoryReviewers:
		if r.Reviewers == nil || r.Feedback != nil || r.CI != nil || r.Rules != nil || r.Diff != nil || r.Checks != nil || r.Statuses != nil || r.Reviewers.Validate(r.Items[0]) != nil {
			return invalid()
		}
	case RepositoryFeedback:
		if r.Feedback == nil || r.CI != nil || r.Rules != nil || r.Diff != nil || r.Checks != nil || r.Statuses != nil || r.Feedback.Validate(r.Items[0]) != nil {
			return invalid()
		}
	case RepositoryCI:
		if r.CI == nil || r.Rules != nil || r.Diff != nil || r.Checks != nil || r.Statuses != nil || r.CI.Validate(r.Items[0]) != nil {
			return invalid()
		}
	case RepositoryRules:
		if r.Rules == nil || r.Diff != nil || r.Checks != nil || r.Statuses != nil || r.Rules.Validate(r.Items[0]) != nil {
			return invalid()
		}
	case RepositoryDiff:
		if r.Diff == nil || r.Checks != nil || r.Statuses != nil || r.Diff.Validate(r.Items[0]) != nil {
			return invalid()
		}
	case RepositoryChecks:
		if r.Checks == nil || r.Diff != nil || r.Statuses != nil || r.Checks.Validate(r.Items[0], q.PageSize) != nil {
			return invalid()
		}
	case RepositoryStatuses:
		if r.Statuses == nil || r.Diff != nil || r.Checks != nil || r.Statuses.Validate(r.Items[0], q.PageSize) != nil {
			return invalid()
		}
	default:
		if r.Diff != nil || r.Checks != nil || r.Statuses != nil {
			return invalid()
		}
	}
	seen := map[string]bool{}
	for _, item := range r.Items {
		if item.Validate(r.Repository, itemQuery) != nil || seen[item.ID] {
			return invalid()
		}
		seen[item.ID] = true
	}
	return nil
}
