// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const maxForkBytes = 256 << 20
const maxForkEntries = 100000
const maxOpenCodeForkEntries = 8192

type ForkProfile string

const OpenCodeGeneralChatForkV1 ForkProfile = "opencode-general-chat-v1"

func (r PrepareRequest) forkEntryLimit() int {
	if r.ForkProfile == OpenCodeGeneralChatForkV1 {
		return maxOpenCodeForkEntries
	}
	return maxForkEntries
}

// ForkSnapshot is private read-only evidence held across workspace preparation
// and the native fork. A filesystem edit during either operation invalidates it.
type ForkSnapshot struct {
	copies        []forkCopy
	git           Git
	requestDigest string
}

func (m *Manager) InspectForkSnapshot(ctx context.Context, source Manifest, request PrepareRequest) (*ForkSnapshot, error) {
	if request.validateStructure() != nil || source.SessionID != request.ForkSourceID || source.State != Ready || m.initialize() != nil {
		return nil, ResultUncertain()
	}
	git := m.Git
	git.OwnerID, git.readOnly = request.SessionID, true
	snapshot := &ForkSnapshot{git: git, requestDigest: preparationDigest(request)}
	paths := []string{source.PrimaryPath}
	if source.Type != domain.GeneralChat {
		paths = nil
		for _, repo := range source.Repositories {
			paths = append(paths, repo.Path)
		}
		if len(paths) != len(request.Repositories) {
			return nil, ResultUncertain()
		}
	}
	for i, path := range paths {
		marker, err := captureForkRootMarker(path)
		if err != nil {
			return nil, err
		}
		digest, err := scanForkTreePinned(ctx, path, "", source.Type != domain.GeneralChat, request.forkEntryLimit(), &marker)
		if err != nil {
			return nil, err
		}
		copy := forkCopy{sourceMarker: &marker, source: path, tree: digest, git: source.Type != domain.GeneralChat, entryLimit: request.forkEntryLimit()}
		if copy.git {
			head, err := git.run(ctx, path, "rev-parse", "--verify", "HEAD")
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(string(head)) != request.Repositories[i].Base.Name {
				return nil, forkSnapshotChanged()
			}
			_, index, err := forkIndex(ctx, git, path)
			if err != nil {
				return nil, err
			}
			hash := sha256.Sum256(index)
			copy.head, copy.index = request.Repositories[i].Base.Name, hex.EncodeToString(hash[:])
		}
		if err := copy.verifySourceMarker(); err != nil {
			return nil, err
		}
		snapshot.copies = append(snapshot.copies, copy)
	}
	return snapshot, nil
}

// PrepareFork verifies the original pre-copy observation before publishing a
// ready workspace. At this point no native fork has been sent, so a detected
// mismatch can use ordinary owned preparation rollback. Git process/cleanup
// uncertainty keeps its RecoveryRequired classification and retained journal.
func (m *Manager) PrepareFork(ctx context.Context, request PrepareRequest, snapshot *ForkSnapshot) (Manifest, error) {
	if snapshot == nil || request.ForkSourceID == "" || snapshot.requestDigest != preparationDigest(request) {
		return Manifest{}, ResultUncertain()
	}
	for _, copy := range snapshot.copies {
		if err := copy.verifySourceMarker(); err != nil {
			return Manifest{}, err
		}
	}
	return m.prepare(ctx, request, snapshot)
}

func forkSnapshotChanged() error {
	return domain.Fail(domain.Conflict, "The fork workspace changed during snapshot copying.", "Wait for source edits to finish, then request a new fork after verified cleanup.")
}

func (s *ForkSnapshot) sourceMarker(path string) *forkGitMarker {
	if s != nil {
		for _, copy := range s.copies {
			if copy.source == path {
				return copy.sourceMarker
			}
		}
	}
	return nil
}

func (s *ForkSnapshot) Verify(ctx context.Context, child Manifest) error {
	if s == nil || child.State != Ready {
		return ResultUncertain()
	}
	for i, copy := range s.copies {
		copy.target = child.PrimaryPath
		if copy.git {
			if len(child.Repositories) != len(s.copies) {
				return ResultUncertain()
			}
			copy.target = child.Repositories[i].Path
		}
		if err := copy.verify(ctx, s.git); err != nil {
			return err
		}
	}
	return nil
}

type forkReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r forkReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type forkCopy struct {
	sourceMarker   *forkGitMarker
	source, target string
	tree           string
	git            bool
	head, index    string
	entryLimit     int
}

func forkUnsupported() error {
	return domain.Fail(domain.Unsupported, "This workspace cannot be copied consistently by the fork profile.", "Use bounded regular files and directories without links, nested repositories or special files; preserve the source before retrying.")
}

// Local sharing must outlive deletion of the source session. Managed source
// worktrees belong to that source's cleanup plan and cannot be borrowed by a
// child without a separately implemented shared-ownership lifetime.
func ValidateLocalForkSource(source Manifest) error {
	if source.Type != domain.Local {
		return localForkUnsupported()
	}
	for _, repo := range source.Repositories {
		if repo.Owned {
			return localForkUnsupported()
		}
	}
	return nil
}

func localForkUnsupported() error {
	return domain.Fail(domain.Unsupported, "Local fork sharing requires user-owned Local checkouts.", "Use an independent Worktree fork for a managed source workspace.")
}

// ForkPreparation derives paths only from the exact original manifest while
// the caller holds InspectClosedExecution. It never selects configured defaults
// or fetches. Explicit Local shares the current paths and owns no source files.
func (m *Manager) ForkPreparation(ctx context.Context, source Manifest, child domain.ID, kind domain.WorkspaceType) (PrepareRequest, error) {
	request := PrepareRequest{SessionID: child, MachineID: source.MachineID, Type: kind, ForkSourceID: source.SessionID, Repositories: []RepositorySpec{}}
	if child.Validate() != nil || child == source.SessionID || source.State != Ready || m.initialize() != nil {
		return request, ResultUncertain()
	}
	if source.Type == domain.GeneralChat {
		if kind != domain.GeneralChat {
			return request, forkUnsupported()
		}
		request.ForkSourcePath = source.PrimaryPath
		return request, nil
	}
	if kind != domain.Worktree && kind != domain.Local {
		return request, forkUnsupported()
	}
	if kind == domain.Local {
		if err := ValidateLocalForkSource(source); err != nil {
			return request, err
		}
		request.OriginMachineID = source.MachineID
	}
	// Reject all manifest-level problems before creating a child process index.
	// An unpublished child has no deletion inventory to retire that index.
	for _, repo := range source.Repositories {
		if repo.LocalHEAD == LocalHEADUnborn {
			return request, forkUnsupported()
		}
		spec := RepositorySpec{ID: repo.ID, Checkout: repo.Path}
		if kind == domain.Local && repo.SourceKind == LocalCheckoutSource {
			spec.SourceKind, spec.RemoteURL = LocalCheckoutSource, repo.RemoteURL
		}
		if kind == domain.Worktree {
			if repo.SourceKind.managed() || repo.SourceKind == LocalCheckoutSource {
				spec.SourceKind, spec.RemoteURL = IndependentForkSource, repo.RemoteURL
			} else {
				spec.ForkRegistrationSource = repo.Source
			}
		}
		request.Repositories = append(request.Repositories, spec)
		if repo.Path == source.PrimaryPath {
			request.PrimaryRepository = repo.ID
		}
	}
	if err := request.validate(); err != nil {
		return request, err
	}
	// General Chat has no preparation Git reads and needs no child process owner.
	if len(source.Repositories) == 0 {
		return request, nil
	}
	git := m.Git
	git.OwnerID, git.readOnly = child, true
	if err := security.PrivateDir(git.ProcessRoot); err != nil {
		return request, ResultUncertain()
	}
	ownerPath := filepath.Join(git.ProcessRoot, string(child))
	// A fresh unpublished child cannot adopt a foreign or retained owner index.
	if err := os.Mkdir(ownerPath, 0700); err != nil {
		return request, ResultUncertain()
	}
	originalIndex, err := os.Lstat(ownerPath)
	request.forkProcessIdentity = originalIndex
	if err != nil {
		return request, ResultUncertain()
	}
	if err := security.SyncParent(ownerPath); err != nil {
		return request, ResultUncertain()
	}
	for i, repo := range source.Repositories {
		head, err := git.run(ctx, repo.Path, "rev-parse", "--verify", "HEAD")
		commit := strings.TrimSpace(string(head))
		if err != nil || !canonicalCommit(commit) {
			return request, ResultUncertain()
		}
		request.Repositories[i].Base = domain.Reference{Type: domain.CommitReference, Name: commit}
		if kind == domain.Worktree {
			request.Repositories[i].Starting = request.Repositories[i].Base
		}
	}
	return request, nil
}

// ForkProcessIdentity returns only the transient identity captured before HEAD
// reads. Decoded or legacy preparation metadata cannot recreate this proof.
func (r PrepareRequest) ForkProcessIdentity() os.FileInfo { return r.forkProcessIdentity }

// scanForkTree uses an opened root for every access. Links and special files are
// outside the initial copy profile, rather than followed or silently omitted.
// Before/after identities and complete content digests reject changed sources.
func scanForkTree(ctx context.Context, source, target string, gitTree bool) (string, error) {
	return scanForkTreeBounded(ctx, source, target, gitTree, maxForkEntries)
}
func scanForkTreeBounded(ctx context.Context, source, target string, gitTree bool, entryLimit int) (string, error) {
	return scanForkTreePinned(ctx, source, target, gitTree, entryLimit, nil)
}
func scanForkTreePinned(ctx context.Context, source, target string, gitTree bool, entryLimit int, original *forkGitMarker) (string, error) {
	if entryLimit != maxForkEntries && entryLimit != maxOpenCodeForkEntries {
		return "", forkUnsupported()
	}
	canonical, err := filepath.EvalSymlinks(source)
	if err != nil || canonical != source || !filepath.IsAbs(source) {
		return "", forkUnsupported()
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		return "", forkUnsupported()
	}
	defer root.Close()
	if original != nil {
		if err := original.verifyIdentity(root, "."); err != nil {
			return "", err
		}
	}
	var outputRoot *os.Root
	if target != "" {
		canonical, err := filepath.EvalSymlinks(target)
		if err != nil || canonical != target || !filepath.IsAbs(target) {
			return "", forkUnsupported()
		}
		outputRoot, err = os.OpenRoot(target)
		if err != nil {
			return "", forkUnsupported()
		}
		defer outputRoot.Close()
	}
	hash := sha256.New()
	var size int64
	entries := 0
	var visit func(string) error
	visit = func(name string) error {
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		entries++
		if entries > entryLimit {
			return domain.Fail(domain.ResourceExhausted, "The fork workspace exceeds its entry bound.", "Reduce the source workspace before forking.")
		}
		before, err := root.Lstat(name)
		if err != nil {
			return forkUnsupported()
		}
		if !before.IsDir() && !before.Mode().IsRegular() {
			return forkUnsupported()
		}
		if before.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return forkUnsupported()
		}
		metadata, _ := json.Marshal(struct {
			Name string
			Mode uint32
			Size int64
		}{name, uint32(before.Mode().Perm()), forkFileSize(before)})
		hash.Write(metadata)
		opened, err := root.OpenFile(name, os.O_RDONLY|forkNonblockFlag(), 0)
		if err != nil {
			return forkUnsupported()
		}
		defer opened.Close()
		actual, err := opened.Stat()
		if err != nil || !os.SameFile(before, actual) || before.Mode() != actual.Mode() {
			return forkSnapshotChanged()
		}
		if before.IsDir() {
			if target != "" && name != "." {
				if err := outputRoot.Mkdir(name, before.Mode().Perm()|0700); err != nil {
					return domain.SafeError(err)
				}
			}
			children, err := opened.ReadDir(entryLimit + 1)
			if err == io.EOF {
				err = nil
			}
			if err != nil {
				return forkUnsupported()
			}
			if len(children) > entryLimit {
				return domain.Fail(domain.ResourceExhausted, "The fork workspace exceeds its entry bound.", "Reduce the source workspace before forking.")
			}
			// File.ReadDir returns native order; sort to make the complete digest
			// independent of directory insertion and enumeration order.
			sortForkEntries(children)
			marker, err := inspectForkGitMarker(root, name)
			if err == nil && name == "." && original != nil {
				err = original.verifyIdentity(root, name)
			}
			if err != nil {
				return err
			}
			for _, child := range children {
				administration, err := marker.matches(root, name, child.Name())
				if err != nil {
					return err
				}
				if administration {
					if name == "." && gitTree {
						continue
					}
					return forkUnsupported()
				}
				if err := visit(filepath.Join(name, child.Name())); err != nil {
					return err
				}
				if administration, err := marker.matches(root, name, child.Name()); err != nil {
					return err
				} else if administration {
					return forkSnapshotChanged()
				}
			}
			if err := marker.verify(root, name); err != nil {
				return err
			}
			if name == "." && original != nil {
				if err := original.verifyIdentity(root, name); err != nil {
					return err
				}
			}
			if target != "" {
				if err := outputRoot.Chmod(name, before.Mode().Perm()); err != nil {
					return domain.SafeError(err)
				}
				// File Sync alone cannot publish new directory entries durably.
				// Sync each copied directory bottom-up before the ready manifest.
				if err := security.SyncParent(filepath.Join(target, name, ".fork-directory-sync")); err != nil {
					return domain.SafeError(err)
				}
			}
		} else {
			if before.Size() < 0 || before.Size() > maxForkBytes-size {
				return domain.Fail(domain.ResourceExhausted, "The fork workspace exceeds its byte bound.", "Reduce the source workspace before forking.")
			}
			size += before.Size()
			var output *os.File
			writer := io.Writer(hash)
			if target != "" {
				output, err = outputRoot.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, before.Mode().Perm())
				if err != nil {
					return domain.SafeError(err)
				}
				defer output.Close()
				writer = io.MultiWriter(hash, output)
			}
			count, err := io.Copy(writer, forkReader{ctx, io.LimitReader(opened, before.Size()+1)})
			if err != nil {
				return domain.SafeError(err)
			}
			if count != before.Size() {
				return forkSnapshotChanged()
			}
			if output != nil {
				// Creation obeys the process umask; restore the original regular
				// mode explicitly before publishing or comparing the copied tree.
				if err := output.Chmod(before.Mode().Perm()); err != nil {
					return domain.SafeError(err)
				}
				if err := output.Sync(); err != nil {
					return domain.SafeError(err)
				}
			}
		}
		after, err := root.Lstat(name)
		if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			return forkSnapshotChanged()
		}
		return nil
	}
	if err := visit("."); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func copyForkTree(ctx context.Context, source, target string, gitTree bool) (forkCopy, error) {
	return copyForkTreeBounded(ctx, source, target, gitTree, maxForkEntries)
}
func copyForkTreeBounded(ctx context.Context, source, target string, gitTree bool, entryLimit int) (forkCopy, error) {
	return copyForkTreePinned(ctx, source, target, gitTree, entryLimit, nil)
}
func copyForkTreePinned(ctx context.Context, source, target string, gitTree bool, entryLimit int, original *forkGitMarker) (forkCopy, error) {
	marker, err := captureForkRootMarker(source)
	if err != nil {
		return forkCopy{}, err
	}
	if original != nil {
		marker = *original
	}
	digest, err := scanForkTreePinned(ctx, source, target, gitTree, entryLimit, &marker)
	return forkCopy{sourceMarker: &marker, source: source, target: target, tree: digest, git: gitTree, entryLimit: entryLimit}, err
}

func forkIndex(ctx context.Context, git Git, checkout string) (string, []byte, error) {
	shared, err := git.run(ctx, checkout, "rev-parse", "--shared-index-path")
	if err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(string(shared)) != "" {
		return "", nil, forkUnsupported()
	}
	path, err := git.run(ctx, checkout, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return "", nil, err
	}
	index := filepath.FromSlash(strings.TrimSpace(string(path)))
	info, err := os.Lstat(index)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxForkBytes {
		return "", nil, forkUnsupported()
	}
	canonical, err := filepath.EvalSymlinks(index)
	if err != nil || !sameNativePath(canonical, index) {
		return "", nil, forkUnsupported()
	}
	f, err := os.OpenFile(index, os.O_RDONLY|forkNonblockFlag(), 0)
	if err != nil {
		return "", nil, forkUnsupported()
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return "", nil, forkSnapshotChanged()
	}
	raw, err := io.ReadAll(forkReader{ctx, io.LimitReader(f, info.Size()+1)})
	after, statErr := os.Lstat(index)
	if err != nil {
		return "", nil, domain.SafeError(err)
	}
	if statErr != nil || !os.SameFile(info, after) || !info.ModTime().Equal(after.ModTime()) || int64(len(raw)) != info.Size() {
		return "", nil, forkSnapshotChanged()
	}
	// Split indexes have external shared-index dependencies. Their exact native
	// ownership needs its own adapter; never copy an incomplete index silently.
	return index, raw, nil
}

func copyForkRepository(ctx context.Context, git Git, source, target, commit string) (forkCopy, error) {
	return copyForkRepositoryPinned(ctx, git, source, target, commit, nil)
}
func copyForkRepositoryPinned(ctx context.Context, git Git, source, target, commit string, original *forkGitMarker) (forkCopy, error) {
	_, index, err := forkIndex(ctx, git, source)
	if err != nil {
		return forkCopy{}, err
	}
	copy, err := copyForkTreePinned(ctx, source, target, true, maxForkEntries, original)
	if err != nil {
		return copy, err
	}
	path, err := git.run(ctx, target, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return copy, err
	}
	if err := security.WriteAtomic(filepath.FromSlash(strings.TrimSpace(string(path))), index); err != nil {
		return copy, domain.SafeError(err)
	}
	digest := sha256.Sum256(index)
	copy.head, copy.index = commit, hex.EncodeToString(digest[:])
	return copy, nil
}

func (c forkCopy) verify(ctx context.Context, git Git) error {
	entryLimit := c.entryLimit
	if entryLimit == 0 {
		entryLimit = maxForkEntries
	}
	digest, err := scanForkTreePinned(ctx, c.source, "", c.git, entryLimit, c.sourceMarker)
	if err != nil {
		return err
	}
	if digest != c.tree {
		return forkSnapshotChanged()
	}
	childDigest, err := scanForkTreeBounded(ctx, c.target, "", c.git, entryLimit)
	if err != nil {
		return err
	}
	if childDigest != c.tree {
		return forkSnapshotChanged()
	}
	if c.git {
		head, err := git.run(ctx, c.source, "rev-parse", "--verify", "HEAD")
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(head)) != c.head {
			return forkSnapshotChanged()
		}
		_, raw, err := forkIndex(ctx, git, c.source)
		if err != nil {
			return err
		}
		index := sha256.Sum256(raw)
		if hex.EncodeToString(index[:]) != c.index {
			return forkSnapshotChanged()
		}
		_, raw, err = forkIndex(ctx, git, c.target)
		if err != nil {
			return err
		}
		index = sha256.Sum256(raw)
		if hex.EncodeToString(index[:]) != c.index {
			return forkSnapshotChanged()
		}
	}
	return c.verifySourceMarker()
}

// The original source pin survives phase boundaries; child administration has
// its own lifetime and is never compared to the source inode.
func captureForkRootMarker(path string) (forkGitMarker, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return forkGitMarker{}, forkUnsupported()
	}
	defer root.Close()
	return inspectForkGitMarker(root, ".")
}
func (c forkCopy) verifySourceMarker() error {
	if c.sourceMarker == nil {
		return nil
	}
	root, err := os.OpenRoot(c.source)
	if err != nil {
		return forkSnapshotChanged()
	}
	defer root.Close()
	return c.sourceMarker.verifyIdentity(root, ".")
}

func sortForkEntries(entries []os.DirEntry) {
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
}

func forkFileSize(info os.FileInfo) int64 {
	if info.IsDir() {
		return -1
	}
	return info.Size()
}

// forkGitMarker binds the native .git entry in one parent without following a
// symlink or reading a gitdir pointer. Native case aliases share its identity;
// distinct .GIT content on a case-sensitive filesystem remains ordinary data.
type forkGitMarker struct {
	info os.FileInfo
}

func inspectForkGitMarker(root *os.Root, parent string) (forkGitMarker, error) {
	info, err := root.Lstat(filepath.Join(parent, ".git"))
	if os.IsNotExist(err) {
		return forkGitMarker{}, nil
	}
	if err != nil {
		return forkGitMarker{}, forkUnsupported()
	}
	return forkGitMarker{info: info}, nil
}

// Cross-phase directory metadata may change when the owned fork adds its
// worktree registration. Its native inode/mode remain original; regular gitdir
// references retain byte-length/time checks. Each scan separately pins metadata.
func (m forkGitMarker) verifyIdentity(root *os.Root, parent string) error {
	current, err := root.Lstat(filepath.Join(parent, ".git"))
	if m.info == nil && os.IsNotExist(err) {
		return nil
	}
	if err != nil || m.info == nil || !os.SameFile(m.info, current) || m.info.Mode() != current.Mode() {
		return forkSnapshotChanged()
	}
	if !m.info.IsDir() && (m.info.Size() != current.Size() || !m.info.ModTime().Equal(current.ModTime())) {
		return forkSnapshotChanged()
	}
	return nil
}

func (m forkGitMarker) verify(root *os.Root, parent string) error {
	current, err := root.Lstat(filepath.Join(parent, ".git"))
	if m.info == nil && os.IsNotExist(err) {
		return nil
	}
	if err != nil || m.info == nil || !os.SameFile(m.info, current) || m.info.Mode() != current.Mode() || m.info.Size() != current.Size() || !m.info.ModTime().Equal(current.ModTime()) {
		return forkSnapshotChanged()
	}
	return nil
}

func (m forkGitMarker) matches(root *os.Root, parent, entry string) (bool, error) {
	if err := m.verify(root, parent); err != nil {
		return false, err
	}
	if entry == ".git" {
		return true, nil
	}
	if m.info == nil {
		return false, nil
	}
	info, err := root.Lstat(filepath.Join(parent, entry))
	if err != nil {
		return false, forkSnapshotChanged()
	}
	return os.SameFile(m.info, info), nil
}
