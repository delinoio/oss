// SPDX-License-Identifier: Apache-2.0
package shim

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ResolveOptions must be populated for each invocation, not at installation.
// CcowExecutable is the running native executable. ShimDir is its private install directory.
type ResolveOptions struct {
	Path           string
	PathExt        string
	ShimDir        string
	CcowExecutable string
}
type Candidate struct {
	Path       string
	Excluded   bool
	Executable bool
}
type Resolution struct {
	Path       string
	Candidates []Candidate
}
type ResolveError struct {
	Tool   Tool
	Denied bool
}

func (e *ResolveError) Error() string {
	if e.Denied {
		return fmt.Sprintf("%s exists but is not executable; correct its permissions or PATH", e.Tool)
	}
	return fmt.Sprintf("real %s executable not found; install the tool and add it to PATH after the CacheCow shims", e.Tool)
}
func (e *ResolveError) ExitCode() int {
	if e.Denied {
		return 126
	}
	return 127
}

func Resolve(tool Tool, opts ResolveOptions) (Resolution, error) {
	var result Resolution
	if !tool.Valid() {
		return result, fmt.Errorf("invalid shim tool %q", tool)
	}
	excluded := map[string]bool{}
	var identities []os.FileInfo
	if opts.CcowExecutable != "" {
		st, err := os.Stat(opts.CcowExecutable)
		if err != nil {
			return result, err
		}
		identities = append(identities, st)
		b, err := os.ReadFile(opts.CcowExecutable)
		if err != nil {
			return result, err
		}
		excluded[digest(b)] = true
	}
	if opts.ShimDir != "" {
		m, err := readManifest(opts.ShimDir)
		if err != nil {
			return result, err
		}
		for _, hashes := range m.Hashes {
			for _, h := range hashes {
				excluded[h] = true
			}
		}
	}
	names := []string{string(tool)}
	if runtime.GOOS == "windows" {
		ext := opts.PathExt
		if ext == "" {
			ext = ".COM;.EXE;.BAT;.CMD"
		}
		names = nil
		for _, e := range strings.Split(ext, ";") {
			if strings.HasPrefix(e, ".") && !strings.ContainsAny(e, `/\`) {
				names = append(names, string(tool)+e)
			}
		}
	}
	denied := false
	for _, dir := range filepath.SplitList(opts.Path) {
		if dir == "" {
			dir = "."
		}
		for _, name := range names {
			p, err := filepath.Abs(filepath.Join(dir, name))
			if err != nil {
				return result, err
			}
			st, err := os.Stat(p)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				if errors.Is(err, os.ErrPermission) {
					denied = true
					continue
				}
				return result, err
			}
			c := Candidate{Path: p, Executable: st.Mode().IsRegular() && (runtime.GOOS == "windows" || st.Mode().Perm()&0111 != 0)}
			for _, id := range identities {
				if os.SameFile(st, id) {
					c.Excluded = true
				}
			}
			if opts.ShimDir != "" {
				real, e := filepath.EvalSymlinks(p)
				root, re := filepath.EvalSymlinks(opts.ShimDir)
				if e == nil && re == nil && filepath.Dir(real) == root {
					c.Excluded = true
				}
			}
			if c.Executable && !c.Excluded && len(excluded) > 0 {
				b, err := os.ReadFile(p)
				if err != nil {
					return result, err
				}
				c.Excluded = excluded[digest(b)]
			}
			result.Candidates = append(result.Candidates, c)
			if c.Excluded {
				continue
			}
			if !c.Executable {
				denied = true
				continue
			}
			result.Path = p
			return result, nil
		}
	}
	return result, &ResolveError{Tool: tool, Denied: denied}
}
