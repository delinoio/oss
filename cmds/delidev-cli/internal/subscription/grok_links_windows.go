//go:build windows

// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"os"

	"golang.org/x/sys/windows"
)

// grokSingleLinkFile reports whether an opened credential file has exactly one
// directory entry. Unknown metadata fails closed.
func grokSingleLinkFile(file *os.File, _ os.FileInfo) bool {
	var info windows.ByHandleFileInformation
	return windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info) == nil && info.NumberOfLinks == 1
}

// grokSingleLinkPath reports whether the entry at path has exactly one
// directory entry, by inspecting a handle opened on it.
func grokSingleLinkPath(path string, _ os.FileInfo) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	return grokSingleLinkFile(file, nil)
}
