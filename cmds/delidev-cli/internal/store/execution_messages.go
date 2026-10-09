package store

import (
	"database/sql"
	"errors"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

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

// The execution index bounds this lookup; closed first-text history must match
// its one original assistant record, not merely the most recent chunk event.
func (t *Tx) GrokFirstTextMessage(execution domain.ID) (Record, error) {
	if err := execution.Validate(); err != nil {
		return Record{}, err
	}
	var id domain.ID
	err := t.tx.QueryRowContext(t.ctx, `SELECT e.id FROM execution_messages m JOIN entities e ON e.id=m.message_id
 WHERE m.execution_id=? AND json_extract(e.body,'$.grok_text.response_ordinal')=1 ORDER BY e.id LIMIT 1`, execution).Scan(&id)
	if err != nil {
		return Record{}, storageError(err)
	}
	return t.Get(domain.MessageKind, id)
}

// Original completed root content may include separately retained Read results.
// Answered questions additionally require their independently settled callback.
// Other tools and callbacks retain their separate continuation gates.
func (t *Tx) ClaudeRootContentContinuation(execution domain.ID) (bool, error) {
	if err := execution.Validate(); err != nil {
		return false, err
	}
	var content, unsupported bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT
 EXISTS(SELECT 1 FROM execution_messages m JOIN entities e ON e.id=m.message_id WHERE m.execution_id=? AND m.state='complete' AND json_type(e.body,'$.claude')='object'),
	 EXISTS(SELECT 1 FROM execution_messages m JOIN entities e ON e.id=m.message_id WHERE m.execution_id=? AND (m.state!='complete' OR json_type(e.body,'$.tool')='object' OR json_type(e.body,'$.claude_interruption')='object' OR EXISTS(SELECT 1 FROM json_each(e.body,'$.claude.blocks') b WHERE json_type(b.value,'$.block.web') IS NOT NULL)))`, execution, execution).Scan(&content, &unsupported)
	if err != nil || !content || unsupported {
		return false, storageError(err)
	}
	if eligible, err := t.claudeCitationContinuation(execution); err != nil || !eligible {
		return false, err
	}
	rows, err := t.tx.QueryContext(t.ctx, `SELECT e.id,e.body FROM execution_messages m JOIN entities e ON e.id=m.message_id WHERE m.execution_id=? AND json_type(e.body,'$.claude_tool')='object'`, execution)
	if err != nil {
		return false, storageError(err)
	}
	defer rows.Close()
	questions := 0
	for rows.Next() {
		var id domain.ID
		var raw []byte
		var message domain.ExecutionMessage
		if err := rows.Scan(&id, &raw); err != nil {
			return false, storageError(err)
		}
		if domain.Decode(raw, &message) != nil || message.ExecutionID != execution || message.State != domain.MessageComplete || message.Role != domain.ToolMessage || message.ClaudeTool.ClaudeContinuationHistoryKind() == "" || id != message.ClaudeTool.Reference.ID || message.NativeParentID != message.ClaudeTool.NativeMessageID || message.NativeID != message.ClaudeTool.Reference.NativeID {
			return false, nil
		}
		if message.ClaudeTool.ClaudeQuestionContinuationCandidate() {
			questions++
		}
	}
	if err := rows.Err(); err != nil {
		return false, storageError(err)
	}
	if err := rows.Close(); err != nil {
		return false, storageError(err)
	}
	return t.claudeCallbackContinuation(execution, questions)
}

// The native part identity and provider call identity are different namespaces.
// Check both within the same serialized publication transaction; an OpenCode
// call cannot be relabeled as a second part. The execution index bounds this
// scan to the existing 10,000-item retention limit without a schema migration.
func (t *Tx) HasOpenCodeToolCall(execution domain.ID, call string) (bool, error) {
	if execution.Validate() != nil || domain.Text(call, "native tool call", 1024, true) != nil {
		return false, domain.Fail(domain.InvalidArgument, "Invalid native tool ownership.", "Preserve the original execution and call identity.")
	}
	var exists bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM execution_messages m JOIN entities e ON e.id=m.message_id
 WHERE m.execution_id=? AND ((json_extract(e.body,'$.tool.started.kind')='opencode-read' AND json_extract(e.body,'$.tool.started.read.call_id')=?) OR (json_extract(e.body,'$.tool.started.kind')='opencode-shell' AND json_extract(e.body,'$.tool.started.shell.call_id')=?) OR (json_extract(e.body,'$.tool.started.kind')='opencode-todo' AND json_extract(e.body,'$.tool.started.todo.call_id')=?) OR (json_extract(e.body,'$.tool.started.kind')='opencode-builtin' AND json_extract(e.body,'$.tool.started.builtin.call_id')=?)))`, execution, call, call, call, call).Scan(&exists)
	return exists, storageError(err)
}

func (t *Tx) HasCompletedOpenCodeInput(execution, input domain.ID, turn string) (bool, error) {
	if execution.Validate() != nil || input.Validate() != nil || domain.NativeIdentity(turn).Validate(domain.OpenCode, domain.NativeTurnIdentity) != nil {
		return false, domain.Fail(domain.InvalidArgument, "Invalid original input ownership.", "Retain the execution's original native input.")
	}
	var exists bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM execution_messages m JOIN entities e ON e.id=m.message_id WHERE m.execution_id=? AND m.native_turn_id=? AND m.state='complete' AND json_extract(e.body,'$.role')='user' AND json_extract(e.body,'$.input_id')=? AND json_extract(e.body,'$.native_parent_id')=?)`, execution, turn, input, turn).Scan(&exists)
	return exists, storageError(err)
}

func (t *Tx) HasCompletedClaudeInput(execution, input domain.ID, turn string) (bool, error) {
	if execution.Validate() != nil || input.Validate() != nil || domain.NativeIdentity(turn).Validate(domain.ClaudeCode, domain.NativeTurnIdentity) != nil {
		return false, domain.Fail(domain.InvalidArgument, "Invalid original input ownership.", "Retain the execution's original native input.")
	}
	var exists bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM execution_messages m JOIN entities e ON e.id=m.message_id WHERE m.execution_id=? AND m.native_turn_id=? AND m.native_item_id=? AND m.state='complete' AND json_extract(e.body,'$.role')='user' AND json_extract(e.body,'$.input_id')=? AND json_extract(e.body,'$.native_parent_id') IS NULL)`, execution, turn, input, input).Scan(&exists)
	return exists, storageError(err)
}

func (t *Tx) HasExecutionNativeMessage(execution domain.ID, native string) (bool, error) {
	if execution.Validate() != nil || domain.Text(native, "native message identity", 1024, true) != nil {
		return false, domain.Fail(domain.InvalidArgument, "Invalid native message identity.", "Preserve original execution ownership.")
	}
	var exists bool
	err := t.tx.QueryRowContext(t.ctx, "SELECT EXISTS(SELECT 1 FROM execution_messages WHERE execution_id=? AND native_item_id=?)", execution, native).Scan(&exists)
	return exists, storageError(err)
}
