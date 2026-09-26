package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// Use the indexed session message set with a hard bound. Original progress event
// identities are not native message identities and cannot occupy that index.
func (t *Tx) CheckOpenCodeProgressEvent(session, execution domain.ID, event string) error {
	if session.Validate() != nil || execution.Validate() != nil || domain.NativeIdentity(event).Validate(domain.OpenCode, domain.NativeEventIdentity) != nil {
		return domain.Fail(domain.InvalidArgument, "Invalid native progress ownership.", "Retain the original session and event identity.")
	}
	var count int
	if err := t.tx.QueryRowContext(t.ctx, `SELECT COUNT(*) FROM (SELECT id FROM entities WHERE kind=? AND session_id=? LIMIT 100001)`, domain.MessageKind, session).Scan(&count); err != nil {
		return storageError(err)
	}
	if count >= 100000 {
		return domain.Fail(domain.ResourceExhausted, "Native progress retention reached its session bound.", "Retain original evidence without truncation.")
	}
	var exists bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM entities WHERE kind=? AND session_id=? AND json_extract(body,'$.execution_id')=? AND (json_extract(body,'$.progress.todo.native_event_id')=? OR json_extract(body,'$.progress.changes.native_event_id')=?))`, domain.MessageKind, session, execution, event, event).Scan(&exists)
	if err != nil {
		return storageError(err)
	}
	if exists {
		return domain.Fail(domain.Conflict, "This native progress event is already retained.", "Reconcile the original publication receipt.")
	}
	return nil
}
