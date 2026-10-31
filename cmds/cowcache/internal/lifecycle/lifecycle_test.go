package lifecycle

import (
	"context"
	"github.com/delinoio/oss/cmds/cowcache/internal/state"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"testing"
)

func tree(t *testing.T, p, s string) {
	t.Helper()
	if e := os.MkdirAll(p, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(p, "data"), []byte(s), 0600); e != nil {
		t.Fatal(e)
	}
}
func content(t *testing.T, p, want string) {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(p, "data"))
	if e != nil || string(b) != want {
		t.Fatalf("%s: %q %v, want %q", p, b, e, want)
	}
}
func TestReplacementAndIsolation(t *testing.T) {
	d := t.TempDir()
	src, dst := filepath.Join(d, "source"), filepath.Join(d, "target")
	tree(t, src, "source")
	tree(t, dst, "old")
	p := []Pair{{src, dst}}
	record := filepath.Join(d, "txn.json")
	r, e := Prepare(context.Background(), record, p, false, true)
	if e != nil || !r.Reused {
		t.Fatal(r, e)
	}
	content(t, dst, "old")
	r, e = Prepare(context.Background(), record, p, true, true)
	if e != nil || r.Reused {
		t.Fatal(r, e)
	}
	content(t, dst, "source")
	tree(t, dst, "derived")
	content(t, src, "source")
}
func TestFailedSecondTreePreservesBoth(t *testing.T) {
	d := t.TempDir()
	a, b, c, f := filepath.Join(d, "a"), filepath.Join(d, "b"), filepath.Join(d, "c"), filepath.Join(d, "f")
	tree(t, a, "new")
	tree(t, b, "old-b")
	tree(t, c, "new-c")
	tree(t, f, "old-f")
	if e := os.Symlink(a, filepath.Join(c, "unsafe")); e != nil {
		t.Skip(e)
	}
	_, e := Prepare(context.Background(), filepath.Join(d, "txn.json"), []Pair{{a, b}, {c, f}}, true, true)
	if e == nil {
		t.Fatal("accepted external link")
	}
	content(t, b, "old-b")
	content(t, f, "old-f")
	entries, _ := os.ReadDir(d)
	for _, v := range entries {
		if len(v.Name()) > 20 {
			t.Fatal("staging leaked", v.Name())
		}
	}
}
func TestRecoverPartialPublication(t *testing.T) {
	d := t.TempDir()
	src, dst := filepath.Join(d, "src"), filepath.Join(d, "dst")
	tree(t, src, "new")
	tree(t, dst, "new")
	id, _ := uuid.NewV7()
	v := entry{Pair: Pair{src, dst}, Stage: dst + ".cowc-" + id.String() + ".stage", Backup: dst + ".cowc-" + id.String() + ".backup", Existing: true}
	tree(t, v.Backup, "old")
	record := filepath.Join(d, "txn.json")
	if e := state.Write(record, journal{SchemaVersion: 1, Entries: []entry{v}}); e != nil {
		t.Fatal(e)
	}
	if e := Recover(record, []Pair{v.Pair}); e != nil {
		t.Fatal(e)
	}
	content(t, dst, "old")
}
func TestUnknownJournalPreserved(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "txn.json")
	if e := state.Write(p, journal{SchemaVersion: 99}); e != nil {
		t.Fatal(e)
	}
	if e := Recover(p, nil); e == nil {
		t.Fatal("accepted unknown version")
	}
	if _, e := os.Stat(p); e != nil {
		t.Fatal(e)
	}
}
