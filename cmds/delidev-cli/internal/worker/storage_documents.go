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
	return workspace.DecodeStorageJob(raw, target)
}
