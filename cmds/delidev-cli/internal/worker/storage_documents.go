// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func decodeAssignedJob(raw []byte, target *domain.Job) error {
	if len(raw) <= 1<<20 {
		return domain.Decode(raw, target)
	}
	if domain.DecodeRepositoryBranchesJob(raw, target) == nil {
		return nil
	}
	if err := domain.DecodeCompactionJob(raw, target); err == nil {
		return nil
	}
	if domain.DecodeWithLimit(raw, target, domain.MaxCompactionJobBytes) == nil && target.Type == domain.ChangeSessionDirectoryJob && target.Validate() == nil {
		var input domain.SessionDirectoryInput
		if domain.DecodeWithLimit(target.Input, &input, domain.MaxCompactionInputBytes) == nil && input.Validate() == nil {
			return nil
		}
	}
	return workspace.DecodeStorageJob(raw, target)
}
