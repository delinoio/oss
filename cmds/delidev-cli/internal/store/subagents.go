// SPDX-License-Identifier: Apache-2.0
package store

import (
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Generic resources need no schema migration. This read joins original native
// identities before publication, including records from a previous execution.
func (t *Tx) SubagentIdentitiesOwned(session, execution domain.ID, batch []domain.SubagentObservation) (bool, error) {
	if len(batch) == 0 || len(batch) > 128 {
		return false, nil
	}
	ids := make(map[string]domain.ID, len(batch))
	args := []any{domain.SubagentKind, session}
	for _, child := range batch {
		ids[child.NativeID] = child.ID
		args = append(args, child.NativeID)
	}
	// Parse the session's large JSON records once per complete batch, rather
	// than rescanning their source coverage for every individual child.
	query := `SELECT id,json_extract(body,'$.execution_id'),json_extract(body,'$.observation.native_id') FROM entities WHERE kind=? AND session_id=? AND json_extract(body,'$.observation.native_id') IN (` + strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",") + `)`
	rows, err := t.tx.QueryContext(t.ctx, query, args...)
	if err != nil {
		return false, storageError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, owner domain.ID
		var native string
		if err := rows.Scan(&id, &owner, &native); err != nil {
			return false, storageError(err)
		}
		if ids[native] != id || owner != execution {
			return false, nil
		}
	}
	return true, storageError(rows.Err())
}
