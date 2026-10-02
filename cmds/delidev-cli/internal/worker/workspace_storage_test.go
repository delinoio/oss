// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"google.golang.org/protobuf/proto"
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
			if err := prepareStorageRetirement(config, job, result); err != nil {
				t.Fatal("pre-report retirement was not durable", err)
			}
			retirementRaw, err := security.ReadPrivate(filepath.Join(manager.Root, "storage-removal-retirements", string(result.JobID)+".json"), 4096)
			var pending storageRetirement
			if err != nil || domain.Decode(retirementRaw, &pending) != nil || !pending.PendingReport {
				t.Fatal("pre-report retirement was not marked pending", err)
			}
			intent := filepath.Join(manager.Root, "storage-removal-intents", string(original.OperationID)+".json")
			claimPath := filepath.Join(manager.Root, "storage-removal-claims", string(original.OperationID)+".json")
			accepted := job
			accepted.State = domain.JobUncertain
			ack := proto.Clone(resource).(*pb.Resource)
			ack.Revision++
			ack.DocumentJson, _ = json.Marshal(accepted)
			if err := acknowledgeStorageRemoval(context.Background(), config, resource, job, result, ack); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(intent); err != nil {
				t.Fatal("uncertain server report retired evidence", err)
			}
			if _, err := os.Stat(claimPath); err != nil {
				t.Fatal("uncertain server report retired verified claim", err)
			}
			accepted.State, accepted.Output = domain.JobSucceeded, result.Output
			ack.DocumentJson, _ = json.Marshal(accepted)
			result.State = journalReported
			if err := acknowledgeStorageRemoval(context.Background(), config, resource, job, result, ack); err == nil {
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
			if err := acknowledgeStorageRemoval(ctx, config, resource, job, result, ack); err == nil {
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
			if _, err := os.Stat(claimPath); !os.IsNotExist(err) {
				t.Fatal("acknowledged verified claim retained", err)
			}
			entries, err := os.ReadDir(filepath.Join(manager.Root, "storage-removal-retirements"))
			if err != nil || len(entries) != 0 {
				t.Fatal("retirement receipt retained", err)
			}
		})
	}
}

func TestStorageRetirementHandlesUnpublishedRecoveryAndTerminalFailure(t *testing.T) {
	for _, unpublished := range []bool{false, true} {
		t.Run(fmt.Sprint("unpublished=", unpublished), func(t *testing.T) {
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
			instance := domain.NewID()
			config := Config{Root: manager.Root, Logger: logger}
			if err := security.PrivateDir(filepath.Join(manager.Root, "jobs")); err != nil {
				t.Fatal(err)
			}
			if unpublished {
				raw, _ := json.Marshal(input)
				original := domain.Job{Type: domain.WorkspaceStorageJob, State: domain.JobClaimed, MachineID: prepare.MachineID, InstanceID: instance, Input: raw, AcceptedAt: time.Now().UTC()}
				document, _ := json.Marshal(original)
				hash := sha256.Sum256(document)
				digest := hex.EncodeToString(hash[:])
				prior := journal{Version: 1, JobID: input.OperationID, InstanceID: instance, Revision: 2, Digest: digest, State: journalStarted, ReportID: domain.NewID()}
				if err := writeJSON(filepath.Join(manager.Root, "jobs", string(input.OperationID)+".json"), prior); err != nil {
					t.Fatal(err)
				}
				claim := workspace.StorageJournalClaim{JobID: input.OperationID, InstanceID: instance, Revision: 2, AssignmentDigest: digest}
				input = workspace.StorageRequest{Version: 1, OperationID: domain.NewID(), Action: workspace.StorageRecover, Preparation: prepare, Manifest: manifest, SnapshotID: input.SnapshotID, Recovery: &workspace.StorageRecovery{Original: input, Claims: []workspace.StorageJournalClaim{claim}, InstanceID: instance, Revision: 2, AssignmentDigest: digest}}
			} else {
				input.Action = workspace.StorageCreate
				created, err := manager.Storage(context.Background(), input)
				if err != nil {
					t.Fatal(err)
				}
				input.Action, input.OperationID, input.SnapshotDigest = workspace.StorageDelete, domain.NewID(), created.Snapshot.SHA256
				removal := filepath.Join(manager.Root, "workspace-removals", string(input.OperationID))
				if err := os.Mkdir(removal, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(removal, "foreign"), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			raw, _ := json.Marshal(input)
			job := domain.Job{Type: domain.WorkspaceStorageJob, State: domain.JobClaimed, MachineID: prepare.MachineID, InstanceID: instance, Input: raw, AcceptedAt: time.Now().UTC()}
			document, _ := json.Marshal(job)
			assigned := &pb.Resource{Id: string(input.OperationID), SchemaVersion: 1, Revision: 2, Kind: pb.EntityKind_ENTITY_KIND_JOB, SessionId: string(prepare.SessionID), DocumentJson: document}
			result, err := runJob(context.Background(), config, instance, assigned, job)
			if err != nil {
				t.Fatal(err)
			}
			accepted := job
			accepted.State, accepted.Output = domain.JobSucceeded, result.Output
			if unpublished {
				var output workspace.StorageResult
				if domain.Decode(result.Output, &output) != nil || output.Snapshot != nil || output.RecoveredJobState != domain.JobFailed {
					t.Fatal("fixture did not reconcile unpublished cleanup")
				}
			} else {
				if result.Problem == nil || result.Problem.Code == domain.RecoveryRequired {
					t.Fatal("fixture did not fail before claim", result.Problem)
				}
				accepted.State, accepted.Problem = domain.JobFailed, result.Problem
			}
			result.State = journalReported
			if err := writeJSON(filepath.Join(manager.Root, "jobs", assigned.Id+".json"), result); err != nil {
				t.Fatal(err)
			}
			ack := proto.Clone(assigned).(*pb.Resource)
			ack.Revision++
			ack.DocumentJson, _ = json.Marshal(accepted)
			if err := acknowledgeStorageRemoval(context.Background(), config, assigned, job, result, ack); err != nil {
				t.Fatal("terminal report could not retire intent", err)
			}
			entries, err := os.ReadDir(filepath.Join(manager.Root, "storage-removal-retirements"))
			if err != nil || len(entries) != 0 {
				t.Fatal("terminal retirement blocked startup", err)
			}
			if !unpublished {
				raw, err := os.ReadFile(filepath.Join(manager.Root, "workspace-removals", string(input.OperationID), "foreign"))
				if err != nil || string(raw) != "preserve" {
					t.Fatal("retirement claimed foreign removal data", err)
				}
				if _, err := os.Stat(filepath.Join(manager.Root, "storage-removal-intents", string(input.OperationID)+".json")); !os.IsNotExist(err) {
					t.Fatal("failed original intent retained", err)
				}
			}
		})
	}
}

func TestStorageRetirementSurvivesAcknowledgmentBeforeReportedTransition(t *testing.T) {
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
	raw, _ := json.Marshal(input)
	instance := domain.NewID()
	job := domain.Job{Type: domain.WorkspaceStorageJob, State: domain.JobClaimed, MachineID: prepare.MachineID, InstanceID: instance, Input: raw, AcceptedAt: time.Now().UTC()}
	document, _ := json.Marshal(job)
	assigned := &pb.Resource{Id: string(input.OperationID), Revision: 2, SchemaVersion: 1, Kind: pb.EntityKind_ENTITY_KIND_JOB, SessionId: string(prepare.SessionID), DocumentJson: document}
	if err := security.PrivateDir(filepath.Join(manager.Root, "jobs")); err != nil {
		t.Fatal(err)
	}
	config := Config{Root: manager.Root, Logger: logger}
	result, err := runJob(context.Background(), config, instance, assigned, job)
	if err != nil || result.Problem != nil {
		t.Fatal(err, result.Problem)
	}
	accepted := job
	accepted.State, accepted.Output = domain.JobSucceeded, result.Output
	ack := proto.Clone(assigned).(*pb.Resource)
	ack.Revision++
	ack.DocumentJson, _ = json.Marshal(accepted)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A terminal acknowledgement is persisted, but interruption prevents the
	// reported journal transition. Startup must complete exactly this retirement.
	if err := acknowledgeStorageRemoval(ctx, config, assigned, job, result, ack); err == nil {
		t.Fatal("interrupted retirement succeeded")
	}
	path := filepath.Join(manager.Root, "jobs", assigned.Id+".json")
	raw, err = security.ReadPrivate(path, 2<<20)
	var retained journal
	if err != nil || domain.Decode(raw, &retained) != nil || retained.State != journalFinished {
		t.Fatal("fixture did not retain unreported journal", err)
	}
	if err := retireStorageReports(context.Background(), config); err != nil {
		t.Fatal("restart retirement", err)
	}
	raw, err = security.ReadPrivate(path, 2<<20)
	if err != nil || domain.Decode(raw, &retained) != nil || retained.State != journalReported || retained.ReportID != result.ReportID || !bytes.Equal(retained.Output, result.Output) {
		t.Fatal("original report transition changed", err)
	}
	for _, directory := range []string{"storage-removal-intents", "storage-removal-claims", "storage-removal-retirements"} {
		entries, err := os.ReadDir(filepath.Join(manager.Root, directory))
		if err != nil || len(entries) != 0 {
			t.Fatal("retirement artifacts remain", directory, err)
		}
	}
}
