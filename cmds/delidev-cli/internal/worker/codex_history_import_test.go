// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// v1ImportFixture is a retained version 1 Codex history whose original rollout
// still lives in its legacy runtime directory.
type v1ImportFixture struct {
	checkpointFixture
	legacyPath string
	content    []byte
}

func newV1ImportFixture(t *testing.T) *v1ImportFixture {
	t.Helper()
	f := newCheckpointFixture(t)
	f.input.Configuration.Subscription = true
	digest, err := f.input.Configuration.Digest()
	if err != nil {
		t.Fatal(err)
	}
	f.input.ConfigurationDigest = digest
	f.bound.Effective.Provider = "openai"
	f.ref.Subscription = true
	f.ref.ConfigurationDigest = digest
	f.job.Input, _ = json.Marshal(f.input)
	f.ref.AssignmentInputDigest = executionInputDigest(f.job.Input)
	if err := f.retain(); err != nil {
		t.Fatal(err)
	}
	legacyHome := filepath.Join(f.root, "runtimes", string(f.input.ExecutionID), "codex")
	directory := filepath.Join(legacyHome, "sessions", "2026", "10", "14")
	if err := security.PrivateDir(directory); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, fmt.Sprintf("rollout-2026-10-14T12-00-00-%s.jsonl", f.bound.Thread.ID))
	content := []byte(fmt.Sprintf(`{"timestamp":"2026-10-14T12:00:00Z","type":"session_meta","payload":{"id":"%s"}}`+"\n", f.bound.Thread.ID))
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	return &v1ImportFixture{checkpointFixture: f, legacyPath: path, content: content}
}

func (f *v1ImportFixture) hold(t *testing.T) *sharedHomeHold {
	t.Helper()
	hold, err := holdSharedCodexHome(context.Background(), Config{Root: f.root}, f.ref.MachineID, f.ref.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	return hold
}

func (f *v1ImportFixture) target(t *testing.T) string {
	t.Helper()
	home, err := codexAccountHistoryHome(f.root, f.ref.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(filepath.Join(f.root, "runtimes", string(f.input.ExecutionID), "codex"), f.legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, rel)
}

func TestImportCodexV1HistoryCopiesOnlyValidatedThread(t *testing.T) {
	f := newV1ImportFixture(t)
	hold := f.hold(t)
	defer hold.release()
	path, content := f.legacyPath, f.content

	checkpoint, home, err := importCodexV1History(context.Background(), f.root, f.ref, hold)
	if err != nil || checkpoint.Version != 1 {
		t.Fatalf("validated original history rejected: %#v, %v", checkpoint, err)
	}
	target := filepath.Join(home, "sessions", "2026", "10", "14", filepath.Base(path))
	imported, err := security.ReadPrivate(target, maxLegacyCodexHistoryBytes)
	if err != nil || string(imported) != string(content) {
		t.Fatalf("original bytes were not imported: %v", err)
	}
	if _, _, err := importCodexV1History(context.Background(), f.root, f.ref, hold); err != nil {
		t.Fatalf("idempotent import failed: %v", err)
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"type":"session_meta","payload":{"id":"%s"}}`+"\n", domain.NewID())), 0600); err != nil {
		t.Fatal(err)
	}
	checkpointRecovery(t, func() error {
		_, _, importErr := importCodexV1History(context.Background(), f.root, f.ref, hold)
		return importErr
	}())
}

// The copy writes the account-wide home, so it runs only under the lock of that
// home. One cell per state of the hold the caller passes: a missing hold, a
// released one and a hold of another account's home write nothing, not even a
// directory; a held lock imports.
func TestImportCodexV1HistoryRequiresTheSharedHomeLock(t *testing.T) {
	home := func(f *v1ImportFixture) string {
		path, err := codexAccountHistoryHome(f.root, f.ref.AccountID)
		if err != nil {
			t.Fatal(err)
		}
		return path
	}
	otherAccountHold := func(t *testing.T, f *v1ImportFixture) *sharedHomeHold {
		t.Helper()
		hold, err := holdSharedCodexHome(context.Background(), Config{Root: f.root}, f.ref.MachineID, domain.NewID())
		if err != nil {
			t.Fatal(err)
		}
		return hold
	}
	rows := []struct {
		name     string
		hold     func(*testing.T, *v1ImportFixture) *sharedHomeHold
		imported bool
	}{
		{"no hold", func(*testing.T, *v1ImportFixture) *sharedHomeHold { return nil }, false},
		{"released hold", func(t *testing.T, f *v1ImportFixture) *sharedHomeHold {
			hold := f.hold(t)
			if err := hold.release(); err != nil {
				t.Fatal(err)
			}
			return hold
		}, false},
		{"hold of another account", otherAccountHold, false},
		{"held lock", func(t *testing.T, f *v1ImportFixture) *sharedHomeHold { return f.hold(t) }, true},
	}
	for _, row := range rows {
		row := row
		t.Run(row.name, func(t *testing.T) {
			f := newV1ImportFixture(t)
			hold := row.hold(t, f)
			defer hold.release()
			_, _, err := importCodexV1History(context.Background(), f.root, f.ref, hold)
			_, statErr := os.Lstat(f.target(t))
			if row.imported {
				if err != nil || statErr != nil {
					t.Fatalf("a held lock did not import: %v %v", err, statErr)
				}
				return
			}
			checkpointRecovery(t, err)
			if !os.IsNotExist(statErr) {
				t.Fatalf("the home was written without its lock: %v", statErr)
			}
			if _, err := os.Lstat(filepath.Join(home(f), "sessions")); !os.IsNotExist(err) {
				t.Fatalf("a directory was created in the home without its lock: %v", err)
			}
		})
	}
}
