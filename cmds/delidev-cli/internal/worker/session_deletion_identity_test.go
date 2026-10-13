// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func installDeletionHook(t *testing.T, hook func()) {
	t.Helper()
	sessionDeletionBeforeRemovalHook = hook
	t.Cleanup(func() { sessionDeletionBeforeRemovalHook = nil })
}

func installCaptureHook(t *testing.T, hook func()) {
	t.Helper()
	sessionDeletionAfterCaptureHook = hook
	t.Cleanup(func() { sessionDeletionAfterCaptureHook = nil })
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func expectPendingDeletion(t *testing.T, c Config, w domain.SessionDeletionWork) {
	t.Helper()
	proof, err := deleteSessionCopies(context.Background(), c, w)
	if err == nil || proof.Complete || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("replacement after validation was acknowledged", proof, err)
	}
	if saved := readDeletionProof(t, sessionDeletionObligationPath(c.Root, w, ".json")); saved.Complete {
		t.Fatal("pending cleanup was recorded complete", saved)
	}
}

func TestSessionDeletionPreservesProcessDirectoryReplacedAfterValidation(t *testing.T) {
	c, w, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	original := filepath.Join(c.Root, "processes", string(w.Copies[0].JobID))
	if err := os.MkdirAll(original, 0700); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(c.Root, "moved-original")
	installDeletionHook(t, func() {
		if err := os.Rename(original, moved); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(original, "sentinel"), "foreign sentinel")
	})
	expectPendingDeletion(t, c, w)
	if b, err := os.ReadFile(filepath.Join(original, "sentinel")); err != nil || string(b) != "foreign sentinel" {
		t.Fatal("replacement process directory removed", err)
	}
	if _, err := os.Lstat(moved); err != nil {
		t.Fatal("moved original changed", err)
	}
}

func TestSessionDeletionPreservesJournalReplacedAfterValidation(t *testing.T) {
	c, w, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	path := filepath.Join(c.Root, "jobs", string(w.Copies[0].JobID)+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	installDeletionHook(t, func() {
		if err := os.Rename(path, path+".moved"); err != nil {
			t.Fatal(err)
		}
		// Same bytes, different object: content equality grants no authority.
		mustWrite(t, path, string(raw))
	})
	expectPendingDeletion(t, c, w)
	if b, err := os.ReadFile(path); err != nil || string(b) != string(raw) {
		t.Fatal("same-content foreign journal removed", err)
	}
}

func TestSessionDeletionPreservesCopyRestoredAfterProvenAbsence(t *testing.T) {
	c, w, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	for _, relative := range []string{
		filepath.Join("execution-claims", string(w.SessionID)+".json"),
		filepath.Join("pr-startup", string(w.SessionID), "restored"),
		filepath.Join("workspace-recovery", string(w.Copies[0].JobID)+".json"),
	} {
		path := filepath.Join(c.Root, relative)
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("fixture already owns path", relative, err)
		}
		t.Run(relative, func(t *testing.T) {
			installDeletionHook(t, func() { mustWrite(t, path, "restored foreign copy") })
			expectPendingDeletion(t, c, w)
			if b, err := os.ReadFile(path); err != nil || string(b) != "restored foreign copy" {
				t.Fatal("restored copy removed", err)
			}
			// Reset the fixture for the next restored path.
			_ = os.Remove(path)
			_ = os.Remove(filepath.Dir(path))
		})
	}
}

func TestSessionDeletionRemovesUnchangedOriginalsAndKeepsAbsence(t *testing.T) {
	c, w, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	original := filepath.Join(c.Root, "execution-history", string(w.SessionID))
	mustWrite(t, filepath.Join(original, "nested", "owned.json"), "original")
	owner := filepath.Join(c.Root, "processes", string(w.Copies[0].JobID))
	if err := os.MkdirAll(owner, 0700); err != nil {
		t.Fatal(err)
	}
	absent := filepath.Join(c.Root, "execution-claims", string(w.SessionID)+".json")
	called := false
	installDeletionHook(t, func() { called = true })
	proof, err := deleteSessionCopies(context.Background(), c, w)
	if err != nil || !proof.Complete || !called {
		t.Fatal(proof, err, called)
	}
	for _, path := range []string{original, owner, absent} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("path survived unchanged-original deletion", path, err)
		}
	}
}

// The capture precedes ownership validation, so an object swapped in between
// capture and the validating read can never become the expected identity.
func TestSessionDeletionPreservesJournalReplacedBetweenCaptureAndValidation(t *testing.T) {
	c, w, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	path := filepath.Join(c.Root, "jobs", string(w.Copies[0].JobID)+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	installCaptureHook(t, func() {
		if err := os.Rename(path, path+".moved"); err != nil {
			t.Fatal(err)
		}
		// Valid, same-content journal: validation accepts it, yet it is not the
		// object that was captured.
		mustWrite(t, path, string(raw))
	})
	expectPendingDeletion(t, c, w)
	if b, err := os.ReadFile(path); err != nil || string(b) != string(raw) {
		t.Fatal("journal swapped in before validation was removed", err)
	}
}

func TestSessionDeletionPreservesProcessDirectoryReplacedBetweenCaptureAndValidation(t *testing.T) {
	c, w, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	original := filepath.Join(c.Root, "processes", string(w.Copies[0].JobID))
	if err := os.MkdirAll(original, 0700); err != nil {
		t.Fatal(err)
	}
	installCaptureHook(t, func() {
		if err := os.Rename(original, filepath.Join(c.Root, "moved-original")); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(original, "sentinel"), "foreign sentinel")
	})
	expectPendingDeletion(t, c, w)
	if b, err := os.ReadFile(filepath.Join(original, "sentinel")); err != nil || string(b) != "foreign sentinel" {
		t.Fatal("process directory swapped in before validation was removed", err)
	}
}

// Only the Worker's own owner reconciliation may create a maintenance lock that
// removal adopts. A foreign empty private file at an owner lock name that
// appears after adoption stays intact and keeps cleanup pending.
func TestSessionDeletionDoesNotAdoptForeignMaintenanceLocks(t *testing.T) {
	for name, owner := range map[string]func(domain.SessionDeletionWork) domain.ID{
		"job":     func(w domain.SessionDeletionWork) domain.ID { return w.Copies[0].JobID },
		"session": func(w domain.SessionDeletionWork) domain.ID { return w.SessionID },
	} {
		t.Run(name, func(t *testing.T) {
			c, w, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
			lock := filepath.Join(c.Root, "processes", string(owner(w))+".recovery.lock")
			if _, err := os.Lstat(lock); err == nil {
				t.Skip("owner reconciliation created the lock in this fixture")
			}
			installDeletionHook(t, func() { mustWrite(t, lock, "") })
			expectPendingDeletion(t, c, w)
			if _, err := os.Lstat(lock); err != nil {
				t.Fatal("foreign maintenance lock was removed", err)
			}
		})
	}
}

// A pending attempt has already persisted RemovalStarted. Its retry must compare
// against the identities retained before the first removal, never recapture the
// replacement as a fresh baseline.
func TestSessionDeletionRetryKeepsValidatedIdentityAcrossPendingAttempts(t *testing.T) {
	c, w, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	original := filepath.Join(c.Root, "processes", string(w.Copies[0].JobID))
	if err := os.MkdirAll(original, 0700); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(c.Root, "moved-original")
	installDeletionHook(t, func() {
		if err := os.Rename(original, moved); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(original, "sentinel"), "foreign sentinel")
	})
	expectPendingDeletion(t, c, w)
	if saved := readDeletionProof(t, sessionDeletionObligationPath(c.Root, w, ".json")); !saved.RemovalStarted {
		t.Fatal("first attempt did not persist removal start", saved)
	}
	sessionDeletionBeforeRemovalHook = nil
	for attempt := 2; attempt <= 3; attempt++ {
		expectPendingDeletion(t, c, w)
		if b, err := os.ReadFile(filepath.Join(original, "sentinel")); err != nil || string(b) != "foreign sentinel" {
			t.Fatal("retry removed the replacement", attempt, err)
		}
	}
	// Restoring the validated original lets the retry finish and remove it.
	if err := os.RemoveAll(original); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(moved, original); err != nil {
		t.Fatal(err)
	}
	proof, err := deleteSessionCopies(context.Background(), c, w)
	if err != nil || !proof.Complete {
		t.Fatal("retry did not finish once the original returned", proof, err)
	}
	if _, err := os.Lstat(original); !os.IsNotExist(err) {
		t.Fatal("original survived", err)
	}
}

// A retry cannot adopt anything outside the retained inventory: a copy that was
// absent when removal started stays foreign on every later attempt.
func TestSessionDeletionRetryKeepsProvenAbsence(t *testing.T) {
	c, w, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	path := filepath.Join(c.Root, "execution-claims", string(w.SessionID)+".json")
	installDeletionHook(t, func() { mustWrite(t, path, "restored foreign copy") })
	expectPendingDeletion(t, c, w)
	sessionDeletionBeforeRemovalHook = nil
	expectPendingDeletion(t, c, w)
	if b, err := os.ReadFile(path); err != nil || string(b) != "restored foreign copy" {
		t.Fatal("retry removed a copy restored after absence was proven", err)
	}
}

// pendThenPersist drives a first attempt that persists removal_started and the
// identity inventory, then stays pending because a process directory was swapped.
func pendThenPersist(t *testing.T) (Config, domain.SessionDeletionWork, string) {
	t.Helper()
	c, w, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	original := filepath.Join(c.Root, "processes", string(w.Copies[0].JobID))
	if err := os.MkdirAll(original, 0700); err != nil {
		t.Fatal(err)
	}
	installDeletionHook(t, func() {
		if err := os.Rename(original, filepath.Join(c.Root, "moved-original")); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(original, "sentinel"), "foreign sentinel")
	})
	expectPendingDeletion(t, c, w)
	sessionDeletionBeforeRemovalHook = nil
	return c, w, original
}

// A retry has no legitimate new session lock: a foreign empty private file that
// appeared after the inventory proved the name absent must stay foreign.
func TestSessionDeletionRetryDoesNotAdoptSessionLock(t *testing.T) {
	c, w, _ := pendThenPersist(t)
	lock := filepath.Join(c.Root, "processes", string(w.SessionID)+".recovery.lock")
	if _, err := os.Lstat(lock); err == nil {
		t.Skip("owner reconciliation created the lock in this fixture")
	}
	mustWrite(t, lock, "")
	expectPendingDeletion(t, c, w)
	if _, err := os.Lstat(lock); err != nil {
		t.Fatal("retry adopted and removed a foreign session lock", err)
	}
}
