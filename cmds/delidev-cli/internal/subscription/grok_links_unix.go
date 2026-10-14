//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"os"
	"syscall"
)

// grokSingleLinkFile reports whether an opened credential file has exactly one
// directory entry. info must be the file's own metadata. Unknown metadata fails
// closed.
func grokSingleLinkFile(_ *os.File, info os.FileInfo) bool { return grokSingleLinkInfo(info) }

// grokSingleLinkPath reports whether the already-inspected entry at path has
// exactly one directory entry. Unknown metadata fails closed.
func grokSingleLinkPath(_ string, info os.FileInfo) bool { return grokSingleLinkInfo(info) }

func grokSingleLinkInfo(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && uint64(stat.Nlink) == 1
}
