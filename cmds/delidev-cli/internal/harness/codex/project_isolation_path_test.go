// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestNormalizeProjectKeySpellings(t *testing.T) {
	rows := []struct {
		path, want string
		fold       bool
	}{
		{"/w/p", "/w/p", false},
		{"/w/p/", "/w/p", false},
		{"/w/p/./", "/w/p", false},
		{"/w/p/sub/..", "/w/p", false},
		{"/w//p", "/w/p", false},
		{"/W/P", "/W/P", false},
		{"/W/P/", "/w/p", true},
		{"", "", false},
	}
	for _, row := range rows {
		if got := normalizeProjectKey(row.path, row.fold); got != row.want {
			t.Errorf("%q fold=%v -> %q, want %q", row.path, row.fold, got, row.want)
		}
	}
}

func TestProjectTrustKeysWriteNormalizedSpellingsOnly(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(project, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	sep := string(os.PathSeparator)
	rows := []struct {
		name  string
		input string
		want  []string
	}{
		{"clean", project, []string{project}},
		{"trailing-separator", project + sep, []string{project}},
		{"trailing-dot", project + sep + "." + sep, []string{project}},
		{"parent-segment", filepath.Join(project, "sub") + sep + ".." + sep + ".", []string{project}},
		{"repeated-separator", root + sep + sep + "project", []string{project}},
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(project, link); err == nil {
		rows = append(rows,
			struct {
				name  string
				input string
				want  []string
			}{"symlink-spelling-keeps-both-spellings", link, []string{link, project}},
			struct {
				name  string
				input string
				want  []string
			}{"symlink-with-dot-segments", link + sep + ".", []string{link, project}},
		)
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			got := projectTrustKeys([]string{row.input})
			if !slices.Equal(got, row.want) {
				t.Fatalf("keys %v, want %v", got, row.want)
			}
		})
	}
	// The raw spelling never reaches the file; the same normalized keys come
	// from every equivalent request, so one home records one entry.
	home := filepath.Join(t.TempDir(), "codex")
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{project + sep + "." + sep, project, project + sep} {
		if err := isolateHomeConfig(home, projectTrustKeys([]string{input})); err != nil {
			t.Fatal(err)
		}
	}
	projects := readIsolationDocument(t, home)["projects"].(map[string]any)
	if len(projects) != 1 || isolationProjectLevel(t, home, project) != "untrusted" {
		t.Fatalf("equivalent spellings produced %v", projects)
	}
}
