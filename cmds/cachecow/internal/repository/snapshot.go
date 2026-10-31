// SPDX-License-Identifier: Apache-2.0
package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Snapshot is an ephemeral, same-user validation token, not cache provenance.
// It contains paths and digests only. Capture it client-side immediately before
// requesting a lease, and call Verify under the lease before mutation. Never
// accept a snapshot from an untrusted user or treat it as tool compatibility.
// Its conservative inventory can invalidate on unrelated source-tree changes.
type Snapshot struct {
	Schema          int
	Repository      Info
	Worktrees       []Info
	Caches          []string
	Protected       []string
	Files           []FileStamp
	InventoryDigest string
}
type FileStamp struct {
	Path     string
	Exists   bool
	Mode     uint32
	Size     int64
	Modified int64
	Digest   string
}

// CaptureSnapshot runs Git, validates every cache, and captures the index, ignore
// rules, Git configuration and source namespace. Verification uses no subprocess.
// Cache contents are excluded because the lease owns them; changes to their Git
// tracking/ignore status still change the captured index or ignore-file digests.
func CaptureSnapshot(ctx context.Context, r Info, caches []string, protected ...string) (Snapshot, error) {
	s := Snapshot{Schema: 1, Repository: r, Protected: append([]string(nil), protected...)}
	if len(caches) == 0 {
		return Snapshot{}, errors.New("snapshot requires verified cache roots")
	}
	if e := Verify(r); e != nil {
		return Snapshot{}, e
	}
	for _, p := range caches {
		q, e := Canonical(p)
		if e != nil {
			return Snapshot{}, e
		}
		s.Caches = append(s.Caches, q)
	}
	b, e := git(ctx, r.Root, "worktree", "list", "--porcelain", "-z")
	if e != nil {
		return Snapshot{}, e
	}
	files := map[string]bool{filepath.Join(r.CommonDir, "config"): true, filepath.Join(r.CommonDir, "info", "exclude"): true}
	for _, field := range strings.Split(string(b), "\x00") {
		if !strings.HasPrefix(field, "worktree ") {
			continue
		}
		w, e := Discover(ctx, strings.TrimPrefix(field, "worktree "))
		if e != nil {
			return Snapshot{}, e
		}
		if w.Kind == Bare {
			continue
		}
		s.Worktrees = append(s.Worktrees, w)
		for _, p := range []string{filepath.Join(w.Root, ".git"), filepath.Join(w.GitDir, "index"), filepath.Join(w.GitDir, "HEAD"), filepath.Join(w.GitDir, "commondir"), filepath.Join(w.GitDir, "gitdir"), filepath.Join(w.GitDir, "config.worktree")} {
			if st, e := os.Stat(p); e == nil && st.IsDir() {
				continue
			}
			files[p] = true
		}
	}
	// Includes are represented by their resolved file origins, without retaining
	// any configuration values. Known absent user files are captured as absent.
	b, e = git(ctx, r.Root, "config", "--null", "--show-origin", "--list")
	if e != nil {
		return Snapshot{}, e
	}
	fields := strings.Split(string(b), "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		if strings.HasPrefix(fields[i], "file:") {
			p := strings.TrimPrefix(fields[i], "file:")
			if !filepath.IsAbs(p) {
				p = filepath.Join(r.Root, p)
			}
			files[p] = true
		}
	}
	if home, e := os.UserHomeDir(); e == nil {
		files[filepath.Join(home, ".gitconfig")] = true
		xdg := os.Getenv("XDG_CONFIG_HOME")
		if xdg == "" {
			xdg = filepath.Join(home, ".config")
		}
		files[filepath.Join(xdg, "git", "config")] = true
		files[filepath.Join(xdg, "git", "ignore")] = true
	}
	// --get returns status 1 for absence; the standard ignore location is already
	// captured above. Other Git/config failures are caught by the list call.
	if b, e = git(ctx, r.Root, "config", "--path", "--get", "core.excludesfile"); e == nil {
		p := strings.TrimSuffix(string(b), "\n")
		if !filepath.IsAbs(p) {
			p = filepath.Join(r.Root, p)
		}
		files[p] = true
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		stamp, e := fileStamp(p)
		if e != nil {
			return Snapshot{}, e
		}
		s.Files = append(s.Files, stamp)
	}
	s.InventoryDigest, e = s.inventory()
	if e != nil {
		return Snapshot{}, e
	}
	for _, p := range s.Caches {
		if _, e = r.ValidateCache(ctx, p, protected...); e != nil {
			return Snapshot{}, e
		}
	}
	if e = s.Verify(); e != nil {
		return Snapshot{}, e
	}
	return s, nil
}

// Verify checks the captured claims using filesystem reads only. Run while
// holding all relevant cache/source leases. A mismatch requires fresh discovery.
func (s Snapshot) Verify() error {
	if s.Schema != 1 || len(s.Caches) == 0 || len(s.Worktrees) == 0 || len(s.Files) == 0 || len(s.InventoryDigest) != 64 {
		return errors.New("unknown or incomplete discovery snapshot; repeat discovery")
	}
	if e := Verify(s.Repository); e != nil {
		return e
	}
	for _, r := range s.Worktrees {
		if e := Verify(r); e != nil {
			return e
		}
		if r.CommonDir != s.Repository.CommonDir {
			return errors.New("snapshot contains a different repository")
		}
	}
	for _, p := range s.Caches {
		if _, e := VerifyCacheClaim(s.Repository, p, s.Protected...); e != nil {
			return e
		}
	}
	for _, old := range s.Files {
		now, e := fileStamp(old.Path)
		if e != nil {
			return e
		}
		if old != now {
			return errors.New("repository metadata changed after discovery; repeat discovery")
		}
	}
	digest, e := s.inventory()
	if e != nil {
		return e
	}
	if digest != s.InventoryDigest {
		return errors.New("source paths or ignore rules changed after discovery; repeat discovery")
	}
	return nil
}
func fileStamp(p string) (FileStamp, error) {
	out := FileStamp{Path: p}
	st, e := os.Lstat(p)
	if os.IsNotExist(e) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	out.Exists = true
	out.Mode = uint32(st.Mode())
	out.Size = st.Size()
	out.Modified = st.ModTime().UnixNano()
	var b []byte
	if st.Mode()&os.ModeSymlink != 0 {
		target, e := os.Readlink(p)
		if e != nil {
			return out, e
		}
		b = []byte(target)
		contents, e := os.ReadFile(p)
		if e != nil {
			return out, e
		}
		b = append(b, contents...)
	} else if st.Mode().IsRegular() {
		b, e = os.ReadFile(p)
		if e != nil {
			return out, e
		}
	} else {
		return out, errors.New("snapshot metadata is not a regular file")
	}
	sum := sha256.Sum256(b)
	out.Digest = hex.EncodeToString(sum[:])
	return out, nil
}
func (s Snapshot) inventory() (string, error) {
	h := sha256.New()
	encoder := json.NewEncoder(h)
	for _, r := range s.Worktrees {
		err := filepath.WalkDir(r.Root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if p == filepath.Join(r.Root, ".git") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			for _, cache := range s.Caches {
				if Contains(cache, p) {
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
			}
			// Names/types detect creation, deletion, and replacement of source paths.
			// Source bytes are not hashed: build tools retain freshness responsibility.
			if err = encoder.Encode([]any{p, uint32(d.Type())}); err != nil {
				return err
			}
			if d.Name() == ".gitignore" {
				stamp, e := fileStamp(p)
				if e != nil {
					return e
				}
				return encoder.Encode(stamp)
			}
			if d.Type()&os.ModeSymlink != 0 {
				target, e := os.Readlink(p)
				if e != nil {
					return e
				}
				return encoder.Encode(target)
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	// Worktree registration changes must invalidate the captured membership.
	entries, e := os.ReadDir(filepath.Join(s.Repository.CommonDir, "worktrees"))
	if e != nil && !os.IsNotExist(e) {
		return "", e
	}
	for _, entry := range entries {
		if e = encoder.Encode(entry.Name()); e != nil {
			return "", e
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
