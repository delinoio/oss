// SPDX-License-Identifier: Apache-2.0
package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// Retired Activity records remain only for original dependent deletion and backup restoration.
// Remove all activity owned by a deleted session, including the reservation
// recorded before its original attempt was bound. Shared PR evidence itself is
// session-independent and remains retained for other associations.
func (t *Tx) deleteSessionPRActivity(session domain.ID) error {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT id,revision FROM entities WHERE kind='problem' AND json_extract(body,'$.type') IN ('pull-request-activity','pull-request-handling-verification') AND (session_id=? OR json_extract(body,'$.source_id') IN (SELECT id FROM entities WHERE kind='problem' AND json_extract(body,'$.type')='pull-request-remediation-attempt' AND json_extract(body,'$.session_id')=?))`, session, session)
	if err != nil {
		return storageError(err)
	}
	type ref struct {
		id       domain.ID
		revision uint64
	}
	var refs []ref
	for rows.Next() {
		var r ref
		if err = rows.Scan(&r.id, &r.revision); err != nil {
			break
		}
		refs = append(refs, r)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return storageError(err)
	}
	for _, r := range refs {
		if err := t.Delete(domain.ProblemKind, r.id, r.revision); err != nil {
			return err
		}
	}
	return nil
}
