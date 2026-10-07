package domain

import (
	"net/url"
	"strings"
)

type GitHubTokenAccess string

const (
	GitHubSelectedRepositories GitHubTokenAccess = "selected-repositories"
	GitHubPublicRepositories   GitHubTokenAccess = "public-repositories"
	GitHubPrivateRepositories  GitHubTokenAccess = "private-repositories"
)

// GitHubTokenForm describes a creation form, never token permissions or access.
type GitHubTokenForm struct {
	ProfileID       ID                `json:"profile_id"`
	ProfileRevision string            `json:"profile_revision"`
	TokenKind       PATKind           `json:"token_kind"`
	ResourceOwner   string            `json:"resource_owner,omitempty"`
	Access          GitHubTokenAccess `json:"access"`
	URL             string            `json:"url"`
}

func GitHubTokenFormURL(kind PATKind, owner string, access GitHubTokenAccess) (string, error) {
	// Saved fine-grained profiles retain their explicit, immutable owner. Only
	// draft preparation may leave owner selection to GitHub's official form.
	if kind == FineGrainedPAT && owner == "" {
		return "", Fail(InvalidArgument, "The token type and repository access selection do not match.", "Use selected repositories with a fine-grained owner, or explicitly choose public/private repositories for a classic token.")
	}
	return GitHubDraftTokenFormURL(kind, owner, access)
}

// GitHubDraftTokenFormURL permits an undeclared draft owner without changing
// saved-profile validation or granting repository access.
func GitHubDraftTokenFormURL(kind PATKind, owner string, access GitHubTokenAccess) (string, error) {
	invalid := func() (string, error) {
		return "", Fail(InvalidArgument, "The token type and repository access selection do not match.", "Use selected repositories for a fine-grained token, or explicitly choose public/private repositories for a classic token.")
	}
	if owner != "" && !GitHubOwner(owner) {
		return invalid()
	}
	q := url.Values{}
	path := "/settings/tokens/new"
	switch kind {
	case FineGrainedPAT:
		if access != GitHubSelectedRepositories {
			return invalid()
		}
		path = "/settings/personal-access-tokens/new"
		q.Set("name", "DeliDev read-only")
		q.Set("description", "Read-only repository inspection")
		if owner != "" {
			q.Set("target_name", owner)
		}
		q.Set("expires_in", "30")
		// The verified official form exposes these five permissions, but no
		// Checks permission or repository-list prefill. Users must explicitly
		// select Only select repositories; the form defaults to All repositories.
		for _, key := range []string{"metadata", "contents", "pull_requests", "statuses", "issues"} {
			q.Set(key, "read")
		}
	case ClassicPAT:
		switch access {
		case GitHubPublicRepositories:
			q.Set("description", "DeliDev public read-only")
		case GitHubPrivateRepositories:
			q.Set("description", "DeliDev private repository lookup")
			q.Set("scopes", "repo")
		default:
			return invalid()
		}
	default:
		return invalid()
	}
	return "https://github.com" + path + "?" + q.Encode(), nil
}

func (f GitHubTokenForm) Validate() error {
	expected, err := GitHubTokenFormURL(f.TokenKind, f.ResourceOwner, f.Access)
	if err != nil || f.ProfileID.Validate() != nil || !PositiveDecimal(f.ProfileRevision) || f.URL != expected {
		return Fail(RecoveryRequired, "The GitHub form does not match the selected profile.", "Refresh the profile and prepare its form again.")
	}
	return nil
}

// ValidateGitHubPresentationURL is the closed OS-presentation boundary shared by
// desktop sidecars and CLI. Remote content cannot open arbitrary URLs or files.
func ValidateGitHubPresentationURL(raw string) error {
	invalid := func() error {
		return Fail(InvalidArgument, "This address is not an allowed GitHub destination.", "Use a prepared official token form or a validated PR/issue address.")
	}
	if len(raw) > 2048 || strings.ContainsAny(raw, "\r\n\t\\\x00") {
		return invalid()
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.Fragment != "" || u.RawFragment != "" || u.Opaque != "" || u.RawPath != "" {
		return invalid()
	}
	if u.Path == "/settings/tokens/new" || u.Path == "/settings/personal-access-tokens/new" {
		q, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return invalid()
		}
		kind, access, owner := ClassicPAT, GitHubPublicRepositories, ""
		if u.Path == "/settings/personal-access-tokens/new" {
			kind, access, owner = FineGrainedPAT, GitHubSelectedRepositories, q.Get("target_name")
		} else if q.Get("scopes") == "repo" {
			access = GitHubPrivateRepositories
		}
		expected, err := GitHubDraftTokenFormURL(kind, owner, access)
		if err != nil || raw != expected {
			return invalid()
		}
		return nil
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) != 5 || parts[0] != "" || ValidateGitHubRepository(parts[1], parts[2]) != nil || (parts[3] != "pull" && parts[3] != "issues") || !PositiveDecimal(parts[4]) || u.RawQuery != "" || u.ForceQuery {
		return invalid()
	}
	kind := RepositoryPullRequest
	if parts[3] == "issues" {
		kind = RepositoryIssue
	}
	if raw != RepositoryItemURL(parts[1], parts[2], kind, parts[4]) {
		return invalid()
	}
	return nil
}
