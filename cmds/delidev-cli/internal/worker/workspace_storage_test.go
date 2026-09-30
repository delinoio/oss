// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestStorageJournalDoesNotReplayCleanupAndRecoveryBindsOriginal(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := workspace.Manager{Root: filepath.Join(t.TempDir(), "worker"), Logger: logger}
	prepare := workspace.PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	manifest, err := manager.Prepare(context.Background(), prepare)
	if err != nil {
		t.Fatal(err)
	}
	input := workspace.StorageRequest{Version: 1, OperationID: domain.NewID(), PreviousState: domain.WorkspacePresent, Action: workspace.StoragePreview, Preparation: prepare, Manifest: manifest}
	preview, err := manager.Storage(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.OperationID = domain.NewID()
	input.Action = workspace.StorageCleanup
	input.SnapshotID = domain.NewID()
	input.PreviewDigest = preview.PreviewDigest
	raw, _ := json.Marshal(input)
	instance := domain.NewID()
	job := domain.Job{Type: domain.WorkspaceStorageJob, State: domain.JobClaimed, MachineID: prepare.MachineID, InstanceID: instance, Input: raw, AcceptedAt: time.Now().UTC()}
	document, _ := json.Marshal(job)
	resource := &pb.Resource{Id: string(input.OperationID), Revision: 2, Kind: pb.EntityKind_ENTITY_KIND_JOB, SessionId: string(prepare.SessionID), DocumentJson: document}
	security.PrivateDir(filepath.Join(manager.Root, "jobs"))
	config := Config{Root: manager.Root, Logger: logger}
	first, err := runJob(context.Background(), config, instance, resource, job)
	if err != nil || first.Problem != nil {
		t.Fatal("storage failed", err, first.Problem)
	}
	replay, err := runJob(context.Background(), config, instance, resource, job)
	if err != nil || string(first.Output) != string(replay.Output) || first.ReportID != replay.ReportID {
		t.Fatal("completion reexecuted", err)
	}
	first.State = journalStarted
	first.Output = nil
	if err := writeJSON(filepath.Join(manager.Root, "jobs", resource.Id+".json"), first); err != nil {
		t.Fatal(err)
	}
	uncertain, err := runJob(context.Background(), config, instance, resource, job)
	if err != nil || uncertain.Problem == nil || uncertain.Problem.Code != domain.RecoveryRequired {
		t.Fatal("interrupted storage replayed", err)
	}
	claim := workspace.StorageJournalClaim{JobID: input.OperationID, InstanceID: instance, Revision: 2, AssignmentDigest: first.Digest}
	recovery := workspace.StorageRequest{Version: 1, OperationID: domain.NewID(), Action: workspace.StorageRecover, Preparation: prepare, Manifest: manifest, SnapshotID: input.SnapshotID, Recovery: &workspace.StorageRecovery{Original: input, Claims: []workspace.StorageJournalClaim{claim}, InstanceID: instance, Revision: 2, AssignmentDigest: first.Digest}}
	raw, _ = json.Marshal(recovery)
	job.Input = raw
	if _, err := execute(context.Background(), config, recovery.OperationID, job); err != nil {
		t.Fatal("original journal did not recover", err)
	}
	uncertain.Digest = "changed"
	writeJSON(filepath.Join(manager.Root, "jobs", resource.Id+".json"), uncertain)
	if _, err := execute(context.Background(), config, recovery.OperationID, job); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("changed journal authorized recovery", err)
	}
	if _, err := os.Lstat(filepath.Join(manager.Root, "workspaces", string(prepare.SessionID))); !os.IsNotExist(err) {
		t.Fatal("recovery recreated source", err)
	}
}

func TestStorageRemovalRetiresOnlyAfterDurableReport(t *testing.T) {
	for _, recoverOriginal := range []bool{false, true} {
		t.Run(fmt.Sprint("recovery=", recoverOriginal), func(t *testing.T) {
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			manager := workspace.Manager{Root: filepath.Join(t.TempDir(), "worker"), Logger: logger}
			prepare := workspace.PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
			manifest, err := manager.Prepare(context.Background(), prepare)
			if err != nil {
				t.Fatal(err)
			}
			input := workspace.StorageRequest{Version: 1, OperationID: domain.NewID(), PreviousState: domain.WorkspacePresent, Action: workspace.StoragePreview, Preparation: prepare, Manifest: manifest}
			preview, err := manager.Storage(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			input.OperationID, input.Action, input.SnapshotID, input.PreviewDigest = domain.NewID(), workspace.StorageCleanup, domain.NewID(), preview.PreviewDigest
			original := input
			instance := domain.NewID()
			config := Config{Root: manager.Root, Logger: logger}
			if err := security.PrivateDir(filepath.Join(manager.Root, "jobs")); err != nil {
				t.Fatal(err)
			}
			makeJob := func(input workspace.StorageRequest) (domain.Job, *pb.Resource, journal) {
				raw, _ := json.Marshal(input)
				job := domain.Job{Type: domain.WorkspaceStorageJob, State: domain.JobClaimed, MachineID: prepare.MachineID, InstanceID: instance, Input: raw, AcceptedAt: time.Now().UTC()}
				document, _ := json.Marshal(job)
				resource := &pb.Resource{Id: string(input.OperationID), Revision: 2, SchemaVersion: 1, Kind: pb.EntityKind_ENTITY_KIND_JOB, SessionId: string(prepare.SessionID), DocumentJson: document}
				result, err := runJob(context.Background(), config, instance, resource, job)
				if err != nil || result.Problem != nil {
					t.Fatal(err, result.Problem)
				}
				return job, resource, result
			}
			job, resource, result := makeJob(input)
			if recoverOriginal {
				claim := workspace.StorageJournalClaim{JobID: input.OperationID, InstanceID: instance, Revision: 2, AssignmentDigest: result.Digest}
				input = workspace.StorageRequest{Version: 1, OperationID: domain.NewID(), Action: workspace.StorageRecover, Preparation: prepare, Manifest: manifest, SnapshotID: original.SnapshotID, Recovery: &workspace.StorageRecovery{Original: original, Claims: []workspace.StorageJournalClaim{claim}, InstanceID: instance, Revision: 2, AssignmentDigest: result.Digest}}
				job, resource, result = makeJob(input)
			}
			intent := filepath.Join(manager.Root, "storage-removal-intents", string(original.OperationID)+".json")
			accepted := job
			accepted.State = domain.JobUncertain
			ack := *resource
			ack.Revision++
			ack.DocumentJson, _ = json.Marshal(accepted)
			if err := acknowledgeStorageRemoval(context.Background(), config, resource, job, result, &ack); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(intent); err != nil {
				t.Fatal("uncertain server report retired evidence", err)
			}
			accepted.State, accepted.Output = domain.JobSucceeded, result.Output
			ack.DocumentJson, _ = json.Marshal(accepted)
			result.State = journalReported
			if err := acknowledgeStorageRemoval(context.Background(), config, resource, job, result, &ack); err == nil {
				t.Fatal("unfinished journal granted retirement")
			}
			if _, err := os.Stat(intent); err != nil {
				t.Fatal("unreported journal retired evidence", err)
			}
			if err := writeJSON(filepath.Join(manager.Root, "jobs", resource.Id+".json"), result); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := acknowledgeStorageRemoval(ctx, config, resource, job, result, &ack); err == nil {
				t.Fatal("canceled retirement unexpectedly finished")
			}
			if _, err := os.Stat(intent); err != nil {
				t.Fatal("canceled retirement lost its intent", err)
			}
			if err := retireStorageReports(context.Background(), config); err != nil {
				t.Fatal("restart failed to complete acknowledged retirement", err)
			}
			if _, err := os.Stat(intent); !os.IsNotExist(err) {
				t.Fatal("acknowledged intent retained", err)
			}
			entries, err := os.ReadDir(filepath.Join(manager.Root, "storage-removal-retirements"))
			if err != nil || len(entries) != 0 {
				t.Fatal("retirement receipt retained", err)
			}
		})
	}
}
