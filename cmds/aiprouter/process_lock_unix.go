//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package main

import (
	"golang.org/x/sys/unix"
	"os"
)

func acquireProcessLock(path string) (*os.File, error) {
	fd, e := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	if e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}
