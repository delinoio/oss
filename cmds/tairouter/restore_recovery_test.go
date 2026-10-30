// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func restoreFixture(t *testing.T, firstExisted bool) (Disk, State, []string) {
	t.Helper()
	d := testDisk(t)
	s, _ := d.Load()
	p := testProfile()
	s.Profiles[p.Name] = p
	root := realTemp(t)
	paths := []string{filepath.Join(root, "one.json"), filepath.Join(root, "two.json")}
	r := Record{Client: "cline", Profile: p.Name}
	for i, path := range paths {
		existed := i != 0 || firstExisted
		before := []byte("original-" + filepath.Base(path))
		if !existed {
			before = nil
		}
		after := []byte("remote-key-settings-" + filepath.Base(path))
		_ = os.WriteFile(path, after, 0600)
		r.Files = append(r.Files, Backup{Path: path, Before: before, After: after, Existed: existed})
	}
	s.Records["cline"] = r
	s.Active["cline"] = p.Name
	if e := d.Save(s); e != nil {
		t.Fatal(e)
	}
	return d, s, paths
}
func TestInterruptedRestoreCompletesWithoutResurrectingProfile(t *testing.T) {
	for _, originalExists := range []bool{true, false} {
		t.Run(map[bool]string{true: "replace", false: "remove"}[originalExists], func(t *testing.T) {
			d, s, paths := restoreFixture(t, originalExists)
			failureAt := 3
			if !originalExists {
				failureAt = 2
			}
			d.Protection = &failingProtection{failAt: failureAt}
			if e := d.Restore(&s, "cline"); e == nil {
				t.Fatal("injected restore failure ignored")
			}
			if !exists(filepath.Join(d.Root, "pending.json")) || len(s.Records) != 1 {
				t.Fatal("backup/journal removed prematurely")
			}
			delete(s.Profiles, "test")
			if e := d.Save(s); e != nil {
				t.Fatal(e)
			}
			loaded, e := d.Load()
			if e != nil {
				t.Fatal(e)
			}
			app := Application{Disk: d, Prompt: answer(true), Out: io.Discard}
			if e = app.Recover(&loaded); e != nil {
				t.Fatal(e)
			}
			if len(loaded.Profiles) != 0 || len(loaded.Records) != 0 || len(loaded.Active) != 0 {
				t.Fatal("disconnect state resurrected or ownership retained")
			}
			for i, path := range paths {
				if i == 0 && !originalExists {
					if exists(path) {
						t.Fatal("removed original-absence target recreated")
					}
					continue
				}
				b, _ := os.ReadFile(path)
				if !bytes.Equal(b, []byte("original-"+filepath.Base(path))) {
					t.Fatal("restoration incomplete")
				}
			}
			if exists(filepath.Join(d.Root, "pending.json")) {
				t.Fatal("completed journal not deleted")
			}
		})
	}
}
func TestRestoreStateSaveFailureIsRecoverable(t *testing.T) {
	d, s, paths := restoreFixture(t, true)
	d.Protection = &failingProtection{failAt: 4}
	if e := d.Restore(&s, "cline"); e == nil {
		t.Fatal("state-save failure ignored")
	}
	app := Application{Disk: d, Prompt: answer(true)}
	if e := app.Recover(&s); e != nil {
		t.Fatal(e)
	}
	for _, p := range paths {
		b, _ := os.ReadFile(p)
		if string(b) != "original-"+filepath.Base(p) {
			t.Fatal("wrong restored content")
		}
	}
	if len(s.Records) != 0 {
		t.Fatal("backup retained after success")
	}
}
func TestRestoreRecoveryPreservesPostFailureUserEdit(t *testing.T) {
	d, s, paths := restoreFixture(t, true)
	d.Protection = &failingProtection{failAt: 3}
	_ = d.Restore(&s, "cline")
	_ = os.WriteFile(paths[1], []byte("user edit after interruption"), 0600)
	app := Application{Disk: d, Prompt: answer(true)}
	if e := app.Recover(&s); e != ErrConflict {
		t.Fatalf("expected conflict, got %v", e)
	}
	b, _ := os.ReadFile(paths[1])
	if string(b) != "user edit after interruption" || !exists(filepath.Join(d.Root, "pending.json")) {
		t.Fatal("conflict or journal lost")
	}
}
func TestProcessLockIsExclusiveAndReusable(t *testing.T) {
	d := testDisk(t)
	unlock, e := d.Lock()
	if e != nil {
		t.Fatal(e)
	}
	if other, e := d.Lock(); e == nil {
		other()
		t.Fatal("concurrent writer admitted")
	}
	unlock()
	next, e := d.Lock()
	if e != nil {
		t.Fatal(e)
	}
	next()
}
