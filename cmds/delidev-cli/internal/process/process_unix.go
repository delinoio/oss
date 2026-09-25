//go:build !windows

package process

import (
	"context"
	"golang.org/x/sys/unix"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type processRow struct {
	pid, ppid, group int
	birth, state     string
}

func processRows() ([]processRow, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "/bin/ps", "-axo", "pid=,ppid=,pgid=,stat=,lstart=")
	c.WaitDelay = time.Second
	c.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
	out, e := c.Output()
	if e != nil {
		return nil, e
	}
	rows := []processRow{}
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) < 9 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		ppid, _ := strconv.Atoi(f[1])
		group, _ := strconv.Atoi(f[2])
		birth, err := preciseBirth(pid)
		if err != nil {
			continue
		}
		rows = append(rows, processRow{pid, ppid, group, birth, f[3]})
	}
	return rows, nil
}
func ProcessIdentity(pid int) (Process, error) {
	birth, err := preciseBirth(pid)
	if err != nil {
		return Process{}, err
	}
	group, err := unix.Getpgid(pid)
	if err != nil {
		return Process{}, err
	}
	return Process{PID: pid, Birth: birth, Group: group}, nil
}
func ProcessAlive(p Process) bool {
	if p.PID <= 0 || p.Birth == "" {
		return false
	}
	current, e := ProcessIdentity(p.PID)
	return e == nil && current.Birth == p.Birth
}
func ReconcileProcess(p Process) error {
	if p.ScopeDir != "" {
		scope, err := readScope(p.ScopeDir)
		if err != nil || scope.OwnerID != p.OwnerID || p.OwnerID.Validate() != nil || scope.Owner.PID != p.PID || scope.Owner.Birth != p.Birth {
			return ownershipError()
		}
		if scope.Complete {
			return nil
		}
		return reconcileScope(p.ScopeDir, p.PID != 0)
	}
	if p.PID <= 0 {
		return nil
	}
	// Legacy sampled ownership cannot exclude an already-reparented descendant.
	// Keep the claim instead of converting absence of known PIDs into proof.
	return ownershipError()
}
func Detached(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
