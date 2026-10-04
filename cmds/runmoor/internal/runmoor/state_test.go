package runmoor

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func sqliteDatabasePath(t *testing.T, db *sql.DB) string {
	t.Helper()
	var sequence int
	var name, path string
	if err := db.QueryRow("PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	return path
}

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
	if _, e = db.Exec("PRAGMA user_version=5"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	_, e = OpenStore(c)
	requireCode(t, e, ErrStateVersion)
	db, _ = sql.Open("sqlite", filepath.Join(c.Storage.State, "state.sqlite"))
	defer db.Close()
	var version int
	db.QueryRow("PRAGMA user_version").Scan(&version)
	if version != 5 {
		t.Fatal("future database modified")
	}
}

func TestStateUsesEscapedSQLiteFilePaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix host storage contract")
	}

	for _, name := range []string{"s?x", "s#x", "s%x", "s with spaces", "s-雪"} {
		t.Run(name, func(t *testing.T) {
			c := fixtureConfig(t)
			c.Storage.State = filepath.Join(filepath.Dir(c.Storage.State), name)
			wantPath := filepath.Join(c.Storage.State, "state.sqlite")

			store, err := OpenStore(c)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if store != nil {
					_ = store.Close()
				}
			})
			installation := store.View().Installation
			if got := sqliteDatabasePath(t, store.db); got != wantPath {
				t.Fatalf("SQLite opened %q, want %q", got, wantPath)
			}
			if _, err = OpenStore(c); err == nil {
				t.Fatal("a second manager opened the same state directory")
			} else {
				requireCode(t, err, ErrLocked)
			}
			if err = store.Update(func(snapshot *Snapshot) error {
				snapshot.Paused = true
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err = store.Close(); err != nil {
				t.Fatal(err)
			}
			store = nil

			reopened, err := OpenStore(c)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if got := sqliteDatabasePath(t, reopened.db); got != wantPath {
				t.Fatalf("reopened SQLite path %q, want %q", got, wantPath)
			}
			if snapshot := reopened.View(); snapshot.Installation != installation || !snapshot.Paused {
				t.Fatal("reopen lost committed state or installation identity")
			}
			for _, suffix := range []string{"", "-wal", "-shm"} {
				info, statErr := os.Stat(wantPath + suffix)
				if suffix == "" && statErr != nil {
					t.Fatalf("SQLite database is missing: %v", statErr)
				}
				if statErr != nil && !os.IsNotExist(statErr) {
					t.Fatalf("cannot inspect SQLite file %q: %v", suffix, statErr)
				}
				if statErr == nil && info.Mode().Perm() != 0600 {
					t.Fatalf("SQLite file %q mode is %#o, want 0600", suffix, info.Mode().Perm())
				}
			}
			if question := strings.IndexByte(wantPath, '?'); question >= 0 {
				if _, err := os.Stat(wantPath[:question]); !os.IsNotExist(err) {
					t.Fatalf("unexpected legacy-prefix database at %q", wantPath[:question])
				}
			}
		})
	}
}

func TestQuestionMarkStatePathsKeepIndependentStores(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix host storage contract")
	}

	a := fixtureConfig(t)
	root := filepath.Dir(a.Storage.State)
	a.Storage.State = filepath.Join(root, "s?a")
	a.Storage.Data = filepath.Join(root, "data-a")
	b := a
	b.Storage.State = filepath.Join(root, "s?b")
	b.Storage.Data = filepath.Join(root, "data-b")

	first, err := OpenStore(a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if first != nil {
			_ = first.Close()
		}
	})
	second, err := OpenStore(b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if second != nil {
			_ = second.Close()
		}
	})
	firstPath := filepath.Join(a.Storage.State, "state.sqlite")
	secondPath := filepath.Join(b.Storage.State, "state.sqlite")
	if got := sqliteDatabasePath(t, first.db); got != firstPath {
		t.Fatalf("first SQLite opened %q, want %q", got, firstPath)
	}
	if got := sqliteDatabasePath(t, second.db); got != secondPath {
		t.Fatalf("second SQLite opened %q, want %q", got, secondPath)
	}
	firstInstallation := first.View().Installation
	secondInstallation := second.View().Installation
	if firstPath == secondPath || firstInstallation == secondInstallation {
		t.Fatal("distinct state paths shared a database or installation")
	}
	if err = first.Update(func(snapshot *Snapshot) error {
		snapshot.Paused = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = second.Update(func(snapshot *Snapshot) error {
		snapshot.Stopping = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	first = nil
	if err = second.Close(); err != nil {
		t.Fatal(err)
	}
	second = nil

	reopenedFirst, err := OpenStore(a)
	if err != nil {
		t.Fatal(err)
	}
	defer reopenedFirst.Close()
	reopenedSecond, err := OpenStore(b)
	if err != nil {
		t.Fatal(err)
	}
	defer reopenedSecond.Close()
	if got := reopenedFirst.View(); got.Installation != firstInstallation || !got.Paused || got.Stopping {
		t.Fatal("first store did not preserve its independent state")
	}
	if got := reopenedSecond.View(); got.Installation != secondInstallation || got.Paused || !got.Stopping {
		t.Fatal("second store did not preserve its independent state")
	}
}

func TestAmbiguousLegacyQuestionMarkStateIsPreserved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix host storage contract")
	}

	for _, createEmptyTarget := range []bool{false, true} {
		name := "missing intended database"
		if createEmptyTarget {
			name = "empty intended database"
		}
		t.Run(name, func(t *testing.T) {
			c := fixtureConfig(t)
			root := filepath.Dir(c.Storage.State)
			c.Storage.State = filepath.Join(root, "old-truncated-target-1007?x")
			legacyPath := strings.SplitN(filepath.Join(c.Storage.State, "state.sqlite"), "?", 2)[0]
			intendedPath := filepath.Join(c.Storage.State, "state.sqlite")

			installation := newID()
			pendingID := newID()
			snapshot, err := json.Marshal(Snapshot{
				SchemaVersion: 3,
				Installation:  installation,
				Pools:         map[string]*PoolState{},
				Runners:       map[string]*Runner{pendingID: {ID: pendingID, Phase: Cleaning}},
				Images:        map[string]*Image{},
				Generations:   map[string]Config{},
				Managed:       map[string]*ManagedPool{},
				Artifacts:     map[string]*RunnerArtifact{},
			})
			if err != nil {
				t.Fatal(err)
			}
			legacy, err := sql.Open("sqlite", legacyPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = legacy.Exec("CREATE TABLE snapshot (id INTEGER PRIMARY KEY CHECK(id=1), body BLOB NOT NULL)"); err != nil {
				t.Fatal(err)
			}
			if _, err = legacy.Exec("PRAGMA user_version=3"); err != nil {
				t.Fatal(err)
			}
			if _, err = legacy.Exec("INSERT INTO snapshot(id,body) VALUES(1,?)", snapshot); err != nil {
				t.Fatal(err)
			}
			if err = legacy.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(legacyPath)
			if err != nil {
				t.Fatal(err)
			}

			if createEmptyTarget {
				if err = os.Mkdir(c.Storage.State, 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(intendedPath, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}

			_, err = OpenStore(c)
			requireCode(t, err, ErrState)
			if !strings.Contains(err.Error(), "Stop all Runmoor managers") || strings.Contains(err.Error(), "old-truncated-target-1007") {
				t.Fatalf("recovery diagnostic is incomplete or includes a private path: %v", err)
			}
			after, readErr := os.ReadFile(legacyPath)
			if readErr != nil || !bytes.Equal(after, before) {
				t.Fatal("ambiguous legacy database was changed")
			}
			if createEmptyTarget {
				info, statErr := os.Stat(intendedPath)
				if statErr != nil || info.Size() != 0 {
					t.Fatal("empty intended database was modified")
				}
				if _, statErr = os.Stat(filepath.Join(c.Storage.State, "manager.lock")); !os.IsNotExist(statErr) {
					t.Fatal("manager lock was created before legacy-state refusal")
				}
			} else if _, statErr := os.Stat(c.Storage.State); !os.IsNotExist(statErr) {
				t.Fatal("state directory was created before legacy-state refusal")
			}
			if _, statErr := os.Stat(c.Storage.Data); !os.IsNotExist(statErr) {
				t.Fatal("data directory was created before legacy-state refusal")
			}
		})
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

func TestStateMigratesV1AtomicallyAndReadOnlyInspectionDoesNotMigrate(t *testing.T) {
	c, s := fixtureStore(t)
	snapshot := s.View()
	snapshot.SchemaVersion = 1
	snapshot.Managed = nil
	snapshot.Artifacts = nil
	snapshot.Requested = Config{}
	id := newID()
	snapshot.Runners[id] = &Runner{ID: id, Phase: Cleaning, Resources: Resources{1, 512}, Image: "original"}
	body, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE snapshot SET body=? WHERE id=1", body); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("PRAGMA user_version=1"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	read, err := ReadSnapshot(c)
	if err != nil || read.SchemaVersion != 1 {
		t.Fatalf("read-only v1 read: %v", err)
	}
	migrated, err := OpenStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	after := migrated.View()
	if after.SchemaVersion != 4 || after.Installation != snapshot.Installation || fingerprint(after.Runners[id]) != fingerprint(snapshot.Runners[id]) || fingerprint(after.Requested) != fingerprint(snapshot.Config) {
		t.Fatal("migration lost original state")
	}
	var version int
	if err = migrated.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 4 {
		t.Fatal("snapshot and database schema differ")
	}
}

func TestStateMigratesV2ToV3WithPrivateTartProcessIdentityState(t *testing.T) {
	c, s := fixtureStore(t)
	snapshot := s.View()
	snapshot.SchemaVersion = 2
	id := newID()
	snapshot.Images[id] = &Image{ID: id, Phase: ImageOpen}
	snapshot.ImageTartPIDs = map[string]int{id: 4242}
	snapshot.RunnerTartStarts = map[string]string{}
	snapshot.ImageTartStarts = nil
	body, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE snapshot SET body=? WHERE id=1", body); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("PRAGMA user_version=2"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	migrated, err := OpenStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	after := migrated.View()
	if after.SchemaVersion != 4 || after.ImageTartPIDs[id] != 4242 || after.ImageTartStarts[id] != "" {
		t.Fatal("v2 migration did not preserve the legacy PID conservatively", after.SchemaVersion, after.ImageTartPIDs, after.ImageTartStarts)
	}
	var version int
	if err = migrated.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 4 {
		t.Fatal("snapshot and database schema differ after v2 migration")
	}
}
