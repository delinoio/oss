package lease

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

var ctx = context.Background()

func setup(t *testing.T) (*Registry, string) {
	t.Helper()
	root := t.TempDir()
	r, err := Open(ctx, filepath.Join(root, "registry"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, root
}
func session(t *testing.T, r *Registry) *Session {
	t.Helper()
	s, err := r.NewSession("native-supervisor-test-boundary")
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func acquire(t *testing.T, s *Session, paths ...string) Grant {
	t.Helper()
	g, err := s.Acquire(ctx, Resources{Destinations: paths}, false)
	if err != nil {
		t.Fatal(err)
	}
	return g
}
func verified(context.Context, Record) error { return nil }
func finish(t *testing.T, s *Session) {
	t.Helper()
	if err := s.Finish(ctx, verified); err != nil {
		t.Fatal(err)
	}
}
func TestCompleteSetsAndIndependentWorktrees(t *testing.T) {
	r, root := setup(t)
	a, b := session(t, r), session(t, r)
	source := filepath.Join(root, "primary", "target")
	dest := filepath.Join(root, "worktree-a", "target")
	if _, err := a.Acquire(ctx, Resources{Sources: []string{source}, Destinations: []string{dest}}, false); err != nil {
		t.Fatal(err)
	}
	acquire(t, b, filepath.Join(root, "worktree-b", "target"))
	for _, p := range []string{source, dest, filepath.Join(dest, "debug"), filepath.Dir(dest)} {
		_, err := session(t, r).Acquire(ctx, Resources{Destinations: []string{p}}, false)
		if !errors.Is(err, ErrBusy) {
			t.Fatalf("%s: %v", p, err)
		}
	}
	finish(t, a)
	acquire(t, session(t, r), source, dest)
}
func TestWaitAndCancellation(t *testing.T) {
	r, root := setup(t)
	path := filepath.Join(root, "cache")
	s := session(t, r)
	acquire(t, s, path)
	c, cancel := context.WithTimeout(ctx, 60*time.Millisecond)
	defer cancel()
	if _, err := session(t, r).Acquire(c, Resources{Sources: []string{path}}, true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := session(t, r).Acquire(ctx, Resources{Destinations: []string{path}}, true)
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("early acquisition: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	finish(t, s)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("waiter did not resume")
	}
}
func TestNestedBorrowAndCycle(t *testing.T) {
	r, root := setup(t)
	a, b := session(t, r), session(t, r)
	x, y := filepath.Join(root, "x"), filepath.Join(root, "y")
	if acquire(t, a, x).Borrowed {
		t.Fatal("first acquisition borrowed")
	}
	acquire(t, b, y)
	if !acquire(t, a, x, filepath.Join(x, "child")).Borrowed {
		t.Fatal("nested acquisition must skip preparation")
	}
	for _, pair := range []struct {
		s *Session
		p string
	}{{a, y}, {b, x}} {
		if _, err := pair.s.Acquire(ctx, Resources{Destinations: []string{pair.p}}, true); !errors.Is(err, ErrNestedConflict) {
			t.Fatal(err)
		}
	}
	if _, err := session(t, r).Acquire(ctx, Resources{Destinations: []string{x}}, false); !errors.Is(err, ErrBusy) {
		t.Fatal("parent protection released", err)
	}
	// Nonconflicting dynamic nesting adds protection retained until Finish.
	z := filepath.Join(root, "z")
	if acquire(t, a, z).Borrowed {
		t.Fatal("new cache borrowed")
	}
	finish(t, a)
	acquire(t, session(t, r), x, z)
}
func TestAliasesAndAbsentCache(t *testing.T) {
	r, root := setup(t)
	actual := filepath.Join(root, "actual")
	if err := os.Mkdir(actual, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(actual, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	acquire(t, session(t, r), filepath.Join(actual, "absent", "cache"))
	if _, err := session(t, r).Acquire(ctx, Resources{Destinations: []string{filepath.Join(alias, "absent")}}, false); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(actual, "absent")); !os.IsNotExist(err) {
		t.Fatal("acquisition created cache", err)
	}
}
func TestDurableOwnerLossAndReconciliation(t *testing.T) {
	r, root := setup(t)
	s := session(t, r)
	path := filepath.Join(root, "cache")
	acquire(t, s, path)
	if err := s.Finish(ctx, nil); !errors.Is(err, ErrUnverified) {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, filepath.Join(root, "registry"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := session(t, reopened).Acquire(ctx, Resources{Destinations: []string{path}}, false); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	all, err := reopened.Records(ctx)
	if err != nil || len(all) != 1 {
		t.Fatal(all, err)
	}
	if err := reopened.Reconcile(ctx, all[0], func(context.Context, Record) error { return errors.New("surviving descendants") }); !errors.Is(err, ErrUnverified) {
		t.Fatal(err)
	}
	if err := reopened.Reconcile(ctx, all[0], verified); err != nil {
		t.Fatal(err)
	}
	acquire(t, session(t, reopened), path)
}
func TestMalformedAndUnknownRecords(t *testing.T) {
	for _, raw := range []string{`{`, `{"version":2}`, `{"version":1,"id":"not-uuid"}`, `{"version":1,"unexpected":true}`} {
		t.Run(raw, func(t *testing.T) {
			r, root := setup(t)
			if _, err := r.db.Exec("INSERT INTO leases VALUES('bad',?)", raw); err != nil {
				t.Fatal(err)
			}
			if _, err := session(t, r).Acquire(ctx, Resources{Destinations: []string{filepath.Join(root, "cache")}}, false); err == nil {
				t.Fatal("invalid record accepted")
			}
			var got string
			if err := r.db.QueryRow("SELECT record FROM leases").Scan(&got); err != nil || got != raw {
				t.Fatal("invalid record rewritten", err)
			}
		})
	}
	r, root := setup(t)
	if _, err := r.db.Exec("PRAGMA user_version=42"); err != nil {
		t.Fatal(err)
	}
	r.Close()
	if other, err := Open(ctx, filepath.Join(root, "registry")); err == nil {
		other.Close()
		t.Fatal("unknown database version accepted")
	}
}
func TestOutsideCacheAndPrivateRegistry(t *testing.T) {
	r, root := setup(t)
	if _, err := session(t, r).Acquire(ctx, Resources{Destinations: []string{root}}, false); err == nil {
		t.Fatal("registry inside cache")
	}
	if runtime.GOOS != "windows" {
		dir := filepath.Join(root, "public")
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if other, err := Open(ctx, dir); err == nil {
			other.Close()
			t.Fatal("public registry accepted")
		}
	}
}
func TestConcurrentRegistries(t *testing.T) {
	r, root := setup(t)
	other, err := Open(ctx, filepath.Join(root, "registry"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reg := r
			if i%2 == 0 {
				reg = other
			}
			s := session(t, reg)
			c, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if _, err := s.Acquire(c, Resources{Destinations: []string{filepath.Join(root, "cache")}}, true); err != nil {
				errs <- err
				return
			}
			// Finish can encounter a short registry transaction. Retrying verification
			// is safe here because this fixture never launches a process.
			for {
				err := s.Finish(c, verified)
				if errors.Is(err, ErrBusy) {
					continue
				}
				errs <- err
				return
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
func TestSubprocessOwnerLoss(t *testing.T) {
	if os.Getenv("CCOW_LEASE_TEST_HELPER") == "1" {
		r, err := Open(ctx, os.Getenv("CCOW_LEASE_TEST_REGISTRY"))
		if err != nil {
			os.Exit(2)
		}
		s, err := r.NewSession("subprocess-intent-before-spawn")
		if err != nil {
			os.Exit(3)
		}
		if _, err := s.Acquire(ctx, Resources{Destinations: []string{os.Getenv("CCOW_LEASE_TEST_CACHE")}}, false); err != nil {
			os.Exit(4)
		}
		fmt.Println("ready")
		// Keep the owner alive until the parent forcibly terminates it.
		var input [1]byte
		os.Stdin.Read(input[:])
		os.Exit(5)
	}
	r, root := setup(t)
	path := filepath.Join(root, "cache")
	cmd := exec.Command(os.Args[0], "-test.run=^TestSubprocessOwnerLoss$")
	cmd.Env = append(os.Environ(), "CCOW_LEASE_TEST_HELPER=1", "CCOW_LEASE_TEST_REGISTRY="+filepath.Join(root, "registry"), "CCOW_LEASE_TEST_CACHE="+path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("helper readiness: %q %v", line, err)
	}
	if _, err := session(t, r).Acquire(ctx, Resources{Destinations: []string{path}}, false); !errors.Is(err, ErrBusy) {
		t.Fatal("live subprocess lost protection", err)
	}
	acquire(t, session(t, r), filepath.Join(root, "independent-worktree", "cache"))
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("expected killed owner")
	}

	if _, err := session(t, r).Acquire(ctx, Resources{Destinations: []string{path}}, false); !errors.Is(err, ErrBusy) {
		t.Fatal("dead owner lost protection", err)
	}
}
func TestReconcileRejectsChangedRecord(t *testing.T) {
	r, root := setup(t)
	s := session(t, r)
	acquire(t, s, filepath.Join(root, "x"))
	all, err := r.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	acquire(t, s, filepath.Join(root, "y"))
	if err := r.Reconcile(ctx, all[0], verified); !errors.Is(err, ErrUnverified) {
		t.Fatal(err)
	}
}

// holdRegistry takes the registry write lock on a separate connection until the
// returned release function runs. release is idempotent.
func holdRegistry(t *testing.T, r *Registry) (release func()) {
	t.Helper()
	conn, err := r.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			conn.ExecContext(ctx, "ROLLBACK")
			conn.Close()
		})
	}
	t.Cleanup(release)
	return release
}

// holdReaders keeps a SHARED lock so concurrent writers can start a transaction
// but SQLite reports BUSY when they try to COMMIT.
func holdReaders(t *testing.T, r *Registry) (release func()) {
	t.Helper()
	conn, err := r.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM leases").Scan(&n); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			conn.ExecContext(ctx, "ROLLBACK")
			conn.Close()
		})
	}
	t.Cleanup(release)
	return release
}

func releaseAfter(d time.Duration, release func()) {
	go func() { time.Sleep(d); release() }()
}

func requireNotOverlapError(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, ErrBusy) || errors.Is(err, ErrNestedConflict) || errors.Is(err, ErrRegistryBusy) {
		t.Fatalf("registry contention reported as %v", err)
	}
}

func TestRegistryTransactionContention(t *testing.T) {
	r, root := setup(t)
	other, err := Open(ctx, filepath.Join(root, "registry"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	holdRegistry(t, r)
	start := time.Now()
	_, err = session(t, other).Acquire(ctx, Resources{Destinations: []string{filepath.Join(root, "cache")}}, false)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrRegistryBusy) || errors.Is(err, ErrBusy) || errors.Is(err, ErrNestedConflict) {
		t.Fatalf("expected only ErrRegistryBusy, got %v", err)
	}
	if elapsed < time.Second || elapsed > time.Second+500*time.Millisecond {
		t.Fatalf("contention budget not honored: %s", elapsed)
	}
	c, cancel := context.WithTimeout(ctx, 60*time.Millisecond)
	defer cancel()
	if _, err := session(t, other).Acquire(c, Resources{Destinations: []string{filepath.Join(root, "cache")}}, true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestRegistryBriefContentionIsRetried(t *testing.T) {
	const hold = 250 * time.Millisecond
	type operation struct {
		name string
		// prepare runs before the lock is held and returns the contended call.
		prepare func(t *testing.T, r *Registry, root string) func() error
	}
	operations := []operation{
		{"Open", func(t *testing.T, r *Registry, root string) func() error {
			return func() error {
				reopened, err := Open(ctx, filepath.Join(root, "registry"))
				if err == nil {
					reopened.Close()
				}
				return err
			}
		}},
		{"Records", func(t *testing.T, r *Registry, root string) func() error {
			return func() error { _, err := r.Records(ctx); return err }
		}},
		{"Reconcile", func(t *testing.T, r *Registry, root string) func() error {
			s := session(t, r)
			acquire(t, s, filepath.Join(root, "reconcile"))
			all, err := r.Records(ctx)
			if err != nil || len(all) != 1 {
				t.Fatal(all, err)
			}
			return func() error { return r.Reconcile(ctx, all[0], verified) }
		}},
		{"AcquireNoWait", func(t *testing.T, r *Registry, root string) func() error {
			s := session(t, r)
			return func() error {
				_, err := s.Acquire(ctx, Resources{Destinations: []string{filepath.Join(root, "acquire")}}, false)
				return err
			}
		}},
		{"NestedAcquire", func(t *testing.T, r *Registry, root string) func() error {
			s := session(t, r)
			acquire(t, s, filepath.Join(root, "outer"))
			return func() error {
				g, err := s.Acquire(ctx, Resources{Destinations: []string{filepath.Join(root, "inner")}}, false)
				if err == nil && (len(g.NewPaths) != 1 || g.Borrowed) {
					return fmt.Errorf("unexpected grant %+v", g)
				}
				return err
			}
		}},
		{"ReleasePreparedSources", func(t *testing.T, r *Registry, root string) func() error {
			s := session(t, r)
			src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
			g, err := s.Acquire(ctx, Resources{Sources: []string{src}, Destinations: []string{dst}}, false)
			if err != nil {
				t.Fatal(err)
			}
			return func() error { return s.ReleasePreparedSources([]string{src}, g) }
		}},
		{"ReleaseUnstartedGrant", func(t *testing.T, r *Registry, root string) func() error {
			s := session(t, r)
			acquire(t, s, filepath.Join(root, "outer"))
			g := acquire(t, s, filepath.Join(root, "inner"))
			return func() error { return s.ReleaseUnstartedGrant(g) }
		}},
	}
	for _, op := range operations {
		t.Run(op.name, func(t *testing.T) {
			r, root := setup(t)
			root, err := filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			call := op.prepare(t, r, root)
			release := holdRegistry(t, r)
			releaseAfter(hold, release)
			start := time.Now()
			err = call()
			elapsed := time.Since(start)
			if err != nil {
				requireNotOverlapError(t, err)
				t.Fatal(err)
			}
			if elapsed < hold/2 {
				t.Fatalf("call did not wait for the registry lock: %s", elapsed)
			}
		})
	}
}

func TestRegistryLongContentionReturnsRegistryBusy(t *testing.T) {
	r, root := setup(t)
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	r.contentionBudget = 200 * time.Millisecond
	nested := session(t, r)
	acquire(t, nested, filepath.Join(root, "outer"))
	prepared := session(t, r)
	src := filepath.Join(root, "src")
	grant, err := prepared.Acquire(ctx, Resources{Sources: []string{src}, Destinations: []string{filepath.Join(root, "dst")}}, false)
	if err != nil {
		t.Fatal(err)
	}
	all, err := r.Records(ctx)
	if err != nil || len(all) != 2 {
		t.Fatal(all, err)
	}
	calls := map[string]func() error{
		"Records":   func() error { _, err := r.Records(ctx); return err },
		"Reconcile": func() error { return r.Reconcile(ctx, all[0], verified) },
		"AcquireNoWait": func() error {
			_, err := session(t, r).Acquire(ctx, Resources{Destinations: []string{filepath.Join(root, "new")}}, false)
			return err
		},
		"NestedAcquire": func() error {
			_, err := nested.Acquire(ctx, Resources{Destinations: []string{filepath.Join(root, "inner")}}, false)
			return err
		},
		"ReleasePreparedSources": func() error { return prepared.ReleasePreparedSources([]string{src}, grant) },
	}
	holdRegistry(t, r)
	for name, call := range calls {
		start := time.Now()
		err := call()
		elapsed := time.Since(start)
		if !errors.Is(err, ErrRegistryBusy) || errors.Is(err, ErrBusy) || errors.Is(err, ErrNestedConflict) {
			t.Fatalf("%s: expected only ErrRegistryBusy, got %v", name, err)
		}
		if elapsed < 200*time.Millisecond || elapsed > 700*time.Millisecond {
			t.Fatalf("%s: contention budget not honored: %s", name, elapsed)
		}
	}
	if _, err := Open(ctx, filepath.Join(root, "registry")); !errors.Is(err, ErrRegistryBusy) {
		t.Fatalf("Open: %v", err)
	}
}

func TestRealOverlapIsNotRegistryBusy(t *testing.T) {
	r, root := setup(t)
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	holder := session(t, r)
	acquire(t, holder, filepath.Join(root, "held"))
	outer := session(t, r)
	acquire(t, outer, filepath.Join(root, "outer"))
	start := time.Now()
	if _, err := session(t, r).Acquire(ctx, Resources{Destinations: []string{filepath.Join(root, "held")}}, false); !errors.Is(err, ErrBusy) || errors.Is(err, ErrRegistryBusy) {
		t.Fatalf("top-level overlap: %v", err)
	}
	if _, err := outer.Acquire(ctx, Resources{Destinations: []string{filepath.Join(root, "held", "child")}}, false); !errors.Is(err, ErrNestedConflict) || errors.Is(err, ErrRegistryBusy) {
		t.Fatalf("nested overlap: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("real overlap waited %s", elapsed)
	}
}

func TestAcquireWaitContinuesThroughContentionAndOverlap(t *testing.T) {
	r, root := setup(t)
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	r.contentionBudget = 100 * time.Millisecond
	path := filepath.Join(root, "cache")
	release := holdRegistry(t, r)
	releaseAfter(350*time.Millisecond, release)
	if _, err := session(t, r).Acquire(ctx, Resources{Destinations: []string{path}}, true); err != nil {
		t.Fatalf("wait through contention: %v", err)
	}
	// The path is now held: a waiter keeps polling the overlap until ctx ends.
	c, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := session(t, r).Acquire(c, Resources{Destinations: []string{path}}, true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait through overlap: %v", err)
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Fatal("overlap waiter returned early")
	}
	// Contention longer than the budget also keeps waiting until ctx ends.
	holdRegistry(t, r)
	c2, cancel2 := context.WithTimeout(ctx, 350*time.Millisecond)
	defer cancel2()
	if _, err := session(t, r).Acquire(c2, Resources{Destinations: []string{filepath.Join(root, "other")}}, true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait through long contention: %v", err)
	}
}

func TestRegistryContentionHonorsContext(t *testing.T) {
	r, root := setup(t)
	holdRegistry(t, r)
	c, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := session(t, r).Acquire(c, Resources{Destinations: []string{filepath.Join(root, "cache")}}, false)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline, got %v", err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("context ignored for %s", time.Since(start))
	}
}

// A reader's SHARED lock lets the writer enter BEGIN IMMEDIATE and run the
// Acquire closure but makes COMMIT report BUSY, so the closure must be retried.
func TestRetriedAcquireRecordsNewPathsOnce(t *testing.T) {
	r, root := setup(t)
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	s := session(t, r)
	outer, inner := filepath.Join(root, "outer"), filepath.Join(root, "inner")
	acquire(t, s, outer)
	release := holdReaders(t, r)
	releaseAfter(300*time.Millisecond, release)
	start := time.Now()
	g, err := s.Acquire(ctx, Resources{Destinations: []string{inner}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 200*time.Millisecond {
		t.Fatal("commit contention did not force a retry")
	}
	if g.Borrowed || len(g.NewPaths) != 1 || g.NewPaths[0] != inner {
		t.Fatalf("grant %+v", g)
	}
	all, err := r.Records(ctx)
	if err != nil || len(all) != 1 {
		t.Fatal(all, err)
	}
	want := []string{inner, outer}
	if len(all[0].Paths) != 2 || all[0].Paths[0] != want[0] || all[0].Paths[1] != want[1] {
		t.Fatalf("durable paths %v want %v", all[0].Paths, want)
	}
	// The in-memory record must match the durable one, so later calls work.
	if err := s.ReleaseUnstartedGrant(g); err != nil {
		t.Fatal(err)
	}
}

func TestSessionIDDoesNotAuthenticate(t *testing.T) {
	r, root := setup(t)
	a := session(t, r)
	path := filepath.Join(root, "cache")
	acquire(t, a, path)
	t.Setenv("CCOW_SESSION_ID", a.ID())
	b, err := r.NewSession(a.record.Ownership)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Acquire(ctx, Resources{Destinations: []string{path}}, false); !errors.Is(err, ErrBusy) {
		t.Fatal("environment or ownership identifier authenticated borrowing", err)
	}
}
func TestPrivateDatabaseAndDanglingAlias(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission and symlink fixture")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "registry")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "leases.sqlite"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if r, err := Open(ctx, dir); err == nil {
		r.Close()
		t.Fatal("public database accepted")
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(filepath.Join(root, "missing"), alias); err != nil {
		t.Fatal(err)
	}
	if _, err := normalize(Resources{Destinations: []string{filepath.Join(alias, "cache")}}); err == nil {
		t.Fatal("dangling alias accepted")
	}
}

func TestFailedCompleteSetDoesNotAcquirePartialResources(t *testing.T) {
	r, root := setup(t)
	a, b := session(t, r), session(t, r)
	x, y := filepath.Join(root, "x"), filepath.Join(root, "y")
	acquire(t, a, x)
	if _, err := b.Acquire(ctx, Resources{Sources: []string{x}, Destinations: []string{y}}, false); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	acquire(t, session(t, r), y)
}
func TestNestedMixedResources(t *testing.T) {
	r, root := setup(t)
	s := session(t, r)
	x, y := filepath.Join(root, "x"), filepath.Join(root, "y")
	acquire(t, s, x)
	g := acquire(t, s, x, y)
	if g.Borrowed || len(g.NewPaths) != 1 || !contains(g.NewPaths[0], y) || !contains(y, g.NewPaths[0]) {
		t.Fatalf("new destination selection: %+v", g)
	}
	if _, err := s.Acquire(ctx, Resources{Destinations: []string{root}}, true); err == nil {
		t.Fatal("nested ancestor extension accepted")
	}
}

func TestRegistryPathCharacters(t *testing.T) {
	name := "registry space 한글"
	if runtime.GOOS != "windows" {
		name += "?query#fragment"
	}
	root := t.TempDir()
	r, err := Open(ctx, filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	acquire(t, session(t, r), filepath.Join(root, "cache"))
	if _, err := os.Stat(filepath.Join(root, name, "leases.sqlite")); err != nil {
		t.Fatal(err)
	}
}
func TestParentTraversalRejected(t *testing.T) {
	if _, err := normalize(Resources{Destinations: []string{"alias/../cache"}}); err == nil {
		t.Fatal("ambiguous traversal accepted")
	}
}

func TestOpenWaitRegistryContention(t *testing.T) {
	r, root := setup(t)
	locked := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- r.transaction(ctx, func(_ *sql.Conn) error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	c, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
	defer cancel()
	_, err := OpenWait(c, filepath.Join(root, "registry"))
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected cancellable initialization wait, got %v", err)
	}
	reopened, err := OpenWait(ctx, filepath.Join(root, "registry"))
	if err != nil {
		t.Fatal(err)
	}
	reopened.Close()
}

func TestPreparedSourceReleaseRetainsDestination(t *testing.T) {
	r, root := setup(t)
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	s := session(t, r)
	source, dest := filepath.Join(root, "source"), filepath.Join(root, "dest")
	g, e := s.Acquire(ctx, Resources{Sources: []string{source}, Destinations: []string{dest}}, false)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ReleasePreparedSources([]string{source}, g); e != nil {
		t.Fatal(e)
	}
	other := session(t, r)
	acquire(t, other, source)
	if _, e = session(t, r).Acquire(ctx, Resources{Destinations: []string{dest}}, false); !errors.Is(e, ErrBusy) {
		t.Fatal("released writer protection", e)
	}
	if e = s.ReleasePreparedSources([]string{dest}, Grant{}); !errors.Is(e, ErrUnverified) {
		t.Fatal("accepted borrowed release", e)
	}
	finish(t, other)
	finish(t, s)
}

func TestReleaseUnstartedGrant(t *testing.T) {
	r, root := setup(t)
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	outer, source, dest := filepath.Join(root, "outer"), filepath.Join(root, "source"), filepath.Join(root, "dest")
	s := session(t, r)
	acquire(t, s, outer)
	paths := func() []string {
		t.Helper()
		all, err := r.Records(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range all {
			if v.ID == s.ID() {
				return v.Paths
			}
		}
		t.Fatal("record missing")
		return nil
	}
	g, err := s.Acquire(ctx, Resources{Sources: []string{source}, Destinations: []string{dest}}, false)
	if err != nil || len(g.NewPaths) != 2 {
		t.Fatal(g, err)
	}
	if err = s.ReleaseUnstartedGrant(g); err != nil {
		t.Fatal(err)
	}
	if got := paths(); len(got) != 1 || got[0] != outer {
		t.Fatalf("paths after release = %v", got)
	}
	// Released resources are free for other sessions and for a fresh grant.
	other := session(t, r)
	acquire(t, other, source)
	finish(t, other)
	if g, err = s.Acquire(ctx, Resources{Destinations: []string{dest}}, false); err != nil || g.Borrowed || len(g.NewPaths) != 1 {
		t.Fatal("released path still borrowed", g, err)
	}
	// An empty or borrowed-only grant is a no-op without a registry write.
	registry := filepath.Join(root, "registry")
	db, err := sql.Open("sqlite", filepath.Join(registry, "leases.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "PRAGMA busy_timeout=0; BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	if err = s.ReleaseUnstartedGrant(Grant{Borrowed: true}); err != nil {
		t.Fatalf("empty grant touched the registry: %v", err)
	}
	if _, err = conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if got := paths(); len(got) != 2 {
		t.Fatalf("paths changed by empty grant: %v", got)
	}
	// Releasing the whole record is refused and leaves it unchanged.
	if err = s.ReleaseUnstartedGrant(Grant{NewPaths: []string{outer, dest}}); !errors.Is(err, ErrUnverified) {
		t.Fatal("emptied the record", err)
	}
	if got := paths(); len(got) != 2 {
		t.Fatalf("refused release changed record: %v", got)
	}
	finish(t, s)
	if err = s.ReleaseUnstartedGrant(g); !errors.Is(err, ErrUnverified) {
		t.Fatal("released after finish", err)
	}
}
