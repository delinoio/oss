package workspace

import (
	"context"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func validatePRPreparation(spec RepositorySpec, kind domain.WorkspaceType) error {
	target := spec.PRTarget
	if target == nil {
		return nil
	}
	if kind != domain.Worktree || target.Validate() != nil || target.Target.RepositoryID != spec.ID || !spec.AutoFetch ||
		spec.Base != (domain.Reference{Type: domain.CommitReference, Name: target.BaseSHA}) ||
		spec.Starting != (domain.Reference{Type: domain.CommitReference, Name: target.HeadSHA}) {
		return domain.Fail(domain.InvalidArgument, "The PR workspace does not match its original repository and commits.", "Prepare a separate Worktree with the exact current PR target and explicit fetch; never replace a Local checkout.")
	}
	return nil
}

func samePRTarget(a, b *domain.PRGitTarget) bool {
	if a == nil || b == nil {
		return a == b
	}
	left, right := *a, *b
	if !left.Target.ObservedAt.Equal(right.Target.ObservedAt) {
		return false
	}
	right.Target.ObservedAt = left.Target.ObservedAt
	return left == right
}

func validPreparedPR(spec RepositorySpec, prepared PreparedRepository, kind domain.WorkspaceType) bool {
	if validatePRPreparation(spec, kind) != nil || !samePRTarget(spec.PRTarget, prepared.PRTarget) {
		return false
	}
	return spec.PRTarget == nil || (prepared.Base == spec.Base && prepared.Starting == spec.Starting && prepared.BaseCommit == spec.PRTarget.BaseSHA && prepared.StartingCommit == spec.PRTarget.HeadSHA)
}

type prGitTransport string

const (
	prGitHTTPS prGitTransport = "https"
	prGitSSH   prGitTransport = "ssh"
	prGitSCP   prGitTransport = "scp"
)

func (t prGitTransport) address(owner, name string) string {
	switch t {
	case prGitHTTPS:
		return "https://github.com/" + owner + "/" + name + ".git"
	case prGitSSH:
		return "ssh://git@github.com/" + owner + "/" + name + ".git"
	case prGitSCP:
		return "git@github.com:" + owner + "/" + name + ".git"
	default:
		return ""
	}
}

func prRemoteProblem() error {
	return domain.Fail(domain.MissingInput, "The selected Worker's Git remote does not identify the original GitHub repository.", "Configure one canonical GitHub HTTPS or SSH remote and its native Git authentication on this Worker, then refresh PR preparation. Server PATs are not used.")
}

func (g Git) prTransport(ctx context.Context, inspection Inspection, preferred string, target domain.PRGitTarget) (prGitTransport, error) {
	remote := preferred
	if remote == "" {
		if slices.Contains(inspection.Remotes, "origin") {
			remote = "origin"
		} else if len(inspection.Remotes) == 1 {
			remote = inspection.Remotes[0]
		}
	}
	if remote == "" || !slices.Contains(inspection.Remotes, remote) {
		return "", prRemoteProblem()
	}
	raw, err := g.run(ctx, inspection.Root, "remote", "get-url", "--all", "--", remote)
	if err != nil {
		return "", err
	}
	address := trimGit(raw)
	for _, transport := range []prGitTransport{prGitHTTPS, prGitSSH, prGitSCP} {
		canonical := transport.address(target.Target.Owner, target.Target.Name)
		if strings.EqualFold(address, canonical) || strings.EqualFold(address, strings.TrimSuffix(canonical, ".git")) {
			return transport, nil
		}
	}
	return "", prRemoteProblem()
}

// The URL is constructed from verified GitHub identities, then checked after
// native insteadOf expansion. Neither remote URLs nor Git stderr are published.
func (g Git) validatePRAddress(ctx context.Context, root, address string) error {
	raw, err := g.run(ctx, root, "ls-remote", "--get-url", "--", address)
	if err != nil {
		return err
	}
	if trimGit(raw) != address {
		return prRemoteProblem()
	}
	return nil
}

func (g Git) requirePRBranch(ctx context.Context, root, address, branch, expected string) error {
	if err := g.validatePRAddress(ctx, root, address); err != nil {
		return err
	}
	raw, err := g.run(ctx, root, "-c", "http.followRedirects=false", "ls-remote", "--exit-code", "--refs", "--", address, "refs/heads/"+branch)
	if err != nil {
		return err
	}
	if trimGit(raw) != expected+"\trefs/heads/"+branch {
		return domain.Fail(domain.Conflict, "The PR's original remote branch changed during preparation.", "Refresh the PR and reconcile its current head and base before preparing another workspace.")
	}
	return nil
}

func (g Git) preparePRObjects(ctx context.Context, inspection Inspection, spec RepositorySpec) error {
	target := *spec.PRTarget
	transport, err := g.prTransport(ctx, inspection, spec.PreferredRemote, target)
	if err != nil {
		return err
	}
	operands := []struct{ address, branch, commit string }{
		{transport.address(target.Target.Owner, target.Target.Name), target.BaseRef, target.BaseSHA},
		{transport.address(target.HeadRepository.Owner, target.HeadRepository.Name), target.HeadRef, target.HeadSHA},
	}
	for _, operand := range operands {
		if _, exit, err := g.runCommand(ctx, inspection.Root, "check-ref-format", "refs/heads/"+operand.branch); err != nil {
			if exit != 1 {
				return err
			}
			return domain.Fail(domain.InvalidArgument, "The PR has an invalid native Git branch reference.", "Refresh the original PR; branch names cannot be interpreted as Git arguments or revision expressions.")
		}
	}
	for _, operand := range operands {
		if err := g.requirePRBranch(ctx, inspection.Root, operand.address, operand.branch, operand.commit); err != nil {
			return err
		}
	}
	for _, operand := range operands {
		// Fetch objects without a destination ref. Suppress configured tracking,
		// pruning, tag following, FETCH_HEAD and maintenance side effects so the
		// original checkout and other sessions keep their own reference state.
		if _, err := g.run(ctx, inspection.Root, "-c", "http.followRedirects=false", "fetch", "--no-tags", "--no-prune", "--no-prune-tags", "--no-recurse-submodules", "--no-write-fetch-head", "--no-auto-maintenance", "--no-write-commit-graph", "--refmap=", "--", operand.address, "refs/heads/"+operand.branch); err != nil {
			return err
		}
		resolved, err := g.Resolve(ctx, inspection, domain.Reference{Type: domain.CommitReference, Name: operand.commit}, false)
		if err != nil {
			return err
		}
		if resolved != operand.commit {
			return ResultUncertain()
		}
	}
	current, err := g.prTransport(ctx, inspection, spec.PreferredRemote, target)
	if err != nil {
		return err
	}
	if current != transport {
		return prRemoteProblem()
	}
	for _, operand := range operands {
		if err := g.requirePRBranch(ctx, inspection.Root, operand.address, operand.branch, operand.commit); err != nil {
			return err
		}
	}
	return nil
}
