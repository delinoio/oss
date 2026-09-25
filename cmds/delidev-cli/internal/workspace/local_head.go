package workspace

import (
	"context"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// localHEAD never chooses a branch or manufactures an initial commit. A failed
// revision lookup alone cannot establish an unborn branch: symbolic-ref must
// resolve a valid branch, and exact show-ref verification must report absence.
// Re-reading the symbolic ref also rejects malformed references and a branch
// change during the absence check. Git remains responsible for its ref backend.
func (g Git) localHEAD(ctx context.Context, root string) (LocalHEADState, string, error) {
	raw, exit, err := g.runCommand(ctx, root, "rev-parse", "--verify", "HEAD^{commit}")
	if err == nil {
		if !canonicalCommit(trimGit(raw)) {
			return "", "", ResultUncertain()
		}
		return LocalHEADCommitted, trimGit(raw), nil
	}
	if exit != 128 {
		return "", "", err
	}
	branch, err := g.run(ctx, root, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		return "", "", err
	}
	ref := trimGit(branch)
	if !strings.HasPrefix(ref, "refs/heads/") {
		return "", "", ResultUncertain()
	}
	if _, err := g.run(ctx, root, "check-ref-format", ref); err != nil {
		return "", "", err
	}
	_, exit, err = g.runCommand(ctx, root, "show-ref", "--verify", "--quiet", "--", ref)
	if exit != 1 {
		if err != nil {
			return "", "", err
		}
		return "", "", domain.Fail(domain.RecoveryRequired, "The Local checkout changed during HEAD inspection.", "Retry after the repository's current Git operation finishes.")
	}
	confirmed, err := g.run(ctx, root, "symbolic-ref", "--quiet", "HEAD")
	if err != nil || trimGit(confirmed) != ref {
		return "", "", ResultUncertain()
	}
	return LocalHEADUnborn, "", nil
}
