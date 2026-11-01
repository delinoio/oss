package process

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

type darwinBackend struct {
	id        uint64
	signalled map[string]bool
}

// These XNU resource-coalition interfaces are not a portable public API.
// Validate the response shape and singleton launchd coalition before starting
// any user code. Unsupported hosts fail closed, never fall back to PID trees.
func coalition(pid int) (uint64, error) {
	var ids [5]uint64
	n, _, err := unix.Syscall6(unix.SYS_PROC_INFO, 2, uintptr(pid), 20, 0, uintptr(unsafe.Pointer(&ids)), unsafe.Sizeof(ids))
	if err != 0 {
		return 0, err
	}
	if n != unsafe.Sizeof(ids) || ids[0] == 0 {
		return 0, ErrUnavailable
	}
	return ids[0], nil
}
func coalitionCount(id uint64) (uint64, error) {
	var usage [2]uint64
	size := uint64(unsafe.Sizeof(usage))
	_, _, err := unix.Syscall6(unix.SYS_COALITION_INFO, 1, uintptr(unsafe.Pointer(&id)), uintptr(unsafe.Pointer(&usage)), uintptr(unsafe.Pointer(&size)), 0, 0)
	if err != 0 {
		return 0, err
	}
	if size != uint64(unsafe.Sizeof(usage)) || usage[1] > usage[0] {
		return 0, ErrReconciliation
	}
	return usage[0] - usage[1], nil
}
func newBackend(s *Scope) (nativeBackend, error) {
	id, err := coalition(os.Getpid())
	if err != nil {
		return nil, err
	}
	count, err := coalitionCount(id)
	if err != nil || count != 1 {
		return nil, ErrUnavailable
	}
	s.NativeID = id
	return &darwinBackend{id: id, signalled: map[string]bool{}}, nil
}
func launch(s Scope, endpoint string, _ Streams) (func(), error) {
	executable, err := os.Executable()
	if err != nil {
		return func() {}, err
	}
	label := "io.delino.ccow." + s.ID
	domain := fmt.Sprintf("user/%d", os.Getuid())
	escape := func(s string) string { var b bytes.Buffer; _ = xml.EscapeText(&b, []byte(s)); return b.String() }
	var b bytes.Buffer
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Label</key><string>%s</string><key>ProgramArguments</key><array>`, escape(label))
	for _, arg := range []string{executable, supervisorArgument, s.Directory, endpoint} {
		fmt.Fprintf(&b, "<string>%s</string>", escape(arg))
	}
	b.WriteString(`</array><key>LimitLoadToSessionType</key><string>Background</string><key>RunAtLoad</key><true/><key>KeepAlive</key><false/><key>AbandonProcessGroup</key><true/></dict></plist>`)
	path := filepath.Join(s.Directory, "supervisor.plist")
	if err = os.WriteFile(path, b.Bytes(), 0600); err != nil {
		return func() {}, err
	}
	ctl := func(args ...string) error {
		c := exec.Command("/bin/launchctl", args...)
		c.Env = []string{"PATH=/usr/bin:/bin"}
		return c.Run()
	}
	if err = ctl("bootstrap", domain, path); err != nil {
		return func() {}, err
	}
	return func() {
		// Never bootout an unfinished supervisor: it still owns the writers.
		// A stale unloaded job definition is safe; interrupted recovery stays blocked.
		if Reconcile(s) == nil {
			_ = ctl("bootout", domain+"/"+label)
		}
	}, nil
}
func (b *darwinBackend) start(c *exec.Cmd) (<-chan int, error) { return startUnix(c) }
func (b *darwinBackend) close()                                {}
func (b *darwinBackend) empty(rootDone bool) (bool, error) {
	if !rootDone {
		return false, nil
	}
	n, err := coalitionCount(b.id)
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, ErrReconciliation
	}
	return n == 1, nil
}
func (b *darwinBackend) signal(force bool) error {
	rows, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return err
	}
	for _, row := range rows {
		pid := int(row.Proc.P_pid)
		if pid <= 0 || pid == os.Getpid() {
			continue
		}
		id, err := coalition(pid)
		if err != nil || id != b.id {
			continue
		}
		key := fmt.Sprintf("%d:%d:%d", pid, row.Proc.P_starttime.Sec, row.Proc.P_starttime.Usec)
		if !force && b.signalled[key] {
			continue
		}
		current, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if err != nil || current.Proc.P_starttime != row.Proc.P_starttime {
			continue
		}
		if id, err = coalition(pid); err != nil || id != b.id {
			continue
		}
		sig := syscall.SIGTERM
		if force {
			sig = syscall.SIGKILL
		}
		// Darwin offers no pidfd equivalent here. Birth/membership revalidation
		// narrows PID recycling risk but cannot atomically bind kill to identity.
		if syscall.Kill(pid, sig) == nil {
			b.signalled[key] = true
		}
	}
	return nil
}
