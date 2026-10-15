// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
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

// A proof that recorded an identity inventory fails closed when it is lost;
// only a started proof from an earlier release (no recorded inventory) recaptures.
func TestSessionDeletionRetryFailsClosedWithoutRetainedInventory(t *testing.T) {
	c, w, original := pendThenPersist(t)
	proofPath := sessionDeletionObligationPath(c.Root, w, ".json")
	if saved := readDeletionProof(t, proofPath); !saved.RemovalStarted || !saved.IdentityInventory {
		t.Fatal("new proof did not record its inventory", saved)
	}
	if err := os.Remove(sessionIdentityPath(c.Root, w)); err != nil {
		t.Fatal(err)
	}
	expectPendingDeletion(t, c, w)
	if b, err := os.ReadFile(filepath.Join(original, "sentinel")); err != nil || string(b) != "foreign sentinel" {
		t.Fatal("retry recaptured a lost inventory and removed the replacement", err)
	}
	// An earlier release's started proof never recorded an inventory.
	saved := readDeletionProof(t, proofPath)
	saved.IdentityInventory = false
	if err := writeJSON(proofPath, saved); err != nil {
		t.Fatal(err)
	}
	proof, err := deleteSessionCopies(context.Background(), c, w)
	if err != nil || !proof.Complete {
		t.Fatal("legacy started proof did not keep its previous behavior", proof, err)
	}
}

// Restoring a retained inventory obeys the same descriptor budget as capture.
func TestSessionDeletionRestoreAppliesHandleBudget(t *testing.T) {
	c, w, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	for _, dir := range []string{"execution-history/" + string(w.SessionID), "pr-startup/" + string(w.SessionID), "processes/" + string(w.Copies[0].JobID)} {
		if err := os.MkdirAll(filepath.Join(c.Root, filepath.FromSlash(dir)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	// A copy restored where absence was proven keeps the first attempt pending
	// after it persisted the inventory, without disturbing any present object.
	foreign := filepath.Join(c.Root, "execution-claims", string(w.SessionID)+".json")
	installDeletionHook(t, func() { mustWrite(t, foreign, "restored foreign copy") })
	expectPendingDeletion(t, c, w)
	sessionDeletionBeforeRemovalHook = nil
	root, err := filepath.EvalSymlinks(c.Root)
	if err != nil {
		t.Fatal(err)
	}
	present := func(ids map[string]security.PathIdentity) (total, held int) {
		for _, id := range ids {
			if id.Present() {
				total++
			}
			if id.Held() {
				held++
			}
		}
		return total, held
	}
	unlimited, err := prepareSessionCopyIdentities(context.Background(), c, root, w, &identityBudget{remaining: 1 << 20}, true, true)
	if err != nil {
		t.Fatal(err)
	}
	total, held := present(unlimited)
	closeSessionCopyIdentities(unlimited)
	if total < 3 || held != total {
		t.Fatal("fixture needs several present objects", total, held)
	}
	limited, err := prepareSessionCopyIdentities(context.Background(), c, root, w, &identityBudget{remaining: 2}, true, true)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSessionCopyIdentities(limited)
	if gotTotal, gotHeld := present(limited); gotTotal != total || gotHeld != 2 {
		t.Fatal("restore ignored the handle budget", gotTotal, gotHeld)
	}
}

func installAdoptionHook(t *testing.T, hook func(lockPath string)) {
	t.Helper()
	sessionDeletionBeforeAdoptionHook = hook
	t.Cleanup(func() { sessionDeletionBeforeAdoptionHook = nil })
}

// maintenanceOwners lists the owner scopes whose reconciliation creates a
// maintenance lock: a job owner (right after job validation) and the session
// owner (inside workspace retirement). Each has a process index directory so the
// reconciler runs and creates its lock.
func maintenanceOwners(t *testing.T, c Config, w domain.SessionDeletionWork) map[string]string {
	t.Helper()
	owners := map[string]string{
		"job":     filepath.Join(c.Root, "processes", string(w.Copies[0].JobID)+".recovery.lock"),
		"session": filepath.Join(c.Root, "processes", string(w.SessionID)+".recovery.lock"),
	}
	for _, id := range []domain.ID{w.Copies[0].JobID, w.SessionID} {
		if err := os.MkdirAll(filepath.Join(c.Root, "processes", string(id)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return owners
}

// A lock the reconciler created and the Worker did not change is adopted on the
// first attempt and removed with the rest of the owner's state.
func TestSessionDeletionAdoptsUnchangedReconcilerMaintenanceLocks(t *testing.T) {
	c, w, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	owners := maintenanceOwners(t, c, w)
	adopted := map[string]bool{}
	installAdoptionHook(t, func(lockPath string) {
		if info, err := os.Lstat(lockPath); err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
			t.Error("reconciler did not leave an empty regular lock", lockPath, err)
		}
		adopted[lockPath] = true
	})
	proof, err := deleteSessionCopies(context.Background(), c, w)
	if err != nil || !proof.Complete {
		t.Fatal("unchanged reconciler locks were not adopted and removed", proof, err)
	}
	for name, lock := range owners {
		if !adopted[lock] {
			t.Fatal("adoption was not attempted for the owner", name)
		}
		if _, err := os.Lstat(lock); !os.IsNotExist(err) {
			t.Fatal("adopted maintenance lock survived", name, err)
		}
	}
}

// Whatever takes the lock's name between owner reconciliation and adoption is
// not the object the reconciler locked: it is never adopted, the deletion stays
// pending, and neither the replacement nor its contents are touched.
func TestSessionDeletionDoesNotAdoptMaintenanceLockReplacedBeforeAdoption(t *testing.T) {
	replacements := map[string]func(t *testing.T, lock string) (probe string, want string){
		"directory with contents": func(t *testing.T, lock string) (string, string) {
			mustWrite(t, filepath.Join(lock, "sentinel"), "foreign sentinel")
			return filepath.Join(lock, "sentinel"), "foreign sentinel"
		},
		"empty directory": func(t *testing.T, lock string) (string, string) {
			if err := os.Mkdir(lock, 0700); err != nil {
				t.Fatal(err)
			}
			return lock, ""
		},
		"nonempty regular file": func(t *testing.T, lock string) (string, string) {
			mustWrite(t, lock, "foreign contents")
			return lock, "foreign contents"
		},
		"distinct empty private regular file": func(t *testing.T, lock string) (string, string) {
			mustWrite(t, lock, "")
			return lock, ""
		},
	}
	for ownerName := range map[string]bool{"job": true, "session": true} {
		for name, install := range replacements {
			t.Run(ownerName+"/"+name, func(t *testing.T) {
				c, w, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
				target := maintenanceOwners(t, c, w)[ownerName]
				var probe, want string
				swapped := false
				installAdoptionHook(t, func(lockPath string) {
					if lockPath != target || swapped {
						return
					}
					swapped = true
					if err := os.Rename(lockPath, lockPath+".moved"); err != nil {
						t.Error(err)
						return
					}
					probe, want = install(t, lockPath)
				})
				expectPendingDeletion(t, c, w)
				if !swapped {
					t.Fatal("the replacement was never installed")
				}
				info, err := os.Lstat(probe)
				if err != nil {
					t.Fatal("replacement was removed", err)
				}
				if info.Mode().IsRegular() {
					if b, err := os.ReadFile(probe); err != nil || string(b) != want {
						t.Fatal("replacement contents changed", err)
					}
				}
				// Nothing was recorded: the replacement never became an authorized
				// identity and removal never started.
				if _, err := os.Lstat(sessionIdentityPath(c.Root, w)); !os.IsNotExist(err) {
					t.Fatal("an identity inventory was persisted for a rejected adoption", err)
				}
				if saved := readDeletionProof(t, sessionDeletionObligationPath(c.Root, w, ".json")); saved.RemovalStarted {
					t.Fatal("removal started despite a rejected adoption", saved)
				}
			})
		}
	}
}

// A retry never adopts: a job-owner lock that appears after the first attempt
// persisted the inventory stays foreign.
func TestSessionDeletionRetryDoesNotAdoptJobMaintenanceLock(t *testing.T) {
	c, w, _ := pendThenPersist(t)
	lock := filepath.Join(c.Root, "processes", string(w.Copies[0].JobID)+".recovery.lock")
	if _, err := os.Lstat(lock); err == nil {
		if err := os.Remove(lock); err != nil {
			t.Fatal(err)
		}
	}
	installAdoptionHook(t, func(string) { t.Error("a retry attempted adoption") })
	mustWrite(t, lock, "")
	expectPendingDeletion(t, c, w)
	if _, err := os.Lstat(lock); err != nil {
		t.Fatal("retry adopted and removed a foreign job lock", err)
	}
}

// Adoption is limited to the owners whose reconciliation produced a binding. A
// symlink at an unrelated owner lock name is outside the deletion inventory: the
// deletion completes without following, adopting or removing it.
func TestSessionDeletionAdoptionIgnoresNamesWithoutReconcilerBinding(t *testing.T) {
	c, w, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	target := filepath.Join(c.Root, "processes", string(domain.NewID())+".recovery.lock")
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(c.Root, "jobs"), target); err != nil {
		t.Skip("symlinks unavailable", err)
	}
	proof, err := deleteSessionCopies(context.Background(), c, w)
	if err != nil || !proof.Complete {
		t.Fatal(proof, err)
	}
	if info, err := os.Lstat(target); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("unrelated symlink was removed", err)
	}
	if _, err := os.Lstat(filepath.Join(c.Root, "jobs")); err != nil {
		t.Fatal("symlink target was touched", err)
	}
}
