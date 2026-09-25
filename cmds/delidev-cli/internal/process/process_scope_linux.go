package process

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func bootIdentity() (string, error) {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(b)), err
}
func prepareScope(_ *processScope, _ string) error { return nil }
func launchScope(_ processScope, dir, socket string) (func() error, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	c := exec.Command(executable, supervisorArgument, dir, socket)
	c.Env = []string{"PATH=/usr/bin:/bin"}
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = c.Start(); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	go func() { _ = c.Wait(); close(done) }()
	return func() error {
		saved, err := readScope(dir)
		if err != nil {
			return err
		}
		if saved.Owner.PID == 0 {
			_ = c.Process.Kill()
		}
		if err = recoverScope(saved, dir); err != nil {
			return err
		}
		select {
		case <-done:
			return nil
		case <-time.After(3 * time.Second):
			return scopeError()
		}
	}, nil
}
func initializeScope(s *processScope) error {
	// A dedicated per-execution subreaper adopts all orphaned descendants, even if
	// every intermediate process exits between observations or calls setsid.
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		return err
	}
	p, err := ProcessIdentity(os.Getpid())
	s.Owner = p
	return err
}
func drainScope(_ processScope, rootDone bool) (bool, error) {
	rows, err := processRows()
	if err != nil {
		return false, err
	}
	owned := map[int]bool{os.Getpid(): true}
	for changed := true; changed; {
		changed = false
		for _, r := range rows {
			if owned[r.ppid] && !owned[r.pid] {
				owned[r.pid] = true
				changed = true
			}
		}
	}
	for _, r := range rows {
		if r.pid != os.Getpid() && owned[r.pid] && ProcessAlive(Process{PID: r.pid, Birth: r.birth}) {
			_ = syscall.Kill(r.pid, syscall.SIGKILL)
		}
	}
	if !rootDone {
		return false, nil
	}
	// Only start reaping after exec.Cmd.Wait has reaped the original shell.
	// ECHILD is a kernel proof: any descendant would either have a live ancestor
	// here or already be adopted by this subreaper. A process-list sample is not.
	for {
		var status syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &status, syscall.WNOHANG, nil)
		if errors.Is(err, syscall.ECHILD) {
			return true, nil
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return false, err
		}
		if pid == 0 {
			return false, nil
		}
	}
}
func recoverScope(s processScope, dir string) error {
	if s.Owner.PID == 0 {
		return nil
	}
	if ProcessAlive(s.Owner) {
		_ = syscall.Kill(s.Owner.PID, syscall.SIGTERM)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		current, err := readScope(dir)
		if err != nil {
			return err
		}
		if !ProcessAlive(s.Owner) {
			if current.Complete || !current.Started {
				return nil
			}
			// If the supervisor itself was killed, the kernel no longer retains its
			// adopted ancestry. Never guess from a now-empty PID list. A host reboot is
			// the independent proof used on the next recovery attempt.
			return scopeError()
		}
		if time.Now().After(deadline) {
			return scopeError()
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func scopeHasSurvivors(_ processScope) (bool, error) { return true, nil }
