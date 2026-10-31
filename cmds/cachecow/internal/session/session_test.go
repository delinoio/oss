package session

import (
	"context"
	"github.com/delinoio/oss/cmds/cachecow/internal/lease"
	"github.com/delinoio/oss/cmds/cachecow/internal/lifecycle"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This fixture checks admission rules only; executable tests in cli exercise
// kernel-authenticated IPC and actual supervisor ownership.
func TestNestedCannotCopyActivelyWritableSource(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	r, e := lease.Open(context.Background(), filepath.Join(root, "leases"))
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	owner, e := r.NewSession("fixture-only")
	if e != nil {
		t.Fatal(e)
	}
	s := &server{root: root, session: owner, validate: func(Plan) error { return nil }, pending: map[string]Plan{}, ambiguous: map[string]bool{}}
	source, dest := filepath.Join(root, "source"), filepath.Join(root, "dest")
	os.Mkdir(source, 0700)
	initial := Plan{SchemaVersion: 1, Root: root, Action: Run, Resources: lease.Resources{Destinations: []string{source}}, Pairs: []lifecycle.Pair{{Source: source, Destination: source}}, Record: filepath.Join(root, "one.json")}
	if _, e = s.apply(context.Background(), initial); e != nil {
		t.Fatal(e)
	}
	borrowed, e := s.apply(context.Background(), initial)
	if e != nil || !borrowed.Reused {
		t.Fatal(borrowed, e)
	}
	nested := Plan{SchemaVersion: 1, Root: root, Action: Run, Resources: lease.Resources{Sources: []string{source}, Destinations: []string{dest}}, Pairs: []lifecycle.Pair{{Source: source, Destination: dest}}, Record: filepath.Join(root, "two.json")}
	if _, e = s.apply(context.Background(), nested); e == nil || !strings.Contains(e.Error(), "already writable") {
		t.Fatal(e)
	}
	if _, e = os.Stat(dest); !os.IsNotExist(e) {
		t.Fatal("copied live source")
	}
}
