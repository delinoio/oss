// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// This small receipt survives a crash after report acknowledgment. It carries
// no workspace inventory and never grants another native removal operation.
type storageRetirement struct {
	Version       int                               `json:"version"`
	PendingReport bool                              `json:"pending_report,omitempty"`
	JobID         domain.ID                         `json:"job_id"`
	ReportID      domain.ID                         `json:"report_id"`
	InstanceID    domain.ID                         `json:"instance_id,omitempty"`
	Digest        string                            `json:"assignment_digest"`
	Revision      uint64                            `json:"assignment_revision"`
	InputDigest   string                            `json:"input_digest,omitempty"`
	ResultDigest  string                            `json:"result_digest"`
	ProblemCode   domain.Code                       `json:"problem_code,omitempty"`
	Removal       workspace.StorageRemovalReference `json:"removal"`
}

func storageRetirementFor(job domain.Job, result journal, pending bool) (storageRetirement, bool, error) {
	if job.Type != domain.WorkspaceStorageJob {
		return storageRetirement{}, false, nil
	}
	var input workspace.StorageRequest
	if domain.Decode(job.Input, &input) != nil || input.OperationID != result.JobID {
		return storageRetirement{}, false, workspace.ResultUncertain()
	}
	if input.Action == workspace.StorageRecover {
		if input.Recovery == nil {
			return storageRetirement{}, false, workspace.ResultUncertain()
		}
		input = input.Recovery.Original
	}
	if input.Action != workspace.StorageCleanup && input.Action != workspace.StorageDelete {
		return storageRetirement{}, false, nil
	}
	if result.Problem != nil && result.Problem.Code == domain.RecoveryRequired {
		// The server keeps uncertain ownership for explicit recovery; there is
		// no terminal removal receipt to replay or retire in this outcome.
		return storageRetirement{}, false, nil
	}
	if input.OperationID.Validate() != nil || input.Preparation.SessionID.Validate() != nil || input.SnapshotID.Validate() != nil || result.ReportID.Validate() != nil || result.InstanceID.Validate() != nil {
		return storageRetirement{}, false, workspace.ResultUncertain()
	}
	inputDigest := sha256.Sum256(job.Input)
	receipt := storageRetirement{
		Version:       1,
		PendingReport: pending,
		JobID:         result.JobID,
		ReportID:      result.ReportID,
		InstanceID:    result.InstanceID,
		Digest:        result.Digest,
		Revision:      result.Revision,
		InputDigest:   hex.EncodeToString(inputDigest[:]),
		ResultDigest:  storageJournalResultDigest(result),
		Removal:       workspace.StorageRemovalReference{OperationID: input.OperationID, SessionID: input.Preparation.SessionID, SnapshotID: input.SnapshotID, Action: input.Action},
	}
	if result.Problem != nil {
		receipt.ProblemCode = result.Problem.Code
	}
	return receipt, true, nil
}

func persistStorageRetirement(config Config, receipt storageRetirement) (string, error) {
	root := filepath.Join(config.Root, "storage-removal-retirements")
	if err := security.PrivateDir(root); err != nil {
		return "", err
	}
	path := filepath.Join(root, string(receipt.JobID)+".json")
	raw, err := json.Marshal(receipt)
	if err != nil {
		return "", err
	}
	if oldRaw, err := security.ReadPrivate(path, 4096); err == nil {
		var old storageRetirement
		if domain.Decode(oldRaw, &old) != nil {
			return "", workspace.ResultUncertain()
		}
		if reflect.DeepEqual(old, receipt) {
			return path, nil
		}
		pending := receipt
		pending.PendingReport = true
		if receipt.PendingReport || !reflect.DeepEqual(old, pending) {
			return "", workspace.ResultUncertain()
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := security.WriteAtomic(path, raw); err != nil {
		return "", err
	}
	return path, nil
}

func prepareStorageRetirement(config Config, job domain.Job, result journal) error {
	receipt, needed, err := storageRetirementFor(job, result, true)
	if err != nil || !needed {
		return err
	}
	_, err = persistStorageRetirement(config, receipt)
	return err
}

func acknowledgeStorageRemoval(ctx context.Context, config Config, assigned *pb.Resource, job domain.Job, result journal, ack *pb.Resource) error {
	if job.Type != domain.WorkspaceStorageJob {
		return nil
	}
	var accepted domain.Job
	if ack == nil || ack.Id != assigned.Id || ack.SessionId != assigned.SessionId || ack.Kind != pb.EntityKind_ENTITY_KIND_JOB || ack.SchemaVersion != 1 || ack.Revision <= assigned.Revision || domain.Decode(ack.DocumentJson, &accepted) != nil || accepted.Type != job.Type || accepted.MachineID != job.MachineID || accepted.InstanceID != job.InstanceID || !bytes.Equal(accepted.Input, job.Input) {
		return workspace.ResultUncertain()
	}
	var input workspace.StorageRequest
	if domain.Decode(job.Input, &input) != nil || input.OperationID != result.JobID {
		return workspace.ResultUncertain()
	}
	// Acknowledged uncertainty still needs the original intent. Failed recovery
	// also preserves its predecessor's ownership for a future explicit recovery.
	switch accepted.State {
	case domain.JobSucceeded:
		if result.Problem != nil || accepted.Problem != nil || !bytes.Equal(accepted.Output, result.Output) {
			return workspace.ResultUncertain()
		}
	case domain.JobUncertain:
		// ReportWork turns a malformed cleanup/delete result into an acknowledged
		// uncertain job. The server has accepted the report attempt but has not
		// accepted its output, so the pending receipt must not replay that same
		// output. The removal intent remains protected for explicit recovery.
		if accepted.Problem == nil || accepted.Problem.Code != domain.RecoveryRequired || len(accepted.Output) != 0 {
			return workspace.ResultUncertain()
		}
	case domain.JobFailed, domain.JobCanceled:
		if result.Problem == nil || accepted.Problem == nil || result.Problem.Code != accepted.Problem.Code || len(result.Output) != 0 || len(accepted.Output) != 0 {
			return workspace.ResultUncertain()
		}
	default:
		return nil
	}
	if result.State != journalFinished && result.State != journalReported {
		return workspace.ResultUncertain()
	}
	// Save the acknowledgement proof before the reported transition. A crash
	// after either publication is recoverable from this original journal/receipt.
	rawJournal, err := security.ReadPrivate(filepath.Join(config.Root, "jobs", string(result.JobID)+".json"), 2<<20)
	var original journal
	if err != nil || domain.Decode(rawJournal, &original) != nil || !reflect.DeepEqual(original, result) {
		return workspace.ResultUncertain()
	}
	if accepted.State == domain.JobUncertain || (input.Action == workspace.StorageRecover && (accepted.State == domain.JobFailed || accepted.State == domain.JobCanceled)) {
		return discardPendingStorageRetirement(config, job, result)
	}
	receipt, needed, err := storageRetirementFor(job, result, false)
	if err != nil {
		return err
	}
	if !needed {
		return nil
	}
	path, err := persistStorageRetirement(config, receipt)
	if err != nil {
		return err
	}
	return retireStorageReport(ctx, config, path, receipt)
}

// An acknowledged uncertain report is terminal for the report attempt, but it
// does not prove source removal. Drop only the replay receipt and retain the
// removal intent/claim for explicit recovery.
func discardPendingStorageRetirement(config Config, job domain.Job, result journal) error {
	receipt, needed, err := storageRetirementFor(job, result, true)
	if err != nil || !needed {
		return err
	}
	path := filepath.Join(config.Root, "storage-removal-retirements", string(receipt.JobID)+".json")
	raw, err := security.ReadPrivate(path, 4096)
	var pending storageRetirement
	if err != nil || domain.Decode(raw, &pending) != nil || !pending.PendingReport || !reflect.DeepEqual(pending, receipt) {
		return workspace.ResultUncertain()
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return security.SyncParent(path)
}

func retireStorageReport(ctx context.Context, config Config, path string, receipt storageRetirement) error {
	if receipt.PendingReport || receipt.Version != 1 || receipt.JobID.Validate() != nil || receipt.ReportID.Validate() != nil || receipt.Revision == 0 {
		return workspace.ResultUncertain()
	}
	raw, err := security.ReadPrivate(filepath.Join(config.Root, "jobs", string(receipt.JobID)+".json"), 2<<20)
	var reported journal
	if err != nil || domain.Decode(raw, &reported) != nil || reported.Version != 1 || (reported.State != journalReported && reported.State != journalFinished) || storageJournalResultDigest(reported) != receipt.ResultDigest || reported.JobID != receipt.JobID || reported.ReportID != receipt.ReportID || reported.Revision != receipt.Revision || reported.Digest != receipt.Digest {
		return workspace.ResultUncertain()
	}
	if receipt.ProblemCode != "" {
		if reported.Problem == nil || reported.Problem.Code != receipt.ProblemCode || receipt.ProblemCode == domain.RecoveryRequired || len(reported.Output) != 0 {
			return workspace.ResultUncertain()
		}
	} else {
		if reported.Problem != nil {
			return workspace.ResultUncertain()
		}
		var output workspace.StorageResult
		if domain.Decode(reported.Output, &output) != nil || !output.CleanupVerified || output.OperationID != receipt.JobID || output.SessionID != receipt.Removal.SessionID || (output.Action != receipt.Removal.Action && (output.Action != workspace.StorageRecover || output.RecoveredJobID != receipt.Removal.OperationID)) {
			return workspace.ResultUncertain()
		}
		if output.Snapshot != nil {
			if output.Snapshot.ID != receipt.Removal.SnapshotID {
				return workspace.ResultUncertain()
			}
		} else if output.Action != workspace.StorageRecover || output.RecoveredJobState != domain.JobFailed || receipt.Removal.Action != workspace.StorageCleanup || output.WorkspaceState != domain.WorkspacePresent || output.RemovedSourceBytes != 0 {
			return workspace.ResultUncertain()
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if reported.State == journalFinished {
		// Only a validated terminal server acknowledgement persisted in the
		// receipt can complete this transition; no native work or report is replayed.
		reported.State = journalReported
		if err := writeJSON(filepath.Join(config.Root, "jobs", string(receipt.JobID)+".json"), reported); err != nil {
			return err
		}
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
		if receipt.PendingReport {
			continue
		}
		if err := retireStorageReport(ctx, config, path, receipt); err != nil {
			return err
		}
	}
	return nil
}

func validateRetriedStorageReport(receipt storageRetirement, result journal, credential Credential, ack *pb.Resource) error {
	if ack == nil || ack.Id != string(receipt.JobID) || ack.SessionId != string(receipt.Removal.SessionID) || ack.Kind != pb.EntityKind_ENTITY_KIND_JOB || ack.SchemaVersion != 1 || ack.Revision <= receipt.Revision {
		return workspace.ResultUncertain()
	}
	var accepted domain.Job
	if domain.Decode(ack.DocumentJson, &accepted) != nil || accepted.Type != domain.WorkspaceStorageJob || accepted.MachineID != credential.MachineID || accepted.InstanceID != receipt.InstanceID || accepted.Input == nil {
		return workspace.ResultUncertain()
	}
	inputDigest := sha256.Sum256(accepted.Input)
	if receipt.InputDigest == "" || hex.EncodeToString(inputDigest[:]) != receipt.InputDigest || accepted.Input == nil {
		return workspace.ResultUncertain()
	}
	if accepted.State == domain.JobUncertain {
		if accepted.Problem == nil || accepted.Problem.Code != domain.RecoveryRequired || len(accepted.Output) != 0 {
			return workspace.ResultUncertain()
		}
		return nil
	}
	if receipt.ProblemCode == "" {
		if result.Problem != nil || accepted.State != domain.JobSucceeded || accepted.Problem != nil || !bytes.Equal(accepted.Output, result.Output) {
			return workspace.ResultUncertain()
		}
	} else if result.Problem == nil || accepted.State != domain.JobFailed && accepted.State != domain.JobCanceled || accepted.Problem == nil || accepted.Problem.Code != receipt.ProblemCode || len(result.Output) != 0 || len(accepted.Output) != 0 {
		return workspace.ResultUncertain()
	}
	return nil
}

// replayPendingStorageReports closes the crash window between the server's
// report commit and the Worker's local reported-journal transition. The
// request ID and original instance are retained so a committed report can be
// replayed idempotently without redispatching native work.
func replayPendingStorageReports(ctx context.Context, config Config, client delidevv1connect.WorkerServiceClient, credential Credential) error {
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
		if err := ctx.Err(); err != nil {
			return err
		}
		// WriteAtomic leaves unpublished files under this private prefix when a
		// process dies after the temporary file is created. They are not report
		// receipts and must remain inert until the atomic publication completes.
		if strings.HasPrefix(name, ".pending-") {
			continue
		}
		path := filepath.Join(root, name)
		raw, err := security.ReadPrivate(path, 4096)
		var receipt storageRetirement
		if err != nil || domain.Decode(raw, &receipt) != nil || name != string(receipt.JobID)+".json" {
			return workspace.ResultUncertain()
		}
		if !receipt.PendingReport {
			continue
		}
		journalRaw, err := security.ReadPrivate(filepath.Join(config.Root, "jobs", string(receipt.JobID)+".json"), 2<<20)
		var result journal
		if err != nil || domain.Decode(journalRaw, &result) != nil || result.Version != 1 || result.State != journalFinished || result.JobID != receipt.JobID || result.ReportID != receipt.ReportID || result.InstanceID != receipt.InstanceID || result.Revision != receipt.Revision || result.Digest != receipt.Digest || storageJournalResultDigest(result) != receipt.ResultDigest {
			return workspace.ResultUncertain()
		}
		report := &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(receipt.ReportID), Id: string(receipt.JobID), ExpectedRevision: receipt.Revision}, MachineId: string(credential.MachineID), InstanceId: string(receipt.InstanceID), OutputJson: result.Output}
		if result.Problem != nil {
			report.Problem = &pb.ErrorDetail{Code: string(result.Problem.Code)}
		}
		attempt, cancel := context.WithTimeout(ctx, 30*time.Second)
		ack, reportErr := client.ReportWork(attempt, authenticated(credential, report))
		cancel()
		if reportErr != nil {
			config.Logger.WarnContext(ctx, "storage_report_replay_pending", "job_id", receipt.JobID, "code", domain.SafeError(reportErr).Code)
			continue
		}
		var acknowledged *pb.Resource
		if ack != nil {
			acknowledged = ack.Msg.Job
		}
		if err := validateRetriedStorageReport(receipt, result, credential, acknowledged); err != nil {
			config.Logger.WarnContext(ctx, "storage_report_replay_unverified", "job_id", receipt.JobID, "code", domain.SafeError(err).Code)
			continue
		}
		var accepted domain.Job
		var input workspace.StorageRequest
		if domain.Decode(acknowledged.DocumentJson, &accepted) != nil || domain.Decode(accepted.Input, &input) != nil {
			return workspace.ResultUncertain()
		}
		if accepted.State == domain.JobUncertain || input.Action == workspace.StorageRecover && (accepted.State == domain.JobFailed || accepted.State == domain.JobCanceled) {
			if err := discardPendingStorageRetirement(config, accepted, result); err != nil {
				return err
			}
			result.State = journalReported
			if err := writeJSON(filepath.Join(config.Root, "jobs", string(result.JobID)+".json"), result); err != nil {
				return err
			}
			continue
		}
		receipt.PendingReport = false
		if _, err := persistStorageRetirement(config, receipt); err != nil {
			return err
		}
		if err := retireStorageReport(ctx, config, path, receipt); err != nil {
			return err
		}
		config.Logger.InfoContext(ctx, "storage_report_replayed", "job_id", receipt.JobID, "report_id", receipt.ReportID)
	}
	return nil
}

func storageJournalResultDigest(j journal) string {
	raw, _ := json.Marshal(struct {
		Output  json.RawMessage
		Problem *domain.Error
	}{j.Output, j.Problem})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
