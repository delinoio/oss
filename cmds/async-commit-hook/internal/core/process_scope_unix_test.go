//go:build !windows

package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestScopeStartBarrierAndOutput(t *testing.T) {
	var output bytes.Buffer
	c := exec.Command("sh", "-c", "printf scope-output; exit 7")
	c.Env = []string{"PATH=/usr/bin:/bin"}
	c.Stdout = &output
	p, err := startProcess(c, filepath.Join(t.TempDir(), "scope"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	if err = p.resume(); err != nil {
		t.Fatal(err)
	}
	if err = p.wait(); err == nil {
		t.Fatal("exit status missing")
	} else if e, ok := err.(interface{ ExitCode() int }); !ok || e.ExitCode() != 7 {
		t.Fatal(err)
	}
	if output.String() != "scope-output" {
		t.Fatalf("output %q", output.String())
	}
	if err = p.terminate(); err != nil {
		t.Fatal(err)
	}
}

// Re-exec fixtures avoid Python, a C compiler, and timing-dependent ancestry
// sampling. The leaf survives both intermediate parents, starts a new session,
// clears its environment, and changes directory before announcing readiness.
func init() {
	if len(os.Args) != 5 || os.Args[1] != "__ach_test_orphan" {
		return
	}
	stage, marker, barrier := os.Args[2], os.Args[3], os.Args[4]
	if stage == "leaf" {
		os.Clearenv()
		_ = os.Chdir("/")
		p, err := ProcessIdentity(os.Getpid())
		if err != nil {
			os.Exit(3)
		}
		b, _ := json.Marshal(p)
		if os.WriteFile(marker, b, 0600) != nil {
			os.Exit(3)
		}
		for {
			time.Sleep(10 * time.Millisecond)
		}
	}
	exe, _ := os.Executable()
	next := "middle"
	if stage == "middle" {
		next = "leaf"
	}
	c := exec.Command(exe, "__ach_test_orphan", next, marker, barrier)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	c.Env = []string{"PATH=/usr/bin:/bin"}
	if c.Start() != nil {
		os.Exit(3)
	}
	if stage == "middle" {
		_ = c.Process.Release()
		os.Exit(0)
	}
	if c.Wait() != nil {
		os.Exit(3)
	}
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	for barrier != "exit" {
		if _, err := os.Stat(barrier); err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	os.Exit(0)
}

func awaitOrphan(t *testing.T, marker string) Process {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		b, err := os.ReadFile(marker)
		var p Process
		if err == nil && json.Unmarshal(b, &p) == nil && p.PID > 0 {
			return p
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("orphan fixture did not become ready")
	return Process{}
}
func TestScopeReapsDaemonizedDescendants(t *testing.T) {
	for _, mode := range []string{"normal-exit", "cancel", "worker-disconnect", "recovery"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "leaf.json")
			barrier := filepath.Join(dir, "exit")
			exe, _ := os.Executable()
			c := exec.Command(exe, "__ach_test_orphan", "root", marker, barrier)
			c.Env = []string{"PATH=/usr/bin:/bin"}
			p, err := startProcess(c, filepath.Join(dir, "scope"))
			if err != nil {
				t.Fatal(err)
			}
			defer p.close()
			// The start barrier must prevent even the first fixture side effect.
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("command ran before resume")
			}
			if err = p.resume(); err != nil {
				t.Fatal(err)
			}
			child := awaitOrphan(t, marker)
			defer func() {
				if ProcessAlive(child) {
					_ = syscall.Kill(child.PID, syscall.SIGKILL)
				}
			}()
			switch mode {
			case "normal-exit":
				if err = os.WriteFile(barrier, nil, 0600); err != nil {
					t.Fatal(err)
				}
				if err = p.wait(); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				if err = p.terminate(); err != nil {
					t.Fatal(err)
				}
			case "worker-disconnect":
				// Closing both directions is what a killed worker does to its socket.
				_ = p.conn.Close()
				<-p.done
				if err = ReconcileProcess(p.snapshot()); err != nil {
					t.Fatal(err)
				}
			case "recovery":
				if err = ReconcileProcess(p.snapshot()); err != nil {
					t.Fatal(err)
				}
			}
			if ProcessAlive(child) {
				t.Fatal("daemonized child survived confirmed completion")
			}
		})
	}
}

func TestScopeMissingJournalCannotConfirmCompletion(t *testing.T) {
	p := Process{PID: 123, ScopeDir: filepath.Join(t.TempDir(), "missing")}
	if err := ReconcileProcess(p); err == nil {
		t.Fatal("missing ownership proof was accepted")
	}
}

func TestScopeCancellationDoesNotTouchAnotherCheck(t *testing.T) {
	exe, _ := os.Executable()
	dir := t.TempDir()
	start := func(name string) (*managedProcess, Process) {
		marker := filepath.Join(dir, name+".json")
		c := exec.Command(exe, "__ach_test_orphan", "root", marker, filepath.Join(dir, name+".exit"))
		c.Env = []string{"PATH=/usr/bin:/bin"}
		p, err := startProcess(c, filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.close)
		if err = p.resume(); err != nil {
			t.Fatal(err)
		}
		child := awaitOrphan(t, marker)
		t.Cleanup(func() {
			if ProcessAlive(child) {
				_ = syscall.Kill(child.PID, syscall.SIGKILL)
			}
		})
		return p, child
	}
	first, a := start("first")
	second, b := start("second")
	if err := first.terminate(); err != nil {
		t.Fatal(err)
	}
	if ProcessAlive(a) || !ProcessAlive(b) {
		t.Fatal("cancellation crossed check ownership")
	}
	if err := second.terminate(); err != nil {
		t.Fatal(err)
	}
}

func TestScopeJournalNeverStoresResolvedEnvironment(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "scope")
	c := exec.Command("sh", "-c", "true")
	c.Env = []string{"PATH=/usr/bin:/bin", "TOKEN=super-secret-fixture"}
	p, err := startProcess(c, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	if err = p.resume(); err != nil {
		t.Fatal(err)
	}
	if err = p.wait(); err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err == nil && bytes.Contains(b, []byte("super-secret-fixture")) {
			t.Fatal("resolved environment persisted")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLegacyScopeCannotClaimUnknownDescendantsExited(t *testing.T) {
	if err := ReconcileProcess(Process{PID: os.Getpid(), Birth: "different-incarnation", Group: os.Getpid()}); err == nil {
		t.Fatal("legacy sampled identity accepted as complete ownership")
	}
}

func TestReplaceReapsDaemonizedDescendantsBeforeNextStarts(t *testing.T) {
	exe, _ := os.Executable()
	dir := t.TempDir()
	marker := filepath.Join(dir, "leaf.json")
	barrier := filepath.Join(dir, "exit")
	command := quoteSh(exe) + " __ach_test_orphan root " + quoteSh(marker) + " " + quoteSh(barrier)
	s, repo := fixture(t, fmt.Sprintf("version=1\n[checks.test]\ncommand=%q\npolicy=\"replace\"\n", command))
	a, err := s.Submit(context.Background(), repo, "", false)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	go func() { done <- s.RunOne(a.RunID) }()
	child := awaitOrphan(t, marker)
	defer func() {
		if ProcessAlive(child) {
			_ = syscall.Kill(child.PID, syscall.SIGKILL)
		}
	}()
	b, err := s.Submit(context.Background(), repo, "", false)
	if err != nil {
		t.Fatal(err)
	}
	go func() { done <- s.RunOne(b.RunID) }()
	awaitCheck(t, s, b.RunID, Running)
	if ProcessAlive(child) {
		t.Fatal("exclusive replacement started with an orphan still alive")
	}
	if err = s.Store.Cancel(b.RunID, Cancelled); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = <-done; err != nil {
			t.Fatal(err)
		}
	}
	ar, err := s.Store.Run(a.RunID)
	if err != nil || ar.State != Replaced {
		t.Fatal("replacement state not recorded", err)
	}
}
