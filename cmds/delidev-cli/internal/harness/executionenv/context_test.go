// SPDX-License-Identifier: Apache-2.0
package executionenv

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

func TestOrdinaryGhPrecedenceAndOriginalDirectory(t *testing.T) {
	cwd := t.TempDir()
	for _, test := range []struct {
		name, goos string
		env        []string
		want       string
	}{
		{"explicit", "linux", []string{"GH_CONFIG_DIR=relative/gh", "XDG_CONFIG_HOME=ignored", "HOME=ignored"}, filepath.Join(cwd, "relative", "gh")},
		{"xdg", "linux", []string{"XDG_CONFIG_HOME=relative/config", "HOME=ignored"}, filepath.Join(cwd, "relative", "config", "gh")},
		{"windows-xdg", "windows", []string{"XDG_CONFIG_HOME=config", "AppData=ignored", "USERPROFILE=ignored"}, filepath.Join(cwd, "config", "gh")},
		{"windows-appdata", "windows", []string{"AppData=appdata", "USERPROFILE=ignored"}, filepath.Join(cwd, "appdata", "GitHub CLI")},
		{"windows-userhome", "windows", []string{"USERPROFILE=profile", "HOME=ignored"}, filepath.Join(cwd, "profile", ".config", "gh")},
		{"home", "linux", []string{"HOME=home", "APPDATA=ignored"}, filepath.Join(cwd, "home", ".config", "gh")},
		{"missing", "linux", nil, ""},
		{"invalid", "linux", []string{"GH_CONFIG_DIR=bad\nvalue", "HOME=ignored"}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := Resolve(test.goos, test.env, cwd)
			if c.ghDirectory != test.want {
				t.Fatal("configuration precedence or anchoring changed")
			}
			env := c.Apply([]string{"HOME=private", "CODEX_HOME=private/codex", "GH_TOKEN=foreign", "GITHUB_TOKEN=foreign", "GH_CONFIG_DIR=foreign"})
			want := []string{"HOME=private", "CODEX_HOME=private/codex"}
			if test.want != "" {
				want = append(want, "GH_CONFIG_DIR="+test.want)
			}
			if !reflect.DeepEqual(env, want) {
				t.Fatal("tool selector widened environment")
			}
			raw, _ := json.Marshal(c)
			if string(raw) != "{}" {
				t.Fatal("internal selector serialized")
			}
		})
	}
}
func TestOrdinaryGhWorkersNeverShareContext(t *testing.T) {
	local, remote := t.TempDir(), t.TempDir()
	a := Resolve("linux", []string{"HOME=" + local}, local)
	b := Resolve("linux", []string{"HOME=" + remote}, remote)
	if a.ghDirectory == b.ghDirectory || b.ghDirectory != filepath.Join(remote, ".config", "gh") {
		t.Fatal("remote Worker inherited desktop context")
	}
}
