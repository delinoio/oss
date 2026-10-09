// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"time"
)

type workerNotificationObservation struct {
	Version       int       `json:"version"`
	Epoch         domain.ID `json:"epoch"`
	Instance      domain.ID `json:"instance"`
	ClockValid    bool      `json:"clock_valid"`
	Available     bool      `json:"available"`
	ObservedAt    time.Time `json:"observed_at"`
	LastSeen      time.Time `json:"last_seen"`
	Authorization domain.ID `json:"authorization"`
}

// Server-owned observation is metadata, like Heartbeat. An original lease edge
// and its Inbox record commit together. A fresh server epoch resets baselines;
// authorization, enablement, process replacement and clock rollback cannot emit.
func (s *Store) ObserveWorkerNotifications(ctx context.Context, epoch domain.ID, after domain.ID) (domain.ID, error) {
	if epoch.Validate() != nil {
		return "", notificationConflict()
	}
	s.gate.RLock()
	defer s.gate.RUnlock()
	if s.deletionFault {
		return "", domain.SessionDeletionPending()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", storageError(err)
	}
	defer tx.Rollback()
	t := &Tx{tx: tx, ctx: ctx, now: time.Now().UTC().Truncate(time.Millisecond), touched: map[domain.ID]bool{}}
	records, err := t.List(Filter{Kind: domain.MachineKind, After: after, Limit: MaxPage})
	if err != nil {
		return "", err
	}
	for _, r := range records {
		machine, err := Decode[domain.Machine](r)
		if err != nil {
			return "", err
		}
		key := "notification-worker-baseline-v1:" + string(r.ID)
		var previous workerNotificationObservation
		exists, err := t.notificationMetadata(key, &previous)
		if err != nil {
			return "", err
		}
		if exists && (previous.Version != 1 || previous.Epoch.Validate() != nil || previous.Instance.Validate() != nil || previous.Authorization.Validate() != nil || previous.ObservedAt.IsZero() || previous.LastSeen.IsZero()) {
			return "", notificationConflict()
		}
		instance, seen, err := t.WorkerInstance(r.ID)
		if err != nil {
			return "", err
		}
		var device domain.ID
		rows, err := tx.QueryContext(ctx, `SELECT id FROM entities WHERE kind='device' AND json_extract(body,'$.type')='worker' AND json_extract(body,'$.machine_id')=? AND COALESCE(json_extract(body,'$.revoked'),0)=0 ORDER BY id LIMIT 2`, r.ID)
		if err != nil {
			return "", storageError(err)
		}
		if rows.Next() {
			if err := rows.Scan(&device); err != nil {
				rows.Close()
				return "", storageError(err)
			}
		}
		multiple := rows.Next()
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", storageError(err)
		}
		if machine.Disabled || device == "" || multiple || instance == "" || seen.IsZero() {
			if _, err := tx.ExecContext(ctx, "DELETE FROM metadata WHERE key=?", key); err != nil {
				return "", storageError(err)
			}
			continue
		}
		current := workerNotificationObservation{Version: 1, Epoch: epoch, Instance: instance, Available: !seen.After(t.now) && t.now.Sub(seen) <= 45*time.Second, ObservedAt: t.now, LastSeen: seen, Authorization: device, ClockValid: !seen.After(t.now)}
		baseline := !previous.ClockValid || !exists || previous.Version != 1 || previous.Epoch != epoch || previous.Instance != instance || previous.Authorization != device || t.now.Before(previous.ObservedAt) || seen.Before(previous.LastSeen) || seen.After(t.now)
		if !baseline && previous.Available != current.Available {
			kind := domain.WorkerUnavailableNotification
			if current.Available {
				kind = domain.WorkerAvailableNotification
			}
			if _, err := t.CreateOperationalInbox(domain.NewID(), domain.InboxOperational{Kind: kind, DeviceID: device, MachineID: r.ID, InstanceID: instance, ObservedAt: t.now}); err != nil {
				return "", err
			}
		}
		if err := t.putNotificationMetadata(key, current); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", storageError(err)
	}
	if len(t.touched) > 0 {
		s.signal()
	}
	if len(records) == MaxPage {
		return records[len(records)-1].ID, nil
	}
	return "", nil
}
