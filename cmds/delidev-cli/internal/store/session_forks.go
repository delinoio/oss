// SPDX-License-Identifier: Apache-2.0
package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// A durable fork job reserves only its original source boundary. Uncertainty
// keeps that reservation; another request cannot bypass native ownership loss.
func (t *Tx) RequireNoSessionFork(session domain.ID) error {
	var exists bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM entities WHERE kind='job' AND session_id=? AND json_extract(body,'$.type')='fork-session' AND json_extract(body,'$.state') IN ('queued','claimed','uncertain'))`, session).Scan(&exists)
	if err != nil {
		return storageError(err)
	}
	if exists {
		return domain.Fail(domain.Conflict, "This session has an unfinished native fork.", "Observe the original fork job before advancing its source boundary.")
	}
	return nil
}

func (t *Tx) RequireForkActor(actor domain.Principal) error {
	if !actor.ValidMetadata() {
		return domain.Fail(domain.PermissionDenied, "Server authentication is required.", "Use the server token or a registered device credential.")
	}
	return t.Authorize()
}
