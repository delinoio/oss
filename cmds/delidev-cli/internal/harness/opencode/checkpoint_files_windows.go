package opencode

import (
	"os"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"golang.org/x/sys/windows"
)

func checkpointReadFlags() int { return os.O_RDONLY }

func ownedCheckpointOpenFile(file *os.File) bool {
	var info windows.ByHandleFileInformation
	return windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info) == nil && info.NumberOfLinks == 1 && info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0
}

func ownedCheckpointEntry(path string, info os.FileInfo) bool {
	// Traversal privileges can bypass ancestor ACLs; every Windows descendant
	// must independently retain the existing owner-only storage policy.
	if info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	if info.IsDir() {
		return security.CheckPrivateDir(path) == nil
	}
	return info.Mode().IsRegular() && security.RegularPrivate(path) == nil
}
