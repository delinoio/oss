package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateHelperCleanupSurvivesSuccessfulJournalRemoval(t *testing.T) {
	s, _ := fixture(t, "version=1\n")
	id := ID()
	candidate := filepath.Join(t.TempDir(), ".ach-new-"+id+".exe")
	if err := os.WriteFile(candidate, []byte("authenticated helper"), 0700); err != nil {
		t.Fatal(err)
	}
	j := UpdateJournal{Executable: filepath.Join(filepath.Dir(candidate), "ach.exe"), Candidate: candidate, SHA256: Hash([]byte("authenticated helper"))}
	if err := s.recordUpdateHelper(j); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(s.Paths.Control, "update-cleanup", id+".json")
	if err := s.cleanupUpdateHelpers(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(candidate); err != nil {
		t.Fatal("live helper removed", err)
	}
	// The process has exited and the successful replacement journal is absent.
	if err := AtomicWrite(marker, Encode(updateHelperCleanup{Path: candidate, SHA256: j.SHA256}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, []byte("unexpected replacement"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.cleanupUpdateHelpers(); err == nil {
		t.Fatal("changed helper was deleted")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("retry metadata lost", err)
	}
	if err := os.WriteFile(candidate, []byte("authenticated helper"), 0700); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.Paths)
	if err != nil {
		t.Fatal(err)
	}
	reopened.Close()
	if _, err := os.Stat(candidate); !os.IsNotExist(err) {
		t.Fatal("exited helper remains", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("completed cleanup marker remains", err)
	}
	if err := s.cleanupUpdateHelpers(); err != nil {
		t.Fatal("cleanup not idempotent", err)
	}
}
