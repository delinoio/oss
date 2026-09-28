package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// The execution index bounds these original messages. Public citation state is
// only a candidate; the original Worker report must also bind native history.
func (t *Tx) claudeCitationContinuation(execution domain.ID) (bool, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT e.body,
 EXISTS(SELECT 1 FROM json_each(e.body,'$.claude.blocks') b WHERE json_type(b.value,'$.citations') IS NOT NULL AND json_type(b.value,'$.citations')!='object')
 FROM execution_messages m JOIN entities e ON e.id=m.message_id WHERE m.execution_id=? AND EXISTS(SELECT 1 FROM json_each(e.body,'$.claude.blocks') b WHERE json_type(b.value,'$.citations') IS NOT NULL)`, execution)
	if err != nil {
		return false, storageError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var malformed bool
		var message domain.ExecutionMessage
		if err := rows.Scan(&raw, &malformed); err != nil {
			return false, storageError(err)
		}
		if malformed || domain.Decode(raw, &message) != nil || message.ExecutionID != execution || message.State != domain.MessageComplete || message.Role != domain.AssistantMessage || message.NativeParentID != "" || message.Claude == nil || message.Claude.Interruption != nil {
			return false, nil
		}
		for _, block := range message.Claude.Blocks {
			if block.Citations != nil && (block.Block.Kind != domain.ClaudeText || block.State != domain.ClaudeBlockStopped || !block.Citations.ContinuationCandidate()) {
				return false, nil
			}
		}
	}
	return true, storageError(rows.Err())
}
