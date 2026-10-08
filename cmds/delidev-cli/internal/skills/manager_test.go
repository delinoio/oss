// SPDX-License-Identifier: Apache-2.0
package skills

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fixture(t *testing.T) (Manager, domain.SkillReadRequest, string) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, ".agents", "skills", "add-issue")
	if e := os.MkdirAll(path, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("---\nname: add-issue\ndescription: A harmless fixture\n---\nOriginal marker\n"), 0600); e != nil {
		t.Fatal(e)
	}
	return Manager{Root: t.TempDir(), Home: home}, domain.SkillReadRequest{MachineID: domain.NewID(), AgentID: domain.NewID(), ActorID: domain.NewID(), WorkerDeviceID: domain.NewID(), WorkerInstanceID: domain.NewID(), AgentRevision: 1}, path
}
func selectEntry(scope domain.SkillReadRequest, entry domain.SkillEntry) domain.SkillReadRequest {
	scope.Selections = []domain.SkillBinding{{InventoryID: entry.InventoryID, SkillID: entry.SkillID, ContentRevision: entry.ContentRevision, SnapshotID: domain.NewID(), WorkerDeviceID: entry.WorkerDeviceID}}
	return scope
}
func TestSnapshotRetainsCompleteOriginalPackageAndExactRetry(t *testing.T) {
	m, scope, path := fixture(t)
	ctx := context.Background()
	if e := os.WriteFile(filepath.Join(path, "resource.txt"), []byte("original resource"), 0600); e != nil {
		t.Fatal(e)
	}
	result, e := m.List(ctx, scope, nil)
	if e != nil || len(result.Entries) != 1 {
		t.Fatal(result, e)
	}
	scope = selectEntry(scope, result.Entries[0])
	if e = m.Prepare(ctx, scope); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(path, "resource.txt"), []byte("changed resource"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = m.Prepare(ctx, scope); e != nil {
		t.Fatal("exact retry changed snapshot", e)
	}
	selected, e := m.CopyToRuntime(ctx, scope.Selections, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(filepath.Dir(selected[0].Path), "resource.txt"))
	if e != nil || string(b) != "original resource" {
		t.Fatal(string(b), e)
	}
	if e = os.WriteFile(filepath.Join(snapshotPath(m.Root, scope.Selections[0].SnapshotID), string(scope.Selections[0].SkillID), "resource.txt"), []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Resolve(ctx, scope.Selections); e == nil {
		t.Fatal("corruption accepted")
	}
}
func TestInventoryContextAndSourceChangesRequireReselection(t *testing.T) {
	for _, kind := range []string{"actor", "worker", "instance", "agent", "revision", "session", "source"} {
		t.Run(kind, func(t *testing.T) {
			m, scope, path := fixture(t)
			ctx := context.Background()
			result, e := m.List(ctx, scope, nil)
			if e != nil {
				t.Fatal(e)
			}
			scope = selectEntry(scope, result.Entries[0])
			switch kind {
			case "actor":
				scope.ActorID = domain.NewID()
			case "worker":
				scope.MachineID = domain.NewID()
			case "instance":
				scope.WorkerInstanceID = domain.NewID()
			case "agent":
				scope.AgentID = domain.NewID()
			case "revision":
				scope.AgentRevision++
			case "session":
				scope.SessionID = domain.NewID()
			case "source":
				os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("changed"), 0600)
			}
			if e = m.Prepare(ctx, scope); e == nil {
				t.Fatal("stale context accepted")
			}
		})
	}
}
func TestDisabledAndSameNameProvenanceRemainDistinct(t *testing.T) {
	m, scope, path := fixture(t)
	project := t.TempDir()
	packagePath := filepath.Join(project, ".agents", "skills", "other")
	os.MkdirAll(packagePath, 0700)
	os.WriteFile(filepath.Join(packagePath, "SKILL.md"), []byte("---\nname: add-issue\ndescription: Project package\n---\nproject\n"), 0600)
	scope.SessionID = domain.NewID()
	result, e := m.List(context.Background(), scope, []string{project})
	if e != nil || len(result.Entries) != 2 || result.Entries[0].SkillID == result.Entries[1].SkillID || result.Entries[0].Provenance == result.Entries[1].Provenance {
		t.Fatal(result, e)
	}
	os.MkdirAll(filepath.Join(m.Home, ".codex"), 0700)
	os.WriteFile(filepath.Join(m.Home, ".codex", "config.toml"), []byte("[[skills.config]]\npath = '"+filepath.ToSlash(filepath.Join(path, "SKILL.md"))+"'\nenabled = false\n"), 0600)
	result, e = m.List(context.Background(), scope, nil)
	if e != nil || len(result.Entries) != 0 {
		t.Fatal(result, e)
	}
}
func TestPackagesRejectEscapingLinksAndBounds(t *testing.T) {
	m, scope, path := fixture(t)
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("secret"), 0600)
	if e := os.Symlink(outside, filepath.Join(path, "escape")); e != nil {
		t.Skip("symlinks unavailable", e)
	}
	if _, e := m.List(context.Background(), scope, nil); e == nil {
		t.Fatal("escaping link accepted")
	}
	os.Remove(filepath.Join(path, "escape"))
	os.WriteFile(filepath.Join(path, "large"), []byte(strings.Repeat("x", MaxPackageBytes+1)), 0600)
	if _, e := m.List(context.Background(), scope, nil); e == nil {
		t.Fatal("oversized package accepted")
	}
}
func TestInternalResourceLinkCopiesContent(t *testing.T) {
	m, scope, path := fixture(t)
	os.WriteFile(filepath.Join(path, "resource"), []byte("internal"), 0600)
	if e := os.Symlink("resource", filepath.Join(path, "linked")); e != nil {
		t.Skip(e)
	}
	ctx := context.Background()
	result, e := m.List(ctx, scope, nil)
	if e != nil {
		t.Fatal(e)
	}
	scope = selectEntry(scope, result.Entries[0])
	if e = m.Prepare(ctx, scope); e != nil {
		t.Fatal(e)
	}
	selected, e := m.Resolve(ctx, scope.Selections)
	if e != nil {
		t.Fatal(e)
	}
	info, e := os.Lstat(filepath.Join(filepath.Dir(selected[0].Path), "linked"))
	if e != nil || !info.Mode().IsRegular() {
		t.Fatal("link was not snapshotted as owned content", e)
	}
}

func TestExecutablePackageModeSurvivesEveryCopyAndIsProved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	m, scope, source := fixture(t)
	script := filepath.Join(source, "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf executable-fixture\n"), 0755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	inventory, err := m.List(ctx, scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	scope = selectEntry(scope, inventory.Entries[0])
	if err = m.Prepare(ctx, scope); err != nil {
		t.Fatal(err)
	}
	execute := func(root string) {
		t.Helper()
		path := filepath.Join(root, "run.sh")
		info, e := os.Stat(path)
		if e != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("executable mode: %v %v", info, e)
		}
		b, e := exec.Command(path).Output()
		if e != nil || string(b) != "executable-fixture" {
			t.Fatalf("execute: %q %v", b, e)
		}
		info, e = os.Stat(filepath.Join(root, "SKILL.md"))
		if e != nil || info.Mode().Perm() != 0600 {
			t.Fatal("non-executable mode changed", e)
		}
	}
	execute(filepath.Join(snapshotPath(m.Root, scope.Selections[0].SnapshotID), string(scope.Selections[0].SkillID)))
	home := t.TempDir()
	selected, err := m.CopyToRuntime(ctx, scope.Selections, home)
	if err != nil {
		t.Fatal(err)
	}
	execute(filepath.Dir(selected[0].Path))
	child, _, err := CloneRuntimePackage(ctx, home, selected[0].Path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	execute(filepath.Dir(child))
	if err = os.Chmod(filepath.Join(filepath.Dir(child), "run.sh"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = VerifyRuntimePackage(ctx, filepath.Dir(filepath.Dir(filepath.Dir(child))), child); err == nil {
		t.Fatal("changed executable mode accepted")
	}
}
