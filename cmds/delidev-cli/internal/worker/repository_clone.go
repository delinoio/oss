// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func executeRepositoryClone(ctx context.Context, config Config, owner domain.ID, job domain.Job) (json.RawMessage, error) {
	if !config.repositoryClone {
		return nil, domain.Fail(domain.Unsupported, "Repository clone was not negotiated.", "Update and reconnect this computer's Worker.")
	}
	var input domain.RepositoryCloneInput
	if err := domain.Decode(job.Input, &input); err != nil {
		return nil, err
	}
	if err := input.Validate(); err != nil {
		return nil, err
	}
	if domain.OwnershipBlocks(domain.OwnershipMachine, domain.ID(owner), input.MachineID != job.MachineID) {
		return nil, domain.Fail(domain.PermissionDenied, "Clone targets another computer.", "Inspect the original assignment.")
	}
	git := workspace.Git{HooksDir: filepath.Join(config.Root, "empty-hooks"), ProcessRoot: filepath.Join(config.Root, "processes"), OwnerID: owner, Logger: config.Logger}
	inspection, err := git.Clone(ctx, config.Root, workspace.CloneRequest{JobID: owner, ParentPath: input.ParentPath, URL: input.URL, DirectoryName: input.DirectoryName})
	result := workspace.CloneResult{}
	if inspection.Root != "" {
		result.Inspection = &inspection
	}
	if err != nil {
		result.Problem = domain.SafeError(err)
	}
	// Generic failed-job reports cannot contain output. This dedicated envelope
	// keeps the published path with a failed clone without changing other jobs.
	return json.Marshal(result)
}
