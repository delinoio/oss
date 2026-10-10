// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSessionDeletionRetainsValidatedCopyAcrossReplacement(t *testing.T) {
	for _, kind := range []string{"journal", "process-root", "absent-claim"} {
		t.Run(kind, func(t *testing.T) {
			config, work, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
			path := filepath.Join(config.Root, "jobs", string(work.Copies[0].JobID)+".json")
			file := path
			if kind == "process-root" {
				path = filepath.Join(config.Root, "processes", string(work.Copies[0].JobID))
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
				file = filepath.Join(path, "sentinel")
			} else if kind == "absent-claim" {
				path = filepath.Join(config.Root, "execution-claims", string(work.SessionID)+".json")
				file = path
			}
			expected := []byte("replacement must remain")
			proof, err := deleteSessionCopiesBeforeRemoval(context.Background(), config, work, func() {
				if kind == "journal" {
					var readErr error
					expected, readErr = os.ReadFile(path)
					if readErr != nil {
						t.Fatal(readErr)
					}
				}
				if kind != "absent-claim" {
					if err := os.Rename(path, path+"-retained"); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, expected, 0600); err != nil {
					t.Fatal(err)
				}
			})
			if err == nil || proof.Complete {
				t.Fatal("replacement acknowledged", proof, err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				retry, retryErr := deleteSessionCopies(context.Background(), config, work)
				if retryErr == nil || retry.Complete || retry.ReportID != proof.ReportID {
					t.Fatal("retry refreshed original deletion authority", retry, retryErr)
				}
				if raw, err := os.ReadFile(file); err != nil || string(raw) != string(expected) {
					t.Fatal("replacement was removed", err)
				}
			}
		})
	}
}

func TestSessionDeletionCopyPlanPreservesNormalRemovalReceipt(t *testing.T) {
	config, work, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	proof, err := deleteSessionCopies(context.Background(), config, work)
	if err != nil || !proof.Complete {
		t.Fatal(proof, err)
	}
	plan, err := readSessionCopyPlan(context.Background(), config.Root, work)
	if err != nil || plan == nil || len(plan.Copies) == 0 {
		t.Fatal("original intent missing", plan, err)
	}
	replay, err := deleteSessionCopies(context.Background(), config, work)
	if err != nil || !replay.Complete || replay.ReportID != proof.ReportID {
		t.Fatal(replay, err)
	}
}
