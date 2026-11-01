// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/cachecow/internal/repository"
)

// symlinkFixture is a Git repository reachable through both a canonical path and a symlinked parent.
type symlinkFixture struct {
	canonical, linked string
}

func newSymlinkFixture(t *testing.T) symlinkFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, "real")
	repo := filepath.Join(real, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if b, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", b, err)
	}
	return symlinkFixture{canonical: repo, linked: filepath.Join(link, "repo")}
}

func touchTest(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDetectPresetFromSymlinkedWorkingDirectory(t *testing.T) {
	cases := []struct {
		name    string
		files   []string
		subdir  string
		want    string
		wantErr string
	}{
		{name: "cargo at root from subdirectory", files: []string{"Cargo.toml"}, subdir: "sub", want: "cargo"},
		{name: "cargo at root", files: []string{"Cargo.toml"}, want: "cargo"},
		{name: "rspack in subdirectory", files: []string{"sub/rspack.config.js"}, subdir: "sub", want: "rspack"},
		{name: "nearest ancestor wins", files: []string{"Cargo.toml", "sub/rsbuild.config.ts"}, subdir: "sub", want: "rsbuild"},
		{name: "ambiguous", files: []string{"Cargo.toml", "rsbuild.config.ts"}, wantErr: "ambiguous preset"},
		{name: "undetectable", files: []string{"README.md"}, subdir: "sub", wantErr: "cannot detect a preset"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSymlinkFixture(t)
			for _, name := range tc.files {
				touchTest(t, filepath.Join(f.canonical, filepath.FromSlash(name)))
			}
			for label, root := range map[string]string{"canonical": f.canonical, "symlinked": f.linked} {
				cwd := filepath.Join(root, tc.subdir)
				a := &app{ctx: context.Background()}
				got, err := a.detect(cwd)
				if tc.wantErr != "" {
					if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
						t.Fatalf("%s: error = %v, want %q", label, err, tc.wantErr)
					}
					continue
				}
				if err != nil || got != tc.want {
					t.Fatalf("%s: detect(%q) = %q, %v; want %q", label, cwd, got, err, tc.want)
				}
			}
		})
	}
}

func TestSourceWorkingDirectoryWithSymlinkedCwd(t *testing.T) {
	f := newSymlinkFixture(t)
	sourceRoot := filepath.Join(t.TempDir(), "source")
	for label, root := range map[string]string{"canonical": f.canonical, "symlinked": f.linked} {
		cwd := filepath.Join(root, "sub")
		info, err := repository.Discover(context.Background(), cwd)
		if err != nil {
			t.Fatal(err)
		}
		got, err := sourceWorkingDirectory(info, repository.Info{Root: sourceRoot})
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if want := filepath.Join(sourceRoot, "sub"); got != want {
			t.Fatalf("%s: source cwd = %q, want %q", label, got, want)
		}
	}
	info, err := repository.Discover(context.Background(), f.linked)
	if err != nil {
		t.Fatal(err)
	}
	got, err := sourceWorkingDirectory(info, repository.Info{Root: sourceRoot})
	if err != nil || got != sourceRoot {
		t.Fatalf("repository root: source cwd = %q, %v; want %q", got, err, sourceRoot)
	}
}
