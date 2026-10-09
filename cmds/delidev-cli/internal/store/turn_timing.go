// SPDX-License-Identifier: Apache-2.0
package store

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// PublishTurnTerminalTiming touches only indexed original primary-input records.
// It shares terminal, session, event and receipt publication; no native fields or
// sequence/state are rewritten and legacy timing is never manufactured.
func (t *Tx) PublishTurnTerminalTiming(session, execution, input domain.ID, thread, turn string, timing domain.TurnTiming) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	if session.Validate() != nil || execution.Validate() != nil || input.Validate() != nil || timing.AcceptedAt.IsZero() || timing.TerminalAt == nil {
		return domain.Fail(domain.Conflict, "Invalid original turn timing ownership.", "Retain the original acceptance and terminal receipt.")
	}
	rows, err := t.tx.QueryContext(t.ctx, `SELECT e.id,m.session_id,m.native_thread_id,m.native_turn_id FROM execution_messages m JOIN entities e ON e.id=m.message_id WHERE m.execution_id=? AND json_extract(e.body,'$.role')='user' AND json_extract(e.body,'$.input_id')=?`, execution, input)
	if err != nil {
		return storageError(err)
	}
	ids := []domain.ID{}
	for rows.Next() {
		var id, indexedSession domain.ID
		var indexedThread, indexedTurn string
		if err := rows.Scan(&id, &indexedSession, &indexedThread, &indexedTurn); err != nil {
			rows.Close()
			return storageError(err)
		}
		if indexedSession != session || indexedThread != thread || indexedTurn != turn {
			rows.Close()
			return domain.Fail(domain.Conflict, "The original turn index changed.", "Retain its exact session and native ownership.")
		}
		ids = append(ids, id)
		if len(ids) > 10000 {
			rows.Close()
			return domain.Fail(domain.ResourceExhausted, "Turn timing reached the original message bound.", "Retain the original transcript without truncation.")
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return storageError(err)
	}
	for _, id := range ids {
		r, err := t.Get(domain.MessageKind, id)
		if err != nil {
			return err
		}
		m, err := Decode[domain.ExecutionMessage](r)
		if err != nil || r.SessionID != session || m.ExecutionID != execution || m.InputID != input || m.NativeThreadID != thread || m.NativeTurnID != turn || m.Inherited != nil || m.TurnTiming == nil || !m.TurnTiming.AcceptedAt.Equal(timing.AcceptedAt) || m.TurnTiming.TerminalAt != nil {
			return domain.Fail(domain.Conflict, "The original turn timing changed.", "Reconcile its original input and publication receipts.")
		}
		value := timing
		m.TurnTiming = &value
		if _, err := t.Put(domain.MessageKind, r.ID, r.Revision, r.SessionID, r.ProjectID, m); err != nil {
			return err
		}
	}
	return nil
}
