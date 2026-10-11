// SPDX-License-Identifier: Apache-2.0
package store

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func historyRecoveryInput(t *testing.T, size int) json.RawMessage {
	t.Helper()
	original := workspace.StorageRequest{Version: 1, Action: workspace.StorageInspect, Manifest: workspace.Manifest{PrimaryPath: strings.Repeat("x", size)}}
	input := workspace.StorageRequest{Version: 1, Action: workspace.StorageRecover, Manifest: original.Manifest, Recovery: &workspace.StorageRecovery{Original: original, Claims: []workspace.StorageJournalClaim{{JobID: domain.NewID(), InstanceID: domain.NewID(), Revision: 1}}}}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Build complete immutable execution and continuation evidence. Padding belongs
// to the two matching workspace preparation copies, not an invented input field.
func historyCompactionInput(t *testing.T, session, machine domain.ID, size int) json.RawMessage {
	t.Helper()
	source := failedForkDeletionInput(t, session, machine)
	assignment := source.SourceAssignment
	progress := source.Progress
	progress.Observed = domain.ObservedExecutionSettings{Model: assignment.Configuration.NativeModel, Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}
	restore := assignment
	action := domain.NewID()
	restore.Version, restore.ExecutionID, restore.InputID = 2, action, domain.NewID()
	restore.ThreadRequestID, restore.TurnRequestID = domain.NewID(), domain.NewID()
	restore.Continuation = &domain.ExecutionContinuation{
		HistoryExecutionID: assignment.ExecutionID, HistoryRequestID: domain.NewID(),
		Previous: progress, Completion: source.Completion, InputMode: assignment.Input.Mode,
		PromptDigest: domain.BindSessionInput(assignment.InputID, assignment.Input).PromptDigest,
		Intent:       domain.ContinueAutomatically,
	}
	input := domain.SessionCompactionInput{Version: 2, ActionID: action, SourceJobID: source.SourceJobID, Assignment: assignment, Restore: restore, Completion: source.Completion, Dispatch: domain.DispatchReady, Intent: domain.ContinueAutomatically}
	marshal := func() []byte {
		original, err := json.Marshal(input.Assignment)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(original)
		input.Restore.Continuation.AssignmentInputDigest = hex.EncodeToString(digest[:])
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	input.Assignment.Preparation = json.RawMessage(`{"padding":""}`)
	input.Restore.Preparation = input.Assignment.Preparation
	raw := marshal()
	if size > len(raw) {
		// The intent spellings differ by an odd number of bytes, allowing an
		// exact document size despite the two equal preparation copies.
		if (size-len(raw))%2 != 0 {
			input.Intent = domain.ContinueExplicitly
			raw = marshal()
		}
		padding := (size - len(raw)) / 2
		input.Assignment.Preparation = json.RawMessage(`{"padding":"` + strings.Repeat("x", padding) + `"}`)
		input.Restore.Preparation = input.Assignment.Preparation
		raw = marshal()
		if len(raw) != size {
			t.Fatalf("compaction fixture size %d, want %d", len(raw), size)
		}
	}
	if err := input.Validate(); err != nil {
		t.Fatal("invalid typed compaction fixture", err)
	}
	return raw
}

func TestBackupRestoreSupportedTypedJobHistory(t *testing.T) {
	for _, test := range []struct {
		name   string
		kind   domain.JobType
		size   int
		queued bool
	}{
		{"ordinary-small", domain.HarnessDiscoveryJob, 512, false},
		{"compaction-small", domain.CompactSessionJob, 512 << 10, false},
		{"compaction-input-bound", domain.CompactSessionJob, domain.MaxCompactionInputBytes, false},
		{"compaction-large", domain.CompactSessionJob, 2 << 20, false},
		{"compaction-queued", domain.CompactSessionJob, 2 << 20, true},
		{"storage-recovery-large", domain.WorkspaceStorageJob, 600 << 10, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, root, ctx, in, session := restoreFixture(t)
			id := domain.NewID()
			now := time.Now().UTC()
			machine := domain.NewID()
			input, err := json.Marshal(struct {
				History string `json:"history"`
			}{strings.Repeat("x", test.size)})
			if err != nil {
				t.Fatal(err)
			}
			if test.kind == domain.WorkspaceStorageJob {
				input = historyRecoveryInput(t, test.size)
			}
			if test.kind == domain.CompactSessionJob {
				input = historyCompactionInput(t, session, machine, test.size)
			}
			// Complete typed compaction evidence is fixture-only; it grants no
			// native admission or live account/platform acceptance.
			job := domain.Job{Type: test.kind, State: domain.JobSucceeded, MachineID: machine, InstanceID: domain.NewID(), Input: input, Output: json.RawMessage(`{"retained":"output"}`), AcceptedAt: now, FinishedAt: &now}
			if test.queued {
				job.State = domain.JobQueued
				job.InstanceID = ""
				job.FinishedAt = nil
			}
			_, err = s.Mutate(ctx, domain.NewID(), "fixture.typed-history", nil, func(tx *Tx) (any, error) { return tx.PutJob(id, 0, session, "", job) })
			if err != nil {
				t.Fatal(err)
			}
			original, err := s.Get(ctx, domain.JobKind, id)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeRestoreJob(original.Data); err != nil {
				t.Fatal("owning storage decoder rejected fixture", err)
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
			source := filepath.Join(root, "backups", string(backup)+".sqlite")
			before, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			if test.queued {
				settled := job
				settled.State = domain.JobCanceled
				settled.FinishedAt = &now
				_, err = s.Mutate(ctx, domain.NewID(), "fixture.settle-current-history", nil, func(tx *Tx) (any, error) { return tx.PutJob(id, original.Revision, session, "", settled) })
				if err != nil {
					t.Fatal(err)
				}
			}
			in.ExpectedRevision, err = s.RestoreRevision(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.RestoreBackup(ctx, domain.NewID(), in); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			row, err := reopened.Get(ctx, domain.JobKind, id)
			if err != nil {
				t.Fatal(err)
			}
			retained, err := decodeRestoreJob(row.Data)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(retained.Input, job.Input) || !bytes.Equal(retained.Output, job.Output) || retained.MachineID != job.MachineID || retained.InstanceID != job.InstanceID || row.SessionID != original.SessionID || row.ProjectID != original.ProjectID {
				t.Fatal("restore changed original history or attribution")
			}
			if test.queued {
				if retained.State != domain.JobCanceled || retained.Problem == nil || retained.Problem.Code != domain.RecoveryRequired || retained.FinishedAt == nil {
					t.Fatal("historical queued job regained authority")
				}
			} else if retained.State != job.State {
				t.Fatal("terminal history changed")
			}
			after, err := os.ReadFile(source)
			if err != nil || sha256.Sum256(after) != sha256.Sum256(before) {
				t.Fatal("source backup changed", err)
			}
		})
	}
}

func TestRestoreJobTransformationRejectsUnsupportedDocumentsBeforePublication(t *testing.T) {
	for _, mode := range []string{"ordinary-size", "compaction-size", "compaction-input-size", "compaction-input-unknown", "compaction-input-duplicate", "compaction-input-malformed", "compaction-input-invariant", "compaction-job-state", "recovery-size", "unknown", "duplicate", "utf8"} {
		t.Run(mode, func(t *testing.T) {
			s, root, ctx, in, session := restoreFixture(t)
			id := domain.NewID()
			now := time.Now().UTC()
			job := domain.Job{Type: domain.HarnessDiscoveryJob, State: domain.JobSucceeded, MachineID: domain.NewID(), Input: json.RawMessage(`{}`), AcceptedAt: now, FinishedAt: &now}
			_, err := s.Mutate(ctx, domain.NewID(), "fixture.small-history", nil, func(tx *Tx) (any, error) { return tx.PutJob(id, 0, session, "", job) })
			if err != nil {
				t.Fatal(err)
			}
			live, err := s.Get(ctx, domain.JobKind, id)
			if err != nil {
				t.Fatal(err)
			}
			backup, err := s.Backup(ctx)
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
			raw := append([]byte(nil), live.Data...)
			switch mode {
			case "ordinary-size", "compaction-size":
				size := 2 << 20
				if mode == "compaction-size" {
					job.Type = domain.CompactSessionJob
					size = 5 << 20
				}
				job.Input, _ = json.Marshal(strings.Repeat("x", size))
				raw, _ = json.Marshal(job)
			case "compaction-input-size", "compaction-input-unknown", "compaction-input-duplicate", "compaction-input-malformed", "compaction-input-invariant", "compaction-job-state":
				job.Type = domain.CompactSessionJob
				size := 0
				if mode == "compaction-input-size" {
					size = domain.MaxCompactionInputBytes + 1
				}
				job.Input = historyCompactionInput(t, session, job.MachineID, size)
				switch mode {
				case "compaction-input-unknown":
					job.Input = append(job.Input[:len(job.Input)-1], []byte(`,"unknown":true}`)...)
				case "compaction-input-duplicate":
					job.Input = append(job.Input[:len(job.Input)-1], []byte(`,"version":2}`)...)
				case "compaction-input-malformed":
					job.Input = json.RawMessage(`[]`)
				case "compaction-input-invariant":
					job.Input = bytes.Replace(job.Input, []byte(`"version":2`), []byte(`"version":99`), 1)
				case "compaction-job-state":
					job.State = domain.JobState("invalid-history")
				}
				raw, err = json.Marshal(job)
				if err != nil || len(raw) >= domain.MaxCompactionJobBytes {
					t.Fatal("invalid bounded compaction rejection fixture", err)
				}
			case "recovery-size":
				job.Type = domain.WorkspaceStorageJob
				job.Input = historyRecoveryInput(t, 2<<20)
				raw, _ = json.Marshal(job)
			case "unknown":
				raw = append(raw[:len(raw)-1], []byte(`,"unknown":true}`)...)
			case "duplicate":
				raw = append(raw[:len(raw)-1], []byte(`,"type":"harness-discovery"}`)...)
			case "utf8":
				raw = bytes.Replace(raw, []byte(`"input":{}`), []byte{'"', 'i', 'n', 'p', 'u', 't', '"', ':', '"', 0xff, '"'}, 1)
			}
			if _, err := decodeRestoreJob(raw); err == nil {
				t.Fatal("owning decoder accepted unsupported document", mode)
			}
			if _, err := decodeRestoreJob(append(append([]byte(nil), live.Data...), []byte(` {}`)...)); err == nil {
				t.Fatal("owning decoder accepted trailing JSON")
			}
			db, err := sql.Open("sqlite", databaseURI(candidate, false))
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.ExecContext(ctx, "UPDATE entities SET body=? WHERE id=?", raw, id)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if err := prepareRestoreImage(ctx, candidate, source, BackupRestore{RequestID: domain.NewID(), Input: in, CreatedAt: now}); err == nil {
				t.Fatal("unsupported candidate transformed", mode)
			}
			db, err = sql.Open("sqlite", databaseURI(candidate, true))
			if err != nil {
				t.Fatal(err)
			}
			var retained []byte
			if err := db.QueryRowContext(ctx, "SELECT body FROM entities WHERE id=?", id).Scan(&retained); err != nil || !bytes.Equal(retained, raw) {
				t.Fatal("rejected candidate changed historical job", err)
			}
			var candidateSession, sourceSession []byte
			if err := db.QueryRowContext(ctx, "SELECT body FROM entities WHERE id=?", session).Scan(&candidateSession); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = sql.Open("sqlite", databaseURI(source, true)+"&immutable=1")
			if err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRowContext(ctx, "SELECT body FROM entities WHERE id=?", session).Scan(&sourceSession); err != nil || !bytes.Equal(candidateSession, sourceSession) {
				t.Fatal("rejected candidate partially quarantined session", err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			after, err := s.Get(ctx, domain.JobKind, id)
			if err != nil || !bytes.Equal(after.Data, live.Data) || after.Revision != live.Revision {
				t.Fatal("failed transform changed live state", err)
			}
			unchanged, err := os.ReadFile(source)
			if err != nil || sha256.Sum256(unchanged) != sha256.Sum256(original) {
				t.Fatal("failed transform changed original backup", err)
			}
		})
	}
}

func TestRestoreJobDecoderRejectsTrailingCompaction(t *testing.T) {
	machine := domain.NewID()
	input := historyCompactionInput(t, domain.NewID(), machine, 0)
	job := domain.Job{Type: domain.CompactSessionJob, State: domain.JobQueued, MachineID: machine, Input: input, AcceptedAt: time.Now().UTC()}
	raw, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeRestoreJob(raw); err != nil {
		t.Fatal("valid original compaction was rejected", err)
	}
	if _, err := decodeRestoreJob(append(raw, []byte(` {}`)...)); err == nil {
		t.Fatal("trailing compaction document was accepted")
	}
}
