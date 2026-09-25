package claude

import (
	"os"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"golang.org/x/sys/windows"
)

func historyReadFlags() int { return os.O_RDONLY }

func ownedHistoryOpenFile(file *os.File) bool {
	var info windows.ByHandleFileInformation
	return windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info) == nil && info.NumberOfLinks == 1 && info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0
}

func ownedHistoryEntry(path string, info os.FileInfo) bool {
	// Windows traversal privileges can bypass parent ACL checks. Require the
	// existing strict owner-only policy on each descendant as well as the root;
	// the Unix private-ancestor exception must never relax Windows file ACLs.
	if info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	if info.IsDir() {
		return security.CheckPrivateDir(path) == nil
	}
	return info.Mode().IsRegular() && security.RegularPrivate(path) == nil
}
