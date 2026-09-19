//go:build !windows

package core

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type managedProcess struct {
	cmd      *exec.Cmd
	identity Process
	tracked  map[int]Process
}
type processRow struct {
	pid, ppid, group int
	birth, state     string
}

func processRows() ([]processRow, error) {
	c := exec.Command("ps", "-axo", "pid=,ppid=,pgid=,stat=,lstart=")
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
		rows = append(rows, processRow{pid, ppid, group, strings.Join(f[4:], " "), f[3]})
	}
	return rows, nil
}
func ProcessIdentity(pid int) (Process, error) {
	rows, e := processRows()
	if e != nil {
		return Process{}, e
	}
	for _, r := range rows {
		if r.pid == pid {
			return Process{PID: pid, Birth: r.birth, Group: r.group}, nil
		}
	}
	return Process{}, os.ErrNotExist
}
func ProcessAlive(p Process) bool {
	if p.PID <= 0 || p.Birth == "" {
		return false
	}
	current, e := ProcessIdentity(p.PID)
	return e == nil && current.Birth == p.Birth
}
func startProcess(c *exec.Cmd) (*managedProcess, error) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if e := c.Start(); e != nil {
		return nil, e
	}
	p, e := ProcessIdentity(c.Process.Pid)
	if e != nil {
		p = Process{PID: c.Process.Pid, Group: c.Process.Pid}
	}
	return &managedProcess{cmd: c, identity: p, tracked: map[int]Process{}}, nil
}
func (p *managedProcess) sample() error {
	rows, e := processRows()
	if e != nil {
		return e
	}
	for changed := true; changed; {
		changed = false
		for _, r := range rows {
			_, parent := p.tracked[r.ppid]
			if r.group == p.identity.Group || r.ppid == p.identity.PID || parent {
				if _, ok := p.tracked[r.pid]; !ok {
					p.tracked[r.pid] = Process{PID: r.pid, Birth: r.birth, Group: r.group}
					changed = true
				}
			}
		}
	}
	return nil
}
func (p *managedProcess) terminate() error {
	if e := p.sample(); e != nil {
		return E("process-reconciliation-failed", "cannot inspect owned process descendants", 3)
	}
	// The original leader's group is exclusive to this check. Birth matching avoids killing a reused PID.
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		for _, v := range p.tracked {
			if ProcessAlive(v) {
				_ = syscall.Kill(v.PID, sig)
			}
		}
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			rows, e := processRows()
			if e != nil {
				return e
			}
			active := false
			for _, r := range rows {
				v, ok := p.tracked[r.pid]
				if ok && v.Birth == r.birth && !strings.HasPrefix(r.state, "Z") {
					active = true
				}
			}
			if !active {
				return nil
			}
			_ = p.sample()
			time.Sleep(30 * time.Millisecond)
		}
	}
	return E("process-reconciliation-failed", fmt.Sprintf("owned process group %d remains active; exclusive replacement is blocked", p.identity.Group), 3)
}
func (p *managedProcess) close() {}
func ReconcileProcess(p Process) error {
	if p.PID <= 0 {
		return nil
	}
	current, e := ProcessIdentity(p.PID)
	if e == nil && p.Birth != "" && current.Birth != p.Birth {
		return nil
	}
	m := &managedProcess{identity: p, tracked: map[int]Process{}}
	return m.terminate()
}
func Detached(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
