// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
	"os"
	"path/filepath"
	"testing"
)

func TestPreparationSessionDeletionRetiresOwnedSnapshot(t *testing.T) {
	config, work, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	home := t.TempDir()
	source := filepath.Join(home, ".agents", "skills", "fixture")
	if e := os.MkdirAll(source, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: fixture\ndescription: Original fixture\n---\nOriginal bytes\n"), 0600); e != nil {
		t.Fatal(e)
	}
	manager := skills.Manager{Root: config.Root, Home: home}
	scope := domain.SkillReadRequest{MachineID: work.MachineID, AgentID: domain.NewID(), ActorID: work.ServerID, WorkerDeviceID: work.DeviceID, WorkerInstanceID: work.Copies[0].InstanceID, AgentRevision: 1, SessionID: work.SessionID}
	listed, e := manager.List(context.Background(), scope, nil)
	if e != nil || len(listed.Entries) != 1 {
		t.Fatal(listed, e)
	}
	entry := listed.Entries[0]
	id := domain.NewID()
	binding := domain.SkillBinding{WorkerDeviceID: work.DeviceID, InventoryID: entry.InventoryID, SkillID: entry.SkillID, ContentRevision: entry.ContentRevision, SnapshotID: id}
	scope.Selections = []domain.SkillBinding{binding}
	if e = manager.Prepare(context.Background(), scope); e != nil {
		t.Fatal(e)
	}
	work.SkillSnapshots = []domain.SkillBinding{binding}
	proof, e := deleteSessionCopies(context.Background(), config, work)
	if e != nil || !proof.Complete {
		t.Fatal(proof, e)
	}
	active, e := os.ReadDir(filepath.Join(config.Root, "skill-preparations"))
	if e != nil || len(active) != 0 {
		t.Fatal("deletion retained capacity", active, e)
	}
	if e = manager.Prepare(context.Background(), scope); e == nil {
		t.Fatal("late original preparation resurrected")
	}
	target := filepath.Join(config.Root, "skill-snapshots", string(id))
	if e = os.MkdirAll(target, 0700); e != nil {
		t.Fatal(e)
	}
	replacement := filepath.Join(target, "replacement")
	os.WriteFile(replacement, []byte("preserved"), 0600)
	if _, e = deleteSessionCopies(context.Background(), config, work); e == nil {
		t.Fatal("completed replay adopted replacement")
	}
	if b, e := os.ReadFile(replacement); e != nil || string(b) != "preserved" {
		t.Fatal(string(b), e)
	}
}
