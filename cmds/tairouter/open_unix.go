//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package main

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func openConfig(path string) (*os.File, error) {
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	return os.NewFile(uintptr(fd), path), nil
}
func validateLinks(f *os.File) error {
	var info unix.Stat_t
	if unix.Fstat(int(f.Fd()), &info) != nil || info.Nlink != 1 {
		return errors.New("shared or unverifiable configuration file; preserve its hard links")
	}
	return nil
}
