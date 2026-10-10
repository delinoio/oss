// SPDX-License-Identifier: Apache-2.0
package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

func (t *Tx) CodexDynamicToolJournal(session, execution domain.ID) ([]domain.ExecutionMessage, error) {
	if session.Validate() != nil || execution.Validate() != nil {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid original Codex dynamic tool journal scope.", "Use the retained session and execution identities.")
	}
	rows, err := t.tx.QueryContext(t.ctx, `SELECT body FROM entities WHERE kind=? AND session_id=? AND json_extract(body,'$.execution_id')=? AND json_type(body,'$.codex_dynamic_tool')='object' ORDER BY json_extract(body,'$.first_sequence') LIMIT 4097`, domain.MessageKind, session, execution)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	result := []domain.ExecutionMessage{}
	size := 0
	for rows.Next() {
		var raw []byte
		var message domain.ExecutionMessage
		if err := rows.Scan(&raw); err != nil {
			return nil, storageError(err)
		}
		size += len(raw)
		if len(result) >= 4096 || size > 8<<20 || domain.Decode(raw, &message) != nil || message.ExecutionID != execution || message.CodexDynamicTool == nil {
			return nil, domain.Fail(domain.RecoveryRequired, "The original Codex dynamic tool journal exceeds its validated scope.", "Preserve every retained observation without replay or truncation.")
		}
		result = append(result, message)
	}
	return result, storageError(rows.Err())
}
