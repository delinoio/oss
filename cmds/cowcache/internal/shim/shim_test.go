// SPDX-License-Identifier: Apache-2.0
package shim

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0700); err != nil {
		t.Fatal(err)
	}
}
func private(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "개인 shims")
	if err := privateDir(p); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestClassification(t *testing.T) {
	tests := []struct {
		tool   Tool
		args   []string
		mode   Mode
		reason Reason
	}{
		{Cargo, []string{"+stable", "--color", "always", "--config", "path = '한 글'", "build", "--target-dir", "a b", "--", "--help"}, Managed, CacheOperation},
		{Cargo, []string{"+nightly", "-vv", "clean", "--target-dir=a b", "-p", "x"}, Clean, CleanOperation},
		{Cargo, []string{"build", "--help"}, Passthrough, Query},
		{Cargo, []string{"-V"}, Passthrough, Query},
		{Cargo, []string{"metadata", "--format-version", "1"}, Passthrough, Query},
		{Cargo, []string{"locate-project"}, Passthrough, Query},
		{Cargo, []string{"tree"}, Passthrough, Query},
		{Cargo, []string{"custom", "build"}, Passthrough, Unknown},
		{Cargo, []string{"--unknown", "build"}, Passthrough, Unknown},
		{Cargo, []string{"--config"}, Passthrough, Unknown},
		{Cargo, nil, Passthrough, Query},
		{Cargo, []string{"run", "--", "--version"}, Managed, CacheOperation},
		{Rspack, nil, Managed, CacheOperation},
		{Rspack, []string{"--watch", "--config", "한 글.js"}, Managed, CacheOperation},
		{Rsbuild, []string{"--config=x.js", "dev"}, Managed, CacheOperation},
		{Rspack, []string{"build", "--config", "a b.js"}, Managed, CacheOperation},
		{Rspack, []string{"serve"}, Managed, CacheOperation},
		{Rsbuild, []string{"dev"}, Managed, CacheOperation},
		{Rsbuild, []string{"preview"}, Passthrough, Query},
		{Rsbuild, []string{"inspect"}, Passthrough, Query},
		{Rsbuild, []string{"custom"}, Passthrough, Unknown},
	}
	for _, tt := range tests {
		before := append([]string(nil), tt.args...)
		got := Classify(tt.tool, tt.args, true, "")
		if got.Mode != tt.mode || got.Reason != tt.reason {
			t.Errorf("%s %q: %+v", tt.tool, tt.args, got)
		}
		if !reflect.DeepEqual(before, tt.args) {
			t.Fatal("arguments modified")
		}
	}
	for _, tool := range Tools() {
		for _, args := range [][]string{{"build"}, {"clean"}, {"--version"}} {
			if got := Classify(tool, args, false, ""); got.Mode != Passthrough || got.Reason != OutsideGit {
				t.Fatal(got)
			}
			if got := Classify(tool, args, true, "1"); got.Mode != Passthrough || got.Reason != Bypass {
				t.Fatal(got)
			}
		}
	}
	if got := Classify(Cargo, []string{"build"}, true, "true"); got.Mode != Managed {
		t.Fatal(got)
	}
	for _, tool := range Tools() {
		got, ok := FromArgv0(filepath.Join("a b", string(tool)+".exe"))
		if !ok || got != tool {
			t.Fatal(got)
		}
	}
}
func TestInstallOwnershipAndRecovery(t *testing.T) {
	dir := private(t)
	source := filepath.Join(t.TempDir(), "cowc")
	write(t, source, "native v1")
	if err := Install(dir, source); err != nil {
		t.Fatal(err)
	}
	if err := Install(dir, source); err != nil {
		t.Fatal(err)
	}
	entries, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.State != Owned {
			t.Fatal(e)
		}
	}
	// Simulate interruption between journal publication and replacing all copies.
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range Tools() {
		m.Hashes[tool] = []string{digest([]byte("native v1")), digest([]byte("native v2"))}
	}
	if err := saveManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, filename(Cargo)), "native v2")
	write(t, source, "native v2")
	if err := Install(dir, source); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "unrelated"), "retain me")
	cargo := filepath.Join(dir, filename(Cargo))
	write(t, cargo, "modified native file")
	if err := Install(dir, source); err == nil {
		t.Fatal("overwrote modified shim")
	}
	if err := Uninstall(dir); err == nil {
		t.Fatal("removed modified shim")
	}
	b, _ := os.ReadFile(cargo)
	if string(b) != "modified native file" {
		t.Fatal(string(b))
	}
	write(t, cargo, "native v2")
	if err := Uninstall(dir); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(dir); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "unrelated")); err != nil || string(b) != "retain me" {
		t.Fatal(err)
	}
}
func TestConflictAndUnknownManifest(t *testing.T) {
	dir := private(t)
	source := filepath.Join(t.TempDir(), "cowc")
	write(t, source, "native")
	conflict := filepath.Join(dir, filename(Rspack))
	write(t, conflict, "other tool")
	if err := Install(dir, source); err == nil {
		t.Fatal("accepted conflict")
	}
	if _, err := os.Stat(filepath.Join(dir, filename(Cargo))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial install before conflict check")
	}
	os.Remove(conflict)
	write(t, filepath.Join(dir, manifestName), `{"version":2,"owner":"cowcache-native-shims","hashes":{}}`)
	if err := Install(dir, source); err == nil {
		t.Fatal("rewrote unknown version")
	}
	if err := Uninstall(dir); err == nil {
		t.Fatal("removed unknown version")
	}
}
func TestResolveDynamicAliases(t *testing.T) {
	dir := private(t)
	source := filepath.Join(t.TempDir(), "cowc")
	write(t, source, "cowc native")
	if err := Install(dir, source); err != nil {
		t.Fatal(err)
	}
	alias := t.TempDir()
	a := t.TempDir()
	b := t.TempDir()
	write(t, filepath.Join(a, filename(Cargo)), "rustup proxy A")
	write(t, filepath.Join(b, filename(Cargo)), "rustup proxy B")
	// Even a copied CowCache binary outside its owned directory must be excluded.
	write(t, filepath.Join(alias, filename(Cargo)), "cowc native")
	options := ResolveOptions{Path: dir + string(os.PathListSeparator) + alias + string(os.PathListSeparator) + a + string(os.PathListSeparator) + b, ShimDir: dir, CowcExecutable: source}
	r, err := Resolve(Cargo, options)
	if err != nil || r.Path != filepath.Join(a, filename(Cargo)) {
		t.Fatalf("%+v %v", r, err)
	}
	options.Path = b + string(os.PathListSeparator) + a
	r, err = Resolve(Cargo, options)
	if err != nil || r.Path != filepath.Join(b, filename(Cargo)) {
		t.Fatalf("%+v %v", r, err)
	}
	options.Path = dir + string(os.PathListSeparator) + alias
	_, err = Resolve(Cargo, options)
	var missing *ResolveError
	if !errors.As(err, &missing) || missing.ExitCode() != 127 {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		os.Remove(filepath.Join(alias, filename(Cargo)))
		if err := os.Symlink(source, filepath.Join(alias, filename(Cargo))); err != nil {
			t.Fatal(err)
		}
		options.Path = alias + string(os.PathListSeparator) + a
		r, err = Resolve(Cargo, options)
		if err != nil || r.Path != filepath.Join(a, filename(Cargo)) {
			t.Fatalf("symlink recursion: %+v %v", r, err)
		}
		os.Remove(filepath.Join(alias, filename(Cargo)))
		if err := os.Link(source, filepath.Join(alias, filename(Cargo))); err != nil {
			t.Fatal(err)
		}
		r, err = Resolve(Cargo, options)
		if err != nil || r.Path != filepath.Join(a, filename(Cargo)) {
			t.Fatalf("hardlink recursion: %+v %v", r, err)
		}
		os.Chmod(filepath.Join(a, filename(Cargo)), 0600)
		options.Path = a
		_, err = Resolve(Cargo, options)
		if !errors.As(err, &missing) || missing.ExitCode() != 126 {
			t.Fatal(err)
		}
	}
}
func TestInstallLockAndSymlinkConflict(t *testing.T) {
	dir := private(t)
	source := filepath.Join(t.TempDir(), "cowc")
	write(t, source, "native")
	unlock, err := lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(dir, source); err == nil {
		t.Fatal("ignored install lock")
	}
	unlock()
	if runtime.GOOS != "windows" {
		if err := os.Symlink(source, filepath.Join(dir, filename(Cargo))); err != nil {
			t.Fatal(err)
		}
		if err := Install(dir, source); err == nil {
			t.Fatal("claimed unowned symlink")
		}
	}
}

// This subprocess runs through an installed copy of the native Go test binary.
// It checks the argv0 contract without introducing a main CLI implementation.
func TestInstalledNativeEntry(t *testing.T) {
	if os.Getenv("COWCACHE_SHIM_ENTRY_TEST") == "1" {
		tool, ok := FromArgv0(os.Args[0])
		if !ok {
			os.Exit(90)
		}
		cwd, _ := os.Getwd()
		_ = json.NewEncoder(os.Stdout).Encode(struct {
			Tool Tool
			Args []string
			Cwd  string
		}{tool, os.Args[3:], cwd})
		os.Exit(23)
	}
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := private(t)
	if err := Install(dir, source); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	args := []string{"+stable", "build", "--target-dir", "한 글/cache", "--", "a'b", "$HOME", ""}
	for _, tool := range Tools() {
		command := exec.Command(filepath.Join(dir, filename(tool)), append([]string{"-test.run=^TestInstalledNativeEntry$", "--"}, args...)...)
		command.Env = append(os.Environ(), "COWCACHE_SHIM_ENTRY_TEST=1")
		command.Dir = cwd
		out, err := command.Output()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 23 {
			t.Fatalf("%s: %v %s", tool, err, out)
		}
		var got struct {
			Tool Tool
			Args []string
			Cwd  string
		}
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err, string(out))
		}
		realCwd, _ := filepath.EvalSymlinks(cwd)
		gotCwd, _ := filepath.EvalSymlinks(got.Cwd)
		if got.Tool != tool || !reflect.DeepEqual(got.Args, args) || gotCwd != realCwd {
			t.Fatalf("bad native entry: %+v", got)
		}
	}
}
