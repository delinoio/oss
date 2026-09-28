package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func recoverPRStartup(ctx context.Context, config Config, request domain.ExecutionRecoveryRequest) (json.RawMessage, error) {
	if request.Startup == nil || request.Validate() != nil {
		return nil, domain.StartupRejectionUncertain()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var preparation workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(request.Preparation, &preparation) != nil || domain.Decode(request.Manifest, &manifest) != nil || preparation.SessionID != request.SessionID || preparation.MachineID != request.MachineID {
		return nil, domain.StartupRejectionUncertain()
	}
	path := filepath.Join(config.Root, "jobs", string(request.JobID)+".json")
	raw, err := security.ReadPrivate(path, 2<<20)
	var original journal
	if err != nil || domain.Decode(raw, &original) != nil || original.Version != 1 || original.JobID != request.JobID || original.InstanceID != request.InstanceID || original.Revision != request.AssignmentRevision || original.Digest != request.AssignmentDigest || original.ReportID.Validate() != nil {
		return nil, domain.StartupRejectionUncertain()
	}
	switch original.State {
	case journalStarted:
		if original.Problem != nil || len(original.Output) != 0 {
			return nil, domain.StartupRejectionUncertain()
		}
	case journalFinished, journalReported:
		// A retry may already have reported original startup uncertainty. That
		// error is not proof; the independent positive phase remains mandatory.
		if original.Problem != nil && (original.Problem.Code != domain.RecoveryRequired || len(original.Output) != 0) || original.Problem == nil && len(original.Output) == 0 {
			return nil, domain.StartupRejectionUncertain()
		}
	default:
		return nil, domain.StartupRejectionUncertain()
	}
	if _, err := os.Lstat(filepath.Join(config.Root, "jobs", string(request.JobID))); !errors.Is(err, os.ErrNotExist) {
		return nil, domain.StartupRejectionUncertain()
	}
	manager := workspace.Manager{Root: config.Root, Logger: config.Logger}
	proof, err := manager.ReadPRStartupRejection(ctx, request.JobID, request.Startup.ExecutionID, preparation, manifest, runtime.GOOS)
	if err != nil {
		return nil, err
	}
	rejected := domain.ExecutionStartupRejection{Version: 1, Type: domain.PRStartupRejectedResult, ServerID: request.ServerID, DeviceID: request.DeviceID, InstanceID: request.InstanceID, MachineID: request.MachineID, InputID: request.Startup.InputID, AccountID: request.AccountID, ConnectionID: request.ConnectionID, AssignmentRevision: request.AssignmentRevision, AssignmentDigest: request.AssignmentDigest, AssignmentInputDigest: request.AssignmentInputDigest, ConfigurationDigest: request.ConfigurationDigest, Workspace: proof}
	if len(original.Output) != 0 {
		var retained domain.ExecutionStartupRejection
		if domain.Decode(original.Output, &retained) != nil || retained != rejected {
			return nil, domain.StartupRejectionUncertain()
		}
	}
	current, err := security.ReadPrivate(path, 2<<20)
	if err != nil || !bytes.Equal(raw, current) || ctx.Err() != nil {
		return nil, domain.StartupRejectionUncertain()
	}
	evidence := domain.PRStartupRecoveryEvidence{Version: 1, JobID: request.JobID, ReportID: original.ReportID, Rejection: rejected}
	if err := evidence.Validate(request); err != nil {
		return nil, err
	}
	config.Logger.InfoContext(ctx, "pr_startup_recovery_verified", "session_id", request.SessionID, "job_id", request.JobID, "execution_id", request.Startup.ExecutionID)
	return json.Marshal(evidence)
}
