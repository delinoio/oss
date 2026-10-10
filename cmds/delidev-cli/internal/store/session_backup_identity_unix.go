// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package store

import (
	"fmt"
	"os"
	"reflect"
	"syscall"
)

// Persist the native file identity and change generation, not access time.
// ctime detects in-place writes even when a writer restores size and mtime.
func sessionBackupFileIdentity(file *os.File) (string, error) {
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Ino == 0 {
		return "", backupUnavailable()
	}
	value := reflect.ValueOf(stat).Elem()
	change := value.FieldByName("Ctim")
	if !change.IsValid() {
		change = value.FieldByName("Ctimespec")
	}
	if !change.IsValid() {
		return "", backupUnavailable()
	}
	seconds, nanos := change.FieldByName("Sec"), change.FieldByName("Nsec")
	if !seconds.IsValid() || !nanos.IsValid() {
		return "", backupUnavailable()
	}
	return fmt.Sprintf("%x:%x:%x:%x", uint64(stat.Dev), stat.Ino, seconds.Int(), nanos.Int()), nil
}
