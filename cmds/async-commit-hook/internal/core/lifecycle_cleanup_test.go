package core

import (
	"os"
	"path/filepath"
	"testing"
)

func staleComponent(t *testing.T, s *Service, kind string) Component {
	t.Helper()
	p, err := ProcessIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	// The PID is live, but its old incarnation is not. No wall-clock race or
	// platform-specific process-kill timing is needed to prove identity handling.
	p.Birth += "-previous-incarnation"
	c := Component{ID: ID(), Kind: kind, Process: p, StateDir: s.Store.Root, ConfigHash: s.configHash()}
	if err := AtomicWrite(filepath.Join(s.Paths.Control, c.ID+".json"), Encode(c), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec("INSERT INTO components(id,kind,pid,birth,config_hash,created) VALUES(?,?,?,?,?,?)", c.ID, kind, p.PID, p.Birth, c.ConfigHash, "test"); err != nil {
		t.Fatal(err)
	}
	return c
}

func assertComponentRemoved(t *testing.T, s *Service, c Component) {
	t.Helper()
	var count int
	if err := s.Store.DB.QueryRow("SELECT count(*) FROM components WHERE id=?", c.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("stale database row retained", count, err)
	}
	if _, err := os.Stat(filepath.Join(s.Paths.Control, c.ID+".json")); !os.IsNotExist(err) {
		t.Fatal("stale control file retained", err)
	}
}

func TestActiveReclaimsDeadOrdinaryComponentsAndRetainsLiveOwners(t *testing.T) {
	s, _ := fixture(t, "version=1\n")
	live, leave, err := s.Enter("cli")
	if err != nil {
		t.Fatal(err)
	}
	defer leave()
	var stale []Component
	for _, kind := range []string{"cli", "viewer", "mcp", "worker", "daemon"} {
		stale = append(stale, staleComponent(t, s, kind))
	}
	for range 2 {
		active, err := s.Active()
		if err != nil || len(active) != 1 || active[0].ID != live.ID {
			t.Fatal("live component lost or stale component retained", active, err)
		}
		for _, c := range stale {
			assertComponentRemoved(t, s, c)
		}
	}
	var count int
	if err := s.Store.DB.QueryRow("SELECT count(*) FROM components WHERE id=?", live.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("live database row removed", err)
	}
}

func TestActiveReclaimsRowsFromPreviousStateWithoutRecreatingRemovedState(t *testing.T) {
	old, _ := fixture(t, "version=1\n")
	current, _ := fixture(t, "version=1\n")
	current.Paths.Control = old.Paths.Control
	c := staleComponent(t, old, "mcp")
	if _, err := current.Active(); err != nil {
		t.Fatal(err)
	}
	assertComponentRemoved(t, old, c)
	c = staleComponent(t, old, "viewer")
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(old.Store.Root); err != nil {
		t.Fatal(err)
	}
	if _, err := current.Active(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old.Store.Root); !os.IsNotExist(err) {
		t.Fatal("removed state recreated", err)
	}
	if _, err := os.Stat(filepath.Join(old.Paths.Control, c.ID+".json")); !os.IsNotExist(err) {
		t.Fatal("stale control file retained", err)
	}
}

func TestActiveRetriesFailedComponentCleanup(t *testing.T) {
	s, _ := fixture(t, "version=1\n")
	c := staleComponent(t, s, "worker")
	if _, err := s.Store.DB.Exec("CREATE TRIGGER reject_component_cleanup BEFORE DELETE ON components BEGIN SELECT RAISE(FAIL, 'injected component cleanup'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Active(); err == nil {
		t.Fatal("cleanup failure ignored")
	}
	if _, err := os.Stat(filepath.Join(s.Paths.Control, c.ID+".json")); err != nil {
		t.Fatal("retry record lost", err)
	}
	if _, err := s.Store.DB.Exec("DROP TRIGGER reject_component_cleanup"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Active(); err != nil {
		t.Fatal(err)
	}
	assertComponentRemoved(t, s, c)
}

func TestActivePreservesOtherControlJournals(t *testing.T) {
	s, _ := fixture(t, "version=1\n")
	c := staleComponent(t, s, "cli")
	journal := []byte(`{"phase":"prepared","executable":"retained"}`)
	for _, name := range []string{"update.json", "other.json"} {
		if err := AtomicWrite(filepath.Join(s.Paths.Control, name), journal, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Active(); err != nil {
		t.Fatal(err)
	}
	assertComponentRemoved(t, s, c)
	for _, name := range []string{"update.json", "other.json"} {
		got, err := os.ReadFile(filepath.Join(s.Paths.Control, name))
		if err != nil || string(got) != string(journal) {
			t.Fatal("unrelated journal changed", name, err)
		}
	}
}

func TestActiveRejectsMismatchedComponentIdentity(t *testing.T) {
	s, _ := fixture(t, "version=1\n")
	c := staleComponent(t, s, "cli")
	path := filepath.Join(s.Paths.Control, c.ID+".json")
	c.ID = ID()
	if err := AtomicWrite(path, Encode(c), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Active(); err == nil {
		t.Fatal("mismatched component identity accepted")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("invalid record removed", err)
	}
}
