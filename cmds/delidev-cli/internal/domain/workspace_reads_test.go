package domain

import (
	"strings"
	"testing"
)

func TestWorkspaceReadPortableNamespace(t *testing.T) {
	for _, path := range []string{strings.Repeat("a/", 64) + "file", "/etc/passwd", "../x", "a/../b", "a//b", "./a", "a/", "C:/a", "a\\b", "a:stream", "NUL", "NUL .txt", "COM1.txt", "LPT³.log", "name.", "name ", "*.txt", "a?b", "a\nb", "\xff"} {
		if WorkspacePath(path) {
			t.Fatalf("accepted ambiguous path %q", path)
		}
	}
	for _, path := range []string{".", "src/file.go", ".git/config", "folder with spaces/한글.txt", "README"} {
		if !WorkspacePath(path) {
			t.Fatalf("rejected portable path %q", path)
		}
	}
}

func TestWorkspaceReadRejectsForgedObservation(t *testing.T) {
	query := WorkspaceReadQuery{Operation: WorkspaceFile, Path: "file"}
	for _, result := range []WorkspaceReadResult{{Text: "data", Size: 3}, {Text: "hidden", Size: 6, Binary: true}, {Text: "\x00", Size: 1}, {Roots: []WorkspaceRoot{{Name: "foreign"}}}, {NextPageToken: "cursor"}, {Size: -1}} {
		if result.Validate(query) == nil {
			t.Fatal("forged file observation accepted")
		}
	}
	query = WorkspaceReadQuery{Operation: WorkspaceDirectory, Path: "."}
	for _, entries := range [][]WorkspaceEntry{{{Name: "a", Kind: WorkspaceEntryFile}, {Name: "a", Kind: WorkspaceEntryFile}}, {{Name: "z", Kind: WorkspaceEntryFile}, {Name: "a", Kind: WorkspaceEntryFile}}, {{Name: "../outside", Kind: WorkspaceEntryFile}}, {{Name: "x", Kind: "unknown"}}} {
		if (WorkspaceReadResult{Entries: entries}).Validate(query) == nil {
			t.Fatal("forged directory observation accepted")
		}
	}
}
