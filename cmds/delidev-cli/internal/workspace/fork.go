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

// ForkSnapshot is private read-only evidence held across workspace preparation
// and the native fork. A filesystem edit during either operation invalidates it.
type ForkSnapshot struct {
	copies []forkCopy
	git    Git
}

func (m *Manager) InspectForkSnapshot(ctx context.Context, source Manifest, request PrepareRequest) (*ForkSnapshot, error) {
	if source.SessionID != request.ForkSourceID || source.State != Ready || m.initialize() != nil {
		return nil, ResultUncertain()
	}
	git := m.Git
	git.OwnerID, git.readOnly = request.SessionID, true
	snapshot := &ForkSnapshot{git: git}
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
		digest, err := scanForkTree(ctx, path, "", source.Type != domain.GeneralChat)
		if err != nil {
			return nil, err
		}
		copy := forkCopy{source: path, tree: digest, git: source.Type != domain.GeneralChat}
		if copy.git {
			head, err := git.run(ctx, path, "rev-parse", "--verify", "HEAD")
			if err != nil || strings.TrimSpace(string(head)) != request.Repositories[i].Base.Name {
				return nil, ResultUncertain()
			}
			_, index, err := forkIndex(ctx, git, path)
			if err != nil {
				return nil, err
			}
			hash := sha256.Sum256(index)
			copy.head, copy.index = request.Repositories[i].Base.Name, hex.EncodeToString(hash[:])
		}
		snapshot.copies = append(snapshot.copies, copy)
	}
	return snapshot, nil
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
	source, target string
	tree           string
	git            bool
	head, index    string
}

func forkUnsupported() error {
	return domain.Fail(domain.Unsupported, "This workspace cannot be copied consistently by the fork profile.", "Use bounded regular files and directories without links, nested repositories or special files; preserve the source before retrying.")
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
		request.OriginMachineID = source.MachineID
	}
	git := m.Git
	git.OwnerID, git.readOnly = child, true
	if err := security.PrivateDir(filepath.Join(git.ProcessRoot, string(child))); err != nil {
		return request, ResultUncertain()
	}
	for _, repo := range source.Repositories {
		if repo.LocalHEAD == LocalHEADUnborn {
			return request, forkUnsupported()
		}
		head, err := git.run(ctx, repo.Path, "rev-parse", "--verify", "HEAD")
		commit := strings.TrimSpace(string(head))
		if err != nil || !canonicalCommit(commit) {
			return request, ResultUncertain()
		}
		spec := RepositorySpec{ID: repo.ID, Checkout: repo.Path, Base: domain.Reference{Type: domain.CommitReference, Name: commit}, Starting: domain.Reference{Type: domain.CommitReference, Name: commit}}
		if kind == domain.Local {
			spec.Starting = domain.Reference{}
		}
		request.Repositories = append(request.Repositories, spec)
		if repo.Path == source.PrimaryPath {
			request.PrimaryRepository = repo.ID
		}
	}
	return request, request.validate()
}

// scanForkTree uses an opened root for every access. Links and special files are
// outside the initial copy profile, rather than followed or silently omitted.
// Before/after identities and complete content digests reject changed sources.
func scanForkTree(ctx context.Context, source, target string, gitTree bool) (string, error) {
	canonical, err := filepath.EvalSymlinks(source)
	if err != nil || canonical != source || !filepath.IsAbs(source) {
		return "", forkUnsupported()
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		return "", forkUnsupported()
	}
	defer root.Close()
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
		if entries > maxForkEntries {
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
			return ResultUncertain()
		}
		if before.IsDir() {
			if target != "" && name != "." {
				if err := outputRoot.Mkdir(name, before.Mode().Perm()|0700); err != nil {
					return domain.SafeError(err)
				}
			}
			children, err := opened.ReadDir(maxForkEntries + 1)
			if err == io.EOF {
				err = nil
			}
			if err != nil {
				return forkUnsupported()
			}
			if len(children) > maxForkEntries {
				return forkUnsupported()
			}
			// File.ReadDir returns native order; sort to make the complete digest
			// independent of directory insertion and enumeration order.
			sortForkEntries(children)
			for _, child := range children {
				if child.Name() == ".git" {
					if name == "." && gitTree {
						continue
					}
					return forkUnsupported()
				}
				if err := visit(filepath.Join(name, child.Name())); err != nil {
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
			if err != nil || count != before.Size() {
				return ResultUncertain()
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
			return ResultUncertain()
		}
		return nil
	}
	if err := visit("."); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func copyForkTree(ctx context.Context, source, target string, gitTree bool) (forkCopy, error) {
	digest, err := scanForkTree(ctx, source, target, gitTree)
	return forkCopy{source: source, target: target, tree: digest, git: gitTree}, err
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
	index := strings.TrimSpace(string(path))
	info, err := os.Lstat(index)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxForkBytes {
		return "", nil, forkUnsupported()
	}
	canonical, err := filepath.EvalSymlinks(index)
	if err != nil || canonical != index {
		return "", nil, forkUnsupported()
	}
	f, err := os.OpenFile(index, os.O_RDONLY|forkNonblockFlag(), 0)
	if err != nil {
		return "", nil, forkUnsupported()
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return "", nil, ResultUncertain()
	}
	raw, err := io.ReadAll(forkReader{ctx, io.LimitReader(f, info.Size()+1)})
	after, statErr := os.Lstat(index)
	if err != nil || statErr != nil || !os.SameFile(info, after) || !info.ModTime().Equal(after.ModTime()) || int64(len(raw)) != info.Size() {
		return "", nil, ResultUncertain()
	}
	// Split indexes have external shared-index dependencies. Their exact native
	// ownership needs its own adapter; never copy an incomplete index silently.
	return index, raw, nil
}

func copyForkRepository(ctx context.Context, git Git, source, target, commit string) (forkCopy, error) {
	_, index, err := forkIndex(ctx, git, source)
	if err != nil {
		return forkCopy{}, err
	}
	copy, err := copyForkTree(ctx, source, target, true)
	if err != nil {
		return copy, err
	}
	path, err := git.run(ctx, target, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return copy, err
	}
	if err := security.WriteAtomic(strings.TrimSpace(string(path)), index); err != nil {
		return copy, domain.SafeError(err)
	}
	digest := sha256.Sum256(index)
	copy.head, copy.index = commit, hex.EncodeToString(digest[:])
	return copy, nil
}

func (c forkCopy) verify(ctx context.Context, git Git) error {
	digest, err := scanForkTree(ctx, c.source, "", c.git)
	if err != nil || digest != c.tree {
		return ResultUncertain()
	}
	childDigest, err := scanForkTree(ctx, c.target, "", c.git)
	if err != nil || childDigest != c.tree {
		return ResultUncertain()
	}
	if c.git {
		head, err := git.run(ctx, c.source, "rev-parse", "--verify", "HEAD")
		if err != nil || strings.TrimSpace(string(head)) != c.head {
			return ResultUncertain()
		}
		_, raw, err := forkIndex(ctx, git, c.source)
		index := sha256.Sum256(raw)
		if err != nil || hex.EncodeToString(index[:]) != c.index {
			return ResultUncertain()
		}
		_, raw, err = forkIndex(ctx, git, c.target)
		index = sha256.Sum256(raw)
		if err != nil || hex.EncodeToString(index[:]) != c.index {
			return ResultUncertain()
		}
	}
	return nil
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
