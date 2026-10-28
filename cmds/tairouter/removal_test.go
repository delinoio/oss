// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestRemovedCacheRefreshRestorationAndForeignConflict(t *testing.T) {
	d := testDisk(t)
	s, _ := d.Load()
	path := filepath.Join(realTemp(t), "cache.json")
	original := []byte(`{"models":["old"]}`)
	if e := os.WriteFile(path, original, 0600); e != nil {
		t.Fatal(e)
	}
	changes := []Change{{Path: path, Before: original, Existed: true, Remove: true}}
	if e := d.Apply(&s, "claude", "test", changes, answer(true)); e != nil {
		t.Fatal(e)
	}
	if exists(path) || !s.Records["claude"].Files[0].Removed {
		t.Fatal("removal not recorded")
	}
	if e := d.Apply(&s, "claude", "test", []Change{{Path: path, Remove: true}}, answer(true)); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte("foreign"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := d.Restore(&s, "claude"); e == nil {
		t.Fatal("foreign recreation overwritten")
	}
	_ = os.Remove(path)
	if e := d.Restore(&s, "claude"); e != nil {
		t.Fatal(e)
	}
	actual, _ := os.ReadFile(path)
	if !bytes.Equal(actual, original) {
		t.Fatal("removed original lost on refresh")
	}
}
func TestInterruptedCacheRemovalRollsBackAndRestorationRollsForward(t *testing.T) {
	d := testDisk(t)
	s, _ := d.Load()
	home := realTemp(t)
	path := filepath.Join(home, "cache.json")
	other := filepath.Join(home, "other.json")
	original := []byte("original")
	_ = os.WriteFile(path, original, 0600)
	d.Protection = &failingProtection{failAt: 2}
	if e := d.Apply(&s, "claude", "test", []Change{{Path: path, Before: original, Existed: true, Remove: true}, {Path: other, After: []byte("settings")}}, answer(true)); e == nil {
		t.Fatal("failure not injected")
	}
	d.Protection = NativeProtector{}
	app := Application{Disk: d, Prompt: answer(false), Out: io.Discard}
	if e := app.Recover(&s); e == nil || exists(path) {
		t.Fatal("recovery needs acceptance")
	}
	app.Prompt = answer(true)
	if e := app.Recover(&s); e != nil {
		t.Fatal(e)
	}
	actual, _ := os.ReadFile(path)
	if !bytes.Equal(actual, original) {
		t.Fatal("removal rollback lost original")
	}
	if e := d.Apply(&s, "claude", "test", []Change{{Path: path, Before: original, Existed: true, Remove: true}}, answer(true)); e != nil {
		t.Fatal(e)
	}
	d.Protection = &failingProtection{failAt: 2}
	if e := d.Restore(&s, "claude"); e == nil {
		t.Fatal("restore failure not injected")
	}
	app.Disk.Protection = NativeProtector{}
	if e := app.Recover(&s); e != nil {
		t.Fatal(e)
	}
	actual, _ = os.ReadFile(path)
	if !bytes.Equal(actual, original) {
		t.Fatal("restore recovery lost original")
	}
}
