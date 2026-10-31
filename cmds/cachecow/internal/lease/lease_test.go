package lease

import (
	"bufio"
	"context"
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

func TestRegistryTransactionContention(t *testing.T) {
	r, root := setup(t)
	other, err := Open(ctx, filepath.Join(root, "registry"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	conn, err := r.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(ctx, "ROLLBACK")
	start := time.Now()
	if _, err := session(t, other).Acquire(ctx, Resources{Destinations: []string{filepath.Join(root, "cache")}}, false); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("clean blocked on registry transaction")
	}
	c, cancel := context.WithTimeout(ctx, 60*time.Millisecond)
	defer cancel()
	if _, err := session(t, other).Acquire(c, Resources{Destinations: []string{filepath.Join(root, "cache")}}, true); !errors.Is(err, context.DeadlineExceeded) {
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
