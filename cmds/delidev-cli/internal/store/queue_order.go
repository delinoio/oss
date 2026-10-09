// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const queueOrderPrefix = "session-queue-order:"

// Nil IDs retain acceptance order until a genuine move captures the list.
// Generation-only records still fence waiting-page membership changes.
type queueOrder struct {
	Version    int         `json:"version"`
	Generation uint64      `json:"generation,string"`
	IDs        []domain.ID `json:"ordered_input_ids"`
}

func queueOrderRecovery() error {
	return domain.Fail(domain.RecoveryRequired, "Waiting input order is unavailable.", "Preserve the original queue and reconcile its ordering metadata.")
}

func (t *Tx) waitingOrder(session domain.ID) (queueOrder, []domain.ID, error) {
	state := queueOrder{Version: 1}
	if session.Validate() != nil {
		return state, nil, domain.InvalidImageInput()
	}
	rows, err := t.tx.QueryContext(t.ctx, "SELECT id FROM entities WHERE kind='queue' AND session_id=? AND json_extract(body,'$.delivery')='queued' ORDER BY json_extract(body,'$.sequence') LIMIT ?", session, domain.MaxPendingInputs+1)
	if err != nil {
		return state, nil, storageError(err)
	}
	ids := []domain.ID{}
	for rows.Next() {
		var id domain.ID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return state, nil, storageError(err)
		}
		if id.Validate() != nil || slices.Contains(ids, id) || len(ids) == domain.MaxPendingInputs {
			rows.Close()
			return state, nil, queueOrderRecovery()
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return state, nil, storageError(err)
	}
	var raw string
	err = t.tx.QueryRowContext(t.ctx, "SELECT value FROM metadata WHERE key=?", queueOrderPrefix+string(session)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return state, ids, nil
	}
	if err != nil {
		return state, nil, storageError(err)
	}
	if len(raw) > 64<<10 || domain.Decode([]byte(raw), &state) != nil || state.Version != 1 || len(state.IDs) > domain.MaxPendingInputs {
		return state, nil, queueOrderRecovery()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &fields) != nil || fields["generation"] == nil || fields["ordered_input_ids"] == nil {
		return state, nil, queueOrderRecovery()
	}
	if state.IDs == nil {
		return state, ids, nil
	}
	if len(state.IDs) != len(ids) {
		return state, nil, queueOrderRecovery()
	}
	seen := map[domain.ID]bool{}
	for _, id := range state.IDs {
		if id.Validate() != nil || seen[id] || !slices.Contains(ids, id) {
			return state, nil, queueOrderRecovery()
		}
		seen[id] = true
	}
	return state, slices.Clone(state.IDs), nil
}

func (t *Tx) saveWaitingOrder(session domain.ID, state queueOrder) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return storageError(err)
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", queueOrderPrefix+string(session), string(raw))
	return storageError(err)
}

type queueMembership struct {
	state   queueOrder
	changed bool
	queued  bool
}

func (t *Tx) prepareQueueMembership(id, session domain.ID, body []byte, expected uint64) (queueMembership, error) {
	var next struct {
		Delivery domain.InputDelivery `json:"delivery"`
	}
	// Public documents remain unchanged; only the closed delivery field is used.
	if json.Unmarshal(body, &next) != nil {
		return queueMembership{}, queueOrderRecovery()
	}
	previous := false
	if expected != 0 {
		old, err := t.Get(domain.QueueKind, id)
		if err != nil {
			return queueMembership{}, err
		}
		if old.SessionID != session {
			return queueMembership{}, domain.Fail(domain.PermissionDenied, "Queue scope cannot change.", "Use the original session.")
		}
		var value struct {
			Delivery domain.InputDelivery `json:"delivery"`
		}
		if json.Unmarshal(old.Data, &value) != nil {
			return queueMembership{}, queueOrderRecovery()
		}
		previous = value.Delivery == domain.InputQueued
	}
	state, _, err := t.waitingOrder(session)
	if err != nil {
		return queueMembership{}, err
	}
	return queueMembership{state: state, changed: previous != (next.Delivery == domain.InputQueued), queued: next.Delivery == domain.InputQueued}, nil
}

func (t *Tx) publishQueueMembership(id, session domain.ID, change queueMembership) error {
	if !change.changed {
		return nil
	}
	if change.state.Generation == math.MaxUint64 {
		return queueOrderRecovery()
	}
	change.state.Generation++
	if change.state.IDs != nil {
		if change.queued {
			if len(change.state.IDs) >= domain.MaxPendingInputs || slices.Contains(change.state.IDs, id) {
				return queueOrderRecovery()
			}
			change.state.IDs = append(change.state.IDs, id)
		} else {
			index := slices.Index(change.state.IDs, id)
			if index < 0 {
				return queueOrderRecovery()
			}
			change.state.IDs = slices.Delete(change.state.IDs, index, index+1)
		}
	}
	return t.saveWaitingOrder(session, change.state)
}

// MoveWaitingInput changes only private dispatch order. The caller's receipt
// transaction owns authorization, selected/anchor revisions and session event.
func (t *Tx) MoveWaitingInput(session, id, before domain.ID, generation uint64) (uint64, bool, error) {
	state, ids, err := t.waitingOrder(session)
	if err != nil {
		return 0, false, err
	}
	if state.Generation != generation {
		return 0, false, domain.Fail(domain.Conflict, "Waiting input order changed.", "Refresh the original waiting queue before another movement.")
	}
	from := slices.Index(ids, id)
	anchor := slices.Index(ids, before)
	if from < 0 || before != "" && anchor < 0 {
		return 0, false, domain.Fail(domain.Conflict, "The input is no longer waiting.", "Refresh the waiting queue.")
	}
	if before == id {
		return 0, false, domain.Fail(domain.InvalidArgument, "An input cannot anchor itself.", "Select another waiting input.")
	}
	if before == "" && from == len(ids)-1 || anchor == from+1 {
		return state.Generation, false, nil
	}
	ids = slices.Delete(ids, from, from+1)
	if before == "" {
		ids = append(ids, id)
	} else {
		ids = slices.Insert(ids, slices.Index(ids, before), id)
	}
	if state.Generation == math.MaxUint64 {
		return 0, false, queueOrderRecovery()
	}
	state.Generation++
	state.IDs = ids
	return state.Generation, true, t.saveWaitingOrder(session, state)
}

// WaitingQueue reads order, generation, count and bounded payloads in one SQL
// snapshot. Acceptance-history cursors and public input documents stay separate.
func (s *Store) WaitingQueue(ctx context.Context, session domain.ID, expected *uint64, after domain.ID, limit int) ([]Record, bool, uint64, uint32, error) {
	if err := (Filter{Kind: domain.QueueKind, SessionID: session, Limit: limit}).validate(); err != nil {
		return nil, false, 0, 0, err
	}
	var result []Record
	var more bool
	var generation uint64
	var count uint32
	err := s.Read(ctx, func(t *Tx) error {
		if _, err := t.Get(domain.SessionKind, session); err != nil {
			return err
		}
		state, ids, err := t.waitingOrder(session)
		if err != nil {
			return err
		}
		generation = state.Generation
		count = uint32(len(ids))
		if expected != nil && *expected != generation {
			return domain.Fail(domain.CursorExpired, "The waiting queue changed.", "Refresh it from the first page.")
		}
		start := 0
		if after != "" {
			index := slices.Index(ids, after)
			if index < 0 {
				return domain.Fail(domain.CursorExpired, "The waiting cursor is unavailable.", "Refresh the waiting queue.")
			}
			start = index + 1
		}
		bytes := 0
		for _, id := range ids[start:] {
			row, err := t.Get(domain.QueueKind, id)
			if err != nil {
				return err
			}
			if len(result) == limit || len(result) > 0 && bytes+len(row.Data) > 3<<20 {
				more = true
				break
			}
			result = append(result, row)
			bytes += len(row.Data)
		}
		return nil
	})
	return result, more, generation, count, err
}

// WaitingQueueGeneration is read-only and does not initialize metadata.
func (t *Tx) WaitingQueueGeneration(session domain.ID) (uint64, error) {
	state, _, err := t.waitingOrder(session)
	return state.Generation, err
}
