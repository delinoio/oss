// SPDX-License-Identifier: Apache-2.0
package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

func (t *Tx) CodexAppCall(session, execution domain.ID, thread, turn, item string) (domain.ExecutionMessage, error) {
	if session.Validate() != nil || execution.Validate() != nil || domain.NativeIdentity(thread).Validate(domain.Codex, domain.NativeThreadIdentity) != nil || domain.NativeIdentity(turn).Validate(domain.Codex, domain.NativeTurnIdentity) != nil || domain.Text(item, "original app call", 1024, true) != nil {
		return domain.ExecutionMessage{}, domain.Fail(domain.InvalidArgument, "Invalid original app call scope.", "Preserve the original account, execution and native identities.")
	}
	var id domain.ID
	if err := t.tx.QueryRowContext(t.ctx, `SELECT message_id FROM execution_messages WHERE session_id=? AND execution_id=? AND native_thread_id=? AND native_turn_id=? AND native_item_id=?`, session, execution, thread, turn, item).Scan(&id); err != nil {
		return domain.ExecutionMessage{}, storageError(err)
	}
	r, err := t.Get(domain.MessageKind, id)
	if err != nil {
		return domain.ExecutionMessage{}, err
	}
	value, err := Decode[domain.ExecutionMessage](r)
	if err != nil {
		return value, err
	}
	if r.SessionID != session || value.ExecutionID != execution || value.NativeThreadID != thread || value.NativeTurnID != turn || value.NativeID != item || value.NativeParentID != "" || value.Role != domain.ToolMessage || value.Tool == nil || value.Tool.Started.Kind != domain.CodexAppTool {
		return domain.ExecutionMessage{}, domain.Fail(domain.Conflict, "The original app call changed.", "Retain its original native ownership.")
	}
	return value, nil
}
