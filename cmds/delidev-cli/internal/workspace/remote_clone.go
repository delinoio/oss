// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
const managedCloneCredentialConfigPattern = `^(credential(\..*)?|core\.sshcommand|ssh\..*)$`

func (g Git) cloneCredentialConfiguration(ctx context.Context, root string) ([][2]string, error) {
	raw, exit, err := g.runCommand(ctx, root, "config", "--null", "--get-regexp", managedCloneCredentialConfigPattern)
	if err != nil {
		// git-config returns 1 when no key matches. An empty retained config is
		// valid; every other status is an execution failure.
		if exit == 1 {
			return nil, nil
		}
		return nil, err
	}
	entries := make([][2]string, 0, 8)
	for _, record := range strings.Split(string(raw), "\x00") {
		if record == "" {
			continue
		}
		key, value, ok := strings.Cut(record, "\n")
		if !ok || key == "" || len(key) > 4096 || len(value) > 16384 {
			return nil, ResultUncertain()
		}
		entries = append(entries, [2]string{key, value})
		if len(entries) > 256 {
			return nil, ResultUncertain()
		}
	}
	return entries, nil
}

func (g Git) cloneProfile(ctx context.Context, root string) (Git, error) {
	credentials, err := g.cloneCredentialConfiguration(ctx, root)
	if err != nil {
		return Git{}, err
	}
	g.cloneDiagnostics = true
	g.restrictedTransport = true
	g.Timeout = RepositoryCloneTimeout
	environment := slices.DeleteFunc(gitEnvironment(), func(value string) bool {
		key, _, _ := strings.Cut(value, "=")
		return strings.EqualFold(key, "GIT_CONFIG_GLOBAL") || strings.EqualFold(key, "GIT_CONFIG_SYSTEM") || strings.EqualFold(key, "GIT_CONFIG_NOSYSTEM") || strings.EqualFold(key, "GIT_CONFIG_COUNT") || strings.HasPrefix(strings.ToUpper(key), "GIT_CONFIG_KEY_") || strings.HasPrefix(strings.ToUpper(key), "GIT_CONFIG_VALUE_")
	})
	// URL insteadOf rules are deliberately excluded. The source URL is checked
	// before this profile is created, and the isolated config prevents a later
	// fetch from silently changing the pinned target. Credential helpers and SSH
	// settings are copied back through Git's config environment so native
	// credentials remain available without restoring rewrite authority.
	environment = append(environment, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull, "GIT_LFS_SKIP_SMUDGE=1", "GIT_CONFIG_COUNT="+strconv.Itoa(len(credentials)))
	for index, entry := range credentials {
		environment = append(environment, "GIT_CONFIG_KEY_"+strconv.Itoa(index)+"="+entry[0], "GIT_CONFIG_VALUE_"+strconv.Itoa(index)+"="+entry[1])
	}
	g.environment = environment
	g.HooksDir = os.DevNull
	if g.Logger != nil {
		g.Logger.DebugContext(ctx, "workspace_clone_config_sanitized", "credential_entries", len(credentials), "url_rewrites", "excluded")
	}
	return g, nil
}

func (g Git) validateManagedCloneSource(ctx context.Context, root, source string) error {
	expected, err := domain.RepositoryCloneSourceIdentity(source)
	if err != nil {
		return err
	}
	// Keep the same redacted timeout/authentication classification if a native
	// Git wrapper fails during the no-network URL probe. The probe itself still
	// uses no restricted clone profile and therefore observes the configured
	// effective URL before the first network-capable command.
	probe := g
	probe.cloneDiagnostics = true
	raw, err := probe.run(ctx, root, "ls-remote", "--get-url", "--", source)
	if err != nil {
		return err
	}
	effective := strings.TrimSpace(string(raw))
	if effective == "" || strings.ContainsAny(effective, "\r\n") {
		return ResultUncertain()
	}
	actual, err := domain.RepositoryCloneSourceIdentity(effective)
	if err != nil || actual != expected {
		return domain.Fail(domain.InvalidArgument, "The Worker Git configuration rewrites the repository source.", "Remove the conflicting Git URL rewrite and retry the repository operation.")
	}
	return nil
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
	startupProgress(ctx, domain.StartupWorkspaceClone, domain.StartupProgressRunning)
	m.Logger.InfoContext(ctx, "workspace_clone_started", "session_id", manifest.SessionID, "repository_id", spec.ID, "source_kind", spec.SourceKind)
	source, remote := spec.RemoteURL, spec.PreferredRemote
	if remote == "" {
		remote = "origin"
	}
	if spec.SourceKind == IndependentForkSource {
		source = spec.Checkout
	}
	// One deadline covers source validation, cloning, remote/ref preparation,
	// optional PR fetches, resolution and final checkout. Individual Git
	// commands keep their shorter launch timeout, but cannot extend this
	// repository-level budget by starting a fresh ten-minute window.
	bounded, cancel := context.WithTimeout(ctx, RepositoryCloneTimeout)
	defer cancel()
	if spec.SourceKind == RemoteCloneSource {
		if err := git.validateManagedCloneSource(bounded, root, source); err != nil {
			return *entry, nil, err
		}
	}
	clone, err := git.cloneProfile(bounded, root)
	if err != nil {
		return *entry, nil, err
	}
	_, err = clone.run(bounded, root, cloneArguments(source, prepared.Path, filepath.Join(m.Root, "empty-hooks"), remote, true, spec.SourceKind == IndependentForkSource)...)
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
	startupProgress(ctx, domain.StartupWorkspaceClone, domain.StartupProgressCompleted)
	if spec.SourceKind == IndependentForkSource {
		if _, err := clone.run(bounded, prepared.Path, "remote", "set-url", remote, spec.RemoteURL); err != nil {
			return *entry, nil, err
		}
	}
	if err := provisionManagedCloneRemotes(bounded, clone, prepared.Path, spec.RemoteURL, spec, remote); err != nil {
		return *entry, nil, err
	}
	if err := materializeManagedCloneBranches(bounded, clone, prepared.Path, spec, remote); err != nil {
		return *entry, nil, err
	}
	startupProgress(ctx, domain.StartupWorkspaceInspect, domain.StartupProgressRunning)
	inspection, err := clone.Inspect(bounded, prepared.Path)
	if err != nil {
		return *entry, nil, err
	}
	startupProgress(ctx, domain.StartupWorkspaceInspect, domain.StartupProgressCompleted)
	startupProgress(ctx, domain.StartupWorkspaceReference, domain.StartupProgressRunning)
	if spec.PRTarget != nil {
		if err := clone.preparePRObjects(bounded, inspection, spec); err != nil {
			return *entry, nil, err
		}
	}
	if entry.Starting.Type == "" {
		entry.Starting, err = DefaultStarting(inspection, remote)
		if err != nil {
			return *entry, nil, err
		}
	}
	entry.StartingCommit, err = clone.Resolve(bounded, inspection, entry.Starting, spec.AutoFetch)
	if err != nil {
		return *entry, nil, err
	}
	if entry.Base.Type == "" {
		entry.Base = entry.Starting
	}
	entry.BaseCommit = entry.StartingCommit
	if entry.Base != entry.Starting {
		entry.BaseCommit, err = clone.Resolve(bounded, inspection, entry.Base, spec.AutoFetch)
		if err != nil {
			return *entry, nil, err
		}
	}
	if err := write(); err != nil {
		return *entry, nil, ResultUncertain()
	}
	startupProgress(ctx, domain.StartupWorkspaceReference, domain.StartupProgressCompleted)
	startupProgress(ctx, domain.StartupWorkspaceCheckout, domain.StartupProgressRunning)
	if spec.SourceKind == IndependentForkSource {
		if _, err := clone.run(bounded, prepared.Path, "update-ref", "--no-deref", "HEAD", entry.StartingCommit); err != nil {
			return *entry, nil, err
		}
	} else if _, err := clone.run(bounded, prepared.Path, "checkout", "--detach", "--no-recurse-submodules", entry.StartingCommit); err != nil {
		return *entry, nil, err
	}
	var copy *forkCopy
	if spec.SourceKind == IndependentForkSource {
		copied, err := copyForkRepository(bounded, clone, spec.Checkout, prepared.Path, entry.StartingCommit)
		if err != nil {
			return *entry, nil, err
		}
		copy = &copied
	}
	if err := verifyIndependentDirectory(*entry, true); err != nil {
		return *entry, nil, err
	}
	startupProgress(ctx, domain.StartupWorkspaceCheckout, domain.StartupProgressCompleted)
	m.Logger.InfoContext(ctx, "workspace_clone_ready", "session_id", manifest.SessionID, "repository_id", spec.ID)
	return *entry, copy, nil
}
