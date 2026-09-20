package core

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
)

// Snapshot the originally read hook through the same common-directory root used
// for publication. Absolute paths cannot safely re-identify it after a redirect.
func readNativeHook(root *os.Root, kind string) (agentFile, error) {
	snapshot := agentFile{path: filepath.Join("hooks", kind)}
	if kind != "post-commit" && kind != "pre-push" {
		return snapshot, E("invalid-hook", "unsupported hook", 2)
	}
	parent, err := root.Lstat("hooks")
	if os.IsNotExist(err) {
		return snapshot, nil
	}
	if err != nil {
		return snapshot, err
	}
	if !parent.IsDir() {
		return snapshot, hookFileError(agentFileConflict())
	}
	info, err := root.Lstat(snapshot.path)
	if os.IsNotExist(err) {
		return snapshot, nil
	}
	if err != nil {
		return snapshot, err
	}
	if !info.Mode().IsRegular() {
		return snapshot, hookFileError(agentFileConflict())
	}
	f, err := openNativeHook(root, snapshot.path)
	if err != nil {
		return snapshot, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return snapshot, hookFileError(agentFileConflict())
	}
	snapshot.data, err = io.ReadAll(f)
	if err != nil {
		return snapshot, err
	}
	current, err := root.Lstat(snapshot.path)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return snapshot, hookFileError(agentFileConflict())
	}
	snapshot.info = opened
	return snapshot, nil
}

func unchangedNativeHook(root *os.Root, previous agentFile) error {
	current, err := readNativeHook(root, filepath.Base(previous.path))
	if err != nil {
		return err
	}
	if previous.info == nil || current.info == nil || !os.SameFile(previous.info, current.info) || !bytes.Equal(previous.data, current.data) {
		return hookFileError(agentFileConflict())
	}
	return nil
}
