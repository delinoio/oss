// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestValidateManagedAuthenticationHomeRequiresWorkspaceSeparation(t *testing.T) {
	root := t.TempDir()
	runtimeHome := filepath.Join(root, "runtime", "codex")
	workspaceRoot := filepath.Join(root, "workspace")
	if err := os.MkdirAll(runtimeHome, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspaceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := validateManagedAuthenticationHome(runtimeHome, []string{workspaceRoot}); err != nil {
		t.Fatalf("separate runtime and workspace were rejected: %v", err)
	}

	if err := validateManagedAuthenticationHome(runtimeHome, []string{root}); err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("runtime under a workspace ancestor was accepted: %v", err)
	}
}

func TestValidateManagedAuthenticationHomeRejectsAliases(t *testing.T) {
	root := t.TempDir()
	realHome := filepath.Join(root, "runtime", "codex")
	workspaceRoot := filepath.Join(root, "workspace")
	if err := os.MkdirAll(realHome, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspaceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(realHome, alias); err != nil {
		t.Fatal(err)
	}
	if err := validateManagedAuthenticationHome(alias, []string{workspaceRoot}); err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("aliased managed authentication home was accepted: %v", err)
	}
}
