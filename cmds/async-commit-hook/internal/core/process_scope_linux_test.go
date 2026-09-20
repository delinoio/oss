package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScopeLostSubreaperFailsClosedUntilBootChanges(t *testing.T) {
	dir := t.TempDir()
	boot, err := bootIdentity()
	if err != nil {
		t.Fatal(err)
	}
	// A reused PID is unrelated; without a completion record its vanished owner
	// cannot prove what happened to its reparented children.
	s := processScope{Version: 1, Boot: boot, Owner: Process{PID: os.Getpid(), Birth: "other-incarnation"}, Started: true}
	if err = saveScope(dir, s); err != nil {
		t.Fatal(err)
	}
	p := s.Owner
	p.ScopeDir = dir
	service, _ := fixture(t, "version=1\n[checks.test]\ncommand=\"true\"\n")
	_, leave, err := service.enterProcess("check-supervisor", p)
	if err != nil {
		t.Fatal(err)
	}
	defer leave()
	active, err := service.Active()
	if err != nil || len(active) != 1 {
		t.Fatal("lost supervisor released its account lease", err)
	}
	if err = ReconcileProcess(p); err == nil {
		t.Fatal("lost subreaper released its claim")
	}
	s.Boot = "previous-host-boot"
	if err = saveScope(dir, s); err != nil {
		t.Fatal(err)
	}
	if err = ReconcileProcess(p); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "ownership.json")); err != nil {
		t.Fatal("recovery discarded ownership evidence")
	}
}
