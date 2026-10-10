// SPDX-License-Identifier: Apache-2.0
package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// The original execution index owns the call. Connector IDs cannot select a
// different session, turn, transcript item or account-bound execution.
func (t *Tx) CodexAppsTool(session, execution domain.ID, thread, turn, item string) (domain.ExecutionMessage, error) {
	if session.Validate() != nil || execution.Validate() != nil || domain.ID(thread).Validate() != nil || domain.ID(turn).Validate() != nil || domain.Text(item, "original native App call", 1024, true) != nil {
		return domain.ExecutionMessage{}, domain.NativeAppsUnavailable()
	}
	var id domain.ID
	if err := t.tx.QueryRowContext(t.ctx, `SELECT message_id FROM execution_messages WHERE session_id=? AND execution_id=? AND native_thread_id=? AND native_turn_id=? AND native_item_id=?`, session, execution, thread, turn, item).Scan(&id); err != nil {
		return domain.ExecutionMessage{}, storageError(err)
	}
	r, err := t.Get(domain.MessageKind, id)
	if err != nil {
		return domain.ExecutionMessage{}, err
	}
	return Decode[domain.ExecutionMessage](r)
}
