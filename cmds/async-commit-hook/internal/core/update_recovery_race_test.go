package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDelayedUpdateHelperCannotUndoRecovery(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		s, _ := fixture(t, "version=1\n")
		dir := t.TempDir()
		j := UpdateJournal{Executable: filepath.Join(dir, "ach"), Candidate: filepath.Join(dir, "candidate"), Backup: filepath.Join(dir, "backup"), SHA256: Hash([]byte("new")), OriginalSHA256: Hash([]byte("old")), Phase: "prepared"}
		for path, data := range map[string]string{j.Executable: "old", j.Candidate: "new"} {
			if err := os.WriteFile(path, []byte(data), 0700); err != nil {
				t.Fatal(err)
			}
		}
		path := filepath.Join(s.Paths.Control, "update.json")
		decodedBeforeParentExit := Encode(j)
		if err := AtomicWrite(path, decodedBeforeParentExit, 0600); err != nil {
			t.Fatal(err)
		}
		ready, resume, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() { close(ready); <-resume; done <- s.applyPreparedUpdate(decodedBeforeParentExit) }()
		<-ready
		result, err := s.RecoverUpdate()
		if err != nil || result["status"] != "recovered" {
			close(resume)
			<-done
			t.Fatalf("recovery failed: %v %v", result, err)
		}
		var next []byte
		if replacement {
			j.Candidate += "-next"
			next = Encode(j)
			if err = AtomicWrite(path, next, 0600); err != nil {
				close(resume)
				<-done
				t.Fatal(err)
			}
		}
		close(resume)
		err = <-done
		var updateErr *Error
		if !errors.As(err, &updateErr) || updateErr.Code != "update-superseded" {
			t.Fatalf("stale helper continued: %v", err)
		}
		got, err := os.ReadFile(j.Executable)
		if err != nil || string(got) != "old" {
			t.Fatal("helper replaced recovered executable", err)
		}
		got, err = os.ReadFile(path)
		if replacement {
			if err != nil || string(got) != string(next) {
				t.Fatal("new journal overwritten", err)
			}
		} else if !os.IsNotExist(err) {
			t.Fatal("recovered journal resurrected", err)
		}
	}
}
