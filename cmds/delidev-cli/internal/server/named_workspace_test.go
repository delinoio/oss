// SPDX-License-Identifier: Apache-2.0
package server

import (
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func TestNamedWorkspaceAssignmentRejectsDowngradedWorkerAndPreservesLegacy(t *testing.T) {
	legacy := workspace.PrepareRequest{Repositories: []workspace.RepositorySpec{{ID: domain.NewID()}}}
	raw, _ := json.Marshal(legacy)
	job := domain.Job{Type: domain.PrepareWorkspaceJob, Input: raw}
	machine := domain.Machine{WorkerCapabilities: []domain.WorkerCapability{domain.RemoteWorkspaceCloneV1}}
	if err := requireNamedWorkspaceJob(machine, job); err != nil {
		t.Fatal("legacy preparation rejected", err)
	}
	legacy.Repositories[0].DirectoryName = "oss"
	job.Input, _ = json.Marshal(legacy)
	if err := requireNamedWorkspaceJob(machine, job); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("downgraded Worker accepted named request", err)
	}
	machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.NamedWorkspaceDirectoriesV1)
	if err := requireNamedWorkspaceJob(machine, job); err != nil {
		t.Fatal("negotiated Worker rejected", err)
	}
}
