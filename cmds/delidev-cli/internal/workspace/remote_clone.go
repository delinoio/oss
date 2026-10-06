// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// Independent forks of new remote or Local sources need their own Git store.
// Sidechat only references the parent and never acquires clone authority.
func ForkRequiresManagedClone(input domain.ForkJobInput) (bool, error) {
	if input.Workspace != domain.Worktree || input.Purpose == domain.SidechatFork {
		return false, nil
	}
	var source Manifest
	if err := domain.Decode(input.SourceAssignment.Manifest, &source); err != nil {
		return false, err
	}
	for _, repository := range source.Repositories {
		switch repository.SourceKind {
		case RemoteCloneSource, IndependentForkSource, LocalCheckoutSource:
			return true, nil
		case CheckoutSource:
		default:
			return false, ResultUncertain()
		}
	}
	return false, nil
}

// The two clone flows share transport, credentials and deadline policy. Their
// publication and lifetime authority stays separate: a managed clone never
// transfers files to the user and a published Local clone never belongs here.
func (g Git) cloneProfile() Git {
	g.cloneDiagnostics = true
	g.restrictedTransport = true
	g.Timeout = RepositoryCloneTimeout
	g.environment = append(gitEnvironment(), "GIT_LFS_SKIP_SMUDGE=1")
	g.HooksDir = os.DevNull
	return g
}
func cloneArguments(source, destination, template, remote string, noCheckout, independentFork bool) []string {
	args := []string{"-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "protocol.ssh.allow=always", "-c", "http.followRedirects=false", "-c", "core.fsmonitor=false", "-c", "submodule.recurse=false", "-c", "fetch.recurseSubmodules=false"}
	if independentFork {
		args = append(args, "-c", "protocol.file.allow=always")
	}
	args = append(args, "clone", "--origin="+remote, "--no-recurse-submodules", "--template="+template)
	if noCheckout {
		args = append(args, "--no-checkout")
	}
	if independentFork {
		args = append(args, "--no-local", "--no-hardlinks")
	}
	return append(args, "--", source, destination)
}

func managedCloneRemotes(spec RepositorySpec, primary string) ([]string, error) {
	if err := (domain.Reference{Type: domain.RemoteBranch, Remote: primary, Name: "branch"}).Validate(false); err != nil {
		return nil, err
	}
	remotes := []string{primary}
	seen := map[string]bool{primary: true}
	add := func(remote string) error {
		if remote == "" || seen[remote] {
			return nil
		}
		if err := (domain.Reference{Type: domain.RemoteBranch, Remote: remote, Name: "branch"}).Validate(false); err != nil {
			return err
		}
		seen[remote] = true
		remotes = append(remotes, remote)
		return nil
	}
	if err := add(spec.PreferredRemote); err != nil {
		return nil, err
	}
	for _, ref := range []domain.Reference{spec.Base, spec.Starting} {
		if ref.Type == domain.RemoteBranch {
			if err := add(ref.Remote); err != nil {
				return nil, err
			}
		}
	}
	return remotes, nil
}

// A repository stores one credential-free URL. Configure every referenced
// remote name against that pinned URL so accepted multi-remote references are
// visible to inspection and later resolution. Fetches still use cloneProfile,
// including the automatic-fetch policy selected by the repository.
func provisionManagedCloneRemotes(ctx context.Context, git Git, path, url string, spec RepositorySpec, primary string) error {
	remotes, err := managedCloneRemotes(spec, primary)
	if err != nil {
		return err
	}
	for _, remote := range remotes[1:] {
		if _, err := git.run(ctx, path, "remote", "add", remote, url); err != nil {
			return err
		}
	}
	if len(remotes) == 1 {
		return nil
	}
	// The pinned URL is the only source URL available to this managed clone.
	// Mirror the refs already obtained by the initial clone into each alias so
	// auto_fetch=false remains meaningful without a stale or missing-reference
	// fallback. Later fetches still target the alias through cloneProfile.
	raw, err := git.run(ctx, path, "for-each-ref", "--format=%(refname)%00%(objectname)", "refs/remotes/"+primary+"/")
	if err != nil {
		return err
	}
	primaryPrefix := "refs/remotes/" + primary + "/"
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if line == "" {
			continue
		}
		ref, object, ok := strings.Cut(line, "\x00")
		if !ok || !strings.HasPrefix(ref, primaryPrefix) || (len(object) != 40 && len(object) != 64) {
			return ResultUncertain()
		}
		if _, err := hex.DecodeString(object); err != nil {
			return ResultUncertain()
		}
		for _, remote := range remotes[1:] {
			alias := "refs/remotes/" + remote + "/" + strings.TrimPrefix(ref, primaryPrefix)
			if _, err := git.run(ctx, path, "update-ref", "--no-deref", alias, object); err != nil {
				return err
			}
		}
	}
	if symbolic, err := git.run(ctx, path, "symbolic-ref", "--quiet", "refs/remotes/"+primary+"/HEAD"); err == nil && strings.HasPrefix(string(symbolic), primaryPrefix) {
		for _, remote := range remotes[1:] {
			alias := "refs/remotes/" + remote + "/" + strings.TrimPrefix(strings.TrimSpace(string(symbolic)), primaryPrefix)
			if _, err := git.run(ctx, path, "symbolic-ref", "refs/remotes/"+remote+"/HEAD", alias); err != nil {
				return err
			}
		}
	}
	return nil
}

func materializeManagedCloneBranches(ctx context.Context, git Git, path string, spec RepositorySpec, primary string) error {
	seen := map[string]bool{}
	for _, ref := range []domain.Reference{spec.Base, spec.Starting} {
		if ref.Type != domain.LocalBranch || seen[ref.Name] {
			continue
		}
		seen[ref.Name] = true
		if err := ref.Validate(false); err != nil {
			return err
		}
		local := "refs/heads/" + ref.Name
		if _, exit, err := git.runCommand(ctx, path, "rev-parse", "--verify", "--end-of-options", local+"^{commit}"); err == nil {
			continue
		} else if exit != 128 {
			return err
		}
		remote := "refs/remotes/" + primary + "/" + ref.Name
		commit, err := git.run(ctx, path, "rev-parse", "--verify", "--end-of-options", remote+"^{commit}")
		if err != nil {
			return err
		}
		if _, err := git.run(ctx, path, "update-ref", "--no-deref", local, strings.TrimSpace(string(commit))); err != nil {
			return err
		}
	}
	return nil
}

func slicesContainManagedClone(repos []RepositorySpec) bool {
	for _, repo := range repos {
		if repo.SourceKind.managed() {
			return true
		}
	}
	return false
}
func directoryIdentityDigest(path string) (string, error) {
	identity, err := directoryPathIdentity(path)
	if err != nil {
		return "", ResultUncertain()
	}
	digest := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(digest[:]), nil
}
func independentDirectoryDigest(repo PreparedRepository) (string, error) {
	root, err := directoryIdentityDigest(repo.Path)
	if err != nil {
		return "", err
	}
	admin, err := directoryIdentityDigest(filepath.Join(repo.Path, ".git"))
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(string(repo.ID) + "\x00" + root + "\x00" + admin))
	return hex.EncodeToString(digest[:]), nil
}
func verifyIndependentDirectory(repo PreparedRepository, ready bool) error {
	if repo.SourceKind != RemoteCloneSource && repo.SourceKind != IndependentForkSource || repo.Source != repo.Path || !digestValid(repo.CloneRootDigest) {
		return ResultUncertain()
	}
	current, err := directoryIdentityDigest(repo.Path)
	if err != nil || current != repo.CloneRootDigest {
		return ResultUncertain()
	}
	if ready || repo.CloneIdentityDigest != "" {
		current, err = independentDirectoryDigest(repo)
		if err != nil || !digestValid(repo.CloneIdentityDigest) || current != repo.CloneIdentityDigest {
			return ResultUncertain()
		}
	}
	return nil
}

func (m *Manager) prepareIndependentRepository(ctx context.Context, git Git, root string, spec RepositorySpec, manifest *Manifest, write func() error) (PreparedRepository, *forkCopy, error) {
	prepared := PreparedRepository{ID: spec.ID, SourceKind: spec.SourceKind, RemoteURL: spec.RemoteURL, Path: filepath.Join(root, string(spec.ID)), Owned: true, Base: spec.Base, Starting: spec.Starting, PRTarget: spec.PRTarget}
	prepared.Source = prepared.Path
	manifest.Repositories = append(manifest.Repositories, prepared)
	entry := &manifest.Repositories[len(manifest.Repositories)-1]
	// Persist the intended namespace before creation. An interruption before its
	// native commitment is synchronized remains uncertain; absence is no proof.
	if err := write(); err != nil {
		return prepared, nil, ResultUncertain()
	}
	if err := security.CreatePrivateDirExclusive(prepared.Path); err != nil {
		return prepared, nil, ResultUncertain()
	}
	var err error
	entry.CloneRootDigest, err = directoryIdentityDigest(prepared.Path)
	if err != nil || security.SyncParent(prepared.Path) != nil || write() != nil {
		return prepared, nil, ResultUncertain()
	}
	m.Logger.InfoContext(ctx, "workspace_clone_started", "session_id", manifest.SessionID, "repository_id", spec.ID, "source_kind", spec.SourceKind)
	source, remote := spec.RemoteURL, spec.PreferredRemote
	if remote == "" {
		remote = "origin"
	}
	if spec.SourceKind == IndependentForkSource {
		source = spec.Checkout
	}
	clone := git.cloneProfile()
	bounded, cancel := context.WithTimeout(ctx, RepositoryCloneTimeout)
	_, err = clone.run(bounded, root, cloneArguments(source, prepared.Path, filepath.Join(m.Root, "empty-hooks"), remote, true, spec.SourceKind == IndependentForkSource)...)
	cancel()
	if err != nil {
		return *entry, nil, err
	}
	if err := verifyIndependentDirectory(*entry, false); err != nil {
		return *entry, nil, err
	}
	entry.CloneIdentityDigest, err = independentDirectoryDigest(*entry)
	if err != nil || write() != nil {
		return *entry, nil, ResultUncertain()
	}
	if spec.SourceKind == IndependentForkSource {
		if _, err := clone.run(ctx, prepared.Path, "remote", "set-url", remote, spec.RemoteURL); err != nil {
			return *entry, nil, err
		}
	}
	if err := provisionManagedCloneRemotes(ctx, clone, prepared.Path, spec.RemoteURL, spec, remote); err != nil {
		return *entry, nil, err
	}
	if err := materializeManagedCloneBranches(ctx, clone, prepared.Path, spec, remote); err != nil {
		return *entry, nil, err
	}
	inspection, err := clone.Inspect(ctx, prepared.Path)
	if err != nil {
		return *entry, nil, err
	}
	if spec.PRTarget != nil {
		if err := clone.preparePRObjects(ctx, inspection, spec); err != nil {
			return *entry, nil, err
		}
	}
	if entry.Starting.Type == "" {
		entry.Starting, err = DefaultStarting(inspection, remote)
		if err != nil {
			return *entry, nil, err
		}
	}
	entry.StartingCommit, err = clone.Resolve(ctx, inspection, entry.Starting, spec.AutoFetch)
	if err != nil {
		return *entry, nil, err
	}
	if entry.Base.Type == "" {
		entry.Base = entry.Starting
	}
	entry.BaseCommit = entry.StartingCommit
	if entry.Base != entry.Starting {
		entry.BaseCommit, err = clone.Resolve(ctx, inspection, entry.Base, spec.AutoFetch)
		if err != nil {
			return *entry, nil, err
		}
	}
	if err := write(); err != nil {
		return *entry, nil, ResultUncertain()
	}
	if spec.SourceKind == IndependentForkSource {
		if _, err := git.run(ctx, prepared.Path, "update-ref", "--no-deref", "HEAD", entry.StartingCommit); err != nil {
			return *entry, nil, err
		}
	} else if _, err := clone.run(ctx, prepared.Path, "checkout", "--detach", "--no-recurse-submodules", entry.StartingCommit); err != nil {
		return *entry, nil, err
	}
	var copy *forkCopy
	if spec.SourceKind == IndependentForkSource {
		copied, err := copyForkRepository(ctx, git, spec.Checkout, prepared.Path, entry.StartingCommit)
		if err != nil {
			return *entry, nil, err
		}
		copy = &copied
	}
	if err := verifyIndependentDirectory(*entry, true); err != nil {
		return *entry, nil, err
	}
	m.Logger.InfoContext(ctx, "workspace_clone_ready", "session_id", manifest.SessionID, "repository_id", spec.ID)
	return *entry, copy, nil
}
