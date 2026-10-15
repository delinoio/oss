// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestHomeConfigLockLifecycle(t *testing.T) {
	baseline := homeConfigLockCount()
	root := t.TempDir()
	home := filepath.Join(root, "a")
	// First holder creates the entry and the release removes it.
	release := lockHomeConfig(home)
	if homeConfigLockCount() != baseline+1 {
		t.Fatal("acquire did not register the home")
	}
	release()
	if homeConfigLockCount() != baseline {
		t.Fatal("release retained the home entry")
	}
	// A waiter keeps the entry alive until it also releases, and the two
	// spellings of one home share one lock.
	release = lockHomeConfig(home)
	acquired := make(chan func(), 1)
	go func() { acquired <- lockHomeConfig(home + string(os.PathSeparator) + ".") }()
	deadline := time.Now().Add(5 * time.Second)
	for homeConfigLockHolders(home) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if homeConfigLockHolders(home) != 2 || homeConfigLockCount() != baseline+1 {
		t.Fatal("a waiter did not share the home entry")
	}
	select {
	case <-acquired:
		t.Fatal("the lock was held by two callers")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	second := <-acquired
	if homeConfigLockCount() != baseline+1 {
		t.Fatal("the entry vanished while a caller still held it")
	}
	second()
	if homeConfigLockCount() != baseline {
		t.Fatal("the last release retained the home entry")
	}
	// Distinct homes never share an entry.
	releaseA, releaseB := lockHomeConfig(filepath.Join(root, "x")), lockHomeConfig(filepath.Join(root, "y"))
	if homeConfigLockCount() != baseline+2 {
		t.Fatal("distinct homes shared an entry")
	}
	releaseA()
	releaseB()
	if homeConfigLockCount() != baseline {
		t.Fatal("distinct homes retained entries")
	}
}

func homeConfigLockHolders(home string) int {
	homeConfigLocks.Lock()
	defer homeConfigLocks.Unlock()
	if entry := homeConfigLocks.entries[filepath.Clean(home)]; entry != nil {
		return entry.holders
	}
	return 0
}

func TestIsolateHomeConfigReleasesItsLockOnEveryPath(t *testing.T) {
	baseline := homeConfigLockCount()
	// Many fresh job-scoped homes, as a long-lived Worker creates them, retain
	// nothing once their isolation completes.
	for i := 0; i < 64; i++ {
		home := filepath.Join(t.TempDir(), "codex")
		if err := security.PrivateDir(home); err != nil {
			t.Fatal(err)
		}
		if err := isolateHomeConfig(home, []string{"/repo"}); err != nil {
			t.Fatal(err)
		}
	}
	if homeConfigLockCount() != baseline {
		t.Fatalf("%d lock entries retained after successful isolation", homeConfigLockCount()-baseline)
	}
	// An unchanged file, an invalid file and a rejected request release as well.
	home := filepath.Join(t.TempDir(), "codex")
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	if err := isolateHomeConfig(home, []string{"/repo"}); err != nil {
		t.Fatal(err)
	}
	if err := isolateHomeConfig(home, []string{"/repo"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("projects = ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if isolateHomeConfig(home, []string{"/repo"}) == nil || isolateHomeConfig("relative", []string{"/repo"}) == nil || isolateHomeConfig(home, nil) == nil {
		t.Fatal("invalid request accepted")
	}
	if homeConfigLockCount() != baseline {
		t.Fatalf("%d lock entries retained after unchanged and rejected isolation", homeConfigLockCount()-baseline)
	}
}
