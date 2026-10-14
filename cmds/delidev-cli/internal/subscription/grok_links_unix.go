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

// grokHoldRetiredHandles keeps an unlinked credential file open until the home
// has been rescanned, so its inode cannot be recycled and mistaken for a link.
const grokHoldRetiredHandles = true

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

// grokUnlinkedFileLinked reports whether an opened credential file whose name
// was just unlinked still has a directory entry anywhere. The link count of a
// held descriptor is exact after the unlink, so a link created in any directory
// up to the unlink is detected; zero is final because an unlinked inode cannot
// be linked again. Unknown metadata fails closed.
func grokUnlinkedFileLinked(file *os.File) bool {
	info, err := file.Stat()
	if err != nil {
		return true
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return !ok || uint64(stat.Nlink) != 0
}

// grokSameEntry reports whether the already-inspected directory entry name is
// another name of the retained file.
func grokSameEntry(_ *os.Root, _ string, entry, retained os.FileInfo) bool {
	return os.SameFile(entry, retained)
}
