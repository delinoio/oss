// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// DirectorySelection retains anchored directory identities while an original
// closed execution owner reloads its native settings. It grants no workspace
// root, checkout relocation or execution authority. Verify immediately before
// native effects and again before retaining the new generation.
type DirectorySelection struct {
	ownerAlive func() bool
	path       string
	roots      []*os.Root
	names      []string
	identities []os.FileInfo
}

func (d *DirectorySelection) Path() string { return d.path }
func (d *DirectorySelection) Close() error {
	var first error
	for n := len(d.roots) - 1; n >= 0; n-- {
		if err := d.roots[n].Close(); first == nil {
			first = err
		}
	}
	d.roots = nil
	return first
}
func directoryChanged() error {
	return domain.Fail(domain.RecoveryRequired, "The selected working directory is no longer the original directory.", "Retain the original directory operation and reconcile its workspace before sending input.")
}
func (d *DirectorySelection) Verify() error {
	if d.ownerAlive != nil && !d.ownerAlive() {
		return directoryChanged()
	}
	if len(d.roots) == 0 || len(d.roots) != len(d.identities) {
		return directoryChanged()
	}
	resolved, err := filepath.EvalSymlinks(d.path)
	if err != nil || resolved != d.path {
		return directoryChanged()
	}
	for n, root := range d.roots {
		opened, err := root.Stat(".")
		if err != nil || !os.SameFile(opened, d.identities[n]) {
			return directoryChanged()
		}
		var current os.FileInfo
		if n == 0 {
			current, err = os.Lstat(d.names[0])
		} else {
			current, err = d.roots[n-1].Lstat(d.names[n])
		}
		if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(current, d.identities[n]) {
			return directoryChanged()
		}
	}
	return nil
}

// selectDirectory accepts only an original prepared root and a portable relative
// directory. The caller must already hold its verified original workspace lease.
func selectDirectory(originalRoot, relative string) (selection *DirectorySelection, returned error) {
	if !filepath.IsAbs(originalRoot) || filepath.Clean(originalRoot) != originalRoot || domain.Text(relative, "working directory", 4096, false) != nil || strings.Contains(relative, "\\") || strings.HasPrefix(relative, "/") {
		return nil, directoryChanged()
	}
	if relative == "." {
		relative = ""
	}
	if relative != "" {
		for _, part := range strings.Split(relative, "/") {
			if part == "" || part == "." || part == ".." {
				return nil, directoryChanged()
			}
		}
	}
	resolved, err := filepath.EvalSymlinks(originalRoot)
	if err != nil || resolved != originalRoot {
		return nil, directoryChanged()
	}
	info, err := os.Lstat(originalRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, directoryChanged()
	}
	root, err := os.OpenRoot(originalRoot)
	if err != nil {
		return nil, directoryChanged()
	}
	selection = &DirectorySelection{path: filepath.Join(originalRoot, filepath.FromSlash(relative)), roots: []*os.Root{root}, names: []string{originalRoot}, identities: []os.FileInfo{info}}
	defer func() {
		if returned != nil {
			_ = selection.Close()
		}
	}()
	if relative != "" {
		for _, part := range strings.Split(relative, "/") {
			parent := selection.roots[len(selection.roots)-1]
			expected, err := parent.Lstat(part)
			if err != nil {
				return selection, directoryChanged()
			}
			child, err := openVerifiedChildRoot(parent, part, expected)
			if err != nil {
				return selection, directoryChanged()
			}
			selection.roots = append(selection.roots, child)
			selection.names = append(selection.names, part)
			selection.identities = append(selection.identities, expected)
		}
	}
	return selection, selection.Verify()
}

func originalDirectoryRoot(manifest Manifest, repository domain.ID) (string, error) {
	if len(manifest.Repositories) == 0 {
		if repository != "" {
			return "", directoryChanged()
		}
		return manifest.PrimaryPath, nil
	}
	for _, prepared := range manifest.Repositories {
		if prepared.ID == repository {
			return prepared.Path, nil
		}
	}
	return "", directoryChanged()
}

// SelectDirectory preserves the preparation and execution cleanup claim. It
// returns an anchored selection under the same live lease, never a new root or
// a replacement primary directory. Native generation proof remains independent.
func (l *ExecutionLease) SelectDirectory(repository domain.ID, relative string) (*DirectorySelection, error) {
	if l.closed.Load() {
		return nil, directoryChanged()
	}
	root, err := originalDirectoryRoot(l.manifest, repository)
	if err != nil {
		return nil, err
	}
	selection, err := selectDirectory(root, relative)
	if err != nil {
		return nil, err
	}
	selection.ownerAlive = func() bool { return !l.closed.Load() }
	if err := selection.Verify(); err != nil {
		_ = selection.Close()
		return nil, err
	}
	return selection, nil
}
