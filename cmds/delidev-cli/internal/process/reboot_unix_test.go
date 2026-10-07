// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package process

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func rebootScope(t *testing.T) (string, Process, processScope) {
	t.Helper()
	boot, err := bootIdentity()
	if err != nil || boot == "" {
		t.Fatal("current boot identity unavailable", err)
	}
	root := t.TempDir()
	owner := domain.NewID()
	path := filepath.Join(root, string(owner), string(domain.NewID()))
	if err := security.PrivateDir(path); err != nil {
		t.Fatal(err)
	}
	scope := processScope{Version: 1, Boot: boot + "-previous", OwnerID: owner, Owner: Process{PID: 2147483647, Birth: "fixture-supervisor"}, Coalition: 17, Label: "fixture-supervisor", Domain: "fixture-domain", Started: true}
	if err := saveScope(path, scope); err != nil {
		t.Fatal(err)
	}
	identity := scope.Owner
	identity.ScopeDir, identity.OwnerID = path, owner
	return root, identity, scope
}

func TestRebootCompletionPersistsBeforeReleasedControllerPruning(t *testing.T) {
	root, identity, original := rebootScope(t)
	controller, err := security.TryLock(filepath.Join(identity.ScopeDir, "controller.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	for range 2 {
		if err := ReconcileProcess(identity); err != nil {
			t.Fatal(err)
		}
		saved, err := readScope(identity.ScopeDir)
		want := original
		want.Complete = true
		if err != nil || saved != want {
			t.Fatal("reboot proof was not persisted under the original ownership", err)
		}
	}
	if err := ReconcileOwner(root, identity.OwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(scopePath(identity.ScopeDir)); err != nil {
		t.Fatal("held controller lost its completed journal", err)
	}
	if err := controller.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileOwner(root, identity.OwnerID); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(identity.ScopeDir))
	if err != nil || len(entries) != 0 {
		t.Fatal("released reboot completion did not leave a retained empty owner index", err)
	}
	if err := ReconcileProcess(identity); err == nil {
		t.Fatal("pruned individual journal was accepted as completion proof")
	}
}

func TestRebootCompletionRefusesInvalidOriginalOwnership(t *testing.T) {
	for _, kind := range []string{"version", "owner", "pid", "birth", "boot", "malformed", "missing"} {
		t.Run(kind, func(t *testing.T) {
			_, identity, scope := rebootScope(t)
			switch kind {
			case "version":
				scope.Version = 2
			case "owner":
				scope.OwnerID = domain.NewID()
			case "pid":
				identity.PID--
			case "birth":
				identity.Birth = "foreign-supervisor"
			case "boot":
				scope.Boot = ""
			}
			if err := saveScope(identity.ScopeDir, scope); err != nil {
				t.Fatal(err)
			}
			path := scopePath(identity.ScopeDir)
			switch kind {
			case "malformed":
				if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "permissions":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(path)
			if err != nil && kind != "missing" {
				t.Fatal(err)
			}
			if err := ReconcileProcess(identity); err == nil {
				t.Fatal("invalid ownership acquired reboot completion")
			}
			after, err := os.ReadFile(path)
			if kind == "missing" {
				if !os.IsNotExist(err) {
					t.Fatal("missing journal was recreated", err)
				}
			} else if err != nil || !bytes.Equal(before, after) {
				t.Fatal("refused ownership was rewritten or removed", err)
			}
		})
	}
}

func TestRebootCompletionRequiresDurablePublication(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the fixture's directory write denial")
	}
	_, identity, original := rebootScope(t)
	if err := os.Chmod(identity.ScopeDir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(identity.ScopeDir, 0700) })
	if err := ReconcileProcess(identity); err == nil {
		t.Fatal("failed completion publication returned successful reconciliation")
	}
	saved, err := readScope(identity.ScopeDir)
	if err != nil || saved != original {
		t.Fatal("failed publication changed the retained original journal", err)
	}
	if err := os.Chmod(identity.ScopeDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileProcess(identity); err != nil {
		t.Fatal("reboot completion could not retry publication", err)
	}
}
