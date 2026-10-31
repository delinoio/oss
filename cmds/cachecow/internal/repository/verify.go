// SPDX-License-Identifier: Apache-2.0
package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Verify checks a client's repository identity using filesystem reads only.
// It is suitable for supervisor hooks that cannot run subprocesses. It checks
// ownership, Git indirection, linked-worktree backlinks, and canonical identity.
// It does not re-run tool discovery or interpret the Git index. A caller still
// needs a trusted same-user discovery result before authorizing cache mutation.
func Verify(r Info) error {
	for _, p := range []string{r.Root, r.GitDir, r.CommonDir, r.CWD} {
		canonical, e := Canonical(p)
		if e != nil {
			return e
		}
		if canonical != p {
			return errors.New("repository claim is not canonical; repeat discovery")
		}
		if e = CheckOwner(p); e != nil {
			return e
		}
	}
	sum := sha256.Sum256([]byte(r.GitDir))
	if r.ID != hex.EncodeToString(sum[:]) {
		return errors.New("repository identity changed; repeat discovery")
	}
	if r.Kind == Bare {
		if r.Root != r.GitDir || r.GitDir != r.CommonDir {
			return errors.New("invalid bare repository claim")
		}
	} else if r.Kind == Checkout {
		if !Contains(r.Root, r.CWD) {
			return errors.New("execution directory is outside the claimed checkout")
		}
		actual, e := checkoutGitDir(r.Root)
		if e != nil {
			return e
		}
		if actual != r.GitDir {
			return errors.New("checkout Git directory changed; repeat discovery")
		}
	} else {
		return errors.New("unknown repository kind")
	}
	common := r.GitDir
	b, e := os.ReadFile(filepath.Join(r.GitDir, "commondir"))
	if e == nil {
		common, e = resolveGitPath(r.GitDir, string(b))
		if e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if common != r.CommonDir {
		return errors.New("Git common directory changed; repeat discovery")
	}
	if r.GitDir != r.CommonDir {
		b, e = os.ReadFile(filepath.Join(r.GitDir, "gitdir"))
		if e != nil {
			return e
		}
		backlink, e := resolveGitPath(r.GitDir, string(b))
		if e != nil {
			return e
		}
		expected, e := Canonical(filepath.Join(r.Root, ".git"))
		if e != nil {
			return e
		}
		if backlink != expected {
			return errors.New("linked-worktree backlink changed; repeat discovery")
		}
	}
	for _, p := range []string{filepath.Join(r.GitDir, "HEAD"), filepath.Join(r.CommonDir, "config")} {
		s, e := os.Lstat(p)
		if e != nil {
			return e
		}
		if !s.Mode().IsRegular() {
			return errors.New("Git identity metadata must be regular files")
		}
		if e = CheckOwner(p); e != nil {
			return e
		}
	}
	if r.Primary != "" {
		primary, e := Canonical(r.Primary)
		if e != nil {
			return e
		}
		if primary != r.Primary {
			return errors.New("primary checkout claim is not canonical")
		}
		if e = CheckOwner(primary); e != nil {
			return e
		}
		dir, e := checkoutGitDir(primary)
		if e != nil {
			return e
		}
		if dir != r.CommonDir {
			return errors.New("primary checkout does not own the common Git directory")
		}
	}
	return nil
}
func resolveGitPath(base, value string) (string, error) {
	p := strings.TrimSuffix(strings.TrimSuffix(value, "\n"), "\r")
	if p == "" {
		return "", errors.New("empty Git metadata path")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return Canonical(p)
}
func checkoutGitDir(root string) (string, error) {
	p := filepath.Join(root, ".git")
	s, e := os.Lstat(p)
	if e != nil {
		return "", e
	}
	if s.IsDir() {
		return Canonical(p)
	}
	if !s.Mode().IsRegular() {
		return "", errors.New("checkout .git must be a directory or regular Git pointer file")
	}
	if e = CheckOwner(p); e != nil {
		return "", e
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return "", e
	}
	if !strings.HasPrefix(string(b), "gitdir: ") {
		return "", errors.New("invalid checkout Git pointer")
	}
	return resolveGitPath(root, strings.TrimPrefix(string(b), "gitdir: "))
}

// VerifyCacheClaim rechecks canonical paths, ownership and protected boundaries
// without starting Git or any tool. Unlike ValidateCache, it does not inspect
// tracked/unignored files or enumerate other worktrees. Supply every source root,
// source cache and settings path that the supervisor must protect. Source roots
// containing normal cache subdirectories should be checked separately, since
// protected entries forbid overlap in both directions.
func VerifyCacheClaim(r Info, path string, protected ...string) (string, error) {
	if e := Verify(r); e != nil {
		return "", e
	}
	if r.Kind != Checkout {
		return "", errors.New("cache claim requires a checkout")
	}
	canonical, e := Canonical(path)
	if e != nil {
		return "", e
	}
	if canonical != path {
		return "", errors.New("cache claim is not canonical; repeat discovery")
	}
	if e = CheckOwner(path); e != nil {
		return "", e
	}
	if s, e := os.Stat(path); e == nil && !s.IsDir() {
		return "", errors.New("cache claim is not a directory")
	} else if e != nil && !os.IsNotExist(e) {
		return "", e
	}
	for _, root := range []string{r.Root, r.Primary} {
		if root != "" && Contains(path, root) {
			return "", errors.New("cache claim contains a source checkout")
		}
	}
	for _, v := range append([]string{r.GitDir, r.CommonDir, filepath.Join(r.Root, ".git"), filepath.Join(r.Root, ".ccow")}, protected...) {
		p, e := Canonical(v)
		if e != nil {
			return "", e
		}
		if Overlaps(path, p) {
			return "", errors.New("cache claim overlaps protected storage")
		}
	}
	return canonical, nil
}
