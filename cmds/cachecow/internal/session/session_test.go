package session

import (
	"context"
	"database/sql"
	"errors"
	"github.com/delinoio/oss/cmds/cachecow/internal/lease"
	"github.com/delinoio/oss/cmds/cachecow/internal/lifecycle"
	"os"
	"path/filepath"
	"slices"
	"sort"
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
	if _, e = s.apply(context.Background(), initial, false); e != nil {
		t.Fatal(e)
	}
	borrowed, e := s.apply(context.Background(), initial, true)
	if e != nil || !borrowed.Reused {
		t.Fatal(borrowed, e)
	}
	nested := Plan{SchemaVersion: 1, Root: root, Action: Run, Resources: lease.Resources{Sources: []string{source}, Destinations: []string{dest}}, Pairs: []lifecycle.Pair{{Source: source, Destination: dest}}, Record: filepath.Join(root, "two.json")}
	if _, e = s.apply(context.Background(), nested, true); e == nil || !strings.Contains(e.Error(), "already writable") {
		t.Fatal(e)
	}
	if _, e = os.Stat(dest); !os.IsNotExist(e) {
		t.Fatal("copied live source")
	}
	// The failed request must not keep its new destination leased, and a repeat
	// must fail validation again instead of borrowing the stale destination.
	requireSessionPaths(t, r, owner, source)
	if got, e := s.apply(context.Background(), nested, true); e == nil || got.Reused || !strings.Contains(e.Error(), "already writable") {
		t.Fatal("repeat was not fully validated", got, e)
	}
	requireSessionPaths(t, r, owner, source)
}

// nestedFixture builds a server whose initial request leases only outer.
func nestedFixture(t *testing.T, hooks RecordHooks) (*server, *lease.Registry, *lease.Session, string) {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	r, e := lease.Open(context.Background(), filepath.Join(root, "leases"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { r.Close() })
	owner, e := r.NewSession("fixture-only")
	if e != nil {
		t.Fatal(e)
	}
	s := &server{root: root, session: owner, validate: func(Plan) error { return nil }, records: hooks, pending: map[string]Plan{}, ambiguous: map[string]bool{}}
	outer := filepath.Join(root, "outer")
	if e = os.Mkdir(outer, 0700); e != nil {
		t.Fatal(e)
	}
	initial := Plan{SchemaVersion: 1, Root: root, Action: Run, Resources: lease.Resources{Destinations: []string{outer}}, Pairs: []lifecycle.Pair{{Source: outer, Destination: outer}}, Record: filepath.Join(root, "outer.json")}
	if _, e = s.apply(context.Background(), initial, false); e != nil {
		t.Fatal(e)
	}
	requireSessionPaths(t, r, owner, outer)
	return s, r, owner, root
}

func requireSessionPaths(t *testing.T, r *lease.Registry, owner *lease.Session, want ...string) {
	t.Helper()
	all, e := r.Records(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	for _, record := range all {
		if record.ID == owner.ID() {
			got := append([]string(nil), record.Paths...)
			sort.Strings(got)
			sort.Strings(want)
			if !slices.Equal(got, want) {
				t.Fatalf("session paths = %v, want %v", got, want)
			}
			return
		}
	}
	t.Fatal("session record missing")
}

func writeSource(t *testing.T, dir string) {
	t.Helper()
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "data"), []byte(filepath.Base(dir)), 0600); e != nil {
		t.Fatal(e)
	}
}

func nestedRun(root, name string, source, dest string) Plan {
	return Plan{SchemaVersion: 1, Root: root, Action: Run, AllowCopy: true, Resources: lease.Resources{Sources: []string{source}, Destinations: []string{dest}}, Pairs: []lifecycle.Pair{{Source: source, Destination: dest}}, Record: filepath.Join(root, name+".json")}
}

func TestNestedSourceFailureReleasesAcquiredPaths(t *testing.T) {
	var srcA string
	s, r, owner, root := nestedFixture(t, RecordHooks{ValidateSource: func(p Plan) error {
		if len(p.Resources.Sources) == 1 && p.Resources.Sources[0] == srcA {
			return errors.New("source provenance is unavailable")
		}
		return nil
	}})
	srcA, srcB, dest, outer := filepath.Join(root, "srcA"), filepath.Join(root, "srcB"), filepath.Join(root, "dest"), filepath.Join(root, "outer")
	writeSource(t, srcA)
	writeSource(t, srcB)
	_, e := s.apply(context.Background(), nestedRun(root, "a", srcA, dest), true)
	var sourceErr *SourceError
	if !errors.As(e, &sourceErr) {
		t.Fatalf("want SourceError, got %v", e)
	}
	requireSessionPaths(t, r, owner, outer)
	// Another session can use the released source without waiting.
	other, e := r.NewSession("other")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = other.Acquire(context.Background(), lease.Resources{Destinations: []string{srcA}}, false); e != nil {
		t.Fatalf("source stayed leased: %v", e)
	}
	// Choosing another source for the same destination now succeeds and prepares.
	got, e := s.apply(context.Background(), nestedRun(root, "b", srcB, dest), true)
	if e != nil || got.Reused {
		t.Fatalf("retry with another source: %v %v", got, e)
	}
	if data, e := os.ReadFile(filepath.Join(dest, "data")); e != nil || string(data) != "srcB" {
		t.Fatalf("destination not prepared from srcB: %q %v", data, e)
	}
	requireSessionPaths(t, r, owner, outer, dest)
}

func TestNestedPrepareFailureReleasesAndRetryPrepares(t *testing.T) {
	s, r, owner, root := nestedFixture(t, RecordHooks{})
	src, dest, outer := filepath.Join(root, "src"), filepath.Join(root, "dest"), filepath.Join(root, "outer")
	p := nestedRun(root, "prepare", src, dest)
	if _, e := s.apply(context.Background(), p, true); e == nil || !strings.Contains(e.Error(), "source cache unavailable") {
		t.Fatalf("want unavailable source, got %v", e)
	}
	requireSessionPaths(t, r, owner, outer)
	writeSource(t, src)
	got, e := s.apply(context.Background(), p, true)
	if e != nil || got.Reused {
		t.Fatalf("identical retry must prepare: %v %v", got, e)
	}
	if data, e := os.ReadFile(filepath.Join(dest, "data")); e != nil || string(data) != "src" {
		t.Fatalf("destination not prepared: %q %v", data, e)
	}
	requireSessionPaths(t, r, owner, outer, dest)
}

func TestInitialRequestFailureKeepsSessionPaths(t *testing.T) {
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
	s := &server{root: root, session: owner, validate: func(Plan) error { return nil }, records: RecordHooks{ValidateSource: func(Plan) error { return errors.New("rejected") }}, pending: map[string]Plan{}, ambiguous: map[string]bool{}}
	src, dest := filepath.Join(root, "src"), filepath.Join(root, "dest")
	writeSource(t, src)
	if _, e = s.apply(context.Background(), nestedRun(root, "initial", src, dest), false); e == nil {
		t.Fatal("expected failure")
	}
	// The supervisor finishes the session after an initial failure; apply itself
	// must not release anything for the initial request.
	requireSessionPaths(t, r, owner, src, dest)
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

func TestPreparationEvidenceUsesLeasedResult(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "cloned", true: "created-before-acquisition"}[existing], func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			r, err := lease.Open(context.Background(), filepath.Join(root, "registry"))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			owner, err := r.NewSession("fixture")
			if err != nil {
				t.Fatal(err)
			}
			src, dst := filepath.Join(root, "source"), filepath.Join(root, "dest")
			os.Mkdir(src, 0700)
			os.WriteFile(filepath.Join(src, "data"), []byte("source"), 0600)
			p := Plan{SchemaVersion: 1, Root: root, Action: Prepare, PreparedAll: true, AllowCopy: true, Resources: lease.Resources{Sources: []string{src}, Destinations: []string{dst}}, Pairs: []lifecycle.Pair{{Source: src, Destination: dst}}, Record: filepath.Join(root, "transaction.json")}
			// The plan was built while dst was absent. Another owner creates it before
			// this request acquires protection; it must not acquire new provenance.
			if existing {
				os.Mkdir(dst, 0700)
				os.WriteFile(filepath.Join(dst, "data"), []byte("retained"), 0600)
			}
			s := &server{root: root, session: owner, validate: func(Plan) error { return nil }, pending: map[string]Plan{}, ambiguous: map[string]bool{}}
			if _, err := s.apply(context.Background(), p, false); err != nil {
				t.Fatal(err)
			}
			if got := s.pending[p.Record].PreparedAll; got == existing {
				t.Fatalf("PreparedAll=%v for existing=%v", got, existing)
			}
			data, err := os.ReadFile(filepath.Join(dst, "data"))
			if err != nil {
				t.Fatal(err)
			}
			if existing && string(data) != "retained" {
				t.Fatal("existing cache was overwritten")
			}
		})
	}
}
