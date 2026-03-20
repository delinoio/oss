package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePathsSuccess(t *testing.T) {
	workspace := t.TempDir()
	entryPath := filepath.Join(workspace, "main.ttl")
	if err := os.WriteFile(entryPath, []byte("package build"), 0o600); err != nil {
		t.Fatalf("write entry file: %v", err)
	}

	paths, err := ResolvePaths(workspace, "./main.ttl", "./.ttl/gen")
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	if !strings.HasSuffix(paths.WorkspaceRoot, filepath.Base(workspace)) {
		t.Fatalf("unexpected workspace root: %s", paths.WorkspaceRoot)
	}
	if !strings.HasSuffix(paths.CacheDBPath, filepath.FromSlash(".ttl/cache/cache.sqlite3")) {
		t.Fatalf("unexpected cache db path: %s", paths.CacheDBPath)
	}
}

func TestResolvePathsRejectsNonTtlEntry(t *testing.T) {
	workspace := t.TempDir()
	entryPath := filepath.Join(workspace, "main.txt")
	if err := os.WriteFile(entryPath, []byte("package build"), 0o600); err != nil {
		t.Fatalf("write entry file: %v", err)
	}

	_, err := ResolvePaths(workspace, "./main.txt", "./.ttl/gen")
	if err == nil {
		t.Fatal("expected extension validation error")
	}
	if !strings.Contains(err.Error(), ".ttl extension") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestResolvePathsRejectsWorkspaceEscape(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	outsidePath := filepath.Join(root, "outside.ttl")
	if err := os.WriteFile(outsidePath, []byte("package build"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	_, err := ResolvePaths(workspace, "../outside.ttl", "./.ttl/gen")
	if err == nil {
		t.Fatal("expected workspace escape error")
	}
	if !strings.Contains(err.Error(), "escapes workspace root") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestResolveImportPathRelative(t *testing.T) {
	workspace := t.TempDir()
	libPath := filepath.Join(workspace, "lib.ttl")
	if err := os.WriteFile(libPath, []byte("package lib"), 0o600); err != nil {
		t.Fatalf("write lib file: %v", err)
	}

	currentFile := filepath.Join(workspace, "main.ttl")
	resolved, err := ResolveImportPath(workspace, currentFile, "./lib.ttl")
	if err != nil {
		t.Fatalf("resolve import: %v", err)
	}
	if !strings.HasSuffix(resolved, "lib.ttl") {
		t.Fatalf("unexpected resolved path: %s", resolved)
	}
}

func TestResolveImportPathAutoAppendsTtl(t *testing.T) {
	workspace := t.TempDir()
	subDir := filepath.Join(workspace, "pkg")
	if err := os.MkdirAll(subDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "utils.ttl"), []byte("package utils"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	currentFile := filepath.Join(workspace, "main.ttl")
	resolved, err := ResolveImportPath(workspace, currentFile, "pkg/utils")
	if err != nil {
		t.Fatalf("resolve import: %v", err)
	}
	if !strings.HasSuffix(resolved, filepath.Join("pkg", "utils.ttl")) {
		t.Fatalf("unexpected resolved path: %s", resolved)
	}
}

func TestResolveImportPathRejectsEscape(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	outsidePath := filepath.Join(root, "evil.ttl")
	if err := os.WriteFile(outsidePath, []byte("package evil"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	currentFile := filepath.Join(workspace, "main.ttl")
	_, err := ResolveImportPath(workspace, currentFile, "../evil.ttl")
	if err == nil {
		t.Fatal("expected workspace escape error")
	}
	if !strings.Contains(err.Error(), "escapes workspace root") {
		t.Fatalf("unexpected error: %v", err)
	}
}
