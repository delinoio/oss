// SPDX-License-Identifier: Apache-2.0

// Package repository discovers Git worktrees and validates local cache boundaries.
// Discovery does not authorize mutation: callers must revalidate under their locks.
package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Kind string

const (
	Checkout Kind = "checkout"
	Bare     Kind = "bare"
)

type SourceOrigin string

const (
	Invocation SourceOrigin = "invocation"
	Saved      SourceOrigin = "saved"
	Primary    SourceOrigin = "primary"
)

type Info struct {
	CWD       string
	Root      string
	GitDir    string
	CommonDir string
	Primary   string // Empty for a bare repository, including its linked worktrees.
	Kind      Kind
	ID        string // Derived from the canonical worktree Git directory, stable across cwd changes.
}
type Source struct {
	Repository Info
	Origin     SourceOrigin
	Self       bool
}

// Git ignores ambient repository redirection. The repository is always selected by cwd.
func git(ctx context.Context, cwd string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...)
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GIT_") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	b, err := cmd.Output()
	if err != nil {
		return nil, errors.New("Git discovery failed; check the checkout, permissions, and Git installation")
	}
	return b, nil
}
func gitPath(ctx context.Context, cwd, flag string) (string, error) {
	b, e := git(ctx, cwd, "rev-parse", "--path-format=absolute", flag)
	if e != nil {
		return "", e
	}
	return Canonical(strings.TrimSuffix(string(b), "\n"))
}
func Discover(ctx context.Context, cwd string) (Info, error) {
	var r Info
	p, e := Canonical(cwd)
	if e != nil {
		return r, e
	}
	if e = CheckOwner(p); e != nil {
		return r, e
	}
	r.CWD = p
	r.GitDir, e = gitPath(ctx, p, "--absolute-git-dir")
	if e != nil {
		return r, e
	}
	r.CommonDir, e = gitPath(ctx, p, "--git-common-dir")
	if e != nil {
		return r, e
	}
	for _, v := range []string{r.GitDir, r.CommonDir} {
		if e = CheckOwner(v); e != nil {
			return r, e
		}
	}
	b, e := git(ctx, p, "rev-parse", "--is-bare-repository")
	if e != nil {
		return r, e
	}
	r.Kind = Checkout
	if strings.TrimSpace(string(b)) == "true" {
		r.Kind = Bare
		r.Root = r.CommonDir
	} else {
		r.Root, e = gitPath(ctx, p, "--show-toplevel")
		if e != nil {
			return r, e
		}
	}
	if e = CheckOwner(r.Root); e != nil {
		return r, e
	}
	b, e = git(ctx, p, "worktree", "list", "--porcelain", "-z")
	if e != nil {
		return r, e
	}
	// Git lists the primary worktree (or bare repository) first. NUL records
	// preserve spaces, newlines, and non-ASCII names without Git quote decoding.
	fields := strings.Split(string(b), "\x00")
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "worktree ") {
		return r, errors.New("Git returned an unknown worktree list; upgrade Git")
	}
	bare := false
	for _, f := range fields[1:] {
		if f == "" {
			break
		}
		if f == "bare" {
			bare = true
		}
	}
	if !bare {
		r.Primary, e = Canonical(strings.TrimPrefix(fields[0], "worktree "))
		if e != nil {
			return r, e
		}
	}
	sum := sha256.Sum256([]byte(r.GitDir))
	r.ID = hex.EncodeToString(sum[:])
	return r, nil
}

// SelectSource applies invocation > saved setting > primary checkout precedence.
// An invalid explicit selection never falls back to a different source.
func SelectSource(ctx context.Context, current Info, invocation, saved string) (Source, error) {
	p, origin := invocation, Invocation
	if p == "" {
		p, origin = saved, Saved
	}
	if p == "" {
		p, origin = current.Primary, Primary
	}
	if p == "" {
		return Source{}, errors.New("bare repository has no primary checkout; select a source worktree with --source")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(current.CWD, p)
	}
	r, e := Discover(ctx, p)
	if e != nil {
		return Source{}, e
	}
	if r.Kind == Bare || r.CommonDir != current.CommonDir {
		return Source{}, errors.New("source must be a checkout of this same Git repository; select another --source")
	}
	return Source{Repository: r, Origin: origin, Self: r.ID == current.ID}, nil
}

// Canonical resolves existing ancestors too, so aliases of not-yet-created caches
// share an identity. Dangling links are errors, not nonexistent directories.
func Canonical(path string) (string, error) {
	if path == "" {
		return "", errors.New("empty path is not a cache location")
	}
	// Abs/Clean would erase a symlink followed by ".." before the filesystem
	// resolves it, potentially validating a different cache from the build.
	for _, component := range strings.FieldsFunc(path, func(r rune) bool { return r < 128 && os.IsPathSeparator(uint8(r)) }) {
		if component == ".." {
			return "", errors.New("parent traversal is ambiguous in cache paths; use a resolved path without ..")
		}
	}
	p, e := filepath.Abs(path)
	if e != nil {
		return "", e
	}
	var tail []string
	for {
		_, e = os.Lstat(p)
		if e == nil {
			break
		}
		if !os.IsNotExist(e) {
			return "", e
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", e
		}
		tail = append(tail, filepath.Base(p))
		p = parent
	}
	p, e = filepath.EvalSymlinks(p)
	if e != nil {
		return "", e
	}
	for i := len(tail) - 1; i >= 0; i-- {
		p = filepath.Join(p, tail[i])
	}
	return filepath.Clean(p), nil
}
func Contains(parent, child string) bool {
	rel, e := filepath.Rel(parent, child)
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func Overlaps(a, b string) bool { return Contains(a, b) || Contains(b, a) }

// ValidateCache protects all worktree roots, Git metadata, tracked and unignored files,
// and caller-supplied settings/output/source-cache paths. Cache subdirectories
// are allowed. It returns the canonical path used for coordination.
func (r Info) ValidateCache(ctx context.Context, path string, protected ...string) (string, error) {
	p, e := Canonical(path)
	if e != nil {
		return "", e
	}
	if e = CheckOwner(p); e != nil {
		return "", e
	}
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return "", errors.New("cache root must be a directory")
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if r.Kind == Bare {
		return "", errors.New("cache discovery requires a checkout, not a bare repository")
	}
	for _, v := range append([]string{r.CommonDir, r.GitDir, filepath.Join(r.Root, ".git"), filepath.Join(r.Root, ".ccow")}, protected...) {
		q, e := Canonical(v)
		if e != nil {
			return "", e
		}
		if Overlaps(p, q) {
			return "", errors.New("cache overlaps protected storage; configure a dedicated cache directory")
		}
	}
	b, e := git(ctx, r.Root, "worktree", "list", "--porcelain", "-z")
	if e != nil {
		return "", e
	}
	for _, f := range strings.Split(string(b), "\x00") {
		if !strings.HasPrefix(f, "worktree ") {
			continue
		}
		q, e := Canonical(strings.TrimPrefix(f, "worktree "))
		if e != nil {
			return "", e
		}
		if Contains(p, q) {
			return "", errors.New("cache cannot contain a worktree; configure a dedicated cache directory")
		}
		if q == r.CommonDir {
			continue
		} // Bare primary entry has no index or source tree.
		for _, name := range []string{".git", ".ccow"} {
			protectedPath, e := Canonical(filepath.Join(q, name))
			if e != nil {
				return "", e
			}
			if Overlaps(p, protectedPath) {
				return "", errors.New("cache overlaps worktree metadata or settings")
			}
		}
		files, e := git(ctx, q, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
		if e != nil {
			return "", e
		}
		for _, name := range strings.Split(string(files), "\x00") {
			if name == "" {
				continue
			}
			entry := filepath.Join(q, name)
			if Overlaps(p, entry) {
				return "", errors.New("cache overlaps tracked or unignored files; configure and ignore a dedicated cache directory")
			}
			source, e := sourceLocation(entry, 0)
			if e != nil {
				return "", e
			}
			if Overlaps(p, source) {
				return "", errors.New("cache overlaps tracked or unignored files; configure and ignore a dedicated cache directory")
			}
		}
	}
	return p, nil
}

// Source entries may be dangling symlinks in unrelated worktrees. Resolve their
// intended target too so an absent target inside the selected cache remains
// protected. Cache claims themselves still use strict Canonical validation.
func sourceLocation(path string, depth int) (string, error) {
	resolved, err := Canonical(path)
	if err == nil || !os.IsNotExist(err) || depth >= 40 {
		return resolved, err
	}
	info, statErr := os.Lstat(path)
	if statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		return "", err
	}
	target, err := os.Readlink(path)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	return sourceLocation(target, depth+1)
}
