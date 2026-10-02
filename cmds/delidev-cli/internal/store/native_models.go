// SPDX-License-Identifier: Apache-2.0
package store

import (
	"database/sql"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strconv"
)

// Existing machine/parent/state job indexes retain immutable observations;
// this feature adds no tables or executable migration.
func (t *Tx) LastNativeModelSuccess(scope domain.NativeModelScope) (Record, error) {
	row := t.tx.QueryRowContext(t.ctx, `SELECT e.id,e.kind,e.revision,e.session_id,e.project_id,e.body,e.created_at,e.updated_at
FROM jobs j JOIN entities e ON e.id=j.id
WHERE j.machine_id=? AND j.parent_id=? AND j.state='succeeded'
AND json_extract(e.body,'$.type')=?
AND json_extract(e.body,'$.input.installation_generation')=?
AND json_extract(e.body,'$.input.executable_sha256')=?
AND json_extract(e.body,'$.input.connection_id')=?
AND json_extract(e.body,'$.input.include_hidden')=?
ORDER BY j.id DESC LIMIT 1`, scope.MachineID, scope.AccountID, domain.NativeModelsJob, strconv.FormatUint(scope.InstallationGeneration, 10), scope.ExecutableSHA256, scope.ConnectionID, scope.IncludeHidden)
	record, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, nil
	}
	return record, storageError(err)
}
