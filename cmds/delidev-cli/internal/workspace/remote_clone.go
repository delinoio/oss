// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

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
		if _, err := git.run(ctx, prepared.Path, "remote", "set-url", remote, spec.RemoteURL); err != nil {
			return *entry, nil, err
		}
	}
	inspection, err := git.Inspect(ctx, prepared.Path)
	if err != nil {
		return *entry, nil, err
	}
	if spec.PRTarget != nil {
		if err := git.preparePRObjects(ctx, inspection, spec); err != nil {
			return *entry, nil, err
		}
	}
	if entry.Starting.Type == "" {
		entry.Starting, err = DefaultStarting(inspection, remote)
		if err != nil {
			return *entry, nil, err
		}
	}
	entry.StartingCommit, err = git.Resolve(ctx, inspection, entry.Starting, spec.AutoFetch)
	if err != nil {
		return *entry, nil, err
	}
	if entry.Base.Type == "" {
		entry.Base = entry.Starting
	}
	entry.BaseCommit = entry.StartingCommit
	if entry.Base != entry.Starting {
		entry.BaseCommit, err = git.Resolve(ctx, inspection, entry.Base, spec.AutoFetch)
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
	} else if _, err := git.run(ctx, prepared.Path, "checkout", "--detach", "--no-recurse-submodules", entry.StartingCommit); err != nil {
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
