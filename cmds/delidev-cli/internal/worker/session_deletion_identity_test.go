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
