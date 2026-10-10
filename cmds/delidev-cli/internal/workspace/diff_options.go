package workspace

import (
	"context"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func (g Git) readDiffOptions(ctx context.Context, request ReadRequest, manifest Manifest) (domain.WorkspaceReadResult, error) {
	var repo PreparedRepository
	for _, candidate := range manifest.Repositories {
		if candidate.ID == request.Query.RepositoryID {
			repo = candidate
		}
	}
	if repo.ID == "" {
		return domain.WorkspaceReadResult{}, diffUnsupported()
	}
	inspection, err := g.Inspect(ctx, repo.Path)
	if err != nil {
		return domain.WorkspaceReadResult{}, err
	}
	// Query only local refs. In particular, no ls-remote or fetch can discover
	// options or change the original accepted preparation's base selection.
	raw, err := g.run(ctx, repo.Path, "for-each-ref", "--format=%(refname)%00%(symref)", "refs/heads/", "refs/remotes/")
	if err != nil {
		return domain.WorkspaceReadResult{}, err
	}
	if len(raw) > domain.WorkspaceDiffOptionsLimit {
		return domain.WorkspaceReadResult{}, diffOptionsTooLarge()
	}
	options := domain.WorkspaceDiffOptions{Version: 1, RepositoryID: repo.ID, Choices: []domain.WorkspaceDiffChoice{}}
	for _, row := range strings.Split(string(raw), "\n") {
		if row == "" {
			continue
		}
		parts := strings.Split(row, "\x00")
		if len(parts) != 2 {
			return domain.WorkspaceReadResult{}, ResultUncertain()
		}
		if parts[1] != "" {
			continue
		}
		ref := domain.Reference{}
		if name, ok := strings.CutPrefix(parts[0], "refs/heads/"); ok {
			ref = domain.Reference{Type: domain.LocalBranch, Name: name}
		} else if name, ok := strings.CutPrefix(parts[0], "refs/remotes/"); ok {
			remote, branch, ok := strings.Cut(name, "/")
			if ok {
				ref = domain.Reference{Type: domain.RemoteBranch, Remote: remote, Name: branch}
			}
		}
		if ref.Type == "" || ref.Validate(false) != nil {
			return domain.WorkspaceReadResult{}, diffUnsupported()
		}
		options.Choices = append(options.Choices, domain.WorkspaceDiffChoice{Reference: ref, Available: ref.Type == domain.LocalBranch || slices.Contains(inspection.Remotes, ref.Remote)})
		if len(options.Choices) > domain.WorkspaceDiffReferenceLimit {
			return domain.WorkspaceReadResult{}, diffOptionsTooLarge()
		}
	}
	var saved domain.Reference
	var preferred string
	for _, spec := range request.Preparation.Repositories {
		if spec.ID == repo.ID {
			saved, preferred = spec.Base, spec.PreferredRemote
		}
	}
	selected := saved
	if selected == (domain.Reference{}) {
		selected, _ = DefaultStarting(inspection, preferred)
	}
	if selected != (domain.Reference{}) {
		_, resolveErr := g.Resolve(ctx, inspection, selected, false)
		if ctx.Err() != nil {
			return domain.WorkspaceReadResult{}, ctx.Err()
		}
		available := resolveErr == nil
		found := false
		for i := range options.Choices {
			if options.Choices[i].Reference == selected {
				options.Choices[i].Available = available
				options.Choices[i].Configured = saved != (domain.Reference{})
				found = true
			}
		}
		if !found && saved != (domain.Reference{}) {
			options.Choices = append(options.Choices, domain.WorkspaceDiffChoice{Reference: saved, Configured: true, Available: available})
		}
		if available {
			options.Default = selected
		}
	}
	result := domain.WorkspaceReadResult{DiffOptions: &options}
	return result, result.Validate(request.Query)
}

func diffOptionsTooLarge() error {
	return domain.Fail(domain.ResourceExhausted, "The complete local reference inventory exceeds its limit.", "Keep at most 1,000 local and remote-tracking branches and 128 KiB of reference data; no partial choices were returned.")
}

func (g Git) branchBase(ctx context.Context, root string, ref domain.Reference, head string) (string, string, error) {
	inspection, err := g.Inspect(ctx, root)
	if err != nil {
		return "", "", err
	}
	base, err := g.Resolve(ctx, inspection, ref, false)
	if err != nil {
		return "", "", domain.Fail(domain.Unavailable, "The selected base is unavailable locally.", "Choose an available local reference; comparison does not fetch missing objects.")
	}
	raw, err := g.run(ctx, root, "merge-base", "--all", head, base)
	if err != nil {
		return "", "", domain.Fail(domain.Unavailable, "The selected base has no usable common history.", "Choose a locally available reference with one merge-base.")
	}
	merge := strings.Fields(string(raw))
	if len(merge) != 1 || !canonicalCommit(merge[0]) {
		return "", "", domain.Fail(domain.Unavailable, "The comparison has no single merge-base.", "Choose an unambiguous base; no alternative comparison was substituted.")
	}
	return base, merge[0], nil
}
