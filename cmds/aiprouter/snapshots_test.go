// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func largeSnapshot(marker byte) []byte {
	return append(append([]byte(`{"payload":"`), bytes.Repeat([]byte{marker}, 5<<20)...), []byte(`"}`)...)
}
func snapshotFiles(t *testing.T, d Disk) []os.DirEntry {
	t.Helper()
	entries, e := os.ReadDir(filepath.Join(d.Root, "snapshots"))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		t.Fatal(e)
	}
	return entries
}
func TestLargeSnapshotsBoundMetadataHydrateAndCollectAfterRestore(t *testing.T) {
	d := testDisk(t)
	s, _ := d.Load()
	path := filepath.Join(realTemp(t), "aiprouter-catalog.json")
	before, after := largeSnapshot('a'), largeSnapshot('b')
	if e := os.WriteFile(path, before, 0600); e != nil {
		t.Fatal(e)
	}
	if e := d.Apply(&s, "codex", "test", []Change{{Path: path, Before: before, After: after, Existed: true}}, answer(true)); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(d.Root, "state.json"))
	if e != nil {
		t.Fatal(e)
	}
	if len(raw) > MaxConfigBytes || bytes.Contains(raw, bytes.Repeat([]byte{'a'}, 100)) {
		t.Fatal("snapshot bytes embedded in metadata")
	}
	if len(snapshotFiles(t, d)) != 2 {
		t.Fatal("snapshot deduplication failed")
	}
	loaded, e := d.Load()
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(loaded.Records["codex"].Files[0].Before, before) || !bytes.Equal(loaded.Records["codex"].Files[0].After, after) {
		t.Fatal("hydration changed bytes")
	}
	if e = d.Restore(&loaded, "codex"); e != nil {
		t.Fatal(e)
	}
	actual, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(actual, before) {
		t.Fatal("large original not restored")
	}
	if len(snapshotFiles(t, d)) != 0 {
		t.Fatal("released protected snapshots retained")
	}
}
func TestSnapshotCorruptionAndEscapingReferenceRefuse(t *testing.T) {
	for _, escape := range []bool{false, true} {
		d := testDisk(t)
		s := emptyState()
		before := largeSnapshot('a')
		s.Records["codex"] = Record{Client: "codex", Files: []Backup{{Path: "/fixture/aiprouter-catalog.json", Before: before, Existed: true}}}
		if e := d.Save(s); e != nil {
			t.Fatal(e)
		}
		if escape {
			raw, _ := os.ReadFile(filepath.Join(d.Root, "state.json"))
			var encoded State
			_ = json.Unmarshal(raw, &encoded)
			r := encoded.Records["codex"]
			r.Files[0].BeforeSnapshot = "../escape"
			encoded.Records["codex"] = r
			raw, _ = json.Marshal(encoded)
			_ = os.WriteFile(filepath.Join(d.Root, "state.json"), raw, 0600)
		} else {
			entry := snapshotFiles(t, d)[0]
			_ = os.WriteFile(filepath.Join(d.Root, "snapshots", entry.Name()), []byte("corrupt"), 0600)
		}
		if _, e := d.Load(); e == nil {
			t.Fatal("unverified snapshot loaded")
		}
		if len(snapshotFiles(t, d)) != 1 {
			t.Fatal("unverified snapshot deleted")
		}
	}
}

type snapshotStateFailure struct {
	root   string
	writes int
	failAt int
}

func (p *snapshotStateFailure) Protect(path string, dir bool) error {
	if !dir && filepath.Dir(path) == p.root && strings.HasPrefix(filepath.Base(path), ".aiprouter-") {
		p.writes++
		if p.writes == p.failAt {
			return errors.New("injected state write failure")
		}
	}
	return (NativeProtector{}).Protect(path, dir)
}
func TestSnapshotJournalRetainsAllReferencesUntilRecovery(t *testing.T) {
	d := testDisk(t)
	s, _ := d.Load()
	path := filepath.Join(realTemp(t), "aiprouter-catalog.json")
	before, after := largeSnapshot('a'), largeSnapshot('b')
	_ = os.WriteFile(path, before, 0600)
	d.Protection = &snapshotStateFailure{root: d.Root, failAt: 2}
	if e := d.Apply(&s, "codex", "test", []Change{{Path: path, Before: before, After: after, Existed: true}}, answer(true)); e == nil {
		t.Fatal("state failure ignored")
	}
	raw, e := os.ReadFile(filepath.Join(d.Root, "pending.json"))
	if e != nil || len(raw) > MaxConfigBytes {
		t.Fatal("journal not bounded/durable")
	}
	d.Protection = NativeProtector{}
	if e = d.collectSnapshots(); e != nil {
		t.Fatal(e)
	}
	if len(snapshotFiles(t, d)) != 2 {
		t.Fatal("pending snapshots collected")
	}
	loaded, e := d.Load()
	if e != nil {
		t.Fatal(e)
	}
	app := Application{Disk: d, Prompt: answer(false), Out: io.Discard}
	if e = app.Recover(&loaded); e == nil {
		t.Fatal("recovered without acceptance")
	}
	app.Prompt = answer(true)
	if e = app.Recover(&loaded); e != nil {
		t.Fatal(e)
	}
	actual, _ := os.ReadFile(path)
	if !bytes.Equal(actual, before) {
		t.Fatal("pending snapshot not restored")
	}
	if len(snapshotFiles(t, d)) != 0 {
		t.Fatal("unreferenced recovered snapshots retained")
	}
}
func TestSnapshotACLFailureLeavesTargetAndJournalUntouched(t *testing.T) {
	d := testDisk(t)
	s, _ := d.Load()
	path := filepath.Join(realTemp(t), "aiprouter-catalog.json")
	before, after := largeSnapshot('a'), largeSnapshot('b')
	_ = os.WriteFile(path, before, 0600)
	d.Protection = &failingProtection{failAt: 1}
	if e := d.Apply(&s, "codex", "test", []Change{{Path: path, Before: before, After: after, Existed: true}}, answer(true)); e == nil {
		t.Fatal("unprotected snapshot accepted")
	}
	actual, _ := os.ReadFile(path)
	if !bytes.Equal(actual, before) || exists(filepath.Join(d.Root, "pending.json")) {
		t.Fatal("mutation preceded protected snapshot")
	}
}

func TestSnapshotGCRejectsCorruptReferencesWithoutRemovingOtherBackups(t *testing.T) {
	d := testDisk(t)
	s := emptyState()
	s.Records["codex"] = Record{Client: "codex", Files: []Backup{{Path: "/fixture/aiprouter-catalog.json", Before: largeSnapshot('a'), Existed: true}}}
	if e := d.Save(s); e != nil {
		t.Fatal(e)
	}
	extra := largeSnapshot('z')
	ref, e := d.storeSnapshot(extra)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := os.ReadFile(filepath.Join(d.Root, "state.json"))
	var encoded State
	_ = json.Unmarshal(raw, &encoded)
	r := encoded.Records["codex"]
	r.Files[0].BeforeSnapshot = strings.Repeat("f", 64)
	encoded.Records["codex"] = r
	raw, _ = json.Marshal(encoded)
	_ = os.WriteFile(filepath.Join(d.Root, "state.json"), raw, 0600)
	if e = d.collectSnapshots(); e == nil {
		t.Fatal("GC proceeded without valid referenced backup")
	}
	path, _ := d.snapshotPath(ref)
	if !exists(path) || len(snapshotFiles(t, d)) != 2 {
		t.Fatal("GC removed bytes before validating all references")
	}
}

func TestCommittedSnapshotRecoveryKeepsAppliedFilesWithoutRollbackConsent(t *testing.T) {
	d := testDisk(t)
	s := emptyState()
	path := filepath.Join(realTemp(t), "aiprouter-catalog.json")
	before, after := largeSnapshot('a'), largeSnapshot('b')
	r := Record{Client: "codex", Profile: "test", Files: []Backup{{Path: path, Before: before, After: after, Existed: true}}, Originals: map[string]Backup{}}
	s.Records["codex"] = r
	s.Active["codex"] = "test"
	if e := os.WriteFile(path, after, 0600); e != nil {
		t.Fatal(e)
	}
	journal, e := d.marshalPending(Pending{Kind: TransactionApply, Record: r, Desired: s})
	if e != nil {
		t.Fatal(e)
	}
	if e = d.write(filepath.Join(d.Root, "pending.json"), journal); e != nil {
		t.Fatal(e)
	}
	if e = d.Save(s); e != nil {
		t.Fatal(e)
	}
	app := Application{Disk: d, Prompt: answer(false), Out: io.Discard}
	if e = app.Recover(&s); e != nil {
		t.Fatal(e)
	}
	actual, _ := os.ReadFile(path)
	if !bytes.Equal(actual, after) {
		t.Fatal("committed native catalog rolled back")
	}
	if len(snapshotFiles(t, d)) != 2 {
		t.Fatal("active snapshots collected on journal cleanup")
	}
}
