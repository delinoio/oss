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

// Every indexed callback needs its original accepted response. Each Question
// additionally requires exactly one callback; tool permission is native-owned.
func (t *Tx) claudeCallbackContinuation(execution domain.ID, questions int) (bool, error) {
	// Join and validate one bounded question/result pair at a time. Retaining all
	// tool bodies together would amplify memory with the complete history size.
	rows, err := t.tx.QueryContext(t.ctx, `SELECT e.body,tool.body FROM execution_interactions i
 JOIN entities e ON e.id=i.interaction_id
 LEFT JOIN execution_messages m ON m.execution_id=i.execution_id AND m.message_id=json_extract(e.body,'$.claude.tool.id')
 LEFT JOIN entities tool ON tool.id=m.message_id
 WHERE i.execution_id=?`, execution)
	if err != nil {
		return false, storageError(err)
	}
	defer rows.Close()
	seen := map[domain.ID]bool{}
	answered := 0
	for rows.Next() {
		var raw, toolRaw []byte
		var value domain.ExecutionInteraction
		var tool domain.ExecutionMessage
		if err := rows.Scan(&raw, &toolRaw); err != nil {
			return false, storageError(err)
		}
		if domain.Decode(raw, &value) != nil || value.ExecutionID != execution || value.Claude == nil || domain.Decode(toolRaw, &tool) != nil || seen[value.Claude.Tool.ID] || (!value.ClaudeQuestionContinuationEvidence(tool) && !value.ClaudeToolApprovalContinuationEvidence(tool)) {
			return false, nil
		}
		seen[value.Claude.Tool.ID] = true
		if value.Claude.Kind == domain.ClaudeUserQuestion {
			answered++
		}
	}
	return answered == questions, storageError(rows.Err())
}
