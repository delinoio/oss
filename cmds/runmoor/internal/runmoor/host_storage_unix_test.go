//go:build darwin || linux

package runmoor

import (
	"os"
	"syscall"
	"testing"
)

type hostForeignDirectoryInfo struct {
	os.FileInfo
	stat syscall.Stat_t
}

func (i hostForeignDirectoryInfo) Sys() any { return &i.stat }

func TestHostRemovalParentOwnershipPredicateRejectsForeignOwner(t *testing.T) {
	path := t.TempDir()
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !hostPrivateInfo(info, true) {
		t.Fatal("private current-user parent rejected")
	}
	// Simulate the kernel metadata of a foreign directory without requiring
	// privilege to change filesystem ownership or touching another user's data.
	foreign := hostForeignDirectoryInfo{FileInfo: info, stat: *info.Sys().(*syscall.Stat_t)}
	foreign.stat.Uid++
	if hostPrivateInfo(foreign, true) {
		t.Fatal("removal parent's private-directory check accepted foreign ownership")
	}
}
