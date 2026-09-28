package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// Resolve only the retained original part under the existing execution index.
// Reported call IDs and paths cannot select an unrelated transcript record.
func (t *Tx) OpenCodeInteractionTool(session, execution domain.ID, thread, turn, part string) (domain.ExecutionMessage, error) {
	return t.OpenCodeExecutionPart(session, execution, thread, turn, part)
}

func (t *Tx) OpenCodeExecutionPart(session, execution domain.ID, thread, turn, part string) (domain.ExecutionMessage, error) {
	if session.Validate() != nil || execution.Validate() != nil || domain.NativeIdentity(thread).Validate(domain.OpenCode, domain.NativeThreadIdentity) != nil || domain.NativeIdentity(turn).Validate(domain.OpenCode, domain.NativeTurnIdentity) != nil || domain.NativeIdentity(part).Validate(domain.OpenCode, domain.NativePartIdentity) != nil {
		return domain.ExecutionMessage{}, domain.Fail(domain.InvalidArgument, "Invalid native interaction tool scope.", "Retain the original session, input and tool part.")
	}
	var id domain.ID
	err := t.tx.QueryRowContext(t.ctx, `SELECT message_id FROM execution_messages WHERE session_id=? AND execution_id=? AND native_thread_id=? AND native_turn_id=? AND native_item_id=?`, session, execution, thread, turn, part).Scan(&id)
	if err != nil {
		return domain.ExecutionMessage{}, storageError(err)
	}
	r, err := t.Get(domain.MessageKind, id)
	if err != nil {
		return domain.ExecutionMessage{}, err
	}
	return Decode[domain.ExecutionMessage](r)
}

func (t *Tx) HasOpenCodeInteractionEvent(session, execution domain.ID, event string) (bool, error) {
	if session.Validate() != nil || execution.Validate() != nil || domain.NativeIdentity(event).Validate(domain.OpenCode, domain.NativeEventIdentity) != nil {
		return false, domain.Fail(domain.InvalidArgument, "Invalid native interaction event.", "Retain its original identity.")
	}
	var exists bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM execution_interactions i JOIN entities e ON e.id=i.interaction_id WHERE i.execution_id=? AND e.session_id=? AND (json_extract(e.body,'$.opencode.native_event_id')=? OR json_extract(e.body,'$.response.acceptance.opencode.native_event_id')=? OR json_extract(e.body,'$.approval_response.acceptance.opencode.native_event_id')=? OR json_extract(e.body,'$.opencode_closure.native_event_id')=?))`, execution, session, event, event, event, event).Scan(&exists)
	return exists, storageError(err)
}
