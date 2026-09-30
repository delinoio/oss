package workspace

import (
	"context"
	"slices"
	"strings"
	"unicode"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// GitHubRepository carries only a validated identity, never a remote URL or
// authentication observation. Missing metadata does not invalidate a checkout.
type GitHubRepository struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

func (i Inspection) ValidateGitHubRepositories() error {
	if len(i.GitHubRepositories) > 128 {
		return domain.Fail(domain.InvalidArgument, "Repository metadata exceeds its bound.", "Reduce the remote metadata.")
	}
	for remote, repository := range i.GitHubRepositories {
		if !slices.Contains(i.Remotes, remote) {
			return domain.Fail(domain.InvalidArgument, "GitHub metadata has no corresponding remote.", "Reinspect the repository.")
		}
		if err := domain.ValidateGitHubRepository(repository.Owner, repository.Name); err != nil {
			return err
		}
	}
	return nil
}

// Parse only exact transport prefixes and two plain path components. URL
// normalization must not turn escaped, credential-bearing or foreign input into
// trusted metadata. Raw values stay private to this Worker-local parser.
func parseInspectionGitHub(raw string) (GitHubRepository, bool) {
	if strings.IndexFunc(raw, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 || strings.ContainsAny(raw, "%?#\\") {
		return GitHubRepository{}, false
	}
	var path string
	for _, prefix := range []string{"https://github.com/", "ssh://git@github.com/", "git@github.com:"} {
		if strings.HasPrefix(raw, prefix) {
			path = strings.TrimPrefix(raw, prefix)
			break
		}
	}
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		return GitHubRepository{}, false
	}
	value := GitHubRepository{Owner: parts[0], Name: strings.TrimSuffix(parts[1], ".git")}
	if domain.ValidateGitHubRepository(value.Owner, value.Name) != nil {
		return GitHubRepository{}, false
	}
	return value, true
}

// EnrichInspection is called only after both peers negotiate the capability.
// get-url expands insteadOf locally and never invokes a transport or credential
// helper. An owned-process failure is a failure, not fabricated absent metadata.
func (g Git) EnrichInspection(ctx context.Context, inspection *Inspection) error {
	g.readOnly = true
	inspection.GitHubRepositories = nil
	for _, remote := range inspection.Remotes {
		raw, err := g.run(ctx, inspection.Root, "remote", "get-url", "--all", "--", remote)
		if err != nil {
			return err
		}
		// Exactly one LF-terminated URL (CRLF on Windows) is supported. Multiple
		// URLs and embedded delimiters deliberately produce unavailable metadata.
		value := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
		identity, ok := parseInspectionGitHub(value)
		if !ok {
			continue
		}
		if inspection.GitHubRepositories == nil {
			inspection.GitHubRepositories = map[string]GitHubRepository{}
		}
		inspection.GitHubRepositories[remote] = identity
	}
	if g.Logger != nil {
		g.Logger.InfoContext(ctx, "repository metadata inspected", "phase", "complete", "remote_count", len(inspection.Remotes), "github_count", len(inspection.GitHubRepositories))
	}
	return inspection.ValidateGitHubRepositories()
}
