package session

import (
	"context"
	"database/sql"
	"github.com/delinoio/oss/cmds/cachecow/internal/lease"
	"github.com/delinoio/oss/cmds/cachecow/internal/lifecycle"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestFinishSessionRetriesRegistryContention(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "registry")
	r, err := lease.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	owner, err := r.NewSession("completed-scope")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Acquire(ctx, lease.Resources{Destinations: []string{filepath.Join(filepath.Dir(root), "cache")}}, false); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "leases.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "PRAGMA busy_timeout=5000; BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(ctx, "ROLLBACK")
	verified := make(chan struct{}, 10)
	done := make(chan error, 1)
	go func() {
		done <- finishSession(owner, func(context.Context, lease.Record) error { verified <- struct{}{}; return nil })
	}()
	// Two positive verifications prove Finish retried while the writer held SQL.
	for range 2 {
		select {
		case <-verified:
		case err := <-done:
			t.Fatalf("finished during contention: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("no retry")
		}
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("completion did not release")
	}
	records, err := r.Records(ctx)
	if err != nil || len(records) != 0 {
		t.Fatalf("leases retained: %v %v", records, err)
	}
}
