package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A portable candidate version probe; no shell or installed ach is used.
func init() {
	if len(os.Args) == 3 && os.Args[1] == "version" && os.Args[2] == "--json" {
		fmt.Printf("{\"result\":{\"version\":%q}}\n", Version)
		os.Exit(0)
	}
}

func TestVerifiedUpdateSkipsIdenticalBytesBeforeBackups(t *testing.T) {
	s, _ := fixture(t, "version=1\n")
	binaryPath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	executable := filepath.Join(dir, "ach")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	if err = os.WriteFile(executable, binary, 0700); err != nil {
		t.Fatal(err)
	}
	original, err := os.Stat(executable)
	if err != nil {
		t.Fatal(err)
	}
	// An attempted VACUUM backup would fail: the unchanged branch must never
	// touch state, even when the retained evidence store cannot be copied.
	if err := s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		journal, changed, err := s.prepareVerifiedUpdate(context.Background(), executable, Version, binary)
		if err != nil || changed || journal.Executable != "" {
			t.Fatal(journal, changed, err)
		}
	}
	if _, _, err := s.prepareVerifiedUpdate(context.Background(), executable, "999.0.0", binary); err == nil {
		t.Fatal("identical bytes bypassed release version validation")
	}
	current, err := os.Stat(executable)
	if err != nil || !os.SameFile(original, current) || !current.ModTime().Equal(original.ModTime()) {
		t.Fatal("identical executable replaced", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("unchanged update retained candidate or binary backup", entries, err)
	}
	backups, err := os.ReadDir(filepath.Join(s.Store.Root, "backups"))
	if err != nil || len(backups) != 0 {
		t.Fatal("unchanged update created state backup", backups, err)
	}
	if _, err := os.Stat(filepath.Join(s.Paths.Control, "update.json")); !os.IsNotExist(err) {
		t.Fatal("unchanged update created journal", err)
	}
}

func TestVerifiedUpdateReplacesDifferentBytesAtSameVersion(t *testing.T) {
	s, _ := fixture(t, "version=1\n")
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(t.TempDir(), "ach")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	original := append(append([]byte(nil), binary...), []byte("local-build")...)
	if err = os.WriteFile(executable, original, 0700); err != nil {
		t.Fatal(err)
	}
	if err := verifyUpdateVersion(context.Background(), executable, Version); err != nil {
		t.Fatal(err)
	}
	journal, changed, err := s.prepareVerifiedUpdate(context.Background(), executable, Version, binary)
	if err != nil || !changed || journal.OriginalSHA256 != Hash(original) || journal.SHA256 != Hash(binary) {
		t.Fatal(journal, changed, err)
	}
	if _, err := os.Stat(filepath.Join(journal.StateBackup, "state.sqlite")); err != nil {
		t.Fatal("state backup missing", err)
	}
	if err := s.applyUpdate(journal); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string][]byte{executable: binary, journal.Backup: original} {
		data, err := os.ReadFile(path)
		if err != nil || Hash(data) != Hash(want) {
			t.Fatal("replacement or original backup changed", err)
		}
	}
}
