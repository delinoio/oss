// SPDX-License-Identifier: Apache-2.0
package server

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func requireNamedPreparation(machine domain.Machine, raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var preparation workspace.PrepareRequest
	if domain.Decode(raw, &preparation) != nil {
		return workspace.ResultUncertain()
	}
	if preparation.RequiresNamedDirectories() && !slices.Contains(machine.WorkerCapabilities, domain.NamedWorkspaceDirectoriesV1) {
		return workspace.NamedDirectoriesUnsupported()
	}
	return nil
}

// Recheck queued jobs at assignment, including a Worker downgraded after admission.
func requireNamedWorkspaceJob(machine domain.Machine, job domain.Job) error {
	switch job.Type {
	case domain.ExecuteSessionJob:
		var input domain.ExecutionJobInput
		if domain.Decode(job.Input, &input) != nil {
			return workspace.ResultUncertain()
		}
		return requireNamedPreparation(machine, input.Preparation)
	case domain.RecoverExecutionJob:
		var input domain.ExecutionRecoveryRequest
		if domain.Decode(job.Input, &input) != nil {
			return workspace.ResultUncertain()
		}
		if input.Revert != nil {
			return requireNamedPreparation(machine, input.Revert.Input.Assignment.Preparation)
		}
		return requireNamedPreparation(machine, input.Preparation)
	case domain.CompactSessionJob:
		var input domain.SessionCompactionInput
		if domain.Decode(job.Input, &input) != nil {
			return workspace.ResultUncertain()
		}
		return requireNamedPreparation(machine, input.Assignment.Preparation)
	case domain.PrepareWorkspaceJob:
		return requireNamedPreparation(machine, job.Input)
	case domain.WorkspaceStorageJob:
		var input workspace.StorageRequest
		if workspace.DecodeStorageRequest(job.Input, &input) != nil {
			return workspace.ResultUncertain()
		}
		raw, err := json.Marshal(input.Preparation)
		if err != nil {
			return err
		}
		return requireNamedPreparation(machine, raw)
	case domain.RecoverWorkspaceJob:
		var input workspace.RecoveryRequest
		if domain.Decode(job.Input, &input) != nil {
			return workspace.ResultUncertain()
		}
		raw, err := json.Marshal(input.Preparation)
		if err != nil {
			return err
		}
		return requireNamedPreparation(machine, raw)
	}
	return nil
}
