// SPDX-License-Identifier: Apache-2.0
package worker

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

func decodeNativeAssignment(raw []byte, target *domain.Job) error {
	return decodeAssignedJob(raw, target)
}
