// SPDX-License-Identifier: Apache-2.0
package workspace

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

func ExecutionUsesNamedManagedDirectories(input domain.ExecutionJobInput) (bool, error) {
	var preparation PrepareRequest
	if err := domain.Decode(input.Preparation, &preparation); err != nil {
		return false, err
	}
	return preparation.UsesNamedManagedDirectories(), nil
}

// Capability admission follows each typed original envelope. A job cannot
// remove a naming field from its embedded preparation to acquire old semantics.
func JobUsesNamedManagedDirectories(job domain.Job) (bool, error) {
	switch job.Type {
	case domain.PrepareWorkspaceJob:
		var input PrepareRequest
		if err := domain.Decode(job.Input, &input); err != nil {
			return false, err
		}
		return input.UsesNamedManagedDirectories(), nil
	case domain.RecoverWorkspaceJob:
		var input RecoveryRequest
		if err := domain.Decode(job.Input, &input); err != nil {
			return false, err
		}
		return input.Preparation.UsesNamedManagedDirectories(), nil
	case domain.WorkspaceStorageJob:
		var input StorageRequest
		if err := DecodeStorageRequest(job.Input, &input); err != nil {
			return false, err
		}
		return input.Preparation.UsesNamedManagedDirectories(), nil
	case domain.ExecuteSessionJob:
		var input domain.ExecutionJobInput
		if err := domain.Decode(job.Input, &input); err != nil {
			return false, err
		}
		return ExecutionUsesNamedManagedDirectories(input)
	case domain.ForkSessionJob:
		var input domain.ForkJobInput
		if err := domain.Decode(job.Input, &input); err != nil {
			return false, err
		}
		return ExecutionUsesNamedManagedDirectories(input.SourceAssignment)
	case domain.CompactSessionJob:
		var input domain.SessionCompactionInput
		if err := domain.DecodeCompactionInput(job.Input, &input); err != nil {
			return false, err
		}
		return ExecutionUsesNamedManagedDirectories(input.Assignment)
	case domain.RecoverExecutionJob:
		var input domain.ExecutionRecoveryRequest
		if err := domain.Decode(job.Input, &input); err != nil {
			return false, err
		}
		var preparation PrepareRequest
		if err := domain.Decode(input.Preparation, &preparation); err != nil {
			return false, err
		}
		return preparation.UsesNamedManagedDirectories(), nil
	default:
		return false, nil
	}
}
