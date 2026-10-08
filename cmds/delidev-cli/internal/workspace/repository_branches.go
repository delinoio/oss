// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Discovery has no checkout authority. The Worker private state directory is
// used only as the native process working directory, never as a repository.
func (g Git) DiscoverRepositoryBranches(ctx context.Context, root string, input domain.RepositoryBranchesInput) (domain.RepositoryBranchesResult, error) {
	result := domain.RepositoryBranchesResult{ProjectID: input.ProjectID, ProjectRevision: input.ProjectRevision, RepositoryID: input.RepositoryID, RepositoryRevision: input.RepositoryRevision, MachineID: input.MachineID, MachineRevision: input.MachineRevision, Remote: input.Remote, Branches: []string{}}
	if err := input.Validate(); err != nil {
		return result, err
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := g.validateManagedCloneSource(bounded, root, input.Source); err != nil {
		return result, err
	}
	profile, err := g.cloneProfile(bounded, root)
	if err != nil {
		return result, err
	}
	profile.environment = append(profile.environment, "GIT_CEILING_DIRECTORIES="+root)
	if _, err := os.Lstat(filepath.Join(root, ".git")); !os.IsNotExist(err) {
		return result, domain.Fail(domain.InvalidArgument, "Worker state cannot be a Git checkout.", "Use a private Worker state directory.")
	}
	profile.readOnly = true
	profile.branchInventory = true
	profile.Timeout = 2 * time.Minute
	raw, err := profile.run(bounded, root, "ls-remote", "--heads", "--", input.Source)
	if err != nil {
		return result, err
	}
	names, err := parseRepositoryBranches(raw, input.Remote)
	if err != nil {
		return result, err
	}
	result.Branches = names
	result.ObservedAt = time.Now().UTC()
	if err := result.Validate(input); err != nil {
		return result, err
	}
	if g.Logger != nil {
		g.Logger.InfoContext(ctx, "repository_branches_observed", "owner_id", g.OwnerID, "repository_id", input.RepositoryID, "branch_count", len(names))
	}
	return result, nil
}
func parseRepositoryBranches(raw []byte, remote string) ([]string, error) {
	if len(raw) > domain.MaxRepositoryBranchesBytes {
		return nil, domain.Fail(domain.ResourceExhausted, "Remote branch inventory exceeds its bound.", "Use the saved or manual reference.")
	}
	branches := []string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" {
			continue
		}
		sha, ref, ok := strings.Cut(line, "\t")
		_, err := hex.DecodeString(sha)
		name := strings.TrimPrefix(ref, "refs/heads/")
		if !ok || err != nil || len(sha) != 40 && len(sha) != 64 || !strings.HasPrefix(ref, "refs/heads/") || seen[name] || !domain.ValidRepositoryBranch(name) {
			return nil, domain.Fail(domain.InvalidArgument, "Remote branch inventory is malformed.", "Use the saved or manual reference.")
		}
		seen[name] = true
		branches = append(branches, name)
		if len(branches) > domain.MaxRepositoryBranches {
			return nil, domain.Fail(domain.ResourceExhausted, "Remote branch inventory has too many branches.", "Use the saved or manual reference.")
		}
	}
	slices.Sort(branches)
	return branches, nil
}
