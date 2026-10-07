// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"golang.org/x/sys/unix"
	"os"
	"sync"
)

func watchDesktopParent(parent int, stop func()) (func(), error) {
	fd, err := unix.Kqueue()
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(fd)
	change := unix.Kevent_t{Ident: uint64(parent), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT}
	if _, err := unix.Kevent(fd, []unix.Kevent_t{change}, nil, nil); err != nil {
		unix.Close(fd)
		return nil, err
	}
	if os.Getppid() != parent {
		unix.Close(fd)
		stop()
		return func() {}, nil
	}

	var once sync.Once
	finished := make(chan struct{})
	closing := make(chan struct{})
	go func() {
		defer close(finished)
		events := make([]unix.Kevent_t, 1)
		timeout := unix.NsecToTimespec(250000000)
		for {
			select {
			case <-closing:
				return
			default:
			}
			n, err := unix.Kevent(fd, nil, events, &timeout)
			if err != nil {
				if err == unix.EINTR {
					continue
				}
				stop()
				return
			}
			if n > 0 {
				stop()
				return
			}
		}
	}()
	return func() { once.Do(func() { close(closing); <-finished; unix.Close(fd) }) }, nil
}
