// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// This small receipt survives a crash after report acknowledgment. It carries
// no workspace inventory and never grants another native removal operation.
type storageRetirement struct {
	Version  int                               `json:"version"`
	JobID    domain.ID                         `json:"job_id"`
	ReportID domain.ID                         `json:"report_id"`
	Digest   string                            `json:"assignment_digest"`
	Revision uint64                            `json:"assignment_revision"`
	Removal  workspace.StorageRemovalReference `json:"removal"`
}

func acknowledgeStorageRemoval(ctx context.Context, config Config, assigned *pb.Resource, job domain.Job, result journal, ack *pb.Resource) error {
	if job.Type != domain.WorkspaceStorageJob || result.Problem != nil {
		return nil
	}
	var accepted domain.Job
	if ack == nil || ack.Id != assigned.Id || ack.SessionId != assigned.SessionId || ack.Kind != pb.EntityKind_ENTITY_KIND_JOB || ack.SchemaVersion != 1 || ack.Revision <= assigned.Revision || domain.Decode(ack.DocumentJson, &accepted) != nil || accepted.Type != job.Type || accepted.MachineID != job.MachineID || accepted.InstanceID != job.InstanceID || !bytes.Equal(accepted.Input, job.Input) {
		return workspace.ResultUncertain()
	}
	// A server may accept the report as uncertain after rejecting its output.
	// HTTP success alone must not retire the original recovery evidence.
	if accepted.State != domain.JobSucceeded {
		return nil
	}
	if result.State != journalReported || accepted.Problem != nil || !bytes.Equal(accepted.Output, result.Output) {
		return workspace.ResultUncertain()
	}
	var input workspace.StorageRequest
	if domain.Decode(job.Input, &input) != nil || input.OperationID != result.JobID {
		return workspace.ResultUncertain()
	}
	if input.Action == workspace.StorageRecover {
		if input.Recovery == nil {
			return workspace.ResultUncertain()
		}
		input = input.Recovery.Original
	}
	if input.Action != workspace.StorageCleanup && input.Action != workspace.StorageDelete {
		return nil
	}
	receipt := storageRetirement{Version: 1, JobID: result.JobID, ReportID: result.ReportID, Digest: result.Digest, Revision: result.Revision, Removal: workspace.StorageRemovalReference{OperationID: input.OperationID, SessionID: input.Preparation.SessionID, SnapshotID: input.SnapshotID, Action: input.Action}}
	root := filepath.Join(config.Root, "storage-removal-retirements")
	if err := security.PrivateDir(root); err != nil {
		return err
	}
	path := filepath.Join(root, string(result.JobID)+".json")
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if old, err := security.ReadPrivate(path, 4096); err == nil {
		if !bytes.Equal(old, raw) {
			return workspace.ResultUncertain()
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else if err := security.WriteAtomic(path, raw); err != nil {
		return err
	}
	return retireStorageReport(ctx, config, path, receipt)
}

func retireStorageReport(ctx context.Context, config Config, path string, receipt storageRetirement) error {
	if receipt.Version != 1 || receipt.JobID.Validate() != nil || receipt.ReportID.Validate() != nil || receipt.Revision == 0 {
		return workspace.ResultUncertain()
	}
	raw, err := security.ReadPrivate(filepath.Join(config.Root, "jobs", string(receipt.JobID)+".json"), 2<<20)
	var reported journal
	if err != nil || domain.Decode(raw, &reported) != nil || reported.Version != 1 || reported.State != journalReported || reported.JobID != receipt.JobID || reported.ReportID != receipt.ReportID || reported.Revision != receipt.Revision || reported.Digest != receipt.Digest || reported.Problem != nil {
		return workspace.ResultUncertain()
	}
	var output workspace.StorageResult
	if domain.Decode(reported.Output, &output) != nil || !output.CleanupVerified || output.OperationID != receipt.JobID || output.SessionID != receipt.Removal.SessionID || output.Snapshot == nil || output.Snapshot.ID != receipt.Removal.SnapshotID || (output.Action != receipt.Removal.Action && (output.Action != workspace.StorageRecover || output.RecoveredJobID != receipt.Removal.OperationID)) {
		return workspace.ResultUncertain()
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	manager := workspace.Manager{Root: config.Root, Logger: config.Logger}
	if err := manager.RetireStorageRemoval(bounded, receipt.Removal); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if err := security.SyncParent(path); err != nil {
		return err
	}
	config.Logger.Info("storage_intent_retired", "job_id", receipt.JobID, "operation_id", receipt.Removal.OperationID)
	return nil
}

func retireStorageReports(ctx context.Context, config Config) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	root := filepath.Join(config.Root, "storage-removal-retirements")
	if err := security.PrivateDir(root); err != nil {
		return err
	}
	dir, err := os.Open(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	names, err := dir.Readdirnames(4097)
	if err != nil && err != io.EOF {
		return err
	}
	if len(names) > 4096 {
		return workspace.ResultUncertain()
	}
	for _, name := range names {
		if strings.HasPrefix(name, ".pending-") {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(root, name)
		raw, err := security.ReadPrivate(path, 4096)
		var receipt storageRetirement
		if err != nil || domain.Decode(raw, &receipt) != nil || name != string(receipt.JobID)+".json" {
			return workspace.ResultUncertain()
		}
		if err := retireStorageReport(ctx, config, path, receipt); err != nil {
			return err
		}
	}
	return nil
}
