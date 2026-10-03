// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// The bounded pending projection excludes terminal history, so old completed
// setups cannot starve a newer operation. It carries no protected content.
func (s *Store) PendingSSHInstallations(ctx context.Context) ([]Record, error) {
	var result []Record
	err := s.Read(ctx, func(tx *Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		rows, err := tx.tx.QueryContext(ctx, "SELECT id,kind,revision,session_id,project_id,body,created_at,updated_at FROM entities WHERE kind=? AND (json_extract(CAST(body AS TEXT),'$.state') IN ('REQUESTED','RUNNING') OR json_extract(CAST(body AS TEXT),'$.reconcile_requested')=1 OR (json_extract(CAST(body AS TEXT),'$.cancellation_requested')=1 AND json_extract(CAST(body AS TEXT),'$.credential_removed')=0 AND json_extract(CAST(body AS TEXT),'$.state') IN ('CANCELED','SUCCEEDED','FAILED'))) ORDER BY id LIMIT 16", domain.SSHSetupKind)
		if err != nil {
			return storageError(err)
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scan(rows)
			if err != nil {
				return err
			}
			result = append(result, r)
		}
		return storageError(rows.Err())
	})
	return result, err
}
