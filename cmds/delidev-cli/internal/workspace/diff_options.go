package workspace

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func branchUnavailable() error {
	return domain.Fail(domain.Unsupported, "The selected branch base is unavailable locally.", "Select an available explicit base; no fetch or fallback comparison was performed.")
}
func diffRepository(manifest Manifest, id domain.ID) (PreparedRepository, bool) {
	for _, repo := range manifest.Repositories {
		if repo.ID == id {
			return repo, true
		}
	}
	return PreparedRepository{}, false
}
func (g Git) readDiffOptions(ctx context.Context, request ReadRequest, manifest Manifest) (domain.WorkspaceReadResult, error) {
	result := domain.WorkspaceReadResult{}
	repo, ok := diffRepository(manifest, request.Query.RepositoryID)
	if !ok {
		return result, diffUnsupported()
	}
	inspection, err := g.Inspect(ctx, repo.Path)
	if err != nil {
		return result, err
	}
	// Inspect only local refs. Explicit Base comes from the immutable accepted
	// preparation, never a manifest's defaulted Starting or captured Local HEAD.
	var spec RepositorySpec
	for _, candidate := range request.Preparation.Repositories {
		if candidate.ID == repo.ID {
			spec = candidate
		}
	}
	options := domain.WorkspaceDiffOptions{Version: 1, RepositoryID: repo.ID, Path: request.Query.Path, Choices: []domain.DiffBaseChoice{}}
	raw, err := g.run(ctx, repo.Path, "for-each-ref", "--format=%(refname)%00%(symref)", "refs/heads/", "refs/remotes/")
	if err != nil {
		return result, err
	}
	if len(raw) > 128<<10 {
		return result, domain.Fail(domain.ResourceExhausted, "The complete local reference inventory exceeds 128 KiB.", "Reduce local references; no partial choices were returned.")
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\x00")
		if len(fields) != 2 {
			return result, ResultUncertain()
		}
		if fields[1] != "" && strings.HasSuffix(fields[0], "/HEAD") {
			continue
		} // Symbolic HEAD aliases are not selectable branches.
		ref := domain.Reference{}
		if strings.HasPrefix(fields[0], "refs/heads/") {
			ref.Type, ref.Name = domain.LocalBranch, strings.TrimPrefix(fields[0], "refs/heads/")
		} else if strings.HasPrefix(fields[0], "refs/remotes/") {
			parts := strings.SplitN(strings.TrimPrefix(fields[0], "refs/remotes/"), "/", 2)
			if len(parts) != 2 {
				return result, ResultUncertain()
			}
			ref.Type, ref.Remote, ref.Name = domain.RemoteBranch, parts[0], parts[1]
		} else {
			return result, ResultUncertain()
		}
		if len(options.Choices) >= 1000 {
			return result, domain.Fail(domain.ResourceExhausted, "The complete local reference inventory exceeds 1,000 references.", "Reduce local references; no partial choices were returned.")
		}
		if ref.Validate(false) != nil {
			return result, branchUnavailable()
		}
		options.Choices = append(options.Choices, domain.DiffBaseChoice{Reference: ref})
	}
	var selected domain.Reference
	if spec.Base != (domain.Reference{}) {
		selected = spec.Base
	} else {
		selected, _ = DefaultStarting(inspection, spec.PreferredRemote)
	}
	if selected != (domain.Reference{}) {
		options.Default = &selected
		found := false
		for i := range options.Choices {
			if options.Choices[i].Reference == selected {
				options.Choices[i].Configured = spec.Base != (domain.Reference{})
				found = true
			}
		}
		if !found {
			options.Choices = append(options.Choices, domain.DiffBaseChoice{Reference: selected, Configured: true})
		}
		_, resolveErr := g.Resolve(ctx, inspection, selected, false)
		if resolveErr != nil && domain.SafeError(resolveErr).Code == domain.RecoveryRequired {
			return result, resolveErr
		}
		options.DefaultAvailable = resolveErr == nil
	}
	encoded, _ := json.Marshal(options)
	if len(encoded) > 128<<10 {
		return result, domain.Fail(domain.ResourceExhausted, "The complete comparison choices exceed 128 KiB.", "Reduce local references; no partial choices were returned.")
	}
	result.DiffOptions = &options
	return result, result.Validate(request.Query)
}

func (g Git) branchBase(ctx context.Context, repo PreparedRepository, ref *domain.Reference, head string) (string, string, error) {
	if ref == nil || head == "" {
		return "", "", branchUnavailable()
	}
	inspection, err := g.Inspect(ctx, repo.Path)
	if err != nil {
		return "", "", err
	}
	base, err := g.Resolve(ctx, inspection, *ref, false)
	if err != nil {
		if domain.SafeError(err).Code == domain.RecoveryRequired {
			return "", "", err
		}
		return "", "", branchUnavailable()
	}
	raw, err := g.run(ctx, repo.Path, "merge-base", "--all", head, base)
	if err != nil {
		if domain.SafeError(err).Code == domain.RecoveryRequired {
			return "", "", err
		}
		return "", "", branchUnavailable()
	}
	merge := trimGit(raw)
	if !canonicalCommit(merge) {
		return "", "", branchUnavailable()
	}
	return base, merge, nil
}
