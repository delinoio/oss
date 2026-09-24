package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

// CompletedExecutionRef is derived from the original accepted assignment and
// already-published terminal progress. Its version-1 expected completion is a
// comparison target, not cleanup proof. No prompt, answer or credential is
// needed to inspect the original private Worker records after instance change.
type CompletedExecutionRef struct {
	ServerID           domain.ID
	DeviceID           domain.ID
	InstanceID         domain.ID
	AssignmentRevision uint64
	AssignmentDigest   string
	Checkpoint         ExecutionCheckpointRef
	Preparation        workspace.PrepareRequest
	Manifest           workspace.Manifest
}

// CompletedExecutionEvidence contains only the original result/receipt and
// content-free checkpoint binding. A coordinator must independently authorize
// recovery, match terminal progress and preserve pause; this is no new report
// authority for an old Worker instance and never authorizes input replay.
type CompletedExecutionEvidence struct {
	Version    uint32                     `json:"version"`
	JobID      domain.ID                  `json:"job_id"`
	ReportID   domain.ID                  `json:"report_id"`
	Completion domain.ExecutionCompletion `json:"completion"`
}

type completionInspectionStage string

const (
	inspectionJournal    completionInspectionStage = "journal-and-outbox"
	inspectionWorkspace  completionInspectionStage = "workspace"
	inspectionCheckpoint completionInspectionStage = "checkpoint"
	inspectionRecheck    completionInspectionStage = "recheck"
)

// InspectCompletedExecution joins the old operation journal, fully acknowledged
// event outbox, immutable native checkpoint and closed workspace/process owner.
// It neither reconstructs missing terminal evidence nor replays an RPC/native
// operation. The caller still needs a dedicated server recovery transaction.
func InspectCompletedExecution(ctx context.Context, manager *workspace.Manager, ref CompletedExecutionRef) (evidence CompletedExecutionEvidence, returned error) {
	if manager == nil || ref.Checkpoint.validate() != nil || ref.Checkpoint.Completion.Version != 1 || ref.AssignmentRevision == 0 || !canonicalDigest(ref.AssignmentDigest) || domain.UniqueIDs([]domain.ID{ref.ServerID, ref.DeviceID, ref.InstanceID}) != nil || ref.Preparation.SessionID != ref.Checkpoint.SessionID || ref.Preparation.MachineID != ref.Checkpoint.MachineID {
		return evidence, executionCheckpointUncertain()
	}
	logger := manager.Logger
	if logger == nil {
		logger = slog.Default()
	}
	stage := inspectionJournal
	defer func() {
		if returned != nil {
			logger.WarnContext(ctx, "completed_native_execution_inspection_failed", "session_id", ref.Checkpoint.SessionID, "job_id", ref.Checkpoint.JobID, "execution_id", ref.Checkpoint.Completion.ExecutionID, "stage", stage, "code", domain.SafeError(returned).Code)
		}
	}()
	bounded, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := bounded.Err(); err != nil {
		return evidence, domain.SafeError(err)
	}
	root := manager.Root
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || !filepath.IsAbs(root) || resolved != root {
		return evidence, executionCheckpointUncertain()
	}
	job := ref.Checkpoint.JobID
	directory := filepath.Join(root, "jobs", string(job))
	for _, path := range []string{root, filepath.Join(root, "jobs"), directory} {
		if security.CheckPrivateDir(path) != nil {
			return evidence, executionCheckpointUncertain()
		}
	}
	// Match normal launch's publisher-before-workspace lock order. A live old
	// publisher/lease is not a completed execution and cannot be displaced.
	lock, err := security.TryLock(filepath.Join(directory, "publication.lock"))
	if err != nil {
		return evidence, executionCheckpointUncertain()
	}
	defer func() {
		if err := lock.Close(); err != nil {
			evidence, returned = CompletedExecutionEvidence{}, executionCheckpointUncertain()
		}
	}()
	prior, completion, err := readCompletedExecution(root, ref)
	if err != nil {
		return evidence, err
	}
	stage = inspectionWorkspace
	inspection, err := manager.InspectClosedExecution(bounded, workspace.ExecutionPredecessor{JobID: job, ExecutionID: completion.ExecutionID}, ref.Preparation, ref.Manifest)
	if err != nil {
		return evidence, err
	}
	defer func() {
		if err := inspection.Close(); err != nil {
			evidence, returned = CompletedExecutionEvidence{}, executionCheckpointUncertain()
		}
	}()
	stage = inspectionCheckpoint
	checkpointRef := ref.Checkpoint
	checkpointRef.Completion = completion
	checkpoint, err := ReadCodexExecutionCheckpoint(root, checkpointRef)
	if err != nil || filepath.Clean(checkpoint.Native.Effective.Cwd) != filepath.Clean(inspection.WorkingDirectory()) {
		return evidence, executionCheckpointUncertain()
	}
	// Reported may advance after Finished without changing original result or
	// receipt. Any other journal/outbox change invalidates this observation.
	stage = inspectionRecheck
	fresh, current, err := readCompletedExecution(root, ref)
	if err != nil || fresh.ReportID != prior.ReportID || current != completion {
		return evidence, executionCheckpointUncertain()
	}
	if err := bounded.Err(); err != nil {
		return evidence, domain.SafeError(err)
	}
	manager.Logger.InfoContext(bounded, "completed_native_execution_inspected", "session_id", ref.Checkpoint.SessionID, "job_id", job, "execution_id", completion.ExecutionID, "outcome", completion.Outcome)
	return CompletedExecutionEvidence{Version: 1, JobID: job, ReportID: prior.ReportID, Completion: completion}, nil
}

func readCompletedExecution(root string, ref CompletedExecutionRef) (journal, domain.ExecutionCompletion, error) {
	job := ref.Checkpoint.JobID
	raw, err := security.ReadPrivate(filepath.Join(root, "jobs", string(job)+".json"), 2<<20)
	var prior journal
	var completion domain.ExecutionCompletion
	if err != nil || domain.Decode(raw, &prior) != nil || prior.Version != 1 || prior.JobID != job || prior.InstanceID != ref.InstanceID || prior.Revision != ref.AssignmentRevision || prior.Digest != ref.AssignmentDigest || (prior.State != journalFinished && prior.State != journalReported) || prior.Problem != nil || prior.ReportID.Validate() != nil || domain.Decode(prior.Output, &completion) != nil || completion.Version != 2 || completion.Validate() != nil {
		return prior, completion, executionCheckpointUncertain()
	}
	terminal := completion
	terminal.Version, terminal.NativeCheckpointDigest = 1, ""
	if terminal != ref.Checkpoint.Completion {
		return prior, completion, executionCheckpointUncertain()
	}
	raw, err = security.ReadPrivate(filepath.Join(root, "jobs", string(job), "publication.json"), 1<<20)
	var publication publicationJournal
	if err != nil || domain.Decode(raw, &publication) != nil || publication.Version != 1 || publication.JobID != job || publication.InstanceID != ref.InstanceID || publication.ServerID != ref.ServerID || publication.DeviceID != ref.DeviceID || publication.Revision != ref.AssignmentRevision || publication.AssignmentDigest != ref.AssignmentDigest || publication.Pending != nil || publication.LastSequence != completion.LastSequence {
		return prior, completion, executionCheckpointUncertain()
	}
	return prior, completion, nil
}

func canonicalDigest(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == sha256.Size && hex.EncodeToString(raw) == value
}
