package worker

import (
	"context"
	"encoding/hex"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func recoverExecution(ctx context.Context, config Config, job domain.Job) (json.RawMessage, error) {
	var request domain.ExecutionRecoveryRequest
	if domain.Decode(job.Input, &request) != nil || request.Validate() != nil || request.JobID != job.ParentID || request.MachineID != job.MachineID {
		return nil, domain.ExecutionRecoveryUncertain()
	}
	credential, err := LoadCredential(config.Root)
	if err != nil || credential.Type != domain.WorkerDevice || credential.ServerID != request.ServerID || credential.MachineID != request.MachineID || credential.DeviceID != request.DeviceID {
		return nil, domain.ExecutionRecoveryUncertain()
	}
	var preparation workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(request.Preparation, &preparation) != nil || domain.Decode(request.Manifest, &manifest) != nil || preparation.SessionID != request.SessionID || preparation.MachineID != request.MachineID {
		return nil, domain.ExecutionRecoveryUncertain()
	}
	digest, _ := hex.DecodeString(request.PromptDigest)
	var promptDigest [32]byte
	copy(promptDigest[:], digest)
	manager := &workspace.Manager{Root: config.Root, Logger: config.Logger}
	evidence, err := InspectCompletedExecution(ctx, manager, CompletedExecutionRef{
		ServerID: request.ServerID, DeviceID: request.DeviceID, InstanceID: request.InstanceID, AssignmentRevision: request.AssignmentRevision, AssignmentDigest: request.AssignmentDigest,
		Checkpoint:  ExecutionCheckpointRef{JobID: request.JobID, SessionID: request.SessionID, MachineID: request.MachineID, HistoryExecutionID: request.HistoryExecutionID, AssignmentInputDigest: request.AssignmentInputDigest, ConfigurationDigest: request.ConfigurationDigest, AccountID: request.AccountID, ConnectionID: request.ConnectionID, Completion: request.Completion, InputMode: request.InputMode, PromptDigest: promptDigest, AcceptedInputs: request.AcceptedInputs},
		Preparation: preparation, Manifest: manifest,
	})
	if err != nil {
		return nil, err
	}
	if err := evidence.Validate(request); err != nil {
		return nil, err
	}
	return json.Marshal(evidence)
}
