// SPDX-License-Identifier: Apache-2.0
package skills

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"path/filepath"
	"testing"
)

func managedFixture(t *testing.T) (Manager, domain.ManagedSkillRequest, string) {
	t.Helper()
	m := Manager{Root: t.TempDir()}
	source := t.TempDir()
	if e := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: fixture\ndescription: Fixture package\n---\nOriginal instructions\n"), 0600); e != nil {
		t.Fatal(e)
	}
	request := domain.ManagedSkillRequest{Action: domain.ManagedSkillImport, ServerID: domain.NewID(), ActorID: domain.NewID(), MachineID: domain.NewID(), WorkerDeviceID: domain.NewID(), WorkerInstanceID: domain.NewID(), RequestID: domain.NewID(), SourceDirectory: source}
	request.PackageID = request.RequestID
	return m, request, source
}
func TestManagedSkillIndependentCopyAndReplay(t *testing.T) {
	m, r, source := managedFixture(t)
	ctx := context.Background()
	first, e := m.Managed(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	if len(first.Packages) != 1 || !first.Packages[0].Enabled || first.Packages[0].Revision != 1 {
		t.Fatal(first)
	}
	if e = os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("changed"), 0600); e != nil {
		t.Fatal(e)
	}
	r.WorkerInstanceID = domain.NewID()
	replay, e := m.Managed(ctx, r)
	if e != nil || replay.Packages[0].ContentRevision != first.Packages[0].ContentRevision {
		t.Fatalf("%+v %v", replay, e)
	}
	view := r
	view.Action = domain.ManagedSkillView
	view.SourceDirectory = ""
	view.RequestID = domain.NewID()
	got, e := m.Managed(ctx, view)
	if e != nil || got.SkillMarkdown == "changed" {
		t.Fatalf("%+v %v", got, e)
	}
	view.Action = domain.ManagedSkillEnable
	view.ExpectedRevision = 1
	view.Enabled = false
	got, e = m.Managed(ctx, view)
	if e != nil || got.Packages[0].Enabled || got.Packages[0].Revision != 2 {
		t.Fatalf("%+v %v", got, e)
	}
	view.Action = domain.ManagedSkillDelete
	view.RequestID = domain.NewID()
	view.ExpectedRevision = 2
	got, e = m.Managed(ctx, view)
	if e != nil || len(got.Packages) != 0 {
		t.Fatalf("%+v %v", got, e)
	}
	if _, e = os.Stat(filepath.Join(source, "SKILL.md")); e != nil {
		t.Fatal("source deleted", e)
	}
	if _, e = m.Managed(ctx, view); e != nil {
		t.Fatal("delete replay", e)
	}
}
func TestManagedSkillRejectsChangedModesAndUnlistedDeletion(t *testing.T) {
	for _, change := range []string{"mode", "extra", "extra-directory"} {
		t.Run(change, func(t *testing.T) {
			m, r, _ := managedFixture(t)
			if _, e := m.Managed(context.Background(), r); e != nil {
				t.Fatal(e)
			}
			target := m.managedPath(r.PackageID)
			switch change {
			case "mode":
				os.Chmod(filepath.Join(target, "SKILL.md"), 0640)
			case "extra":
				os.WriteFile(filepath.Join(target, "unlisted"), []byte("retain"), 0600)
			case "extra-directory":
				os.Mkdir(filepath.Join(target, "unlisted"), 0700)
			}
			r.Action = domain.ManagedSkillDelete
			r.RequestID = domain.NewID()
			r.ExpectedRevision = 1
			r.SourceDirectory = ""
			if _, e := m.Managed(context.Background(), r); e == nil {
				t.Fatal("changed ownership accepted")
			}
			if _, e := os.Stat(target); e != nil {
				t.Fatal("changed root removed", e)
			}
		})
	}
}
func TestManagedSkillRejectsLinksAndDuplicateNames(t *testing.T) {
	m, r, source := managedFixture(t)
	if e := os.Symlink(filepath.Join(source, "SKILL.md"), filepath.Join(source, "linked")); e != nil {
		t.Skip(e)
	}
	if _, e := m.Managed(context.Background(), r); e == nil {
		t.Fatal("link accepted")
	}
	os.Remove(filepath.Join(source, "linked"))
	if _, e := m.Managed(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	r.RequestID = domain.NewID()
	r.PackageID = r.RequestID
	if _, e := m.Managed(context.Background(), r); e == nil {
		t.Fatal("duplicate name accepted")
	}
}

func TestManagedSkillSnapshotsAndForkRuntimeRemainIndependent(t *testing.T) {
	m, r, _ := managedFixture(t)
	ctx := context.Background()
	result, e := m.Managed(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	pkg := result.Packages[0]
	binding := domain.SkillBinding{WorkerDeviceID: r.WorkerDeviceID, InventoryID: r.PackageID, SkillID: r.PackageID, SnapshotID: domain.NewID(), ContentRevision: pkg.LegacyRevision, ManagedRevision: pkg.ContentRevision}
	scope := domain.SkillReadRequest{MachineID: r.MachineID, AgentID: domain.NewID(), ActorID: r.ServerID, AgentRevision: 1, WorkerDeviceID: r.WorkerDeviceID, WorkerInstanceID: r.WorkerInstanceID, Selections: []domain.SkillBinding{binding}}
	if e = m.Prepare(ctx, scope); e != nil {
		t.Fatal(e)
	}
	parent := t.TempDir()
	child := t.TempDir()
	original, e := m.ManagedRuntime(ctx, scope.Selections, parent)
	if e != nil {
		t.Fatal(e)
	}
	copied, e := CloneManagedRuntime(ctx, parent, child)
	if e != nil || len(copied) != 1 {
		t.Fatal(copied, e)
	}
	r.Action = domain.ManagedSkillEnable
	r.RequestID = domain.NewID()
	r.SourceDirectory = ""
	r.ExpectedRevision = 1
	r.Enabled = false
	if _, e = m.Managed(ctx, r); e != nil {
		t.Fatal(e)
	}
	if e = m.Prepare(ctx, domain.SkillReadRequest{MachineID: scope.MachineID, AgentID: scope.AgentID, ActorID: scope.ActorID, AgentRevision: 1, WorkerDeviceID: scope.WorkerDeviceID, WorkerInstanceID: scope.WorkerInstanceID, Selections: []domain.SkillBinding{{WorkerDeviceID: binding.WorkerDeviceID, InventoryID: binding.InventoryID, SkillID: binding.SkillID, SnapshotID: domain.NewID(), ContentRevision: binding.ContentRevision, ManagedRevision: binding.ManagedRevision}}}); e == nil {
		t.Fatal("disabled catalog became a future snapshot")
	}
	r.Action = domain.ManagedSkillDelete
	r.RequestID = domain.NewID()
	r.ExpectedRevision = 2
	if _, e = m.Managed(ctx, r); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Resolve(ctx, scope.Selections); e != nil {
		t.Fatal("catalog deletion changed snapshot", e)
	}
	if e = m.DeletePreparedSnapshot(ctx, r.MachineID, binding); e != nil {
		t.Fatal(e)
	}
	if e = VerifyRuntimePackage(ctx, parent, original[0].Path); e != nil {
		t.Fatal(e)
	}
	if e = os.RemoveAll(parent); e != nil {
		t.Fatal(e)
	}
	if e = VerifyRuntimePackage(ctx, child, copied[0].Path); e != nil {
		t.Fatal("Fork acquired parent lifetime", e)
	}
	if _, e = m.ManagedRuntime(ctx, scope.Selections, child); e != nil {
		t.Fatal("independent copied runtime depended on deleted snapshot", e)
	}
}

func TestManagedSkillInterruptedImportCancellationPreservesUnknownResources(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "original-empty", true: "unlisted"}[unknown], func(t *testing.T) {
			m, r, _ := managedFixture(t)
			ctx := context.Background()
			if _, e := m.Managed(ctx, r); e != nil {
				t.Fatal(e)
			}
			journal := filepath.Join(m.Root, "managed-skill-receipts", string(r.RequestID)+".json")
			raw, e := readBounded(journal, 512<<10)
			if e != nil {
				t.Fatal(e)
			}
			var receipt managedReceipt
			if domain.Decode(raw, &receipt) != nil {
				t.Fatal("invalid fixture receipt")
			}
			receipt.Complete = false
			if e = writeJSON(journal, receipt); e != nil {
				t.Fatal(e)
			}
			os.Remove(filepath.Join(m.managedCatalog(), string(r.PackageID)+".json"))
			os.Remove(filepath.Join(m.managedPath(r.PackageID), "SKILL.md"))
			if unknown {
				os.WriteFile(filepath.Join(m.managedPath(r.PackageID), "unlisted"), []byte("retain"), 0600)
			}
			r.Action = domain.ManagedSkillCancelImport
			r.RequestID = domain.NewID()
			r.SourceDirectory = ""
			_, e = m.Managed(ctx, r)
			if unknown {
				if e == nil {
					t.Fatal("unlisted cleanup accepted")
				}
				if _, e = os.Stat(filepath.Join(m.managedPath(r.PackageID), "unlisted")); e != nil {
					t.Fatal("unknown bytes deleted", e)
				}
			} else {
				if e != nil {
					t.Fatal(e)
				}
				if _, e = os.Stat(m.managedPath(r.PackageID)); !os.IsNotExist(e) {
					t.Fatal("original incomplete copy not removed", e)
				}
				if _, e = m.Managed(ctx, r); e != nil {
					t.Fatal("cancel replay", e)
				}
			}
		})
	}
}
func TestManagedSkillCancelRecoversCompleteReceiptWithoutDeletingPackage(t *testing.T) {
	m, r, _ := managedFixture(t)
	ctx := context.Background()
	imported, e := m.Managed(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	r.Action = domain.ManagedSkillCancelImport
	r.RequestID = domain.NewID()
	r.SourceDirectory = ""
	observed, e := m.Managed(ctx, r)
	if e != nil || len(observed.Packages) != 1 || observed.Packages[0].ContentRevision != imported.Packages[0].ContentRevision {
		t.Fatal(observed, e)
	}
	if _, e = os.Stat(m.managedPath(r.PackageID)); e != nil {
		t.Fatal("complete package removed as incomplete", e)
	}
}

func TestManagedSkillExplicitComposerInventoryAndFullModeCleanup(t *testing.T) {
	m, r, _ := managedFixture(t)
	m.Home = t.TempDir()
	ctx := context.Background()
	imported, e := m.Managed(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	pkg := imported.Packages[0]
	scope := domain.SkillReadRequest{MachineID: r.MachineID, AgentID: domain.NewID(), ActorID: r.ServerID, AgentRevision: 1, WorkerDeviceID: r.WorkerDeviceID, WorkerInstanceID: r.WorkerInstanceID, ManagedServerID: r.ServerID, ManagedPackages: []domain.ManagedSkillSelection{{PackageID: r.PackageID, WorkerDeviceID: r.WorkerDeviceID, ContentRevision: pkg.ContentRevision}}}
	inventory, e := m.List(ctx, scope, nil)
	if e != nil || len(inventory.Entries) != 1 {
		t.Fatal(inventory, e)
	}
	entry := inventory.Entries[0]
	if entry.ManagedRevision != pkg.ContentRevision || entry.Provenance != "managed" {
		t.Fatal("composer lost complete managed ownership", entry)
	}
	binding := domain.SkillBinding{ManagedRevision: entry.ManagedRevision, WorkerDeviceID: entry.WorkerDeviceID, InventoryID: entry.InventoryID, SkillID: entry.SkillID, ContentRevision: entry.ContentRevision, SnapshotID: domain.NewID()}
	scope.Selections = []domain.SkillBinding{binding}
	if e = m.Prepare(ctx, scope); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(snapshotPath(m.Root, binding.SnapshotID), string(binding.SkillID), "SKILL.md")
	if e = os.Chmod(target, 0640); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Resolve(ctx, scope.Selections); e == nil {
		t.Fatal("changed complete permissions accepted")
	}
	if e = m.DeletePreparedSnapshot(ctx, r.MachineID, binding); e == nil {
		t.Fatal("changed complete permissions deleted")
	}
	if _, e = os.Stat(target); e != nil {
		t.Fatal("uncertain bytes removed", e)
	}
}
