// SPDX-License-Identifier: Apache-2.0
package store

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

// Exercise the storage decoder's finite recovery document profile. These
// historical bytes prove no native ownership or live recovery admission.
func quarantineRecoveryInput(t *testing.T) json.RawMessage {
	t.Helper()
	var input workspace.StorageRequest
	if err := json.Unmarshal(historyRecoveryInput(t, 900<<10), &input); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Manifest.PrimaryPath += strings.Repeat("x", domain.MaxStorageRecoveryInputBytes-len(raw))
	raw, err = json.Marshal(input)
	if err != nil || len(raw) != domain.MaxStorageRecoveryInputBytes {
		t.Fatal("invalid recovery size fixture", err)
	}
	if err := workspace.DecodeStorageRequest(raw, &input); err != nil {
		t.Fatal("owning recovery decoder rejected fixture", err)
	}
	return raw
}

func quarantineSizedJob(t *testing.T, kind domain.JobType, session domain.ID, target int, terminal bool) domain.Job {
	t.Helper()
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	job := domain.Job{Type: kind, State: domain.JobQueued, MachineID: domain.NewID(), Output: json.RawMessage(`""`), AcceptedAt: now}
	if kind == domain.CompactSessionJob {
		job.Input = historyCompactionInput(t, session, job.MachineID, domain.MaxCompactionInputBytes)
	} else {
		job.Input = quarantineRecoveryInput(t)
	}
	if terminal {
		job.State, job.FinishedAt = domain.JobSucceeded, &now
	}
	raw, err := json.Marshal(job)
	if err != nil || target < len(raw) {
		t.Fatal("invalid Job target size", err)
	}
	job.Output = json.RawMessage(`"` + strings.Repeat("x", target-len(raw)) + `"`)
	raw, err = json.Marshal(job)
	if err != nil || len(raw) != target {
		t.Fatal("invalid Job size fixture", err)
	}
	if _, err := decodeRestoreJob(raw); err != nil {
		t.Fatal("owning Job decoder rejected original fixture", err)
	}
	return job
}

func TestRestoreQuarantineRevalidatesTypedJobEntityBound(t *testing.T) {
	for _, kind := range []domain.JobType{domain.CompactSessionJob, domain.WorkspaceStorageJob} {
		for _, mode := range []string{"overflow", "headroom", "terminal"} {
			t.Run(string(kind)+"/"+mode, func(t *testing.T) {
				s, root, ctx, in, _ := restoreFixture(t)
				session := domain.NewID()
				if _, err := s.Mutate(ctx, domain.NewID(), "fixture.quarantine-session", nil, func(tx *Tx) (any, error) {
					return tx.Put(domain.SessionKind, session, 0, session, "", domain.Session{Name: "quarantine fixture", Outcome: domain.ExecutionNotStarted, Archive: domain.NotArchived, Recovery: domain.NoRecovery, Dispatch: domain.DispatchBlocked})
				}); err != nil {
					t.Fatal(err)
				}
				backup, err := s.Backup(ctx)
				if err != nil {
					t.Fatal(err)
				}
				observed, err := s.InspectBackup(ctx, backup, in.ServerID)
				if err != nil {
					t.Fatal(err)
				}
				in.Backup, in.SHA256 = observed.Backup, observed.SHA256
				in.ExpectedRevision, err = s.RestoreRevision(ctx)
				if err != nil {
					t.Fatal(err)
				}
				target := domain.MaxCompactionJobBytes - 1
				if mode == "headroom" {
					target -= 1024
				}
				job := quarantineSizedJob(t, kind, session, target, mode == "terminal")
				id := domain.NewID()
				// Seed only the maximum typed input through the normal mutation path.
				// The full historical output fixture is larger than the ordinary
				// mutation-result envelope, so add it to the stored row afterward.
				seed := job
				seed.Output = json.RawMessage(`""`)
				if _, err := s.Mutate(ctx, domain.NewID(), "fixture.quarantine-size", nil, func(tx *Tx) (any, error) { return tx.PutJob(id, 0, session, "", seed) }); err != nil {
					t.Fatal(err)
				}
				raw, err := json.Marshal(job)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.ExecContext(ctx, "UPDATE entities SET body=? WHERE id=?", raw, id); err != nil {
					t.Fatal(err)
				}
				live, err := s.Get(ctx, domain.JobKind, id)
				if err != nil {
					t.Fatal(err)
				}
				backup, err = s.Backup(ctx)
				if err != nil {
					t.Fatal(err)
				}
				source := filepath.Join(root, "backups", string(backup)+".sqlite")
				original, err := os.ReadFile(source)
				if err != nil {
					t.Fatal(err)
				}
				candidate := filepath.Join(t.TempDir(), "candidate.sqlite")
				if err := os.WriteFile(candidate, original, 0o600); err != nil {
					t.Fatal(err)
				}
				receipt := BackupRestore{RequestID: domain.NewID(), Input: in, CreatedAt: job.AcceptedAt.Add(time.Hour)}
				err = prepareRestoreImage(ctx, candidate, source, receipt)
				if mode == "overflow" {
					if err == nil {
						t.Fatal("quarantine exceeded the owning outer bound")
					}
				} else if err != nil {
					t.Fatal("bounded history rejected", err)
				}
				db, err := sql.Open("sqlite", databaseURI(candidate, true))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				var retained []byte
				if err := db.QueryRowContext(ctx, "SELECT body FROM entities WHERE id=?", id).Scan(&retained); err != nil {
					t.Fatal(err)
				}
				if mode == "overflow" {
					if !bytes.Equal(retained, live.Data) {
						t.Fatal("rejected candidate changed original Job")
					}
					var candidateSession []byte
					if err := db.QueryRowContext(ctx, "SELECT body FROM entities WHERE id=?", session).Scan(&candidateSession); err != nil {
						t.Fatal(err)
					}
					originalSession, err := s.Get(ctx, domain.SessionKind, session)
					if err != nil || !bytes.Equal(candidateSession, originalSession.Data) {
						t.Fatal("rejected candidate partially quarantined Session", err)
					}
				} else {
					decoded, err := decodeRestoreJob(retained)
					if err != nil || !bytes.Equal(decoded.Input, job.Input) || !bytes.Equal(decoded.Output, job.Output) {
						t.Fatal("retained Job lost bounded original history", err)
					}
					if mode == "terminal" {
						if !bytes.Equal(retained, live.Data) {
							t.Fatal("terminal Job size or settlement changed")
						}
					} else if decoded.State != domain.JobCanceled || decoded.Problem == nil || decoded.Problem.Code != domain.RecoveryRequired || decoded.FinishedAt == nil || !decoded.FinishedAt.Equal(receipt.CreatedAt) {
						t.Fatal("accepted quarantine lost required metadata")
					}
				}
				current, err := s.Get(ctx, domain.JobKind, id)
				if err != nil || !bytes.Equal(current.Data, live.Data) || current.Revision != live.Revision {
					t.Fatal("private transformation changed live state", err)
				}
				unchanged, err := os.ReadFile(source)
				if err != nil || sha256.Sum256(unchanged) != sha256.Sum256(original) {
					t.Fatal("private transformation changed source backup", err)
				}
			})
		}
	}
}

func TestRestoreTransformationCountsRetainedQuarantineBytes(t *testing.T) {
	job := quarantineSizedJob(t, domain.CompactSessionJob, domain.NewID(), domain.MaxCompactionJobBytes-1024, false)
	original, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	finished := job.AcceptedAt.Add(time.Hour)
	job.State, job.Problem, job.FinishedAt = domain.JobCanceled, restoreQuarantined().(*domain.Error), &finished
	final, err := json.Marshal(job)
	if err != nil || len(final) <= len(original) {
		t.Fatal("quarantine did not enlarge fixture", err)
	}
	if _, err := decodeRestoreJob(final); err != nil {
		t.Fatal("final Job exceeds individual bound", err)
	}
	// Model prior retained rows with scalar sizes rather than allocating 256 MiB.
	budget := restoreDocumentBudget{}
	if err := budget.add((256 << 20) - len(original)); err != nil {
		t.Fatal(err)
	}
	originalBudget := budget
	if err := originalBudget.add(len(original)); err != nil {
		t.Fatal("original accounting should fit exactly", err)
	}
	if err := budget.add(len(final)); err == nil {
		t.Fatal("final retained quarantine bytes escaped aggregate bound")
	}
	if budget.documents != 1 || budget.bytes != (256<<20)-int64(len(original)) {
		t.Fatal("rejected accounting changed accepted totals")
	}
}

func TestRestoreTransformationCountsUnchangedRetainedRows(t *testing.T) {
	budget := restoreDocumentBudget{}
	if err := budget.add(256 << 20); err != nil {
		t.Fatal("exact retained byte bound rejected", err)
	}
	if err := budget.add(1); err == nil {
		t.Fatal("unchanged retained row escaped byte accounting")
	}
	budget = restoreDocumentBudget{}
	for range 100000 {
		if err := budget.add(0); err != nil {
			t.Fatal("exact retained document bound rejected", err)
		}
	}
	if err := budget.add(0); err == nil {
		t.Fatal("unchanged retained row escaped document accounting")
	}
}
