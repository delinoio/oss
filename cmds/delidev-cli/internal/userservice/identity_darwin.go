package userservice

import (
	"bytes"
	"strconv"

	"golang.org/x/sys/unix"
)

func processOwner(pid int) (string, string, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || info.Proc.P_pid != int32(pid) {
		return "", "", failure()
	}
	// KERN_PROCARGS2 starts with argc then the executable path. The kernel
	// provides this identity without process output or layout-dependent parsing.
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(raw) < 5 {
		return "", "", failure()
	}
	end := bytes.IndexByte(raw[4:], 0)
	if end <= 0 {
		return "", "", failure()
	}
	return string(raw[4 : 4+end]), strconv.FormatUint(uint64(info.Eproc.Ucred.Uid), 10), nil
}
func renameExclusive(from, to string) error { return unix.RenamexNp(from, to, unix.RENAME_EXCL) }
