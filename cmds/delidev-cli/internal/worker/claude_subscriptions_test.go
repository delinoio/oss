// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

func TestNativeClaudeProfileExclusiveOriginalMachineOwnership(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	config := Config{Root: root}
	credential := Credential{ServerID: domain.NewID(), MachineID: domain.NewID()}
	account, profile := domain.NewID(), domain.NewID()
	installation := domain.Installation{Harness: domain.ClaudeCode, Version: domain.ClaudeProtocolVersion, ResolvedPath: executable}
	first, err := openClaudeProfile(config, credential, account, profile, installation, domain.NewID(), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = openClaudeProfile(config, credential, account, profile, installation, domain.NewID(), false); err == nil {
		t.Fatal("two native owners acquired one profile")
	}
	other, err := openClaudeProfile(config, credential, domain.NewID(), domain.NewID(), installation, domain.NewID(), true)
	if err != nil {
		t.Fatal("independent accounts shared an exclusive owner", err)
	}
	if first.home == other.home {
		t.Fatal("native profiles overlap")
	}
	if err = other.close(); err != nil {
		t.Fatal(err)
	}
	if err = first.close(); err != nil {
		t.Fatal(err)
	}
	foreign := credential
	foreign.MachineID = domain.NewID()
	if _, err = openClaudeProfile(config, foreign, account, profile, installation, domain.NewID(), false); err == nil {
		t.Fatal("another machine adopted native authentication")
	}
	if _, err = openClaudeProfile(config, credential, account, profile, installation, domain.NewID(), true); err == nil {
		t.Fatal("duplicate login replaced the original profile")
	}
	original, err := openClaudeProfile(config, credential, account, profile, installation, domain.NewID(), false)
	if err != nil {
		t.Fatal(err)
	}
	if err = subscription.CleanupRuntime(original.root, original.original); err != nil {
		t.Fatal(err)
	}
	if err = original.close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(original.root); !os.IsNotExist(err) {
		t.Fatal("owned cleanup did not remove whole native profile", err)
	}
	if _, err = os.Stat(other.root); err != nil {
		t.Fatal("cleanup removed an independent profile", err)
	}
}
