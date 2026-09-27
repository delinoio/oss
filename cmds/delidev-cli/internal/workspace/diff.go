package workspace

import (
	"bytes"
	"context"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func diffUnsupported() error {
	return domain.Fail(domain.Unsupported, "This repository comparison is unavailable.", "Choose a prepared Git repository; creation comparisons require a Worktree, and working-tree views cannot run configured clean/process filters.")
}

func (g Git) readDiff(ctx context.Context, request ReadRequest, manifest Manifest) (domain.WorkspaceReadResult, error) {
	result := domain.WorkspaceReadResult{}
	q := request.Query
	var repo PreparedRepository
	for _, candidate := range manifest.Repositories {
		if candidate.ID == q.RepositoryID {
			repo = candidate
		}
	}
	if repo.ID == "" || q.Comparison == domain.DiffCreation && manifest.Type != domain.Worktree {
		return result, diffUnsupported()
	}
	headState, head, err := g.localHEAD(ctx, repo.Path)
	if err != nil {
		return result, err
	}
	value := domain.WorkspaceDiff{Comparison: q.Comparison, RepositoryID: repo.ID, Path: q.Path, Base: domain.DiffCommit, BaseObject: head, HeadCommit: head, Untracked: []string{}}
	if headState == LocalHEADUnborn {
		// Hashing empty stdin does not write an object, index, branch or commit.
		// Git recognizes its canonical empty tree for both object formats.
		raw, err := g.run(ctx, repo.Path, "hash-object", "-t", "tree", "--stdin")
		if err != nil || !canonicalCommit(trimGit(raw)) {
			return result, diffUnsupported()
		}
		value.Base, value.BaseObject = domain.DiffEmptyTree, trimGit(raw)
	}
	if q.Comparison == domain.DiffCreation {
		value.BaseObject = repo.StartingCommit
	}
	if q.Comparison != domain.DiffStaged {
		// Diff/textconv helpers are disabled below, but Git's clean/process
		// filters are a separate execution path. Do not invoke or silently
		// replace configured transformations during a product read operation.
		if err := g.checkDiffFilters(ctx, repo.Path, q.Path); err != nil {
			return result, err
		}
	}
	args := []string{"-c", "core.fsmonitor=false", "-c", "core.quotePath=true", "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--no-renames", "--diff-algorithm=myers", "--no-indent-heuristic", "--unified=3", "--inter-hunk-context=0", "--src-prefix=a/", "--dst-prefix=b/", "--submodule=short", "--ignore-submodules=dirty", "--no-relative"}
	if q.Comparison == domain.DiffStaged {
		args = append(args, "--cached")
	}
	args = append(args, value.BaseObject, "--")
	args = append(args, diffPathspec(q.Path)...)
	patch, err := g.run(ctx, repo.Path, args...)
	if err != nil {
		return result, err
	}
	if len(patch) > domain.WorkspacePreviewLimit {
		return result, domain.Fail(domain.ResourceExhausted, "The Git diff exceeds the 64 KiB preview limit.", "Select a narrower relative path; no partial diff was returned.")
	}
	value.Patch = string(patch)
	args = append([]string{"ls-files", "--others", "--exclude-standard", "-z", "--"}, diffPathspec(q.Path)...)
	raw, err := g.run(ctx, repo.Path, args...)
	if err != nil {
		return result, err
	}
	if len(raw) > 16<<10 || bytes.Count(raw, []byte{0}) > 100 {
		return result, domain.Fail(domain.ResourceExhausted, "Too many untracked paths for this comparison.", "Select a narrower relative path.")
	}
	if len(raw) > 0 {
		if raw[len(raw)-1] != 0 {
			return result, ResultUncertain()
		}
		value.Untracked = strings.Split(string(raw[:len(raw)-1]), "\x00")
		slices.Sort(value.Untracked)
	}
	freshState, freshHead, err := g.localHEAD(ctx, repo.Path)
	if err != nil {
		return result, err
	}
	if freshState != headState || freshHead != head {
		return result, domain.Fail(domain.Conflict, "The Git HEAD changed during comparison.", "Refresh the comparison after the commit or checkout finishes.")
	}
	value.Revision = value.Digest()
	result.Diff = &value
	return result, result.Validate(q)
}

func (g Git) checkDiffFilters(ctx context.Context, root, path string) error {
	// Global installations such as Git LFS can define drivers unused by this
	// repository. Inspect only attributes of the selected tracked paths.
	args := append([]string{"ls-files", "--cached", "-z", "--"}, diffPathspec(path)...)
	raw, err := g.run(ctx, root, args...)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	if raw[len(raw)-1] != 0 || bytes.Count(raw, []byte{0}) > domain.WorkspaceDirectoryLimit {
		return diffUnsupported()
	}
	paths := strings.Split(string(raw[:len(raw)-1]), "\x00")
	for start := 0; start < len(paths); start += 64 {
		selected := paths[start:min(start+64, len(paths))]
		for _, p := range selected {
			if !domain.WorkspacePath(p) {
				return diffUnsupported()
			}
		}
		args := append([]string{"check-attr", "-z", "filter", "--"}, selected...)
		attributes, err := g.run(ctx, root, args...)
		if err != nil {
			return err
		}
		parts := strings.Split(string(attributes), "\x00")
		if len(parts) != len(selected)*3+1 || parts[len(parts)-1] != "" {
			return ResultUncertain()
		}
		for i, p := range selected {
			if parts[3*i] != p || parts[3*i+1] != "filter" {
				return ResultUncertain()
			}
			if parts[3*i+2] != "unspecified" && parts[3*i+2] != "unset" {
				return diffUnsupported()
			}
		}
	}
	return nil
}

func diffPathspec(path string) []string {
	if path == "." {
		return nil
	}
	return []string{":(top,literal)" + path}
}
