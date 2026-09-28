package workspace

import (
	"context"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func prWorkspaceChanged() error {
	return domain.Fail(domain.Conflict, "The prepared PR workspace has changed before execution.", "Preserve its files and reconcile the original PR head and local changes before starting the fix.")
}

// Preparation is historical evidence. A first PR execution rechecks the real
// working tree and remote operands under the existing session lock, before any
// execution claim or native harness is created. This read never fetches, resets
// files, selects a branch or proves permission to push.
func (m *Manager) preflightPreparedPRs(ctx context.Context, input PrepareRequest, manifest Manifest) error {
	for i, spec := range input.Repositories {
		if spec.PRTarget == nil {
			continue
		}
		prepared := manifest.Repositories[i]
		if !validPreparedPR(spec, prepared, input.Type) {
			return ResultUncertain()
		}
		m.Logger.InfoContext(ctx, "workspace_pr_preflight_started", "session_id", input.SessionID, "repository_id", spec.ID)
		git := m.Git
		git.OwnerID, git.readOnly = input.SessionID, true
		if err := git.preflightPR(ctx, prepared.Path, spec); err != nil {
			m.Logger.WarnContext(ctx, "workspace_pr_preflight_failed", "session_id", input.SessionID, "repository_id", spec.ID, "code", domain.SafeError(err).Code)
			return err
		}
		m.Logger.InfoContext(ctx, "workspace_pr_preflight_ready", "session_id", input.SessionID, "repository_id", spec.ID)
	}
	return nil
}

type prBranchSelection uint8

const (
	prDetachedOnly prBranchSelection = iota
	prHeadOrDetached
)

func (g Git) preflightPR(ctx context.Context, root string, spec RepositorySpec) error {
	return g.preflightPRWorkspace(ctx, root, spec, prDetachedOnly)
}

func (g Git) preflightPRWorkspace(ctx context.Context, root string, spec RepositorySpec, branchMode prBranchSelection) error {
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	target := *spec.PRTarget
	checkLocal := func() error {
		raw, err := g.run(bounded, root, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return err
		}
		if trimGit(raw) != target.HeadSHA {
			return prWorkspaceChanged()
		}
		branch, exit, err := g.runCommand(bounded, root, "symbolic-ref", "--quiet", "HEAD")
		if err == nil {
			if branchMode != prHeadOrDetached || trimGit(branch) != "refs/heads/"+target.HeadRef {
				return prWorkspaceChanged()
			}
		} else if exit != 1 {
			return err
		}
		// Include ignored/untracked data as well as the index and working tree.
		// An explicit fresh PR worktree may not silently adopt unrelated files.
		raw, err = g.run(bounded, root, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=matching", "--ignore-submodules=none")
		if err != nil {
			return err
		}
		if len(raw) != 0 {
			return prWorkspaceChanged()
		}
		return nil
	}
	if err := checkLocal(); err != nil {
		return err
	}
	inspection, err := g.Inspect(bounded, root)
	if err != nil {
		return err
	}
	if inspection.Root != root {
		return ResultUncertain()
	}
	transport, err := g.prTransport(bounded, inspection, spec.PreferredRemote, target)
	if err != nil {
		return err
	}
	if err := g.requirePRBranch(bounded, root, transport.address(target.Target.Owner, target.Target.Name), target.BaseRef, target.BaseSHA); err != nil {
		return err
	}
	if err := g.requirePRBranch(bounded, root, transport.address(target.HeadRepository.Owner, target.HeadRepository.Name), target.HeadRef, target.HeadSHA); err != nil {
		return err
	}
	current, err := g.prTransport(bounded, inspection, spec.PreferredRemote, target)
	if err != nil {
		return err
	}
	if current != transport {
		return prRemoteProblem()
	}
	return checkLocal()
}
