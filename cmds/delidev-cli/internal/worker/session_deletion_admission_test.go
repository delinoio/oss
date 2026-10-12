// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func admissionRuntimeCopy(t *testing.T, c Config, w *domain.SessionDeletionWork) string {
	t.Helper()
	copy := domain.SessionDeletionCopy{JobID: domain.NewID(), Type: domain.ExecuteSessionJob, ExecutionID: domain.NewID(), InstanceID: domain.NewID(), Revision: 1, Digest: strings.Repeat("b", 64)}
	if err := writeJSON(filepath.Join(c.Root, "jobs", string(copy.JobID)+".json"), journal{Version: 1, JobID: copy.JobID, InstanceID: copy.InstanceID, Revision: copy.Revision, Digest: copy.Digest, ReportID: domain.NewID(), State: journalReported, Problem: domain.Fail(domain.Unsupported, "Input rejected.", "No execution was started.")}); err != nil {
		t.Fatal(err)
	}
	w.Copies = append(w.Copies, copy)
	if err := w.Validate(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.Root, "runtimes", string(copy.ExecutionID))
	if err := security.PrivateDir(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "sentinel"), []byte("original runtime"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestSessionDeletionAdmissionRejectsOriginalValidationBoundarySwaps(t *testing.T) {
	for _, kind := range []string{"process-directory", "journal-file", "journal-directory", "runtime", "runtime-ancestor", "restored-workspace", "creation-after-absence"} {
		t.Run(kind, func(t *testing.T) {
			config, work, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
			path := filepath.Join(config.Root, "processes", string(work.Copies[0].JobID))
			sentinel := filepath.Join(path, "sentinel")
			switch kind {
			case "process-directory":
				if err := security.PrivateDir(path); err != nil {
					t.Fatal(err)
				}
			case "journal-file":
				path = filepath.Join(config.Root, "jobs", string(work.Copies[0].JobID)+".json")
				sentinel = path
			case "journal-directory":
				path = filepath.Join(config.Root, "jobs", string(work.Copies[0].JobID))
				if err := security.PrivateDir(path); err != nil {
					t.Fatal(err)
				}
				sentinel = filepath.Join(path, "sentinel")
				if err := os.WriteFile(sentinel, []byte("retained journal material"), 0600); err != nil {
					t.Fatal(err)
				}
			case "runtime", "runtime-ancestor":
				path = admissionRuntimeCopy(t, config, &work)
				sentinel = filepath.Join(path, "sentinel")
				if kind == "runtime-ancestor" {
					path = filepath.Dir(path)
				}
			case "restored-workspace":
				path = filepath.Join(config.Root, "workspaces", string(work.SessionID))
				sentinel = filepath.Join(path, "manifest.json")
			}
			old := path + "-original"
			var original []byte
			if kind == "journal-file" || kind == "restored-workspace" {
				var err error
				original, err = os.ReadFile(sentinel)
				if err != nil {
					t.Fatal(err)
				}
			}
			hook := func() {
				if kind != "creation-after-absence" {
					if err := os.Rename(path, old); err != nil {
						t.Fatal(err)
					}
				}
				if kind == "journal-file" {
					if err := os.WriteFile(path, original, 0600); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.MkdirAll(filepath.Dir(sentinel), 0700); err != nil {
						t.Fatal(err)
					}
					body := []byte("foreign sentinel")
					if kind == "restored-workspace" {
						body = original
					}
					if err := os.WriteFile(sentinel, body, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			proof, err := deleteSessionCopiesWithAdmissionHooks(context.Background(), config, work, hook, nil)
			if err == nil || proof.Complete || proof.RemovalStarted {
				t.Fatal("replacement acquired cleanup authority", err)
			}
			raw, err := os.ReadFile(sentinel)
			if err != nil || len(raw) == 0 {
				t.Fatal("foreign material was erased", err)
			}
			paths, _ := workspace.SessionDeletionProofPaths(config.Root, work)
			var retained sessionDeletionProof
			b, err := security.ReadPrivate(paths.Worker, 4096)
			if err != nil || domain.Decode(b, &retained) != nil || retained.ReportID != proof.ReportID || retained.RemovalStarted {
				t.Fatal("original report changed", err)
			}
			if err := os.Rename(path, path+"-foreign"); err != nil {
				t.Fatal(err)
			}
			if kind != "creation-after-absence" {
				if err := os.Rename(old, path); err != nil {
					t.Fatal(err)
				}
			}
			again, err := deleteSessionCopies(context.Background(), config, work)
			if err != nil || !again.Complete || again.ReportID != proof.ReportID {
				t.Fatal("original restoration did not finish exact receipt", err)
			}
		})
	}
}
func TestSessionDeletionAdmissionReusesDurableNativeRecordAfterInterruption(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "same-content foreign journal"}[replace], func(t *testing.T) {
			config, work, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
			journalPath := filepath.Join(config.Root, "jobs", string(work.Copies[0].JobID)+".json")
			original, err := os.ReadFile(journalPath)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			hook := func() {
				if replace {
					if err := os.Rename(journalPath, journalPath+"-original"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(journalPath, original, 0600); err != nil {
						t.Fatal(err)
					}
				} else {
					cancel()
				}
			}
			proof, err := deleteSessionCopiesWithAdmissionHooks(ctx, config, work, nil, hook)
			if err == nil || !proof.RemovalStarted || proof.AdmissionDigest == "" || proof.Complete {
				t.Fatal("interruption lost original admission", err)
			}
			paths, _ := workspace.SessionDeletionProofPaths(config.Root, work)
			admitted, err := security.ReadPrivate(sessionRemovalAdmissionPath(paths.Worker), security.MaxRemovalAdmissionBytes)
			if err != nil || executionInputDigest(admitted) != proof.AdmissionDigest {
				t.Fatal("native admission not durable", err)
			}
			if replace {
				again, err := deleteSessionCopies(context.Background(), config, work)
				if err == nil || again.ReportID != proof.ReportID {
					t.Fatal("replacement gained retry authority", err)
				}
				if raw, err := os.ReadFile(journalPath); err != nil || string(raw) != string(original) {
					t.Fatal("foreign journal changed", err)
				}
				if err := os.Rename(journalPath, journalPath+"-foreign"); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(journalPath+"-original", journalPath); err != nil {
					t.Fatal(err)
				}
			}
			again, err := deleteSessionCopies(context.Background(), config, work)
			if err != nil || again.ReportID != proof.ReportID || !again.Complete || again.AdmissionDigest != proof.AdmissionDigest {
				t.Fatal("exact native admission was not reused", err)
			}
			after, err := security.ReadPrivate(sessionRemovalAdmissionPath(paths.Worker), security.MaxRemovalAdmissionBytes)
			if err != nil || string(after) != string(admitted) {
				t.Fatal("native record rewritten on retry", err)
			}
		})
	}
}
func TestSessionDeletionAdmissionStartedLegacyIsAbsenceOnly(t *testing.T) {
	for _, absent := range []bool{false, true} {
		t.Run(map[bool]string{false: "original journal present", true: "all copies absent"}[absent], func(t *testing.T) {
			config, works := failedDeletionObligations(t)
			work := works[0]
			proof, path, lock, err := loadSessionDeletionProof(config.Root, work)
			if err != nil {
				t.Fatal(err)
			}
			proof.RemovalStarted = true
			if err := writeJSON(path, proof); err != nil {
				t.Fatal(err)
			}
			lock.Close()
			journalPath := filepath.Join(config.Root, "jobs", string(work.Copies[0].JobID)+".json")
			if absent {
				if err := os.Remove(journalPath); err != nil {
					t.Fatal(err)
				}
			}
			again, err := deleteSessionCopies(context.Background(), config, work)
			if absent {
				if err != nil || !again.Complete || again.ReportID != proof.ReportID {
					t.Fatal("proven absence did not finish legacy receipt", err)
				}
			} else {
				if err == nil || again.Complete || again.ReportID != proof.ReportID {
					t.Fatal("legacy progress captured fresh authority", err)
				}
				if _, err := os.Lstat(journalPath); err != nil {
					t.Fatal("legacy original journal erased", err)
				}
			}
			if _, err := os.Lstat(sessionRemovalAdmissionPath(path)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("started legacy invented native admission", err)
			}
		})
	}
}
func TestSessionDeletionAdmissionBindsOriginalDeviceWorkAndReceipt(t *testing.T) {
	config, works := failedDeletionObligations(t)
	first := works[0]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	proof, err := deleteSessionCopiesWithAdmissionHooks(ctx, config, first, nil, cancel)
	if err == nil || !proof.RemovalStarted {
		t.Fatal("fixture did not retain admission", err)
	}
	firstPaths, _ := workspace.SessionDeletionProofPaths(config.Root, first)
	raw, err := security.ReadPrivate(sessionRemovalAdmissionPath(firstPaths.Worker), security.MaxRemovalAdmissionBytes)
	if err != nil {
		t.Fatal(err)
	}
	second := works[1]
	_, secondPath, lock, err := loadSessionDeletionProof(config.Root, second)
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
	if err := security.WriteAtomic(sessionRemovalAdmissionPath(secondPath), raw); err != nil {
		t.Fatal(err)
	}
	if _, err := deleteSessionCopies(context.Background(), config, second); err == nil {
		t.Fatal("foreign device borrowed admission")
	}
	if _, err := os.Lstat(filepath.Join(config.Root, "jobs", string(second.Copies[0].JobID)+".json")); err != nil {
		t.Fatal("foreign work removed", err)
	}
}

func TestSessionDeletionAdmissionPinsNamespacesBeforeInventory(t *testing.T) {
	for _, namespace := range []string{"root", "title-runtimes", "storage-removal-intents", "workspace-removal-roots", "creation-after-absence"} {
		t.Run(namespace, func(t *testing.T) {
			config, work, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
			path := config.Root
			if namespace != "root" {
				name := namespace
				if namespace == "creation-after-absence" {
					name = "title-runtimes"
				}
				path = filepath.Join(config.Root, name)
			}
			if namespace != "creation-after-absence" {
				if err := security.PrivateDir(path); err != nil {
					t.Fatal(err)
				}
			}
			sentinel := filepath.Join(path, "foreign-sentinel")
			_, err := captureSessionRemovalAdmission(context.Background(), config.Root, work, func() {
				if namespace != "creation-after-absence" {
					if err := os.Rename(path, path+"-original"); err != nil {
						t.Fatal(err)
					}
				}
				if err := security.PrivateDir(path); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(sentinel, []byte("retain"), 0600); err != nil {
					t.Fatal(err)
				}
			})
			if err == nil {
				t.Fatal("replacement became inventory baseline")
			}
			if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "retain" {
				t.Fatal("foreign namespace changed", err)
			}
		})
	}
}

func TestSessionDeletionAdmissionReconcilesOriginalSessionProcessRoot(t *testing.T) {
	config, work, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	original := filepath.Join(config.Root, "processes", string(work.SessionID))
	if err := security.PrivateDir(original); err != nil {
		t.Fatal(err)
	}
	proof, err := deleteSessionCopies(context.Background(), config, work)
	if err != nil || !proof.Complete {
		t.Fatal("original process index did not reconcile before cleanup root switch", err)
	}
	if _, err := os.Lstat(original); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("original joined index not removed", err)
	}
}
