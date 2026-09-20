package core

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
)

// agentFile records the exact input to a merge, independently of parsing defaults.
// The ach installation lock does not exclude edits by agent clients or editors.
type agentFile struct {
	path string
	info os.FileInfo
	data []byte
}

func agentFileConflict() error {
	return E("agent-conflict", "agent configuration or skill changed during integration; changes were preserved; retry after concurrent edits finish", 2)
}
func readAgentFile(path string) (agentFile, error) {
	snapshot := agentFile{path: path}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return snapshot, nil
	}
	if err != nil {
		return snapshot, err
	}
	if !info.Mode().IsRegular() {
		return snapshot, agentFileConflict()
	}
	// Reuse the no-follow, nonblocking regular-file opener so replacement links
	// and FIFOs cannot redirect this read or block the final publication check.
	f, err := openCredentialFile(path)
	if err != nil {
		return snapshot, agentFileConflict()
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return snapshot, agentFileConflict()
	}
	snapshot.data, err = io.ReadAll(f)
	if err != nil {
		return snapshot, err
	}
	current, err := os.Lstat(path)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return snapshot, agentFileConflict()
	}
	snapshot.info = opened
	return snapshot, nil
}
func (previous agentFile) unchanged() error {
	current, err := readAgentFile(previous.path)
	if err != nil {
		return err
	}
	if previous.info == nil {
		if current.info == nil {
			return nil
		}
	} else if current.info != nil && os.SameFile(previous.info, current.info) && bytes.Equal(previous.data, current.data) {
		return nil
	}
	return agentFileConflict()
}
func (previous agentFile) write(body []byte) (agentFile, error) {
	next := agentFile{path: previous.path, data: body}
	if err := os.MkdirAll(filepath.Dir(previous.path), 0700); err != nil {
		return next, err
	}
	f, err := os.CreateTemp(filepath.Dir(previous.path), ".ach-agent-*")
	if err != nil {
		return next, err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(body); err == nil {
		err = f.Sync()
	}
	if err == nil {
		next.info, err = f.Stat()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return next, err
	}
	if previous.info == nil {
		// No-replace publication also protects a destination created after discovery.
		err = os.Link(f.Name(), previous.path)
		if os.IsExist(err) {
			err = agentFileConflict()
		}
	} else {
		if err = previous.unchanged(); err == nil {
			err = replaceFile(f.Name(), previous.path)
		}
	}
	return next, err
}
func (previous agentFile) remove() error {
	if err := previous.unchanged(); err != nil {
		return err
	}
	if previous.info == nil {
		return nil
	}
	return os.Remove(previous.path)
}
func (published agentFile) restore(previous agentFile) error {
	if previous.info == nil {
		return published.remove()
	}
	_, err := published.write(previous.data)
	return err
}
