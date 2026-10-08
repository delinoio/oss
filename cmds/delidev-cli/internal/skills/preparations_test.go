// SPDX-License-Identifier: Apache-2.0
package skills

import (
	"context"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"path/filepath"
	"testing"
)

func preparationFixture(t *testing.T) (Manager, domain.SkillReadRequest) {
	t.Helper()
	m, s, _ := fixture(t)
	r, e := m.List(context.Background(), s, nil)
	if e != nil || len(r.Entries) != 1 {
		t.Fatal(r, e)
	}
	return m, preparationScope(selectEntry(s, r.Entries[0]))
}
func cleanupScope(s domain.SkillReadRequest) domain.SkillReadRequest {
	s.Action = domain.CleanupSkillPreparation
	return s
}
func TestPreparationLostDispatchTombstone(t *testing.T) {
	m, s := preparationFixture(t)
	if e := m.CleanupPreparation(context.Background(), cleanupScope(s)); e != nil {
		t.Fatal(e)
	}
	if e := m.Prepare(context.Background(), s); e == nil {
		t.Fatal("delayed copy resurrected")
	}
	if _, e := os.Stat(snapshotPath(m.Root, s.Preparation.RequestID)); !os.IsNotExist(e) {
		t.Fatal(e)
	}
}
func TestPreparationCleanupOriginalAndReappearance(t *testing.T) {
	m, s := preparationFixture(t)
	if e := m.Prepare(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	v, e := readPreparation(m.preparationPath(s.Preparation.RequestID))
	if e != nil || !v.Ready || v.RootIdentity == "" || len(v.Files) != 2 {
		t.Fatal(v, e)
	}
	c := cleanupScope(s)
	c.WorkerInstanceID = domain.NewID()
	if e = m.CleanupPreparation(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	if e = m.CleanupPreparation(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	target := snapshotPath(m.Root, s.Preparation.RequestID)
	os.Mkdir(target, 0700)
	file := filepath.Join(target, "replacement")
	os.WriteFile(file, []byte("preserved"), 0600)
	if e = m.CleanupPreparation(context.Background(), c); e == nil {
		t.Fatal("replacement adopted")
	}
	if b, e := os.ReadFile(file); e != nil || string(b) != "preserved" {
		t.Fatal(string(b), e)
	}
}
func TestPreparationCleanupUncertainOwnership(t *testing.T) {
	for _, kind := range []string{"changed", "unlisted", "root", "proof"} {
		t.Run(kind, func(t *testing.T) {
			m, s := preparationFixture(t)
			if e := m.Prepare(context.Background(), s); e != nil {
				t.Fatal(e)
			}
			target := snapshotPath(m.Root, s.Preparation.RequestID)
			c := cleanupScope(s)
			switch kind {
			case "changed":
				os.WriteFile(filepath.Join(target, string(s.Selections[0].SkillID), "SKILL.md"), []byte("changed"), 0600)
			case "unlisted":
				os.WriteFile(filepath.Join(target, "foreign"), []byte("preserved"), 0600)
			case "root":
				if e := os.Rename(target, target+"-original"); e != nil {
					t.Fatal(e)
				}
				os.Mkdir(target, 0700)
				os.WriteFile(filepath.Join(target, "foreign"), []byte("preserved"), 0600)
			case "proof":
				p := *s.Preparation
				p.ServerID = domain.NewID()
				c.Preparation = &p
			}
			if e := m.CleanupPreparation(context.Background(), c); e == nil {
				t.Fatal("uncertain ownership accepted")
			}
			if _, e := os.Stat(filepath.Join(target, "snapshot.json")); kind != "root" && e != nil {
				t.Fatal("metadata removed before inspection", e)
			}
		})
	}
}
func TestPreparationGateJoinsCopyCleanup(t *testing.T) {
	m, s := preparationFixture(t)
	g, e := m.preparationGate(s.Preparation.RequestID)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.CleanupPreparation(context.Background(), cleanupScope(s)); e == nil {
		t.Fatal("cleanup crossed gate")
	}
	if e = m.Prepare(context.Background(), s); e == nil {
		t.Fatal("copy crossed gate")
	}
	g.Close()
	if e = m.Prepare(context.Background(), s); e != nil {
		t.Fatal(e)
	}
}
func TestPreparationCanceledIntent(t *testing.T) {
	m, s := preparationFixture(t)
	ctx, c := context.WithCancel(context.Background())
	c()
	if e := m.Prepare(ctx, s); e == nil {
		t.Fatal("canceled copy succeeded")
	}
	if _, e := readPreparation(m.preparationPath(s.Preparation.RequestID)); e != nil {
		t.Fatal("intent missing", e)
	}
	if e := m.CleanupPreparation(context.Background(), cleanupScope(s)); e != nil {
		t.Fatal(e)
	}
}
func TestPreparationPartialCopyRestart(t *testing.T) {
	m, s := preparationFixture(t)
	if e := m.Prepare(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	v, e := readPreparation(m.preparationPath(s.Preparation.RequestID))
	if e != nil {
		t.Fatal(e)
	}
	v.Ready = false
	if e = writePreparation(m.preparationPath(s.Preparation.RequestID), v); e != nil {
		t.Fatal(e)
	}
	target := snapshotPath(m.Root, s.Preparation.RequestID)
	if e = os.Remove(filepath.Join(target, "snapshot.json")); e != nil {
		t.Fatal(e)
	}
	if e = m.CleanupPreparation(context.Background(), cleanupScope(s)); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("partial bytes remain", e)
	}
}

func TestPreparationCapacityRejectsBeforeCopy(t *testing.T) {
	m, s := preparationFixture(t)
	dir := filepath.Dir(m.preparationPath(s.Preparation.RequestID))
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < preparationLimit; i++ {
		if e := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%04d.json", i)), []byte("{}"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := m.Prepare(context.Background(), s); e == nil {
		t.Fatal("capacity admitted copying")
	}
	if _, e := os.Stat(snapshotPath(m.Root, s.Preparation.RequestID)); !os.IsNotExist(e) {
		t.Fatal("copy bytes created at capacity", e)
	}
}

func TestPreparationOriginalRetryAcrossReconnect(t *testing.T) {
	m, s := preparationFixture(t)
	if e := m.Prepare(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	s.WorkerInstanceID = domain.NewID()
	if e := m.Prepare(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	if entries, e := m.PreparedEntries(context.Background(), s); e != nil || len(entries) != 1 {
		t.Fatal(entries, e)
	}
	changed := *s.Preparation
	changed.OriginalInstanceID = domain.NewID()
	s.Preparation = &changed
	if _, e := m.PreparedEntries(context.Background(), s); e == nil {
		t.Fatal("replacement original instance accepted")
	}
}
func TestPreparationSessionCleanupReleasesActiveCapacity(t *testing.T) {
	m, s := preparationFixture(t)
	if e := m.Prepare(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	if e := m.DeletePreparedSnapshot(context.Background(), s.MachineID, s.Selections[0]); e != nil {
		t.Fatal(e)
	}
	entries, e := os.ReadDir(filepath.Dir(m.preparationPath(s.Preparation.RequestID)))
	if e != nil || len(entries) != 0 {
		t.Fatal("completed cleanup still occupies capacity", entries, e)
	}
	if e = m.Prepare(context.Background(), s); e == nil {
		t.Fatal("terminal tombstone allowed resurrection")
	}
}

func TestPreparationJournalRejectsOversizedResourceClaim(t *testing.T) {
	m, s := preparationFixture(t)
	if e := m.Prepare(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	v, e := readPreparation(m.preparationPath(s.Preparation.RequestID))
	if e != nil {
		t.Fatal(e)
	}
	v.Files[0].Size = MaxPackageBytes + 1
	if e = writePreparation(m.preparationPath(s.Preparation.RequestID), v); e != nil {
		t.Fatal(e)
	}
	if e = m.CleanupPreparation(context.Background(), cleanupScope(s)); e == nil {
		t.Fatal("oversized private claim accepted")
	}
	if _, e = os.Stat(filepath.Join(snapshotPath(m.Root, s.Preparation.RequestID), "snapshot.json")); e != nil {
		t.Fatal("invalid claim acquired deletion authority", e)
	}
}
