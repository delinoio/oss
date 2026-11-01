//go:build linux || darwin

// SPDX-License-Identifier: Apache-2.0
package repository

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// CheckOwner checks the existing object, or the nearest existing parent.
func CheckOwner(path string) error {
	for {
		s, e := os.Stat(path)
		if e == nil {
			st, ok := s.Sys().(*syscall.Stat_t)
			if !ok || st.Uid != uint32(os.Geteuid()) {
				return errors.New("path is not owned by the current user; select current-user storage")
			}
			return nil
		}
		if !os.IsNotExist(e) {
			return e
		}
		p := filepath.Dir(path)
		if p == path {
			return e
		}
		path = p
	}
}
