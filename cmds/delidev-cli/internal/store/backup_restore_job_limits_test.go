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
	for _, mode := range []string{"ordinary-size", "compaction-size", "recovery-size", "unknown", "duplicate", "utf8"} {
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
			if _, err := Decode[domain.Job](Record{ID: id, Kind: domain.JobKind, Data: raw}); err == nil {
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
