package core

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateRecoveryAuthenticatesBackupBeforeRestoring(t *testing.T) {
	for _, kind := range []string{"valid", "changed", "missing", "directory", "symlink", "missing-digest", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := fixture(t, "version=1\n")
			dir := t.TempDir()
			j := UpdateJournal{Executable: filepath.Join(dir, "ach"), Backup: filepath.Join(dir, "backup"), Candidate: filepath.Join(dir, "candidate"), OriginalSHA256: Hash([]byte("original")), SHA256: Hash([]byte("candidate")), Phase: "original-backed-up"}
			if err := os.WriteFile(j.Candidate, []byte("candidate"), 0700); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "missing":
			case "directory":
				if err := os.Mkdir(j.Backup, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(dir, "external-original")
				if err := os.WriteFile(target, []byte("original"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, j.Backup); err != nil {
					t.Skipf("host does not permit symlink fixture: %v", err)
				}
			default:
				contents := "original"
				if kind == "changed" {
					contents = "changed"
				}
				if kind == "missing-digest" {
					j.OriginalSHA256 = ""
				}
				if err := os.WriteFile(j.Backup, []byte(contents), 0700); err != nil {
					t.Fatal(err)
				}
				if kind == "oversized" {
					if err := os.Truncate(j.Backup, 128*1024*1024+1); err != nil {
						t.Fatal(err)
					}
				}
			}
			journal := filepath.Join(s.Paths.Control, "update.json")
			expected := Encode(j)
			if err := AtomicWrite(journal, expected, 0600); err != nil {
				t.Fatal(err)
			}
			result, err := s.RecoverUpdate()
			if kind == "valid" {
				if err != nil || result["status"] != "recovered" || result["binary_backup"] != j.Backup {
					t.Fatal(result, err)
				}
				for _, path := range []string{j.Executable, j.Backup} {
					if data, err := os.ReadFile(path); err != nil || string(data) != "original" {
						t.Fatal("authenticated original was not preserved", path, err)
					}
				}
				if _, err := os.Stat(journal); !os.IsNotExist(err) {
					t.Fatal("completed recovery retained journal", err)
				}
			} else {
				var typed *Error
				if !errors.As(err, &typed) || typed.Code != "update-recovery-required" || typed.Exit != 3 {
					t.Fatal("invalid backup accepted", result, err)
				}
				if _, err := os.Lstat(j.Executable); !os.IsNotExist(err) {
					t.Fatal("unverified executable published", err)
				}
				if data, err := os.ReadFile(journal); err != nil || !bytes.Equal(data, expected) {
					t.Fatal("failed recovery lost its journal", err)
				}
			}
			if data, err := os.ReadFile(j.Candidate); err != nil || string(data) != "candidate" {
				t.Fatal("recovery changed the candidate", err)
			}
		})
	}
}
