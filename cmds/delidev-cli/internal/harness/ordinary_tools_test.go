// SPDX-License-Identifier: Apache-2.0
package harness

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/executionenv"
	"path/filepath"
	"strings"
	"testing"
)

func TestOrdinaryGhPrivateRuntimeRetainsAuxiliaryIsolation(t *testing.T) {
	original := t.TempDir()
	t.Setenv("GH_CONFIG_DIR", original)
	t.Setenv("GH_TOKEN", "synthetic-ambient-token")
	t.Setenv("GITHUB_TOKEN", "synthetic-ambient-token")
	context := executionenv.Current()
	env, err := PrivateRuntimeEnvironment(filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	for _, denied := range []string{"GH_CONFIG_DIR=", "GH_TOKEN=", "GITHUB_TOKEN=", original, "synthetic-ambient-token"} {
		if strings.Contains(joined, denied) {
			t.Fatal("shared discovery/login/auxiliary runtime inherited gh authority")
		}
	}
	if !strings.Contains(strings.Join(context.Apply(env), "\n"), "GH_CONFIG_DIR="+original) {
		t.Fatal("ordinary context was lost after private reconstruction")
	}
}
