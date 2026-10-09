// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Captured order belongs to the restored queue, rather than the current safety
// image's different membership. Advance beyond both generations to expire cursors.
func restoreWaitingOrder(ctx context.Context, tx *sql.Tx, now time.Time) error {
	rows, err := tx.QueryContext(ctx, "SELECT id FROM entities WHERE kind='session' ORDER BY id LIMIT 100001")
	if err != nil {
		return storageError(err)
	}
	ids := []domain.ID{}
	for rows.Next() {
		var id domain.ID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return storageError(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return storageError(err)
	}
	if len(ids) > 100000 {
		return domain.Fail(domain.ResourceExhausted, "Waiting order restore exceeds its session bound.", "Preserve both original images for maintenance.")
	}
	owner := &Tx{tx: tx, ctx: ctx, now: now}
	for _, id := range ids {
		state, _, err := owner.waitingOrder(id)
		if err != nil {
			return err
		}
		var raw string
		err = tx.QueryRowContext(ctx, "SELECT value FROM current_state.metadata WHERE key=?", queueOrderPrefix+string(id)).Scan(&raw)
		if err == nil {
			var current queueOrder
			if len(raw) > 64<<10 || domain.Decode([]byte(raw), &current) != nil || current.Version != 1 {
				return queueOrderRecovery()
			}
			state.Generation = max(state.Generation, current.Generation)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return storageError(err)
		}
		if state.Generation == math.MaxUint64 {
			return queueOrderRecovery()
		}
		state.Generation++
		if err := owner.saveWaitingOrder(id, state); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM metadata WHERE key LIKE 'session-queue-order:%' AND substr(key,length('session-queue-order:')+1) NOT IN (SELECT id FROM entities WHERE kind='session')")
	return storageError(err)
}
