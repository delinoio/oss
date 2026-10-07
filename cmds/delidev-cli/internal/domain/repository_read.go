package domain

import (
	"strings"
)

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
