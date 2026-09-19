package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestScopeRecoveryAfterSupervisorDeath(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "leaf.json")
	exe, _ := os.Executable()
	c := exec.Command(exe, "__ach_test_orphan", "root", marker, filepath.Join(dir, "exit"))
	c.Env = []string{"PATH=/usr/bin:/bin"}
	p, err := startProcess(c, filepath.Join(dir, "scope"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	if err = p.resume(); err != nil {
		t.Fatal(err)
	}
	child := awaitOrphan(t, marker)
	t.Cleanup(func() {
		if ProcessAlive(child) {
			_ = syscall.Kill(child.PID, syscall.SIGKILL)
		}
	})
	scope, err := readScope(p.identity.ScopeDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recoverScope(scope, p.identity.ScopeDir) })
	s, _ := fixture(t, "version=1\n[checks.test]\ncommand=\"true\"\n")
	_, leave, err := s.enterProcess("check-supervisor", p.identity)
	if err != nil {
		t.Fatal(err)
	}
	defer leave()
	if err = syscall.Kill(p.identity.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	<-p.done
	active, err := s.Active()
	if err != nil || len(active) != 1 {
		t.Fatal("dead supervisor with live descendants lost its account lease", err)
	}
	if err = ReconcileProcess(p.snapshot()); err != nil {
		t.Fatal(err)
	}
	remaining, err := coalitionActive(scope.Coalition)
	if err != nil || remaining != 0 || ProcessAlive(child) {
		t.Fatal("recovery did not drain the supervisor's entire coalition", err)
	}
}
