//go:build darwin || linux

package runmoor

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func tartCleanupProcess() int {
	var req tartCleanupRequest
	body, err := io.ReadAll(io.LimitReader(os.Stdin, 8193))
	if err != nil || len(body) > 8192 || json.Unmarshal(body, &req) != nil || !filepath.IsAbs(req.Executable) {
		return tartCleanupOwnershipExit
	}
	ready := os.NewFile(7, "cleanup-ready")
	defer ready.Close()
	info, err := ready.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return tartCleanupOwnershipExit
	}
	if _, err = unix.FcntlInt(ready.Fd(), unix.F_SETFD, unix.FD_CLOEXEC); err != nil {
		return tartCleanupPendingExit
	}
	files := []*os.File{os.NewFile(3, "owned-vm"), os.NewFile(4, "vm-namespace"), os.NewFile(5, "owner-record"), os.NewFile(6, "tart-home")}
	defer closeTartCleanupFiles(files)
	lock, err := prepareTartCleanup(req, files)
	if err != nil {
		if p, ok := err.(*Problem); ok && p.Code == ErrOwnership {
			return tartCleanupOwnershipExit
		}
		return tartCleanupPendingExit
	}
	defer lock.Close()
	// POSIX locks survive exec only if a same-inode descriptor stays open. Never
	// open/close another config descriptor here: any such close drops this PID's
	// locks. Keep the one lock FD inheritable until Tart acquires its own lock.
	if _, err = unix.FcntlInt(lock.Fd(), unix.F_SETFD, 0); err != nil {
		return tartCleanupPendingExit
	}
	if req.cancelled() {
		return tartCleanupPendingExit
	}
	// This private pipe closes on exec, so a Tart exit status cannot be mistaken
	// for a pre-exec ownership rejection from the cleanup helper.
	if _, err = ready.Write([]byte{tartCleanupExecReady}); err != nil {
		return tartCleanupPendingExit
	}
	env := tartEnvAt(req.config(), "/dev/fd/6")
	if syscall.Exec(req.Executable, []string{req.Executable, "delete", deletionVMName(req.Owner.Entity)}, env) != nil {
		return tartCleanupPendingExit
	}
	return tartCleanupPendingExit
}
func cleanupFileInfo(f *os.File, directory bool) (os.FileInfo, error) {
	if f == nil {
		return nil, os.ErrInvalid
	}
	info, err := f.Stat()
	if err != nil || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) || directory && !info.IsDir() || !directory && (!info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0) {
		return nil, os.ErrPermission
	}
	return info, nil
}
func cleanupNamedDirectory(parent *os.File, name string, expected os.FileInfo) bool {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	info, err := cleanupFileInfo(f, true)
	return err == nil && os.SameFile(info, expected)
}
func prepareTartCleanup(req tartCleanupRequest, files []*os.File) (*os.File, error) {
	if len(files) != 4 || !filepath.IsAbs(req.Data) || !safeName.MatchString(req.Owner.VM) || !validID(req.Owner.Installation) || !validID(req.Owner.Entity) || req.Source != req.Owner.VM && req.Source != deletionVMName(req.Owner.Entity) {
		return nil, ambiguousVMOwnership()
	}
	vm, err := cleanupFileInfo(files[0], true)
	if err != nil {
		return nil, ambiguousVMOwnership()
	}
	namespace, err := cleanupFileInfo(files[1], true)
	if err != nil {
		return nil, ambiguousVMOwnership()
	}
	record, err := cleanupFileInfo(files[2], false)
	if err != nil {
		return nil, ambiguousVMOwnership()
	}
	if _, err = cleanupFileInfo(files[3], true); err != nil {
		return nil, ambiguousVMOwnership()
	}
	// Verify all retained authority descriptors before acquiring the config lock.
	// These checks read only the owner marker and record, never config.json.
	matchesOwner := func(body []byte) bool {
		var owner vmOwner
		return len(body) <= 4096 && json.Unmarshal(body, &owner) == nil && owner == req.Owner
	}
	if _, err = files[2].Seek(0, io.SeekStart); err != nil {
		return nil, ambiguousVMOwnership()
	}
	actual, err := io.ReadAll(io.LimitReader(files[2], 4097))
	if err != nil || !matchesOwner(actual) {
		return nil, ambiguousVMOwnership()
	}
	markerFile, err := openTartVMFileAt(files[0], vmOwnerMarkerName)
	if err != nil {
		return nil, ambiguousVMOwnership()
	}
	defer markerFile.Close()
	markerInfo, err := cleanupFileInfo(markerFile, false)
	if err != nil {
		return nil, ambiguousVMOwnership()
	}
	readMarker := func() bool {
		if _, err := markerFile.Seek(0, io.SeekStart); err != nil {
			return false
		}
		marker, err := io.ReadAll(io.LimitReader(markerFile, 4097))
		return err == nil && matchesOwner(marker)
	}
	c := req.config()
	if !readMarker() || !cleanupNamedFile(unix.AT_FDCWD, vmOwnerPath(c, req.Owner.VM), record, true) {
		return nil, ambiguousVMOwnership()
	}
	if !cleanupNamedDirectory(files[3], "vms", namespace) || !cleanupNamedDirectory(files[1], req.Source, vm) {
		return nil, ambiguousVMOwnership()
	}
	if req.cancelled() {
		return nil, tartCleanupFailure()
	}
	lock, err := lockTartVMConfigAt(files[0])
	if err != nil {
		return nil, tartCleanupFailure()
	}
	fail := func(err error) (*os.File, error) { lock.Close(); return nil, err }
	configInfo, err := lock.Stat()
	if err != nil || os.SameFile(configInfo, markerInfo) || os.SameFile(configInfo, record) {
		return fail(ambiguousVMOwnership())
	}
	// After locking, stat names and read retained, distinct owner descriptors.
	// Reopening a hard-linked marker or record could close the config inode and
	// release every POSIX lock held by this PID.
	ownedFiles := func() bool {
		if !cleanupNamedFile(int(files[0].Fd()), "config.json", configInfo, false) || !cleanupNamedFile(int(files[0].Fd()), vmOwnerMarkerName, markerInfo, true) || !cleanupNamedFile(unix.AT_FDCWD, vmOwnerPath(c, req.Owner.VM), record, true) {
			return false
		}
		if _, err := files[2].Seek(0, io.SeekStart); err != nil {
			return false
		}
		actual, err := io.ReadAll(io.LimitReader(files[2], 4097))
		return err == nil && matchesOwner(actual) && readMarker()
	}
	if !cleanupNamedDirectory(files[1], req.Source, vm) {
		return fail(ambiguousVMOwnership())
	}
	if !ownedFiles() {
		return fail(ambiguousVMOwnership())
	}
	target := deletionVMName(req.Owner.Entity)
	if req.cancelled() {
		return fail(tartCleanupFailure())
	}
	if req.Source != target {
		if err = renameTartVMAtNoReplace(files[1], req.Source, target); err != nil {
			return fail(tartCleanupFailure())
		}
		if !cleanupNamedDirectory(files[1], target, vm) {
			_ = renameTartVMAtNoReplace(files[1], target, req.Source)
			return fail(ambiguousVMOwnership())
		}
		if err = files[1].Sync(); err != nil {
			return fail(tartCleanupFailure())
		}
	}
	if req.cancelled() {
		return fail(tartCleanupFailure())
	}
	// A namespace replacement cannot redirect the descriptor-selected Tart home.
	if !cleanupNamedDirectory(files[3], "vms", namespace) || !cleanupNamedDirectory(files[1], target, vm) {
		return fail(ambiguousVMOwnership())
	}
	if !ownedFiles() {
		return fail(ambiguousVMOwnership())
	}
	return lock, nil
}

// Confirm absence in the same retained namespace used by the cleanup child.
// A replacement of the configured Tart home cannot substitute an empty home.
func confirmTartCleanupRemoved(namespace, vm *os.File, target string) error {
	var st unix.Stat_t
	err := unix.Fstatat(int(namespace.Fd()), target, &st, unix.AT_SYMLINK_NOFOLLOW)
	if err == unix.ENOENT {
		return nil
	}
	if err != nil {
		return tartCleanupFailure()
	}
	info, err := vm.Stat()
	if err != nil || !cleanupNamedDirectory(namespace, target, info) {
		return ambiguousVMOwnership()
	}
	return tartCleanupFailure()
}

func cleanupNamedFile(parent int, name string, expected os.FileInfo, private bool) bool {
	var st unix.Stat_t
	if unix.Fstatat(parent, name, &st, unix.AT_SYMLINK_NOFOLLOW) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Geteuid()) || private && st.Mode&0077 != 0 {
		return false
	}
	original := expected.Sys().(*syscall.Stat_t)
	return uint64(st.Dev) == uint64(original.Dev) && st.Ino == original.Ino
}
