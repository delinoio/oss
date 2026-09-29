package store

import "crypto/sha256"
import "encoding/hex"
import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

func (t *Tx) GrokExecutionItem(session, execution domain.ID, thread, turn, item string) (domain.ExecutionMessage, error) {
	if session.Validate() != nil || execution.Validate() != nil || domain.NativeIdentity(thread).Validate(domain.GrokBuild, domain.NativeThreadIdentity) != nil || domain.NativeIdentity(turn).Validate(domain.GrokBuild, domain.NativeTurnIdentity) != nil || domain.Text(item, "native item", 1024, true) != nil {
		return domain.ExecutionMessage{}, domain.Fail(domain.InvalidArgument, "Invalid Grok item scope.", "Retain the original input and native item.")
	}
	var id domain.ID
	err := t.tx.QueryRowContext(t.ctx, `SELECT message_id FROM execution_messages WHERE session_id=? AND execution_id=? AND native_thread_id=? AND native_turn_id=? AND native_item_id=?`, session, execution, thread, turn, item).Scan(&id)
	if err != nil {
		return domain.ExecutionMessage{}, storageError(err)
	}
	r, err := t.Get(domain.MessageKind, id)
	if err != nil {
		return domain.ExecutionMessage{}, err
	}
	return Decode[domain.ExecutionMessage](r)
}

// Retained response text is compared in original publication order, not by
// assuming every response contains text or summing overlapping usage totals.
func (t *Tx) GrokTextProof(execution domain.ID) (string, uint32, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT e.body FROM execution_messages m JOIN entities e ON e.id=m.message_id WHERE m.execution_id=? AND json_type(e.body,'$.grok_text')='object' ORDER BY json_extract(e.body,'$.first_sequence')`, execution)
	if err != nil {
		return "", 0, storageError(err)
	}
	defer rows.Close()
	output := sha256.New()
	var chunks uint32
	for rows.Next() {
		var raw []byte
		var message domain.ExecutionMessage
		if err := rows.Scan(&raw); err != nil {
			return "", 0, storageError(err)
		}
		if domain.Decode(raw, &message) != nil || message.GrokText == nil || message.State != domain.MessageComplete {
			return "", 0, domain.Fail(domain.Conflict, "Original Grok text is incomplete.", "Retain its native response and outbox.")
		}
		_, _ = output.Write([]byte(message.Text))
		chunks += uint32(len(message.GrokText.Chunks))
	}
	return hex.EncodeToString(output.Sum(nil)), chunks, storageError(rows.Err())
}
