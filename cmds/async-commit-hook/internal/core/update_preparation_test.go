package core

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStateBackupDiscardsFailedPreparation(t *testing.T) {
	for _, failure := range []string{"vacuum", "evidence-walk", "partial-copy"} {
		t.Run(failure, func(t *testing.T) {
			s, _ := fixture(t, "version=1\n")
			backups := filepath.Join(s.Store.Root, "backups")
			retained := filepath.Join(backups, "previous-backup")
			if err := os.WriteFile(retained, []byte("retain"), 0600); err != nil {
				t.Fatal(err)
			}
			evidence := filepath.Join(s.Store.Root, "evidence")
			switch failure {
			case "vacuum":
				if err := s.Store.DB.Close(); err != nil {
					t.Fatal(err)
				}
			case "evidence-walk":
				if err := os.Remove(evidence); err != nil {
					t.Fatal(err)
				}
			case "partial-copy":
				if err := os.WriteFile(filepath.Join(evidence, "a-copied-first"), []byte("copied evidence"), 0600); err != nil {
					t.Fatal(err)
				}
				outside := filepath.Join(t.TempDir(), "outside")
				if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(evidence, "z-unsafe")); err != nil {
					if runtime.GOOS == "windows" {
						t.Skip("symlink privilege unavailable:", err)
					}
					t.Fatal(err)
				}
			}
			for attempt := 0; attempt < 3; attempt++ {
				path, err := s.stateBackup()
				if err == nil || path != "" {
					t.Fatal("failed preparation exposed a backup", path, err)
				}
				entries, err := os.ReadDir(backups)
				if err != nil || len(entries) != 1 || entries[0].Name() != "previous-backup" {
					t.Fatal("partial backup retained or prior backup removed", entries, err)
				}
				if data, err := os.ReadFile(retained); err != nil || string(data) != "retain" {
					t.Fatal("prior backup changed", err)
				}
			}
		})
	}
}

func TestUpdateJournalFailureDiscardsPreparedFiles(t *testing.T) {
	s, _ := fixture(t, "version=1\n")
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	executable := filepath.Join(dir, "ach")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	original := []byte("installed executable remains unchanged")
	if err = os.WriteFile(executable, original, 0700); err != nil {
		t.Fatal(err)
	}
	// Fail publication after successful VACUUM/copy and candidate probing.
	// A nonempty directory cannot be replaced on any supported platform.
	journalPath := filepath.Join(s.Paths.Control, "update.json")
	if err = os.Mkdir(journalPath, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(journalPath, "existing")
	if err = os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		_, changed, err := s.prepareVerifiedUpdate(context.Background(), executable, Version, binary)
		if err == nil || changed {
			t.Fatal("journal failure was accepted", changed, err)
		}
		entries, err := os.ReadDir(filepath.Join(s.Store.Root, "backups"))
		if err != nil || len(entries) != 0 {
			t.Fatal("unjournaled backup retained", entries, err)
		}
		entries, err = os.ReadDir(dir)
		if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(executable) {
			t.Fatal("unjournaled candidate retained", entries, err)
		}
		if data, err := os.ReadFile(executable); err != nil || string(data) != string(original) {
			t.Fatal("original executable changed", err)
		}
		if data, err := os.ReadFile(marker); err != nil || string(data) != "preserve" {
			t.Fatal("existing journal path changed", err)
		}
	}
}
