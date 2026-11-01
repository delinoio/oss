package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/delinoio/oss/cmds/cachecow/internal/lease"
	"github.com/delinoio/oss/cmds/cachecow/internal/lifecycle"
	"github.com/delinoio/oss/cmds/cachecow/internal/process"
	"os"
	"path/filepath"
	"runtime"
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

func TestRegistryBusyCrossesResultBoundary(t *testing.T) {
	busy := fmt.Errorf("open lease registry: %w", lease.ErrRegistryBusy)
	raw, err := json.Marshal(failedResponse(busy))
	if err != nil {
		t.Fatal(err)
	}
	var decoded Response
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	got := decoded.failure()
	if !errors.Is(got, lease.ErrRegistryBusy) || errors.Is(got, lease.ErrBusy) || got.Error() != busy.Error() {
		t.Fatalf("registry contention lost across the boundary: %v", got)
	}
	if other := failedResponse(lease.ErrBusy).failure(); errors.Is(other, lease.ErrRegistryBusy) {
		t.Fatalf("cache overlap reported as registry contention: %v", other)
	}
}

// writeJournal stores a scope.json fixture and returns its journal directory.
func writeJournal(t *testing.T, fields map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	fields["id"], fields["directory"] = filepath.Base(dir), dir
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "scope.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// previousBoot is a version 2 journal whose identity cannot name the current
// boot (Unix) or a running supervisor (Windows).
func previousBoot() map[string]any {
	fields := map[string]any{"version": 2, "owner_pid": 2147483647, "started": true, "complete": false, "exit_code": 125}
	if runtime.GOOS == "windows" {
		fields["owner_created"] = int64(1)
	} else {
		fields["boot_id"] = "00000000-0000-0000-0000-000000000000"
	}
	return fields
}

func conflictFixture(t *testing.T, journal string) (*lease.Registry, string, string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	r, err := lease.Open(ctx, filepath.Join(root, "leases"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	cache := filepath.Join(root, "target")
	owner, err := r.NewSession(journal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Acquire(ctx, lease.Resources{Destinations: []string{cache}}, false); err != nil {
		t.Fatal(err)
	}
	return r, cache, journal
}

func TestWaitingAcquireRecoversLeaseOfPreviousBoot(t *testing.T) {
	r, cache, journal := conflictFixture(t, writeJournal(t, previousBoot()))
	var notice strings.Builder
	waiter, err := r.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	waiter.SetConflictHandler(ConflictHandler(r, &notice))
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err = waiter.Acquire(c, lease.Resources{Destinations: []string{cache}}, true); err != nil {
		t.Fatalf("waiting acquire did not recover: %v", err)
	}
	if notice.Len() != 0 {
		t.Fatalf("unexpected notice for a recoverable lease: %q", notice.String())
	}
	scope, err := process.LoadScope(journal)
	if err != nil || !scope.Complete || scope.ExitCode != 125 {
		t.Fatalf("journal not completed by recovery: %+v %v", scope, err)
	}
}

func TestUnrecoverableConflictNoticesExactlyOnce(t *testing.T) {
	legacy := writeJournal(t, map[string]any{"version": 1, "owner_pid": 2147483647, "started": true, "complete": false, "exit_code": 125})
	r, cache, journal := conflictFixture(t, legacy)
	var notice strings.Builder
	waiter, err := r.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	waiter.SetConflictHandler(ConflictHandler(r, &notice))
	// Wait long enough for the handler to be offered the conflict repeatedly.
	c, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	if _, err = waiter.Acquire(c, lease.Resources{Destinations: []string{cache}}, true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected the wait to end by deadline, got %v", err)
	}
	got := notice.String()
	if n := strings.Count(got, "\n"); n != 1 {
		t.Fatalf("notice lines = %d, want exactly 1: %q", n, got)
	}
	for _, want := range []string{cache, journal, string(process.RecoveryNoNativeIdentity)} {
		if !strings.Contains(got, want) {
			t.Errorf("notice %q does not name %q", got, want)
		}
	}
	// The lease is still protected and the journal untouched.
	if all, err := r.Records(context.Background()); err != nil || len(all) != 1 {
		t.Fatalf("lease released without proof: %+v %v", all, err)
	}
	if scope, _ := process.LoadScope(journal); scope.Complete {
		t.Fatal("version 1 journal was completed")
	}
}

func TestCleanStyleAcquireRecoversOrReportsBusy(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		fields    map[string]any
		wantBusy  bool
		wantNotes int
	}{
		"recoverable":   {previousBoot(), false, 0},
		"unrecoverable": {map[string]any{"version": 1, "owner_pid": 2147483647, "started": true, "complete": false, "exit_code": 125}, true, 1},
	} {
		t.Run(name, func(t *testing.T) {
			r, cache, _ := conflictFixture(t, writeJournal(t, tc.fields))
			var notice strings.Builder
			cleaner, err := r.NewSession(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			cleaner.SetConflictHandler(ConflictHandler(r, &notice))
			_, err = cleaner.Acquire(ctx, lease.Resources{Destinations: []string{cache}}, false)
			if tc.wantBusy != errors.Is(err, lease.ErrBusy) || (!tc.wantBusy && err != nil) {
				t.Fatalf("busy=%v, err=%v", tc.wantBusy, err)
			}
			if got := strings.Count(notice.String(), "\n"); got != tc.wantNotes {
				t.Fatalf("notices = %d, want %d: %q", got, tc.wantNotes, notice.String())
			}
		})
	}
}

func TestRecoverLeaseKeepsRecordWhenJournalMissing(t *testing.T) {
	r, _, _ := conflictFixture(t, filepath.Join(t.TempDir(), "missing"))
	all, err := r.Records(context.Background())
	if err != nil || len(all) != 1 {
		t.Fatalf("records: %+v %v", all, err)
	}
	reason, recovered, err := RecoverLease(context.Background(), r, all[0])
	if err != nil || recovered || reason != process.RecoveryJournalUnreadable {
		t.Fatalf("reason=%s recovered=%v err=%v", reason, recovered, err)
	}
}
