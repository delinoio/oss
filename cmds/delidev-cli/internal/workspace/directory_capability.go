// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// JobRequiresNamedDirectories inspects only typed original workspace operands.
// Prompt/native content can never grant or suppress a negotiated capability.
func JobRequiresNamedDirectories(job domain.Job) (bool, error) {
	var raw json.RawMessage
	switch job.Type {
	case domain.PrepareWorkspaceJob:
		raw = job.Input
	case domain.ExecuteSessionJob:
		var input domain.ExecutionJobInput
		if err := domain.Decode(job.Input, &input); err != nil {
			return false, err
		}
		raw = input.Preparation
	case domain.RecoverExecutionJob:
		var input domain.ExecutionRecoveryRequest
		if err := domain.Decode(job.Input, &input); err != nil {
			return false, err
		}
		raw = input.Preparation
	case domain.RecoverWorkspaceJob:
		var input RecoveryRequest
		if err := domain.Decode(job.Input, &input); err != nil {
			return false, err
		}
		return RequiresNamedDirectories(input.Preparation), nil
	case domain.WorkspaceStorageJob:
		var input StorageRequest
		if err := DecodeStorageRequest(job.Input, &input); err != nil {
			return false, err
		}
		return RequiresNamedDirectories(input.Preparation), nil
	case domain.ForkSessionJob:
		var input domain.ForkJobInput
		if err := domain.Decode(job.Input, &input); err != nil {
			return false, err
		}
		raw = input.SourceAssignment.Preparation
	default:
		return false, nil
	}
	var preparation PrepareRequest
	if err := domain.Decode(raw, &preparation); err != nil {
		return false, err
	}
	return RequiresNamedDirectories(preparation), nil
}
