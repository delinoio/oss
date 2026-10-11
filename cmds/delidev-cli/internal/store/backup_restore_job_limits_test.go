// SPDX-License-Identifier: Apache-2.0
package store

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
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

// historyCompactionInput retains a valid immutable source and continuation.
// Opaque workspace evidence gives the typed envelope its requested fixture size;
// this fixture grants no native process, filesystem or account authority.
func historyCompactionInput(t *testing.T, size int) json.RawMessage {
	t.Helper()
	account := domain.NewID()
	assignment := domain.ExecutionJobInput{
		Version: 1, SessionID: domain.NewID(), MachineID: domain.NewID(), ExecutionID: domain.NewID(),
		InputID: domain.NewID(), ThreadRequestID: domain.NewID(), TurnRequestID: domain.NewID(), AccountID: account, ConnectionID: domain.NewID(),
		Configuration: domain.ExecutionConfiguration{AgentID: domain.NewID(), AgentRevision: 1, Harness: domain.Codex,
			ModelRevision: 1, ProviderID: domain.NewID(), NativeModel: "fixture-model", Routing: domain.Priority,
			Accounts: []domain.WeightedAccount{{ID: account, Weight: 1}}, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly}},
		Input: domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "retained fixture input"},
		Installation: domain.Installation{Harness: domain.Codex, State: domain.InstallationDetected, Version: domain.CodexProtocolVersion,
			ProtocolVerified: true, Protocol: &domain.ProtocolObservation{Protocol: domain.CodexAppServer, State: domain.ProtocolVerified}},
		Preparation: json.RawMessage(`{}`), Manifest: json.RawMessage(`{}`),
	}
	assignment.Configuration.ModelID = (domain.ModelIdentity{ProviderID: assignment.Configuration.ProviderID, NativeID: assignment.Configuration.NativeModel}).Key()
	assignment.ConfigurationDigest, _ = assignment.Configuration.Digest()
	done := domain.ExecutionCompletion{Version: 2, ExecutionID: assignment.ExecutionID, InputID: assignment.InputID,
		NativeThreadID: domain.NativeIdentity(domain.NewID()), NativeTurnID: domain.NativeIdentity(domain.NewID()),
		LastSequence: 3, Outcome: domain.ExecutionSucceeded, CleanupVerified: true, NativeCheckpointDigest: strings.Repeat("ab", 32)}
	source, action := domain.NewID(), domain.NewID()
	progress := domain.ExecutionProgress{JobID: source, ExecutionID: assignment.ExecutionID, InputID: assignment.InputID,
		NativeThreadID: string(done.NativeThreadID), NativeTurnID: string(done.NativeTurnID), LastSequence: done.LastSequence,
		Outcome: done.Outcome, CleanupVerified: true,
		Observed:       domain.ObservedExecutionSettings{Model: assignment.Configuration.NativeModel, Permission: domain.PermissionReadOnly, ApprovalPolicy: "never"},
		AcceptedInputs: []domain.ExecutionInputBinding{domain.BindSessionInput(assignment.InputID, assignment.Input)},
	}
	restored := assignment
	restored.Version, restored.ExecutionID, restored.InputID = 2, action, domain.NewID()
	restored.ThreadRequestID, restored.TurnRequestID = domain.NewID(), domain.NewID()
	restored.Continuation = &domain.ExecutionContinuation{HistoryExecutionID: assignment.ExecutionID, HistoryRequestID: domain.NewID(),
		Previous: progress, Completion: done, InputMode: assignment.Input.Mode,
		PromptDigest: domain.BindSessionInput(assignment.InputID, assignment.Input).PromptDigest, Intent: domain.ContinueAutomatically}
	input := domain.SessionCompactionInput{Version: 2, ActionID: action, SourceJobID: source, Assignment: assignment, Restore: restored,
		Completion: done, Dispatch: domain.DispatchReady, Intent: domain.ContinueAutomatically}
	encode := func() json.RawMessage {
		original, err := json.Marshal(input.Assignment)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(original)
		input.Restore.Continuation.AssignmentInputDigest = fmt.Sprintf("%x", digest)
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	raw := encode()
	if size > len(raw) {
		padding, err := json.Marshal(strings.Repeat("x", (size-len(raw))/2))
		if err != nil {
			t.Fatal(err)
		}
		input.Assignment.Preparation, input.Restore.Preparation = padding, padding
		raw = encode()
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
		{"compaction-large", domain.CompactSessionJob, 2 << 20, false},
		{"compaction-queued", domain.CompactSessionJob, 2 << 20, true},
		{"compaction-near-input-bound", domain.CompactSessionJob, domain.MaxCompactionInputBytes - 1024, false},
		{"storage-recovery-large", domain.WorkspaceStorageJob, 600 << 10, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, root, ctx, in, session := restoreFixture(t)
			id := domain.NewID()
			now := time.Now().UTC()
			input, err := json.Marshal(struct {
				History string `json:"history"`
			}{strings.Repeat("x", test.size)})
			if err != nil {
				t.Fatal(err)
			}
			if test.kind == domain.CompactSessionJob {
				input = historyCompactionInput(t, test.size)
			}
			if test.kind == domain.WorkspaceStorageJob {
				input = historyRecoveryInput(t, test.size)
			}
			// Storage-envelope evidence only: full contextual native request
			// admission remains the independent server/Worker owner's validation.
			job := domain.Job{Type: test.kind, State: domain.JobSucceeded, MachineID: domain.NewID(), InstanceID: domain.NewID(), Input: input, Output: json.RawMessage(`{"retained":"output"}`), AcceptedAt: now, FinishedAt: &now}
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
			if _, err := Decode[domain.Job](original); err != nil {
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
			retained, err := Decode[domain.Job](row)
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
	for _, mode := range []string{"ordinary-size", "compaction-size", "compaction-input-size", "compaction-input-unknown", "compaction-input-malformed", "compaction-input-invariant", "compaction-invalid-state", "recovery-size", "unknown", "duplicate", "utf8"} {
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
			case "compaction-input-size", "compaction-input-unknown", "compaction-input-malformed", "compaction-input-invariant", "compaction-invalid-state":
				job.Type = domain.CompactSessionJob
				job.Input = historyCompactionInput(t, 512)
				switch mode {
				case "compaction-input-size":
					job.Input = historyCompactionInput(t, domain.MaxCompactionInputBytes+1024)
				case "compaction-input-unknown":
					job.Input = append(job.Input[:len(job.Input)-1], []byte(`,"unknown":true}`)...)
				case "compaction-input-malformed":
					job.Input = json.RawMessage(`{"version":2}`)
				case "compaction-input-invariant":
					var input domain.SessionCompactionInput
					if err := json.Unmarshal(job.Input, &input); err != nil {
						t.Fatal(err)
					}
					input.Completion.CleanupVerified = false
					job.Input, err = json.Marshal(input)
					if err != nil {
						t.Fatal(err)
					}
				case "compaction-invalid-state":
					job.State = domain.JobState("foreign-state")
				}
				raw, err = json.Marshal(job)
				if err != nil || len(raw) >= domain.MaxCompactionJobBytes {
					t.Fatal("typed regression must remain below the outer cap", err, len(raw))
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
			if strings.HasPrefix(mode, "compaction-input-") || mode == "compaction-invalid-state" {
				if _, err := Decode[domain.Job](Record{ID: id, Kind: domain.JobKind, Data: raw}); err != nil {
					t.Fatal("outer-only decoder unexpectedly rejected regression", err)
				}
				var compact domain.Job
				if domain.DecodeCompactionJob(raw, &compact) == nil {
					t.Fatal("owning compaction decoder accepted unsupported input", mode)
				}
			} else if _, err := Decode[domain.Job](Record{ID: id, Kind: domain.JobKind, Data: raw}); err == nil {
				t.Fatal("owning decoder accepted unsupported document", mode)
			}
			if _, err := Decode[domain.Job](Record{ID: id, Kind: domain.JobKind, Data: append(append([]byte(nil), live.Data...), []byte(` {}`)...)}); err == nil {
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
			candidateDB, err := sql.Open("sqlite", databaseURI(candidate, true))
			if err != nil {
				t.Fatal(err)
			}
			var rejected []byte
			readErr := candidateDB.QueryRowContext(ctx, "SELECT body FROM entities WHERE id=?", id).Scan(&rejected)
			closeErr := candidateDB.Close()
			if readErr != nil || closeErr != nil || !bytes.Equal(rejected, raw) {
				t.Fatal("rejected candidate published a partial quarantine", readErr, closeErr)
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
