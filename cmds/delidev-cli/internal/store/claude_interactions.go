package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

func (t *Tx) HasClaudeInteractionConflict(session, execution, arrival domain.ID, call string) (bool, error) {
	if session.Validate() != nil || execution.Validate() != nil || arrival.Validate() != nil || domain.Text(call, "native tool call", 1024, true) != nil {
		return false, domain.Fail(domain.InvalidArgument, "Invalid original Claude callback scope.", "Preserve original execution and arrival identity.")
	}
	var exists bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM execution_interactions i JOIN entities e ON e.id=i.interaction_id WHERE i.execution_id=? AND e.session_id=? AND (json_extract(e.body,'$.claude.arrival_id')=? OR (json_type(e.body,'$.claude')='object' AND json_extract(e.body,'$.closure')='open' AND json_extract(e.body,'$.native_item_id')=?)))`, execution, session, arrival, call).Scan(&exists)
	return exists, storageError(err)
}
