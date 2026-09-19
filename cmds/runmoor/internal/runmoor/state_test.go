package runmoor

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestStateOwnershipAtomicityAndRecovery(t *testing.T) {
	c, s := fixtureStore(t)
	install := s.View().Installation
	if !validID(install) {
		t.Fatal("installation is not UUID-v7")
	}
	_, e := OpenStore(c)
	requireCode(t, e, ErrLocked)
	e = s.Update(func(v *Snapshot) error { v.Paused = true; return errors.New("abort") })
	if e == nil || s.View().Paused {
		t.Fatal("failed transition persisted")
	}
	id := newID()
	if e = s.Update(func(v *Snapshot) error {
		v.Runners[id] = &Runner{ID: id, Phase: Cleaning, Resources: Resources{1, 128}, Problem: problem(ErrCleanup, "Cleanup pending.", "Restore Docker.")}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := OpenStore(c)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if reopened.View().Installation != install || reopened.View().Runners[id].Phase != Cleaning {
		t.Fatal("recovery lost ownership or cleanup progress")
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		st, e := os.Stat(filepath.Join(c.Storage.State, "state.sqlite") + suffix)
		if e == nil && st.Mode().Perm() != 0600 {
			t.Fatal("SQLite file is not private")
		}
	}
}
func TestStateRejectsFutureVersionWithoutConversion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix state")
	}
	c := fixtureConfig(t)
	s, e := OpenStore(c)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	db, e := sql.Open("sqlite", filepath.Join(c.Storage.State, "state.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("PRAGMA user_version=2"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	_, e = OpenStore(c)
	requireCode(t, e, ErrStateVersion)
	db, _ = sql.Open("sqlite", filepath.Join(c.Storage.State, "state.sqlite"))
	defer db.Close()
	var version int
	db.QueryRow("PRAGMA user_version").Scan(&version)
	if version != 2 {
		t.Fatal("future database modified")
	}
}
func TestRetentionKeepsUnresolvedRecords(t *testing.T) {
	_, s := fixtureStore(t)
	now := time.Now()
	if e := s.Update(func(v *Snapshot) error {
		for _, phase := range []RunnerPhase{Completed, Cleaning, Quarantined} {
			id := string(phase)
			v.Runners[id] = &Runner{ID: id, Phase: phase, CompletedAt: now.Add(-8 * 24 * time.Hour)}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e := s.Prune(now); e != nil {
		t.Fatal(e)
	}
	v := s.View()
	if v.Runners[string(Completed)] != nil || v.Runners[string(Cleaning)] == nil || v.Runners[string(Quarantined)] == nil {
		t.Fatal("retention removed unresolved ownership")
	}
}
func TestDiagnosticRetentionEnforcesAgeAndBytes(t *testing.T) {
	c, _ := fixtureStore(t)
	dir := diagnosticDir(c)
	if e := privateDir(dir); e != nil {
		t.Fatal(e)
	}
	old := filepath.Join(dir, "old.log")
	os.WriteFile(old, []byte("old"), 0600)
	past := time.Now().Add(-8 * 24 * time.Hour)
	os.Chtimes(old, past, past)
	big := filepath.Join(dir, "big.log")
	f, e := os.OpenFile(big, os.O_CREATE|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	f.Truncate(diagnosticLimit)
	f.Close()
	if e = PruneDiagnostics(dir, time.Now(), 1); e != nil {
		t.Fatal(e)
	}
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) != 0 {
		t.Fatal("age/size retention not enforced")
	}
}
