//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"os"
	"syscall"
)

// grokOpenReadFlags keeps an open of a credential entry that was swapped for a
// FIFO between inspection and open from blocking the Worker.
const grokOpenReadFlags = syscall.O_NONBLOCK

// syncGrokDirectory makes entries of an open directory durable.
func syncGrokDirectory(dir *os.File) error { return dir.Sync() }

// grokSingleLinkFile reports whether an opened credential file has exactly one
// directory entry. info must be the file's own metadata. Unknown metadata fails
// closed.
func grokSingleLinkFile(_ *os.File, info os.FileInfo) bool { return grokSingleLinkInfo(info) }

// grokSingleLinkEntry reports whether the already-inspected entry name inside
// root has exactly one directory entry. Unknown metadata fails closed.
func grokSingleLinkEntry(_ *os.Root, _ string, info os.FileInfo) bool {
	return grokSingleLinkInfo(info)
}

func grokSingleLinkInfo(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && uint64(stat.Nlink) == 1
}
