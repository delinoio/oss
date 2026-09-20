package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type updateHelperCleanup struct {
	Path    string  `json:"path"`
	SHA256  string  `json:"sha256"`
	Process Process `json:"process"`
}

// Persist separately from the replacement journal: successful installation may
// remove that journal while Windows still has the original helper mapped.
func (s *Service) recordUpdateHelper(j UpdateJournal) error {
	id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(j.Candidate), ".ach-new-"), ".exe")
	if !ValidID(id) || filepath.Base(j.Candidate) != ".ach-new-"+id+".exe" || filepath.Dir(j.Candidate) != filepath.Dir(j.Executable) {
		return E("update-helper-invalid", "unexpected update helper location", 3)
	}
	process, err := ProcessIdentity(os.Getpid())
	if err != nil {
		return err
	}
	return AtomicWrite(filepath.Join(s.Paths.Control, "update-cleanup", id+".json"), Encode(updateHelperCleanup{j.Candidate, j.SHA256, process}), 0600)
}

func (s *Service) cleanupUpdateHelpers() error {
	lock, err := TryLock(filepath.Join(s.Paths.Control, "lifecycle.lock"))
	if err != nil || lock == nil {
		return err
	}
	defer lock.Close()
	root := filepath.Join(s.Paths.Control, "update-cleanup")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !ValidID(id) || entry.Name() != id+".json" || entry.IsDir() {
			return E("update-helper-invalid", "unexpected update cleanup record", 3)
		}
		data, err := ReadOwned(root, entry.Name(), 64*1024)
		if err != nil {
			return err
		}
		var record updateHelperCleanup
		if json.Unmarshal(data, &record) != nil || !filepath.IsAbs(record.Path) || filepath.Base(record.Path) != ".ach-new-"+id+".exe" {
			return E("update-helper-invalid", "invalid update cleanup record", 3)
		}
		if ProcessAlive(record.Process) {
			continue
		}
		info, err := os.Lstat(record.Path)
		if err == nil {
			if !info.Mode().IsRegular() {
				return E("update-helper-invalid", "update helper is no longer a regular file", 3)
			}
			var evidence Evidence
			if err = digestFile(record.Path, &evidence); err != nil {
				return err
			}
			if evidence.SHA256 != record.SHA256 {
				return E("update-helper-invalid", "update helper contents changed; cleanup retained", 3)
			}
			if err = os.Remove(record.Path); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if err = os.Remove(filepath.Join(root, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
