// SPDX-License-Identifier: Apache-2.0
package workspace

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// A published checkout survives registration failure. Both its metadata and the
// closed failure code are retained in the original job, never retried as a clone.
type CloneResult struct {
	Inspection         *Inspection   `json:"inspection,omitempty"`
	Problem            *domain.Error `json:"problem,omitempty"`
	RepositoryID       domain.ID     `json:"repository_id,omitempty"`
	RepositoryRevision uint64        `json:"repository_revision,omitempty"`
}
