package store

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Exact execution ownership and the original 4 MiB native journal bound keep
// this read finite. These records never create a native restoration capability.
func (t *Tx) GrokToolJournal(session, execution domain.ID) ([]domain.ExecutionMessage, error) {
	if session.Validate() != nil || execution.Validate() != nil {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid original Grok journal scope.", "Use the retained session and execution identities.")
	}
	rows, err := t.tx.QueryContext(t.ctx, `SELECT body FROM entities WHERE kind=? AND session_id=? AND json_extract(body,'$.execution_id')=? AND json_type(body,'$.grok_tool')='object' ORDER BY json_extract(body,'$.first_sequence') LIMIT 4097`, domain.MessageKind, session, execution)
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
		if len(result) >= 4096 || size > 8<<20 || domain.Decode(raw, &message) != nil || message.ExecutionID != execution || message.GrokTool == nil {
			return nil, domain.Fail(domain.RecoveryRequired, "The original Grok journal exceeds its validated scope.", "Preserve every retained observation without replay or truncation.")
		}
		result = append(result, message)
	}
	return result, storageError(rows.Err())
}
func (t *Tx) GrokInteractionRecords(session, execution domain.ID) ([]Record, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT id FROM entities WHERE kind=? AND session_id=? AND json_extract(body,'$.execution_id')=? AND json_type(body,'$.grok')='object' LIMIT 129`, domain.InteractionKind, session, execution)
	if err != nil {
		return nil, storageError(err)
	}
	ids := []domain.ID{}
	for rows.Next() {
		var id domain.ID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, storageError(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, storageError(err)
	}
	if len(ids) > 128 {
		return nil, domain.Fail(domain.ResourceExhausted, "Original Grok interactions reached their bound.", "Preserve the original native input without replay.")
	}
	result := make([]Record, 0, len(ids))
	for _, id := range ids {
		r, err := t.Get(domain.InteractionKind, id)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, nil
}
