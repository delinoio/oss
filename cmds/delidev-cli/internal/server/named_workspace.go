// SPDX-License-Identifier: Apache-2.0
package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	"slices"
)

func requireNamedWorkspaceCapability(machine domain.Machine, input workspace.PrepareRequest) error {
	if workspace.RequiresNamedDirectories(input) && !slices.Contains(machine.WorkerCapabilities, domain.NamedWorkspaceDirectoriesV1) {
		return workspace.NamedDirectoriesUnsupported()
	}
	return nil
}
func requireNamedJobCapability(machine domain.Machine, job domain.Job) error {
	named, err := workspace.JobRequiresNamedDirectories(job)
	if err != nil {
		return err
	}
	if named && !slices.Contains(machine.WorkerCapabilities, domain.NamedWorkspaceDirectoriesV1) {
		return workspace.NamedDirectoriesUnsupported()
	}
	return nil
}

func requireNamedForkCapability(tx *store.Tx, input domain.ForkJobInput) error {
	var preparation workspace.PrepareRequest
	if err := domain.Decode(input.SourceAssignment.Preparation, &preparation); err != nil {
		return err
	}
	if !workspace.RequiresNamedDirectories(preparation) {
		return nil
	}
	_, machine, err := activeMachine(tx, input.SourceAssignment.MachineID)
	if err != nil {
		return err
	}
	return requireNamedWorkspaceCapability(machine, preparation)
}
