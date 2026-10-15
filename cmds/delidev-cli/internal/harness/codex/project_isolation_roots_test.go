// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// rootMarkerCell is one state of the project_root_markers dimension of the
// private home: how the markers are configured.
type rootMarkerCell struct {
	name string
	// input is the private config.toml.
	input string
	// markers are the markers the configuration selects (the default Git marker
	// is not listed), or invalid when the shape must be rejected.
	markers []string
	invalid bool
}

func rootMarkerCells() []rootMarkerCell {
	return []rootMarkerCell{
		{name: "absent-selects-the-default", input: "model = \"m\"\n"},
		{name: "default-spelled-out", input: "project_root_markers = [\".git\"]\n", markers: []string{".git"}},
		{name: "custom", input: "project_root_markers = [\"MARK_A\"]\n", markers: []string{"MARK_A"}},
		{name: "multiple", input: "project_root_markers = [\"MARK_A\", \"MARK_B\"]\n", markers: []string{"MARK_A", "MARK_B"}},
		{name: "duplicates-collapse", input: "project_root_markers = [\"MARK_A\", \"MARK_A\"]\n", markers: []string{"MARK_A"}},
		{name: "custom-with-git", input: "project_root_markers = [\".git\", \"MARK_A\"]\n", markers: []string{".git", "MARK_A"}},
		{name: "empty-selects-no-marker", input: "project_root_markers = []\n"},
		{name: "profile-level", input: "[profiles.p]\nproject_root_markers = [\"MARK_A\"]\n", markers: []string{"MARK_A"}},
		{name: "top-level-and-profiles-union", input: "project_root_markers = [\"MARK_A\"]\n[profiles.p]\nproject_root_markers = [\"MARK_B\", \"MARK_A\"]\n[profiles.q]\nmodel = \"m\"\n", markers: []string{"MARK_A", "MARK_B"}},
		{name: "scalar", input: "project_root_markers = \"MARK_A\"\n", invalid: true},
		{name: "table", input: "[project_root_markers]\nMARK_A = true\n", invalid: true},
		{name: "non-string-entry", input: "project_root_markers = [\"MARK_A\", 3]\n", invalid: true},
		{name: "empty-entry", input: "project_root_markers = [\"\"]\n", invalid: true},
		{name: "dot-entry", input: "project_root_markers = [\".\"]\n", invalid: true},
		{name: "parent-entry", input: "project_root_markers = [\"..\"]\n", invalid: true},
		{name: "separator-entry", input: "project_root_markers = [\"a/b\"]\n", invalid: true},
		{name: "profile-scalar", input: "[profiles.p]\nproject_root_markers = 1\n", invalid: true},
		{name: "profiles-scalar", input: "profiles = 1\n", invalid: true},
	}
}

func TestConfiguredRootMarkersCells(t *testing.T) {
	for _, cell := range rootMarkerCells() {
		t.Run(cell.name, func(t *testing.T) {
			document := map[string]any{}
			if err := toml.Unmarshal([]byte(cell.input), &document); err != nil {
				t.Fatal(err)
			}
			markers, err := configuredRootMarkers(document)
			if cell.invalid {
				if err == nil {
					t.Fatalf("invalid shape accepted: %v", markers)
				}
				return
			}
			if err != nil || !slices.Equal(markers, cell.markers) {
				t.Fatalf("markers %v err %v, want %v", markers, err, cell.markers)
			}
		})
	}
}

// markerTree is the directory layout of the marker position cells:
//
//	base/                      (no marker)
//	  outer/       MARK_OUTER, MARK_A
//	    inner/     MARK_INNER, MARK_A
//	      cwd/     MARK_AT
//	        below/ MARK_BELOW, MARK_A
type markerTree struct {
	base, outer, inner, cwd, below string
}

func newMarkerTree(t *testing.T) markerTree {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tree := markerTree{base: base}
	tree.outer = filepath.Join(base, "outer")
	tree.inner = filepath.Join(tree.outer, "inner")
	tree.cwd = filepath.Join(tree.inner, "cwd")
	tree.below = filepath.Join(tree.cwd, "below")
	if err := os.MkdirAll(tree.below, 0o700); err != nil {
		t.Fatal(err)
	}
	for directory, names := range map[string][]string{
		tree.outer: {"MARK_OUTER", "MARK_A"},
		tree.inner: {"MARK_INNER", "MARK_A"},
		tree.cwd:   {"MARK_AT"},
		tree.below: {"MARK_BELOW", "MARK_A"},
	} {
		for _, name := range names {
			if err := os.WriteFile(filepath.Join(directory, name), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return tree
}

// TestProjectTrustKeysForRootMarkerPositionCells maps the marker configuration
// and the marker position relative to the working directory to the keys that
// must be untrusted. A marker above the directory locates a root that Codex
// can hold trusted; a marker at the directory adds no key; a marker only below
// the directory locates no root of it.
func TestProjectTrustKeysForRootMarkerPositionCells(t *testing.T) {
	tree := newMarkerTree(t)
	rows := []struct {
		name    string
		markers []string
		want    []string
	}{
		{"default-markers", nil, []string{tree.cwd}},
		{"git-only", []string{".git"}, []string{tree.cwd}},
		{"empty-list", []string{}, []string{tree.cwd}},
		{"marker-above-at-nearest", []string{"MARK_INNER"}, []string{tree.cwd, tree.inner}},
		{"marker-above-farther", []string{"MARK_OUTER"}, []string{tree.cwd, tree.outer}},
		{"marker-shared-by-several-ancestors-seeds-every-one", []string{"MARK_A"}, []string{tree.cwd, tree.inner, tree.outer}},
		{"multiple-markers-nearest-first", []string{"MARK_OUTER", "MARK_INNER"}, []string{tree.cwd, tree.inner, tree.outer}},
		{"marker-at-the-directory", []string{"MARK_AT"}, []string{tree.cwd}},
		{"marker-only-below-the-directory", []string{"MARK_BELOW"}, []string{tree.cwd}},
		{"marker-nowhere", []string{"MARK_NOWHERE"}, []string{tree.cwd}},
		{"git-with-custom", []string{".git", "MARK_INNER"}, []string{tree.cwd, tree.inner}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if got := projectTrustKeysFor([]string{tree.cwd}, row.markers); !slices.Equal(got, row.want) {
				t.Fatalf("keys %v, want %v", got, row.want)
			}
		})
	}
	t.Run("workspace-roots-contribute-their-own-ancestors", func(t *testing.T) {
		got := projectTrustKeysFor([]string{tree.cwd, tree.below}, []string{"MARK_OUTER"})
		if want := []string{tree.cwd, tree.outer, tree.below}; !slices.Equal(got, want) {
			t.Fatalf("keys %v, want %v", got, want)
		}
	})
	t.Run("default-wrapper-adds-no-marker-root", func(t *testing.T) {
		if got := projectTrustKeys([]string{tree.cwd}); !slices.Equal(got, []string{tree.cwd}) {
			t.Fatalf("keys %v", got)
		}
	})
	t.Run("git-root-and-marker-root-are-both-kept", func(t *testing.T) {
		repository := filepath.Join(tree.inner, "repo")
		nested := filepath.Join(repository, "pkg")
		if err := os.MkdirAll(filepath.Join(repository, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(nested, 0o700); err != nil {
			t.Fatal(err)
		}
		got := projectTrustKeysFor([]string{nested}, []string{"MARK_OUTER"})
		if want := []string{nested, repository, tree.outer}; !slices.Equal(got, want) {
			t.Fatalf("keys %v, want %v", got, want)
		}
	})
}

func seedRootsHome(t *testing.T, content string) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "codex")
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	if content != "" {
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// TestIsolateHomeDirectoriesRootMarkerCells applies every marker configuration
// to a private home that already trusts the root a marker locates, through the
// file entry point the thread binding uses.
func TestIsolateHomeDirectoriesRootMarkerCells(t *testing.T) {
	tree := newMarkerTree(t)
	trusted := func(directories ...string) string {
		var out strings.Builder
		for _, directory := range directories {
			fmt.Fprintf(&out, "[projects.%q]\ntrust_level = \"trusted\"\n", directory)
		}
		return out.String()
	}
	rows := []struct {
		name string
		// markers is the project_root_markers line of the private home.
		markers string
		// untrusted are the directories that must end untrusted; stays are the
		// trusted directories that must stay trusted.
		untrusted, stays []string
	}{
		{"absent-keeps-only-the-directory", "", []string{tree.cwd}, []string{tree.inner, tree.outer}},
		{"default-spelled-out", "project_root_markers = [\".git\"]\n", []string{tree.cwd}, []string{tree.inner, tree.outer}},
		{"empty-list", "project_root_markers = []\n", []string{tree.cwd}, []string{tree.inner, tree.outer}},
		{"custom-nearest", "project_root_markers = [\"MARK_INNER\"]\n", []string{tree.cwd, tree.inner}, []string{tree.outer}},
		{"custom-farther", "project_root_markers = [\"MARK_OUTER\"]\n", []string{tree.cwd, tree.outer}, []string{tree.inner}},
		{"custom-shared-by-ancestors", "project_root_markers = [\"MARK_A\"]\n", []string{tree.cwd, tree.inner, tree.outer}, nil},
		{"multiple", "project_root_markers = [\"MARK_OUTER\", \"MARK_INNER\"]\n", []string{tree.cwd, tree.inner, tree.outer}, nil},
		{"marker-only-below", "project_root_markers = [\"MARK_BELOW\"]\n", []string{tree.cwd}, []string{tree.inner, tree.outer}},
		{"profile-level", "[profiles.p]\nproject_root_markers = [\"MARK_INNER\"]\n", []string{tree.cwd, tree.inner}, []string{tree.outer}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			home := seedRootsHome(t, row.markers+trusted(tree.inner, tree.outer))
			if err := isolateHomeDirectories(home, []string{tree.cwd}); err != nil {
				t.Fatal(err)
			}
			for _, directory := range row.untrusted {
				if isolationProjectLevel(t, home, directory) != "untrusted" {
					t.Errorf("%s is not untrusted", directory)
				}
			}
			for _, directory := range row.stays {
				if isolationProjectLevel(t, home, directory) != "trusted" {
					t.Errorf("%s changed although no marker locates it", directory)
				}
			}
			// A second pass finds nothing to change.
			before, _ := os.ReadFile(filepath.Join(home, "config.toml"))
			if err := isolateHomeDirectories(home, []string{tree.cwd}); err != nil {
				t.Fatal(err)
			}
			if after, _ := os.ReadFile(filepath.Join(home, "config.toml")); string(after) != string(before) {
				t.Fatal("isolation is not idempotent")
			}
		})
	}
	for _, cell := range rootMarkerCells() {
		if !cell.invalid {
			continue
		}
		t.Run("rejects/"+cell.name, func(t *testing.T) {
			home := seedRootsHome(t, cell.input)
			if isolateHomeDirectories(home, []string{tree.cwd}) == nil {
				t.Fatal("invalid marker shape accepted")
			}
			if raw, _ := os.ReadFile(filepath.Join(home, "config.toml")); string(raw) != cell.input {
				t.Fatal("invalid configuration was overwritten")
			}
		})
	}
}

// TestManagedThreadIsolationSeedsRootsLocatedByConfiguredMarkers is the
// end-to-end row: the private home trusts the root that a configured marker
// locates and that root's own configuration defines an MCP server. Isolation
// must settle the root untrusted before the probe, so the thread starts.
func TestManagedThreadIsolationSeedsRootsLocatedByConfiguredMarkers(t *testing.T) {
	for _, resume := range []bool{false, true} {
		for _, row := range []struct {
			name string
			// markers is the project_root_markers line of the private home.
			markers string
			// accepted reports whether the configured markers locate the root.
			accepted bool
		}{
			{"custom-marker-above", "project_root_markers = [\"MARK_ROOT\"]\n", true},
			{"multiple-markers-above", "project_root_markers = [\"MARK_OTHER\", \"MARK_ROOT\"]\n", true},
			{"profile-level-marker-above", "[profiles.p]\nproject_root_markers = [\"MARK_ROOT\"]\n", true},
			// A layer outside the private home can select markers this adapter
			// cannot read; the probe still fails closed on the surviving server.
			{"marker-selected-outside-the-private-home", "", false},
		} {
			t.Run(fmt.Sprintf("%s/resume=%v", row.name, resume), func(t *testing.T) {
				var root string
				run := runManagedIsolationSeeded(t, "thread-managed-ready", resume, func(s *ThreadSettings) {
					base, err := filepath.EvalSymlinks(t.TempDir())
					if err != nil {
						t.Fatal(err)
					}
					root = filepath.Join(base, "workspace")
					s.Cwd = filepath.Join(root, "pkg", "inner")
					if err := os.MkdirAll(s.Cwd, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(root, "MARK_ROOT"), nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}, func(home string, _ ThreadSettings) []string {
					content := row.markers + fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", root)
					if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(content), 0o600); err != nil {
						t.Fatal(err)
					}
					return []string{"DELIDEV_CODEX_MARKER_ROOT=" + root}
				})
				if row.accepted {
					if run.err != nil {
						t.Fatalf("the located root stayed trusted: %v", run.err)
					}
					if isolationProjectLevel(t, run.home, root) != "untrusted" {
						t.Fatal("the root located by the configured marker was not isolated")
					}
					return
				}
				if run.err == nil || !strings.Contains(run.logs, "stage=mcp-inventory-preflight") || !strings.Contains(run.logs, "reason="+string(isolationInventoryNonEmpty)) {
					t.Fatalf("a trusted root with a server was accepted: %v %s", run.err, run.logs)
				}
				for _, entry := range run.capture {
					if entry["method"] == "thread/start" || entry["method"] == "thread/resume" || entry["method"] == "turn/start" {
						t.Fatalf("a native %v ran before the survivor was rejected", entry["method"])
					}
				}
			})
		}
	}
}
