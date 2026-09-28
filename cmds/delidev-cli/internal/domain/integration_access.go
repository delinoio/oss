package domain

import (
	"strconv"
	"strings"
	"time"
)

type IntegrationFeature string

const (
	RepositoryMetadataFeature  IntegrationFeature = "repository-metadata"
	RepositoryContentsFeature  IntegrationFeature = "repository-contents"
	PullRequestsFeature        IntegrationFeature = "pull-requests"
	IssuesFeature              IntegrationFeature = "issues"
	ChecksFeature              IntegrationFeature = "checks"
	CommitStatusesFeature      IntegrationFeature = "commit-statuses"
	RulesetsFeature            IntegrationFeature = "rulesets"
	ReviewerPermissionsFeature IntegrationFeature = "reviewer-permissions"
)

func IntegrationFeatures() [8]IntegrationFeature {
	return [8]IntegrationFeature{RepositoryMetadataFeature, RepositoryContentsFeature, PullRequestsFeature, IssuesFeature, ChecksFeature, CommitStatusesFeature, RulesetsFeature, ReviewerPermissionsFeature}
}

type IntegrationAccessState string

const (
	IntegrationAccessAvailable    IntegrationAccessState = "available"
	IntegrationAccessInvalidToken IntegrationAccessState = "invalid-token"
	IntegrationAccessDenied       IntegrationAccessState = "restricted"
	IntegrationAccessNotFound     IntegrationAccessState = "not-found-or-inaccessible"
	IntegrationAccessSSORequired  IntegrationAccessState = "sso-required"
	IntegrationAccessRateLimited  IntegrationAccessState = "rate-limited"
	IntegrationAccessUnavailable  IntegrationAccessState = "unavailable"
	IntegrationAccessNotEvaluated IntegrationAccessState = "not-evaluated"
)

// Available means the exact read endpoint was observed successfully. It is not
// a passing CI check, a ruleset decision, or permission for an API mutation.
type IntegrationFeatureAccess struct {
	Feature IntegrationFeature     `json:"feature"`
	State   IntegrationAccessState `json:"state"`
	Problem *Error                 `json:"problem,omitempty"`
}
type RemoteRepository struct {
	Provider      IntegrationProvider `json:"provider"`
	ID            string              `json:"id"`
	NodeID        string              `json:"node_id"`
	Owner         string              `json:"owner"`
	Name          string              `json:"name"`
	Private       bool                `json:"private"`
	DefaultBranch string              `json:"default_branch,omitempty"`
	HeadCommit    string              `json:"head_commit,omitempty"`
}

func GitHubRepositoryName(name string) bool {
	if name == "" || len(name) > 100 || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}
func ValidateGitHubRepository(owner, name string) error {
	if !GitHubOwner(owner) || !GitHubRepositoryName(name) {
		return Fail(InvalidArgument, "A GitHub repository owner and name are required.", "Select the exact GitHub.com owner and repository in repository settings.")
	}
	return nil
}
func (d IntegrationDefinition) AllowsGitHubOwner(owner string) bool {
	return d.Provider == GitHubCom && (d.ResourceOwner == "" && d.TokenKind == ClassicPAT || strings.EqualFold(d.ResourceOwner, owner))
}

// Access is a fresh read-only observation, never a persisted authorization
// grant. Each later query rechecks the selected profile and exact generation.
type RepositoryIntegrationAccess struct {
	RepositoryID       ID                         `json:"repository_id"`
	RepositoryRevision string                     `json:"repository_revision"`
	ProfileID          ID                         `json:"profile_id"`
	GenerationID       ID                         `json:"generation_id"`
	ObservedAt         time.Time                  `json:"observed_at"`
	Identity           *GitHubIdentity            `json:"identity,omitempty"`
	Repository         *RemoteRepository          `json:"repository,omitempty"`
	Features           []IntegrationFeatureAccess `json:"features"`
}

func (a RepositoryIntegrationAccess) Validate() error {
	invalid := func() error {
		return Fail(RecoveryRequired, "The repository access observation is inconsistent.", "Refresh the exact repository selection; no access or successful checks can be inferred.")
	}
	revision, err := strconv.ParseUint(a.RepositoryRevision, 10, 64)
	if a.RepositoryID.Validate() != nil || a.ProfileID.Validate() != nil || a.GenerationID.Validate() != nil || err != nil || revision == 0 || strconv.FormatUint(revision, 10) != a.RepositoryRevision || a.ObservedAt.IsZero() {
		return invalid()
	}
	features := IntegrationFeatures()
	if len(a.Features) != len(features) {
		return invalid()
	}
	if a.Identity != nil && a.Identity.Validate() != nil {
		return invalid()
	}
	if r := a.Repository; r != nil {
		id, err := strconv.ParseUint(r.ID, 10, 64)
		if a.Identity == nil || r.Provider != GitHubCom || err != nil || id == 0 || strconv.FormatUint(id, 10) != r.ID || Text(r.NodeID, "repository node identity", 256, true) != nil || ValidateGitHubRepository(r.Owner, r.Name) != nil || Text(r.DefaultBranch, "default branch", 1024, false) != nil || strings.ContainsAny(r.DefaultBranch, "\r\n") {
			return invalid()
		}
		if r.HeadCommit != "" {
			if len(r.HeadCommit) != 40 || r.DefaultBranch == "" {
				return invalid()
			}
			for _, c := range r.HeadCommit {
				if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
					return invalid()
				}
			}
		}
	}
	for i, f := range a.Features {
		if f.Feature != features[i] {
			return invalid()
		}
		if f.State == IntegrationAccessAvailable {
			if f.Problem != nil || a.Repository == nil || a.Identity == nil {
				return invalid()
			}
			if (f.Feature == ChecksFeature || f.Feature == CommitStatusesFeature || f.Feature == RepositoryContentsFeature) && a.Repository.HeadCommit == "" {
				return invalid()
			}
			if f.Feature == RulesetsFeature && a.Repository.DefaultBranch == "" {
				return invalid()
			}
		} else {
			switch f.State {
			case IntegrationAccessInvalidToken, IntegrationAccessDenied, IntegrationAccessNotFound, IntegrationAccessSSORequired, IntegrationAccessRateLimited, IntegrationAccessUnavailable, IntegrationAccessNotEvaluated:
			default:
				return invalid()
			}
			if f.Problem == nil || Text(f.Problem.Message, "access problem", 4096, true) != nil || Text(f.Problem.Guidance, "access guidance", 4096, false) != nil {
				return invalid()
			}
		}
	}
	if (a.Repository != nil) != (a.Features[0].State == IntegrationAccessAvailable) {
		return invalid()
	}
	return nil
}
