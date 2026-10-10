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
	original := workspace.StorageRequest{
		Version: 1, OperationID: domain.NewID(), Action: workspace.StorageInspect,
		Preparation: workspace.PrepareRequest{SessionID: domain.NewID()}, SnapshotID: domain.NewID(),
		Manifest: workspace.Manifest{PrimaryPath: strings.Repeat("x", size)},
	}
	instanceID, assignmentDigest := domain.NewID(), strings.Repeat("ab", 32)
	claim := workspace.StorageJournalClaim{JobID: domain.NewID(), InstanceID: instanceID, Revision: 1, AssignmentDigest: assignmentDigest}
	input := workspace.StorageRequest{
		Version: 1, OperationID: domain.NewID(), Action: workspace.StorageRecover,
		Preparation: original.Preparation, Manifest: original.Manifest, SnapshotID: original.SnapshotID,
		Recovery: &workspace.StorageRecovery{Original: original, Claims: []workspace.StorageJournalClaim{claim}, InstanceID: instanceID, Revision: claim.Revision, AssignmentDigest: assignmentDigest},
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Build complete immutable compaction evidence, rather than a generic history
// blob, so restoration exercises the same owner decoder as normal reads.
func historyCompactionInput(t *testing.T, size int) json.RawMessage {
	t.Helper()
	account, provider := domain.NewID(), domain.NewID()
	configuration := domain.ExecutionConfiguration{
		AgentID: domain.NewID(), AgentRevision: 1, Harness: domain.Codex,
		ModelRevision: 1, ProviderID: provider, NativeModel: "fixture-model", Routing: domain.Priority,
		Accounts: []domain.WeightedAccount{{ID: account, Weight: 1}}, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly},
	}
	configuration.ModelID = (domain.ModelIdentity{ProviderID: provider, NativeID: configuration.NativeModel}).Key()
	assignment := domain.ExecutionJobInput{
		Version: 4, Startup: &domain.ExecutionStartupSelection{Harness: domain.Codex},
		SessionID: domain.NewID(), MachineID: domain.NewID(), ExecutionID: domain.NewID(), InputID: domain.NewID(),
		ThreadRequestID: domain.NewID(), TurnRequestID: domain.NewID(), AccountID: account, ConnectionID: domain.NewID(),
		Configuration: configuration, Input: domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "fixture"},
		Preparation: json.RawMessage(`{"history":""}`), Manifest: json.RawMessage(`{}`),
	}
	assignment.ConfigurationDigest, _ = configuration.Digest()
	input := domain.SessionCompactionInput{Version: 2, ActionID: domain.NewID(), SourceJobID: domain.NewID(), Dispatch: domain.DispatchReady, Intent: domain.ContinueExplicitly}
	completion := domain.ExecutionCompletion{Version: 2, ExecutionID: assignment.ExecutionID, InputID: assignment.InputID, NativeThreadID: domain.NativeIdentity(domain.NewID()), NativeTurnID: domain.NativeIdentity(domain.NewID()), LastSequence: 3, Outcome: domain.ExecutionSucceeded, CleanupVerified: true, NativeCheckpointDigest: strings.Repeat("ab", 32)}
	build := func(padding int) []byte {
		assignment.Preparation, _ = json.Marshal(struct {
			History string `json:"history"`
		}{strings.Repeat("x", padding)})
		original, _ := json.Marshal(assignment)
		digest := sha256.Sum256(original)
		restore := assignment
		restore.ExecutionID, restore.InputID = input.ActionID, domain.NewID()
		restore.ThreadRequestID, restore.TurnRequestID = domain.NewID(), domain.NewID()
		restore.Continuation = &domain.ExecutionContinuation{
			HistoryExecutionID: assignment.ExecutionID, HistoryRequestID: domain.NewID(), Completion: completion,
			AssignmentInputDigest: hex.EncodeToString(digest[:]), InputMode: domain.ExecuteMode,
			PromptDigest: domain.BindSessionInput(assignment.InputID, assignment.Input).PromptDigest, Intent: domain.ContinueExplicitly,
			Previous: domain.ExecutionProgress{JobID: input.SourceJobID, ExecutionID: assignment.ExecutionID, InputID: assignment.InputID,
				NativeThreadID: string(completion.NativeThreadID), NativeTurnID: string(completion.NativeTurnID), LastSequence: 3,
				Outcome: domain.ExecutionSucceeded, CleanupVerified: true, Observed: domain.ObservedExecutionSettings{Model: configuration.NativeModel, Permission: domain.PermissionReadOnly, ApprovalPolicy: "never"}},
		}
		input.Assignment, input.Restore, input.Completion = assignment, restore, completion
		raw, err := json.Marshal(input)
		if err != nil || input.Validate() != nil {
			t.Fatal("invalid complete compaction fixture", err, input.Validate())
		}
		return raw
	}
	base := build(0)
	return build(max(0, (size-len(base))/2))
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
		{"compaction-near-input-limit", domain.CompactSessionJob, domain.MaxCompactionInputBytes - 128, true},
		{"storage-recovery-small", domain.WorkspaceStorageJob, 512, false},
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
			retained, err := decodeRestoreJob(row)
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
	for _, mode := range []string{"ordinary-size", "compaction-size", "compaction-input-size", "compaction-input-malformed", "compaction-input-unknown", "compaction-input-foreign", "compaction-invalid-state", "recovery-size", "workspace-input-unknown", "workspace-invalid-recovery-metadata", "unknown", "duplicate", "utf8"} {
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
			case "compaction-input-size", "compaction-input-malformed", "compaction-input-unknown", "compaction-input-foreign", "compaction-invalid-state":
				job.Type = domain.CompactSessionJob
				job.Input = historyCompactionInput(t, 4096)
				switch mode {
				case "compaction-input-size":
					job.Input = historyCompactionInput(t, domain.MaxCompactionInputBytes+2)
				case "compaction-input-malformed":
					job.Input = json.RawMessage(`{"version":2}`)
				case "compaction-input-unknown":
					job.Input = append(job.Input[:len(job.Input)-1], []byte(`,"unknown":true}`)...)
				case "compaction-input-foreign":
					var input domain.SessionCompactionInput
					if domain.DecodeCompactionInput(job.Input, &input) != nil {
						t.Fatal("fixture decode")
					}
					input.Restore.AccountID = domain.NewID()
					job.Input, _ = json.Marshal(input)
				case "compaction-invalid-state":
					job.State = "unknown"
				}
				raw, _ = json.Marshal(job)
				if len(raw) >= domain.MaxCompactionJobBytes {
					t.Fatal("fixture must fit outer cap")
				}
			case "recovery-size":
				job.Type = domain.WorkspaceStorageJob
				job.Input = historyRecoveryInput(t, 2<<20)
				raw, _ = json.Marshal(job)
			case "workspace-input-unknown", "workspace-invalid-recovery-metadata":
				job.Type = domain.WorkspaceStorageJob
				job.Input = historyRecoveryInput(t, 512)
				var input workspace.StorageRequest
				if err := workspace.DecodeStorageRequest(job.Input, &input); err != nil {
					t.Fatal("invalid recovery fixture", err)
				}
				if mode == "workspace-input-unknown" {
					job.Input = append(job.Input[:len(job.Input)-1], []byte(`,"unknown":true}`)...)
				} else {
					input.Recovery.Revision = 0
					job.Input, _ = json.Marshal(input)
				}
				raw, _ = json.Marshal(job)
			case "unknown":
				raw = append(raw[:len(raw)-1], []byte(`,"unknown":true}`)...)
			case "duplicate":
				raw = append(raw[:len(raw)-1], []byte(`,"type":"harness-discovery"}`)...)
			case "utf8":
				raw = bytes.Replace(raw, []byte(`"input":{}`), []byte{'"', 'i', 'n', 'p', 'u', 't', '"', ':', '"', 0xff, '"'}, 1)
			}
			if _, err := decodeRestoreJob(Record{ID: id, Kind: domain.JobKind, Data: raw}); err == nil {
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

func TestRestoreQuarantineRevalidatesTypedJobEntityBound(t *testing.T) {
	for _, kind := range []domain.JobType{domain.CompactSessionJob, domain.WorkspaceStorageJob} {
		for _, mode := range []string{"overflow", "headroom", "terminal"} {
			t.Run(string(kind)+"/"+mode, func(t *testing.T) {
				s, root, ctx, in, session := restoreFixture(t)
				id, now := domain.NewID(), time.Now().UTC()
				job := domain.Job{Type: domain.HarnessDiscoveryJob, State: domain.JobSucceeded, MachineID: domain.NewID(), Input: json.RawMessage(`{}`), AcceptedAt: now, FinishedAt: &now}
				if _, err := s.Mutate(ctx, domain.NewID(), "fixture.original-history", nil, func(tx *Tx) (any, error) { return tx.PutJob(id, 0, session, "", job) }); err != nil {
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
				job.Type, job.State, job.FinishedAt = kind, domain.JobQueued, nil
				if mode == "terminal" {
					job.State, job.FinishedAt = domain.JobSucceeded, &now
				}
				if kind == domain.CompactSessionJob {
					job.Input = historyCompactionInput(t, domain.MaxCompactionInputBytes-128)
				} else {
					// Typed history only: contextual native workspace admission is
					// independent. Retain a bounded original and a large recovery
					// manifest to exercise the permitted 3 MiB/4 MiB envelopes.
					var input workspace.StorageRequest
					if workspace.DecodeStorageRequest(historyRecoveryInput(t, 512<<10), &input) != nil {
						t.Fatal("recovery fixture decode")
					}
					input.Manifest.PrimaryPath = ""
					base, _ := json.Marshal(input)
					input.Manifest.PrimaryPath = strings.Repeat("x", domain.MaxStorageRecoveryInputBytes-128-len(base))
					job.Input, _ = json.Marshal(input)
				}
				job.Output = json.RawMessage(`""`)
				base, _ := json.Marshal(job)
				target := domain.MaxCompactionJobBytes - 1
				if mode == "headroom" {
					target -= 4096
				}
				job.Output, _ = json.Marshal(strings.Repeat("x", target-len(base)))
				raw, err := json.Marshal(job)
				if err != nil || len(raw) != target || job.Validate() != nil {
					t.Fatal("invalid near-bound fixture", len(raw), target, err, job.Validate())
				}
				if _, err := decodeRestoreJob(Record{Kind: domain.JobKind, Data: raw}); err != nil {
					t.Fatal("original typed job rejected", err)
				}
				db, err := sql.Open("sqlite", databaseURI(candidate, false))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, "UPDATE entities SET body=? WHERE id=?", raw, id); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				request := domain.NewID()
				err = prepareRestoreImage(ctx, candidate, source, BackupRestore{RequestID: request, Input: in, CreatedAt: now})
				if mode == "overflow" && err == nil {
					t.Fatal("quarantine exceeded the outer job cap without atomic rejection")
				}
				if mode != "overflow" && err != nil {
					t.Fatal("supported typed history rejected", err)
				}
				db, err = sql.Open("sqlite", databaseURI(candidate, true))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				var retainedRaw []byte
				if err := db.QueryRowContext(ctx, "SELECT body FROM entities WHERE id=?", id).Scan(&retainedRaw); err != nil {
					t.Fatal(err)
				}
				retained, err := decodeRestoreJob(Record{Kind: domain.JobKind, Data: retainedRaw})
				if err != nil || !bytes.Equal(retained.Input, job.Input) || !bytes.Equal(retained.Output, job.Output) {
					t.Fatal("typed history/input/output changed", err)
				}
				if mode == "overflow" {
					if !bytes.Equal(retainedRaw, raw) {
						t.Fatal("rejected candidate retained partial quarantine")
					}
					var receipts int
					if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM receipts WHERE id=?", request).Scan(&receipts); err != nil || receipts != 0 {
						t.Fatal("rejected candidate published a restore receipt", err)
					}
				} else if mode == "headroom" {
					if retained.State != domain.JobCanceled || retained.Problem == nil || retained.Problem.Code != domain.RecoveryRequired || retained.FinishedAt == nil {
						t.Fatal("quarantine settlement missing")
					}
				} else if !bytes.Equal(retainedRaw, raw) {
					t.Fatal("terminal job body changed")
				}
				unchanged, err := s.Get(ctx, domain.JobKind, id)
				if err != nil || !bytes.Equal(unchanged.Data, live.Data) || unchanged.Revision != live.Revision {
					t.Fatal("candidate transformation changed live database", err)
				}
				sourceAfter, err := os.ReadFile(source)
				if err != nil || !bytes.Equal(sourceAfter, original) {
					t.Fatal("candidate transformation changed source backup", err)
				}
			})
		}
	}
}

func TestRestoreTransformationCountsRetainedQuarantineBytes(t *testing.T) {
	// Boundary fixture avoids allocating the whole 256 MiB allowance. The
	// final document fits before metadata and exceeds it only after quarantine.
	before := int64(maxRestoreTransformationBytes - 1)
	if err := restoreTransformationBound(1, before); err != nil {
		t.Fatal(err)
	}
	if err := restoreTransformationBound(1, before+256); err == nil {
		t.Fatal("transformed bytes were not bounded")
	}
	if err := restoreTransformationBound(99999, maxRestoreTransformationBytes); err != nil {
		t.Fatal("supported exact limit rejected", err)
	}
	if err := restoreTransformationBound(100000, 0); err == nil {
		t.Fatal("document count unbounded")
	}
}
