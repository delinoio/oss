package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// The bounded execution index protects the native namespace independently of
// Worker decoding. A new message cannot re-publish an earlier native call.
func (t *Tx) ClaudeWebIdentityAvailable(session, execution domain.ID, thread, turn, native string) (bool, error) {
	var duplicate bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM execution_messages m JOIN entities e ON e.id=m.message_id WHERE m.session_id=? AND m.native_thread_id=? AND m.native_turn_id=? AND (json_extract(e.body,'$.claude_tool.reference.native_id')=? OR EXISTS(SELECT 1 FROM json_each(e.body,'$.claude.blocks') b WHERE json_extract(b.value,'$.block.web.native_id')=?)))`, session, thread, turn, native, native).Scan(&duplicate)
	if err != nil || duplicate {
		return false, storageError(err)
	}
	var count int
	err = t.tx.QueryRowContext(t.ctx, `SELECT (SELECT COUNT(*) FROM execution_messages m JOIN entities e ON e.id=m.message_id JOIN json_each(e.body,'$.claude.blocks') b WHERE m.execution_id=? AND json_extract(b.value,'$.block.kind')='server_tool_use') + (SELECT COUNT(*) FROM execution_messages m JOIN entities e ON e.id=m.message_id WHERE m.execution_id=? AND json_type(e.body,'$.claude_tool')='object')`, execution, execution).Scan(&count)
	return count < 4096, storageError(err)
}
