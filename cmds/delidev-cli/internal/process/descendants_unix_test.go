//go:build !windows

package process

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func init() {
	if len(os.Args) != 5 || os.Args[1] != "__delidev_orphan_fixture" {
		return
	}
	stage, marker, barrier := os.Args[2], os.Args[3], os.Args[4]
	if stage == "leaf" {
		os.Clearenv()
		_ = os.Chdir("/")
		identity, err := ProcessIdentity(os.Getpid())
		if err != nil {
			os.Exit(3)
		}
		raw, _ := json.Marshal(identity)
		if os.WriteFile(marker, raw, 0600) != nil {
			os.Exit(3)
		}
		for {
			time.Sleep(time.Second)
		}
	}
	executable, _ := os.Executable()
	next := "middle"
	if stage == "middle" {
		next = "leaf"
	}
	command := exec.Command(executable, "__delidev_orphan_fixture", next, marker, barrier)
	command.Env = []string{"PATH=/usr/bin:/bin"}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if command.Start() != nil {
		os.Exit(3)
	}
	if stage == "middle" {
		_ = command.Process.Release()
		os.Exit(0)
	}
	if command.Wait() != nil {
		os.Exit(3)
	}
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	for {
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
		raw, err := os.ReadFile(marker)
		var identity Process
		if err == nil && json.Unmarshal(raw, &identity) == nil && identity.PID > 0 {
			return identity
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("orphan did not become ready")
	return Process{}
}
func orphan(t *testing.T) (*Handle, Process, string) {
	t.Helper()
	c := config(t, "sleep")
	marker := filepath.Join(c.Cwd, "leaf.json")
	barrier := filepath.Join(c.Cwd, "exit")
	c.Args = []string{"__delidev_orphan_fixture", "root", marker, barrier}
	h, err := Start(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("start barrier allowed side effects")
	}
	if err := h.Resume(); err != nil {
		t.Fatal(err)
	}
	identity := awaitOrphan(t, marker)
	t.Cleanup(func() {
		if ProcessAlive(identity) {
			_ = syscall.Kill(identity.PID, syscall.SIGKILL)
		}
	})
	return h, identity, barrier
}
func TestOwnedDaemonizedDescendants(t *testing.T) {
	for _, mode := range []string{"natural-exit", "stop", "disconnect", "reconcile", "supervisor-death"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "supervisor-death" && runtime.GOOS != "darwin" {
				t.Skip("Linux supervisor death intentionally retains ancestry uncertainty")
			}
			h, child, barrier := orphan(t)
			switch mode {
			case "natural-exit":
				if err := os.WriteFile(barrier, nil, 0600); err != nil {
					t.Fatal(err)
				}
				if err := h.Wait(); err != nil {
					t.Fatal(err)
				}
			case "stop":
				if err := h.Stop(); err != nil {
					t.Fatal(err)
				}
			case "disconnect":
				_ = h.native.conn.Close()
				<-h.Done()
				if err := ReconcileProcess(h.Identity()); err != nil {
					t.Fatal(err)
				}
			case "reconcile":
				if err := ReconcileProcess(h.Identity()); err != nil {
					t.Fatal(err)
				}
			case "supervisor-death":
				if err := syscall.Kill(h.Identity().PID, syscall.SIGKILL); err != nil {
					t.Fatal(err)
				}
				<-h.Done()
				if err := ReconcileProcess(h.Identity()); err != nil {
					t.Fatal(err)
				}
			}
			if ProcessAlive(child) {
				t.Fatal("owned orphan survived confirmed stop")
			}
		})
	}
}
func TestStopCannotCrossExecutionScope(t *testing.T) {
	first, a, _ := orphan(t)
	second, b, _ := orphan(t)
	if err := first.Stop(); err != nil {
		t.Fatal(err)
	}
	if ProcessAlive(a) || !ProcessAlive(b) {
		t.Fatal("stop crossed execution ownership")
	}
	if err := second.Stop(); err != nil {
		t.Fatal(err)
	}
}
func TestMissingJournalDoesNotProveStopped(t *testing.T) {
	c := config(t, "sleep")
	if err := ReconcileProcess(Process{ScopeDir: filepath.Join(c.Directory, "missing"), OwnerID: c.OwnerID, PID: 123, Birth: "old"}); err == nil {
		t.Fatal("missing journal accepted as proof")
	}
}
func TestBlockedInputCanStillBeCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := config(t, "sleep")
	h, err := Start(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := h.Resume(); err != nil {
		t.Fatal(err)
	}
	writing := make(chan error, 1)
	go func() { _, err := h.Write(make([]byte, 1<<20)); writing <- err }()
	cancel()
	select {
	case <-h.Done():
	case <-time.After(12 * time.Second):
		t.Fatal("blocked stdin prevented cancellation")
	}
	select {
	case err := <-writing:
		if err == nil {
			t.Fatal("blocked input falsely acknowledged")
		}
	case <-time.After(time.Second):
		t.Fatal("input writer remained blocked")
	}
}
