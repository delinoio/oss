package process

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// A dedicated subreaper retains orphan ancestry even when a descendant calls
// setsid. No writable delegated cgroup is assumed: most user sessions do not
// have one. If the subreaper dies, its unfinished journal remains blocked.
// Requiring pidfd avoids signalling a recycled PID while enumerating the tree.
type linuxBackend struct{ signalled map[string]bool }

func newBackend(_ *Scope) (nativeBackend, error) {
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		return nil, err
	}
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		return nil, err
	}
	unix.Close(fd)
	return &linuxBackend{signalled: map[string]bool{}}, nil
}
func launch(s Scope, endpoint string, _ Streams) (func(), error) {
	executable, err := os.Executable()
	if err != nil {
		return func() {}, err
	}
	cmd := exec.Command(executable, supervisorArgument, s.Directory, endpoint)
	cmd.Env = []string{}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		return func() {}, err
	}
	go func() { _ = cmd.Wait() }()
	return func() {}, nil
}
func (b *linuxBackend) start(c *exec.Cmd) (<-chan int, error) { return startUnix(c) }
func (b *linuxBackend) close()                                {}

type procRow struct {
	pid, ppid int
	birth     string
}

func linuxRow(pid int) (procRow, error) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procRow{}, err
	}
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return procRow{}, ErrReconciliation
	}
	f := strings.Fields(string(raw)[end+1:])
	if len(f) < 20 {
		return procRow{}, ErrReconciliation
	}
	ppid, err := strconv.Atoi(f[1])
	return procRow{pid, ppid, f[19]}, err
}
func (b *linuxBackend) signal(force bool) error {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return err
	}
	rows := []procRow{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		r, err := linuxRow(pid)
		if err == nil {
			rows = append(rows, r)
		}
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
		if r.pid == os.Getpid() || !owned[r.pid] {
			continue
		}
		key := strconv.Itoa(r.pid) + ":" + r.birth
		if !force && b.signalled[key] {
			continue
		}
		fd, err := unix.PidfdOpen(r.pid, 0)
		if err != nil {
			continue
		}
		current, err := linuxRow(r.pid)
		if err == nil && current.birth == r.birth {
			sig := unix.SIGTERM
			if force {
				sig = unix.SIGKILL
			}
			if unix.PidfdSendSignal(fd, sig, nil, 0) == nil {
				b.signalled[key] = true
			}
		}
		unix.Close(fd)
	}
	return nil
}
func (b *linuxBackend) empty(rootDone bool) (bool, error) {
	if !rootDone {
		return false, nil
	}
	// exec.Cmd.Wait alone reaps the root. Once it finishes, ECHILD proves no
	// live descendants or unreaped adopted zombies remain; snapshots cannot.
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
