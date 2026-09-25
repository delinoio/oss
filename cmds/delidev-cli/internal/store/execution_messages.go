package store

import (
	"database/sql"
	"errors"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const executionMessageSchema = `
CREATE TABLE execution_messages (
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 execution_id TEXT NOT NULL,
 native_thread_id TEXT NOT NULL, native_turn_id TEXT NOT NULL, native_item_id TEXT NOT NULL,
 message_id TEXT NOT NULL UNIQUE REFERENCES entities(id) ON DELETE CASCADE,
 state TEXT NOT NULL CHECK(state IN ('streaming','complete')),
 PRIMARY KEY(session_id,native_thread_id,native_turn_id,native_item_id)
);
CREATE INDEX execution_message_count ON execution_messages(execution_id,state);
PRAGMA user_version=8;
`

// BindExecutionMessage prevents a replacement product ID from duplicating one
// native item. Message/state/event/receipt publication shares its transaction.
func (t *Tx) BindExecutionMessage(session, execution, message domain.ID, thread, turn, item string, state domain.MessageState) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	for _, id := range []domain.ID{session, execution, message} {
		if err := id.Validate(); err != nil {
			return err
		}
	}
	for _, id := range []string{thread, turn, item} {
		if err := domain.Text(id, "native message identity", 1024, true); err != nil {
			return err
		}
	}
	if state != domain.MessageStreaming && state != domain.MessageComplete {
		return domain.Fail(domain.InvalidArgument, "Unknown message state.", "Publish a supported native message lifecycle.")
	}
	var retained, owner domain.ID
	err := t.tx.QueryRowContext(t.ctx, "SELECT message_id,execution_id FROM execution_messages WHERE session_id=? AND native_thread_id=? AND native_turn_id=? AND native_item_id=?", session, thread, turn, item).Scan(&retained, &owner)
	if err == nil {
		if retained != message || owner != execution {
			return domain.Fail(domain.Conflict, "The native message already has another retained identity.", "Reconcile its original product message instead of duplicating it.")
		}
		_, err = t.tx.ExecContext(t.ctx, "UPDATE execution_messages SET state=? WHERE message_id=?", state, message)
		return storageError(err)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return storageError(err)
	}
	var count int
	if err := t.tx.QueryRowContext(t.ctx, "SELECT COUNT(*) FROM execution_messages WHERE execution_id=?", execution).Scan(&count); err != nil {
		return storageError(err)
	}
	if count >= 10000 {
		return domain.Fail(domain.ResourceExhausted, "Execution message retention reached its bound.", "Pause and reconcile the retained transcript; no message was truncated.")
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO execution_messages(session_id,execution_id,native_thread_id,native_turn_id,native_item_id,message_id,state) VALUES(?,?,?,?,?,?,?)", session, execution, thread, turn, item, message, state)
	return storageError(err)
}

func (t *Tx) ExecutionMessagesComplete(execution domain.ID) (bool, error) {
	if err := execution.Validate(); err != nil {
		return false, err
	}
	var pending bool
	err := t.tx.QueryRowContext(t.ctx, "SELECT EXISTS(SELECT 1 FROM execution_messages WHERE execution_id=? AND state='streaming')", execution).Scan(&pending)
	return !pending, storageError(err)
}
