// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// isolationCell is one cell of the private-home state table: a config shape, a
// profile selection, a plugin source and a path spelling mapped to the document
// that must result.
type isolationCell struct {
	name string
	// input is the private config.toml before isolation.
	input string
	// keys are the requested project keys.
	keys []string
	// foldCase selects the case-insensitive platform comparison.
	foldCase bool
	// invalid marks a shape that must be rejected without any change.
	invalid bool
	// check inspects the isolated document.
	check func(t *testing.T, document map[string]any)
}

func tableAt(t *testing.T, value any, path ...string) map[string]any {
	t.Helper()
	table, _ := value.(map[string]any)
	for _, name := range path {
		if table == nil {
			t.Fatalf("missing table before %q", name)
		}
		table, _ = table[name].(map[string]any)
	}
	return table
}

func noMCPAnywhere(t *testing.T, document map[string]any) {
	t.Helper()
	if _, present := document["mcp_servers"]; present {
		t.Fatal("top-level MCP table retained")
	}
	profiles, _ := document["profiles"].(map[string]any)
	for name, raw := range profiles {
		profile, _ := raw.(map[string]any)
		if _, present := profile["mcp_servers"]; present {
			t.Fatalf("profile %q retained an MCP table", name)
		}
		if features, _ := profile["features"].(map[string]any); features != nil {
			if _, present := features["plugins"]; present {
				t.Fatalf("profile %q retained a plugin feature override", name)
			}
		}
	}
	if tableAt(t, document, "features")["plugins"] != false {
		t.Fatal("plugin loading is not disabled")
	}
}

func isolationCells() []isolationCell {
	trustedAndUntrusted := func(trusted ...string) func(*testing.T, map[string]any) {
		return func(t *testing.T, document map[string]any) {
			t.Helper()
			projects := tableAt(t, document, "projects")
			for key, raw := range projects {
				want := "untrusted"
				if slices.Contains(trusted, key) {
					want = "trusted"
				}
				if got := raw.(map[string]any)["trust_level"]; got != want {
					t.Errorf("project %q trust %v, want %v", key, got, want)
				}
			}
		}
	}
	return []isolationCell{
		// Config shape x profile selection.
		{name: "empty-document", input: "", keys: []string{"/p"}, check: func(t *testing.T, d map[string]any) { noMCPAnywhere(t, d) }},
		{name: "top-level-mcp-table-no-profile", input: "[mcp_servers.foo]\ncommand = \"x\"\n", keys: []string{"/p"}, check: func(t *testing.T, d map[string]any) { noMCPAnywhere(t, d) }},
		{name: "top-level-mcp-scalar", input: "mcp_servers = 3\n", keys: []string{"/p"}, check: func(t *testing.T, d map[string]any) { noMCPAnywhere(t, d) }},
		{name: "selected-profile-mcp", input: "profile = \"review\"\nmodel = \"m\"\n[profiles.review]\nmodel = \"profile-model\"\n[profiles.review.mcp_servers.foo]\ncommand = \"x\"\n", keys: []string{"/p"}, check: func(t *testing.T, d map[string]any) {
			noMCPAnywhere(t, d)
			if d["profile"] != "review" || d["model"] != "m" || tableAt(t, d, "profiles", "review")["model"] != "profile-model" {
				t.Fatal("unrelated profile settings were not preserved")
			}
		}},
		{name: "unselected-profile-mcp", input: "profile = \"a\"\n[profiles.b.mcp_servers.foo]\ncommand = \"x\"\n", keys: []string{"/p"}, check: func(t *testing.T, d map[string]any) { noMCPAnywhere(t, d) }},
		{name: "selected-profile-names-missing-profile", input: "profile = \"ghost\"\n[profiles.other.mcp_servers.foo]\ncommand = \"x\"\n", keys: []string{"/p"}, check: func(t *testing.T, d map[string]any) { noMCPAnywhere(t, d) }},
		{name: "every-profile-and-top-level-mcp", input: "profile = \"a\"\n[mcp_servers.t]\ncommand = \"x\"\n[profiles.a.mcp_servers.one]\ncommand = \"x\"\n[profiles.b.mcp_servers.two]\ncommand = \"x\"\n[profiles.c]\nmodel = \"m\"\n", keys: []string{"/p"}, check: func(t *testing.T, d map[string]any) { noMCPAnywhere(t, d) }},
		{name: "profile-mcp-scalar", input: "[profiles.a]\nmcp_servers = 1\n", keys: []string{"/p"}, check: func(t *testing.T, d map[string]any) { noMCPAnywhere(t, d) }},
		{name: "profiles-scalar", input: "profiles = 3\n", keys: []string{"/p"}, invalid: true},
		{name: "profile-entry-scalar", input: "[profiles]\na = 3\n", keys: []string{"/p"}, invalid: true},
		// Plugin source.
		{name: "profile-features-scalar", input: "[profiles.a]\nfeatures = 3\n", keys: []string{"/p"}, invalid: true},
		{name: "plugin-feature-absent", input: "model = \"m\"\n", keys: []string{"/p"}, check: func(t *testing.T, d map[string]any) { noMCPAnywhere(t, d) }},
		{name: "plugin-feature-enabled-top-level", input: "[features]\nplugins = true\nother = true\n", keys: []string{"/p"}, check: func(t *testing.T, d map[string]any) {
			noMCPAnywhere(t, d)
			if tableAt(t, d, "features")["other"] != true {
				t.Fatal("unrelated feature lost")
			}
		}},
		{name: "plugin-feature-enabled-in-selected-profile", input: "profile = \"a\"\n[profiles.a.features]\nplugins = true\nother = true\n", keys: []string{"/p"}, check: func(t *testing.T, d map[string]any) {
			noMCPAnywhere(t, d)
			if tableAt(t, d, "profiles", "a", "features")["other"] != true {
				t.Fatal("unrelated profile feature lost")
			}
		}},
		{name: "enabled-plugin-entry-is-inert-with-feature-off", input: "[plugins.\"x@y\"]\nenabled = true\n", keys: []string{"/p"}, check: func(t *testing.T, d map[string]any) {
			noMCPAnywhere(t, d)
			if tableAt(t, d, "plugins", "x@y")["enabled"] != true {
				t.Fatal("plugin entry was rewritten instead of disabled through the feature")
			}
		}},
		{name: "features-scalar", input: "features = 3\n", keys: []string{"/p"}, invalid: true},
		// Path spelling.
		{name: "existing-trusted-key-with-dot-segments", input: "[projects.\"/w/p/./\"]\ntrust_level = \"trusted\"\n[projects.\"/w/other\"]\ntrust_level = \"trusted\"\n", keys: []string{"/w/p"}, check: trustedAndUntrusted("/w/other")},
		{name: "existing-trusted-key-with-trailing-separator", input: "[projects.\"/w/p/\"]\ntrust_level = \"trusted\"\n", keys: []string{"/w/p"}, check: trustedAndUntrusted()},
		{name: "existing-trusted-key-differs-by-case-on-case-sensitive-platform", input: "[projects.\"/W/P\"]\ntrust_level = \"trusted\"\n", keys: []string{"/w/p"}, check: trustedAndUntrusted("/W/P")},
		{name: "existing-trusted-key-differs-by-case-on-case-insensitive-platform", input: "[projects.\"/W/P\"]\ntrust_level = \"trusted\"\n", keys: []string{"/w/p"}, foldCase: true, check: trustedAndUntrusted()},
		{name: "existing-entry-keeps-unrelated-fields", input: "[projects.\"/w/p/\"]\ntrust_level = \"trusted\"\nextra = 3\n", keys: []string{"/w/p"}, check: func(t *testing.T, d map[string]any) {
			if tableAt(t, d, "projects", "/w/p/")["extra"] != int64(3) {
				t.Fatal("unrelated project field lost")
			}
		}},
		{name: "existing-project-entry-scalar", input: "[projects]\n\"/w/p\" = 3\n", keys: []string{"/w/p"}, invalid: true},
		{name: "existing-equivalent-project-entry-scalar", input: "[projects]\n\"/w/p/\" = 3\n", keys: []string{"/w/p"}, invalid: true},
	}
}

// isolateCell runs one cell under the comparison of its platform.
func isolateCell(document map[string]any, cell isolationCell) (bool, error) {
	previous := projectKeysFoldCase
	projectKeysFoldCase = cell.foldCase
	defer func() { projectKeysFoldCase = previous }()
	return isolateHomeDocument(document, cell.keys)
}

func TestIsolationStateTableCells(t *testing.T) {
	for _, cell := range isolationCells() {
		t.Run(cell.name, func(t *testing.T) {
			document := map[string]any{}
			if err := toml.Unmarshal([]byte(cell.input), &document); err != nil {
				t.Fatal(err)
			}
			changed, err := isolateCell(document, cell)
			if cell.invalid {
				if err == nil {
					t.Fatal("invalid shape accepted")
				}
				return
			}
			if err != nil || !changed {
				t.Fatalf("changed=%v err=%v", changed, err)
			}
			for _, key := range cell.keys {
				if tableAt(t, document, "projects", key)["trust_level"] != "untrusted" {
					t.Fatalf("requested key %q is not untrusted", key)
				}
			}
			cell.check(t, document)
			// A second pass over the encoded result changes nothing.
			var encoded bytes.Buffer
			if err := toml.NewEncoder(&encoded).Encode(document); err != nil {
				t.Fatal(err)
			}
			again := map[string]any{}
			if err := toml.Unmarshal(encoded.Bytes(), &again); err != nil {
				t.Fatal(err)
			}
			if changed, err := isolateCell(again, cell); err != nil || changed {
				t.Fatalf("isolation is not idempotent: changed=%v err=%v", changed, err)
			}
		})
	}
}

func TestIsolateHomeConfigAppliesEveryCellThroughTheFile(t *testing.T) {
	for _, cell := range isolationCells() {
		if cell.invalid || cell.foldCase {
			continue
		}
		t.Run(cell.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "codex")
			if err := security.PrivateDir(home); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cell.input), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := isolateHomeConfig(home, cell.keys); err != nil {
				t.Fatal(err)
			}
			cell.check(t, readIsolationDocument(t, home))
		})
	}
	// The invalid cells leave the file untouched.
	for _, cell := range isolationCells() {
		if !cell.invalid {
			continue
		}
		t.Run("rejects/"+cell.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "codex")
			if err := security.PrivateDir(home); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, "config.toml")
			if err := os.WriteFile(path, []byte(cell.input), 0o600); err != nil {
				t.Fatal(err)
			}
			if isolateHomeConfig(home, cell.keys) == nil {
				t.Fatal("invalid shape accepted")
			}
			if raw, _ := os.ReadFile(path); string(raw) != cell.input {
				t.Fatal("invalid configuration was overwritten")
			}
		})
	}
}
