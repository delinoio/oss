package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWorkspaceStreamsManyAndLargeRawBlobsInOneBatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX Git instrumentation; streaming code is cross-platform")
	}
	s, repo := fixture(t, "version=1\n")
	for i := 0; i < 256; i++ {
		if err := os.WriteFile(filepath.Join(repo, fmt.Sprintf("file-%03d", i)), []byte(fmt.Sprintf("raw-%d\n", i)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	large := bytes.Repeat([]byte("raw\x00binary\r\n"), 1024*1024)
	if err := os.WriteFile(filepath.Join(repo, "large"), large, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "executable"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("large", filepath.Join(repo, "link")); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, args := range [][]string{{"add", "."}, {"-c", "commit.gpgsign=false", "commit", "-qm", "batch fixtures"}} {
		if _, err := Git(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	r, err := s.Plan(ctx, repo, "")
	if err != nil {
		t.Fatal(err)
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	shim := t.TempDir()
	trace := filepath.Join(shim, "calls")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %q\nexec %q \"$@\"\n", trace, realGit)
	if err := os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	workspace, err := s.Store.Prepare(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(calls), "cat-file --batch") != 1 || strings.Contains(string(calls), "cat-file blob") {
		t.Fatalf("per-file Git execution: %s", calls)
	}
	got, err := os.ReadFile(filepath.Join(workspace, "large"))
	if err != nil || sha256.Sum256(got) != sha256.Sum256(large) {
		t.Fatal("large raw blob changed", err)
	}
	for i := 0; i < 256; i++ {
		got, err := os.ReadFile(filepath.Join(workspace, fmt.Sprintf("file-%03d", i)))
		if err != nil || string(got) != fmt.Sprintf("raw-%d\n", i) {
			t.Fatal("batch framing changed", err)
		}
	}
	target, err := os.Readlink(filepath.Join(workspace, "link"))
	if err != nil || target != "large" {
		t.Fatal("symlink changed", err)
	}
	info, err := os.Stat(filepath.Join(workspace, "executable"))
	if err != nil || info.Mode()&0100 == 0 {
		t.Fatal("execute permission lost", err)
	}
}
