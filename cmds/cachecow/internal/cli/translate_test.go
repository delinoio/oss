// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"os"
	"reflect"
	"testing"
)

func TestTranslateRootBoundaries(t *testing.T) {
	sep := string(os.PathListSeparator)
	tr := rootTranslation{from: "/t/repo", to: "/t/src"}
	for _, tc := range []struct{ name, in, want string }{
		{"equal", "/t/repo", "/t/src"},
		{"child", "/t/repo/x", "/t/src/x"},
		{"list", "/a" + sep + "/t/repo/bin" + sep + "/b", "/a" + sep + "/t/src/bin" + sep + "/b"},
		{"list end", "/a" + sep + "/t/repo", "/a" + sep + "/t/src"},
		{"list root then entry", "/t/repo" + sep + "/t/repo/bin", "/t/src" + sep + "/t/src/bin"},
		{"string prefix sibling", "/t/repo-wt/x", "/t/repo-wt/x"},
		{"string prefix suffix", "/t/repository", "/t/repository"},
		{"longer leading path", "/a/t/repo/x", "/a/t/repo/x"},
		{"flag value", "--manifest-path=/t/repo/Cargo.toml", "--manifest-path=/t/src/Cargo.toml"},
		{"direnv marker", "-/t/repo", "-/t/src"},
		{"sibling then real", "/t/repo-wt/x" + sep + "/t/repo/y", "/t/repo-wt/x" + sep + "/t/src/y"},
		{"sibling then real in one value", "/t/repo-wt/x/t/repo", "/t/repo-wt/x/t/repo"},
		{"unrelated", "release", "release"},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tr.value(tc.in); got != tc.want {
				t.Fatalf("value(%q) = %q want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestTranslateEnvAndArgs(t *testing.T) {
	tr := rootTranslation{from: "/t/repo", to: "/t/src"}
	env := []string{"DIRENV_DIR=-/t/repo", "PATH=/usr/bin" + string(os.PathListSeparator) + "/t/repo/node_modules/.bin", "/t/repo=/t/repo", "X=1", "EMPTY=", "NOEQUALS"}
	got := tr.env(env)
	want := []string{"DIRENV_DIR=-/t/src", "PATH=/usr/bin" + string(os.PathListSeparator) + "/t/src/node_modules/.bin", "/t/repo=/t/src", "X=1", "EMPTY=", "NOEQUALS"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("env %q want %q", got, want)
	}
	if env[0] != "DIRENV_DIR=-/t/repo" {
		t.Fatal("input mutated")
	}
	argv := []string{"cargo", "build", "--target-dir", "/t/repo/target", "--manifest-path=/t/repo/Cargo.toml", "/t/repo-wt"}
	gotArgs := tr.args(argv)
	wantArgs := []string{"cargo", "build", "--target-dir", "/t/src/target", "--manifest-path=/t/src/Cargo.toml", "/t/repo-wt"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args %q want %q", gotArgs, wantArgs)
	}
	same := rootTranslation{from: "/t/repo", to: "/t/repo"}
	if !reflect.DeepEqual(same.args(argv), argv) {
		t.Fatal("identity translation changed argv")
	}
}
