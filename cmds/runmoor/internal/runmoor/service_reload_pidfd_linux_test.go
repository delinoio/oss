//go:build linux

package runmoor

import (
	"encoding/json"
	"errors"
	"os"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// Kernel acceptance uses only bounded fixture manager/worker processes. It
// does not create a systemd unit or launch a real runner/account job.
func TestLinuxManagerPIDFDBindsExitAndPreservesWorker(t *testing.T) {
	manager, ready := reloadNativeChild(t, "manager")
	var worker HostProcess
	if err := json.NewDecoder(ready).Decode(&worker); err != nil || worker.PID <= 0 || worker.Start == "" {
		t.Fatal("fixture readiness failed", err)
	}
	t.Cleanup(func() {
		if alive, _ := tartRunProcessAlive(worker.PID, worker.Start); alive {
			p, _ := os.FindProcess(worker.PID)
			p.Signal(syscall.SIGTERM)
		}
	})
	handle, err := openReloadManager(manager.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	flags, err := unix.FcntlInt(uintptr(handle.(*reloadPIDFD).fd), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("pidfd can leak across exec", err)
	}
	if err := handle.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Wait(); err == nil {
		t.Fatal("manager was not signaled")
	}
	if err := handle.Kill(); !errors.Is(err, unix.ESRCH) {
		t.Fatal("retained descriptor did not report original exit", err)
	}
	if alive, err := tartRunProcessAlive(worker.PID, worker.Start); err != nil || !alive {
		t.Fatal("manager descriptor signal reached independent worker", err)
	}
}
