//go:build windows

// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"os"

	"golang.org/x/sys/windows"
)

// grokOpenReadFlags adds nothing on Windows.
const grokOpenReadFlags = 0

// syncGrokDirectory is a no-op on Windows: the atomic publication uses a
// write-through rename and a directory handle cannot be flushed.
func syncGrokDirectory(*os.File) error { return nil }

// grokSingleLinkFile reports whether an opened credential file has exactly one
// directory entry. Unknown metadata fails closed.
func grokSingleLinkFile(file *os.File, _ os.FileInfo) bool {
	var info windows.ByHandleFileInformation
	return windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info) == nil && info.NumberOfLinks == 1
}

// grokSingleLinkEntry reports whether the entry name inside root has exactly
// one directory entry, by inspecting a handle opened relative to root.
func grokSingleLinkEntry(root *os.Root, name string, _ os.FileInfo) bool {
	file, err := root.OpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		return false
	}
	defer file.Close()
	return grokSingleLinkFile(file, nil)
}
