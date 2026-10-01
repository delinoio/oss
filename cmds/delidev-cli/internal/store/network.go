// SPDX-License-Identifier: Apache-2.0
package store

import (
	"database/sql"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Network selections use the existing revisioned entity/event/receipt schema.
// One serialized transaction owns each target; no historical layout changes.
func (t *Tx) NetworkRoute(machine domain.ID) (Record, error) {
	return scan(t.tx.QueryRowContext(t.ctx, "SELECT id,kind,revision,session_id,project_id,body,created_at,updated_at FROM entities WHERE kind=? AND COALESCE(json_extract(CAST(body AS TEXT),'$.machine_id'),'')=?", domain.NetworkRouteKind, machine))
}
func MissingNetworkRoute(err error) bool { return errors.Is(err, sql.ErrNoRows) }
func (t *Tx) NetworkProfileSelected(id domain.ID) (bool, error) {
	var count int
	err := t.tx.QueryRowContext(t.ctx, "SELECT COUNT(*) FROM entities WHERE kind=? AND json_extract(CAST(body AS TEXT),'$.profile_id')=?", domain.NetworkRouteKind, id).Scan(&count)
	return count != 0, storageError(err)
}
func (t *Tx) NetworkProfileCapacity() error {
	var count int
	if err := t.tx.QueryRowContext(t.ctx, "SELECT COUNT(*) FROM entities WHERE kind=?", domain.NetworkProfileKind).Scan(&count); err != nil {
		return storageError(err)
	}
	if count >= 128 {
		return domain.Fail(domain.ResourceExhausted, "The outbound profile limit was reached.", "Delete an unused profile before creating another; at most 128 are retained.")
	}
	return nil
}
