//go:build linux

package runmoor

import "golang.org/x/sys/unix"

type reloadPIDFD struct{ fd int }

func openReloadManager(pid int) (reloadManagerHandle, error) {
	// pidfd_open produces a close-on-exec descriptor bound to this generation.
	// Never fall back to kill(pid), a process group, or a systemd unit name.
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return nil, err
	}
	return &reloadPIDFD{fd: fd}, nil
}
func (p *reloadPIDFD) Kill() error  { return unix.PidfdSendSignal(p.fd, unix.SIGKILL, nil, 0) }
func (p *reloadPIDFD) Close() error { return unix.Close(p.fd) }
