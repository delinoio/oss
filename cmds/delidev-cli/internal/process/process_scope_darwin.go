package process

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// These kernel interfaces exist in the macOS 13 XNU release (8792.41.9).
// Resource coalition membership is inherited by fork/exec and is independent
// of process groups, parent PIDs, working directories and environment strings.
// Fail closed if a future OS rejects the interface or returns a changed shape.
func processCoalition(pid int) (uint64, error) {
	var ids [5]uint64
	n, _, err := unix.Syscall6(unix.SYS_PROC_INFO, 2, uintptr(pid), 20, 0, uintptr(unsafe.Pointer(&ids)), unsafe.Sizeof(ids))
	if err != 0 {
		return 0, err
	}
	if n != unsafe.Sizeof(ids) || ids[0] == 0 {
		return 0, scopeError()
	}
	return ids[0], nil
}
func coalitionActive(id uint64) (uint64, error) {
	var usage [2]uint64
	size := uint64(unsafe.Sizeof(usage))
	_, _, err := unix.Syscall6(unix.SYS_COALITION_INFO, 1, uintptr(unsafe.Pointer(&id)), uintptr(unsafe.Pointer(&usage)), uintptr(unsafe.Pointer(&size)), 0, 0)
	if err == syscall.ESRCH {
		return 0, nil
	}
	if err != 0 {
		return 0, err
	}
	if usage[1] > usage[0] {
		return 0, scopeError()
	}
	return usage[0] - usage[1], nil
}
func bootIdentity() (string, error) { return unix.Sysctl("kern.bootsessionuuid") }
func prepareScope(s *processScope, _ string) error {
	s.Label = "io.delino.delidev.execution." + string(domain.NewID())
	s.Domain = fmt.Sprintf("user/%d", os.Getuid())
	return nil
}
func launchctl(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "/bin/launchctl", args...)
	c.WaitDelay = time.Second
	c.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
	return c.Run()
}
func launchScope(s processScope, dir, socket string) (func() error, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	text := func(value string) string {
		var out bytes.Buffer
		_ = xml.EscapeText(&out, []byte(value))
		return out.String()
	}
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Label</key><string>%s</string><key>ProgramArguments</key><array>`, text(s.Label))
	for _, arg := range []string{executable, supervisorArgument, dir, socket} {
		fmt.Fprintf(&b, "<string>%s</string>", text(arg))
	}
	b.WriteString(`</array><key>LimitLoadToSessionType</key><string>Background</string><key>RunAtLoad</key><true/><key>KeepAlive</key><false/><key>ExitTimeOut</key><integer>1</integer><key>AbandonProcessGroup</key><true/><key>EnvironmentVariables</key><dict><key>PATH</key><string>/usr/bin:/bin</string></dict></dict></plist>`)
	path := filepath.Join(dir, "supervisor.plist")
	if err = security.WriteAtomic(path, b.Bytes()); err != nil {
		return nil, err
	}
	if err = launchctl("bootstrap", s.Domain, path); err != nil {
		_ = launchctl("bootout", s.Domain+"/"+s.Label)
		return nil, err
	}
	return func() error {
		// bootout is only job-definition cleanup: it does not reap a daemon that
		// changed process group. The coalition count is the actual completion proof.
		saved, err := readScope(dir)
		if err != nil {
			return err
		}
		if !saved.Complete && saved.Started {
			return scopeError()
		}
		return recoverScope(saved, dir)
	}, nil
}
func initializeScope(s *processScope) error {
	p, err := ProcessIdentity(os.Getpid())
	if err != nil {
		return err
	}
	id, err := processCoalition(p.PID)
	if err != nil {
		return err
	}
	count, err := coalitionActive(id)
	if err != nil || count != 1 {
		return scopeError()
	}
	s.Owner = p
	s.Coalition = id
	return nil
}
func killCoalition(id uint64, exclude int) error {
	rows, err := processRows()
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.pid == exclude {
			continue
		}
		candidate, err := processCoalition(r.pid)
		if err != nil || candidate != id {
			continue
		}
		p := Process{PID: r.pid, Birth: r.birth}
		// Revalidate both birth and membership immediately before signalling.
		if ProcessAlive(p) {
			if current, e := processCoalition(r.pid); e == nil && current == id {
				_ = syscall.Kill(r.pid, syscall.SIGKILL)
			}
		}
	}
	return nil
}
func drainScope(s processScope, _ bool) (bool, error) {
	count, err := coalitionActive(s.Coalition)
	if err != nil {
		return false, err
	}
	if count == 1 {
		return true, nil
	}
	if count == 0 {
		return false, scopeError()
	}
	return false, killCoalition(s.Coalition, os.Getpid())
}
func recoverScope(s processScope, dir string) error {
	// A journal without an owner is a launch interrupted before the start
	// barrier. The exact randomly-named job cannot have run the command yet.
	_ = launchctl("bootout", s.Domain+"/"+s.Label)
	if s.Coalition == 0 {
		return nil
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		count, err := coalitionActive(s.Coalition)
		if errors.Is(err, syscall.ESRCH) || err == nil && count == 0 {
			s.Complete = true
			return saveScope(dir, s)
		}
		if err != nil {
			return scopeError()
		}
		if err = killCoalition(s.Coalition, 0); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return scopeError()
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func scopeHasSurvivors(s processScope) (bool, error) {
	if s.Coalition == 0 {
		return false, scopeError()
	}
	n, err := coalitionActive(s.Coalition)
	return n > 0, err
}
