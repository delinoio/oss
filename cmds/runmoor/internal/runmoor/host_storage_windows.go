package runmoor

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

func hostPrivateInfo(info os.FileInfo, directory bool) bool {
	if info == nil || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	a, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return ok && a.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 && (directory && info.IsDir() || !directory && info.Mode().IsRegular())
}

// Only used for portable service fixtures on Windows. Host storage uses the
// handle-derived identity below, rather than granting authority from timestamps.
func hostFileIdentity(info os.FileInfo) string {
	if info == nil {
		return ""
	}
	return fmt.Sprintf("windows-info:%d", info.ModTime().UnixNano())
}
func windowsRootIdentity(root *os.Root) string {
	f, e := root.Open(".")
	if e != nil {
		return ""
	}
	defer f.Close()
	var i windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &i) != nil || verifyPrivateHandle(windows.Handle(f.Fd())) != nil {
		return ""
	}
	return fmt.Sprintf("windows:%d:%d:%d", i.VolumeSerialNumber, i.FileIndexHigh, i.FileIndexLow)
}
func hostRenameNoReplace(root *os.Root, from, to string) error {
	// Windows Root.Rename uses MoveFileEx without replacement only when called
	// through this explicit native helper. Verify both paths beneath the root.
	if _, e := root.Lstat(to); !os.IsNotExist(e) {
		return os.ErrExist
	}
	return renameServiceDefinitionNoReplace(root.Name()+`\`+from, root.Name()+`\`+to)
}
func renameServiceDefinitionNoReplace(from, to string) error {
	if e := rejectReparsePath(from); e != nil {
		return e
	}
	if e := rejectReparsePath(to); e != nil {
		return e
	}
	a, e := windows.UTF16PtrFromString(from)
	if e != nil {
		return e
	}
	b, e := windows.UTF16PtrFromString(to)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(a, b, windows.MOVEFILE_WRITE_THROUGH)
}

func init() {
	hostRootIdentity = func(root *os.Root, _ os.FileInfo) string { return windowsRootIdentity(root) }
}
