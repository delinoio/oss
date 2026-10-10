// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSessionDeletionAdmissionPreservesReplacementAndPendingRecovery(t *testing.T) {
	for _, kind := range []string{"process-directory", "journal-directory", "journal-same-content", "restored-runtime", "previously-absent"} {
		t.Run(kind, func(t *testing.T) {
			config, work, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
			path := filepath.Join(config.Root, "processes", string(work.Copies[0].JobID))
			switch kind {
			case "journal-directory":
				path = filepath.Join(config.Root, "jobs", string(work.Copies[0].JobID))
			case "journal-same-content":
				path = filepath.Join(config.Root, "jobs", string(work.Copies[0].JobID)+".json")
			case "restored-runtime", "previously-absent":
				path = filepath.Join(config.Root, "execution-history", string(work.SessionID))
			}
			if kind != "journal-same-content" && kind != "previously-absent" {
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			var sentinel string
			admitted, err := deleteSessionCopiesAtRemoval(context.Background(), config, work, func() error {
				if kind != "previously-absent" {
					if err := os.Rename(path, path+"-original"); err != nil {
						return err
					}
				}
				if kind == "journal-same-content" {
					original, err := os.ReadFile(path + "-original")
					if err != nil {
						return err
					}
					sentinel = path
					return os.WriteFile(path, original, 0600)
				}
				if err := os.MkdirAll(path, 0700); err != nil {
					return err
				}
				sentinel = filepath.Join(path, "sentinel")
				return os.WriteFile(sentinel, []byte("foreign replacement"), 0600)
			})
			if err == nil || domain.SafeError(err).Code != domain.SessionDeletionPending().Code || admitted.Complete {
				t.Fatal("replacement gained removal/acknowledgement", admitted, err)
			}
			if _, err := os.Lstat(sentinel); err != nil {
				t.Fatal("sentinel removed", err)
			}
			if len(admitted.Admissions) == 0 || !admitted.RemovalStarted {
				t.Fatal("original admission not retained", admitted)
			}
			recovered, err := deleteSessionCopies(context.Background(), config, work)
			if err == nil || recovered.Complete || recovered.ReportID != admitted.ReportID {
				t.Fatal("recovery rebaselined replacement", recovered, err)
			}
			if _, err := os.Lstat(sentinel); err != nil {
				t.Fatal("recovery removed sentinel", err)
			}
			// Restoring the exact original native identity, rather than copying the
			// same bytes, permits the original pending cleanup to finish.
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			if kind != "previously-absent" {
				if err := os.Rename(path+"-original", path); err != nil {
					t.Fatal(err)
				}
			}
			recovered, err = deleteSessionCopies(context.Background(), config, work)
			if err != nil || !recovered.Complete || recovered.ReportID != admitted.ReportID {
				t.Fatal("unchanged original did not finish", recovered, err)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("original copy remained", err)
			}
		})
	}
}

func TestSessionDeletionAdmissionRejectsUnprovenLegacyRemoval(t *testing.T) {
	config, work, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	if err := os.MkdirAll(filepath.Join(config.Root, "session-deletions"), 0700); err != nil {
		t.Fatal(err)
	}
	proof := sessionDeletionProof{Version: 1, Digest: work.Digest(), ReportID: domain.NewID(), RemovalStarted: true}
	if err := writeJSON(sessionDeletionPath(config.Root, work.SessionID), proof); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(config.Root, "jobs", string(work.Copies[0].JobID)+".json")
	original, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	result, err := deleteSessionCopies(context.Background(), config, work)
	if err == nil || result.Complete {
		t.Fatal("legacy removal gained a new baseline", result, err)
	}
	current, err := os.ReadFile(journal)
	if err != nil || string(current) != string(original) {
		t.Fatal("legacy journal removed", err)
	}
}
