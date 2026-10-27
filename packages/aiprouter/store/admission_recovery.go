// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"time"
)

var ErrInstanceDraining = errors.New("admission instance is not active")

// RegisterAdmissionInstance registers a fresh server boot before opening listeners.
// It deliberately rejects every reused ID, including active or drained boots.
func (s *Store) RegisterAdmissionInstance(ctx context.Context) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		r, e := tx.ExecContext(ctx, "INSERT INTO admission_instances(id,state) VALUES($1,'active') ON CONFLICT DO NOTHING", s.instanceID)
		if e != nil {
			return e
		}
		n, e := r.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}

func (s *Store) InstanceID() string { return s.instanceID }

// ReleaseUntilRecorded retries only a caller-confirmed completed request. Use a
// service-lifetime context; cancellation preserves the slot for offline recovery.
func (s *Store) ReleaseUntilRecorded(ctx context.Context, l Lease) error {
	delay := 100 * time.Millisecond
	for {
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		e := s.Release(attempt, l)
		cancel()
		if e == nil {
			return nil
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(ctx.Err(), e)
		case <-timer.C:
		}
		if delay < 5*time.Second {
			delay *= 2
			if delay > 5*time.Second {
				delay = 5 * time.Second
			}
		}
	}
}
func (s *Store) guardInstance(ctx context.Context, tx *sql.Tx, id string, requireActive bool) error {
	var state string
	e := tx.QueryRowContext(ctx, "SELECT state FROM admission_instances WHERE id=$1 FOR SHARE", id).Scan(&state)
	if e != nil {
		return noRows(e)
	}
	if state == "terminated" || requireActive && state != "active" {
		return ErrInstanceDraining
	}
	return nil
}

// DrainAdmissionInstance fences new admissions and returns a one-use recovery
// proof. New physical sends are fenced; already-started work may complete.
func (s *Store) DrainAdmissionInstance(ctx context.Context, id string) (proof string, e error) {
	proof = core.Token()
	e = s.tx(ctx, func(tx *sql.Tx) error {
		r, e := tx.ExecContext(ctx, "UPDATE admission_instances SET state='draining',drain_hash=$2 WHERE id=$1 AND state<>'terminated'", id, core.Hash(proof))
		if e != nil {
			return e
		}
		n, _ := r.RowsAffected()
		if n != 1 {
			return ErrNotFound
		}
		return nil
	})
	return
}

// ReconcileTerminatedInstance is a maintenance operation, never a timed lease
// reaper. The caller must independently confirm that this boot instance and all
// its upstream executions have stopped. The DB proof fences stale recovery calls.
func (s *Store) ReconcileTerminatedInstance(ctx context.Context, id, proof string, upstreamsStopped bool) (released int64, e error) {
	if !upstreamsStopped || id == "" || proof == "" {
		return 0, ErrForbidden
	}
	e = s.tx(ctx, func(tx *sql.Tx) error {
		// Lock organizations before the instance row, matching physical dispatch
		// lock order. Draining prevents new admissions from expanding this set.
		rows, e := tx.QueryContext(ctx, "SELECT DISTINCT org_id FROM admissions WHERE instance_id=$1 ORDER BY org_id", id)
		if e != nil {
			return e
		}
		var orgs []string
		for rows.Next() {
			var org string
			if e = rows.Scan(&org); e != nil {
				rows.Close()
				return e
			}
			orgs = append(orgs, org)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, org := range orgs {
			if e = s.lock(ctx, tx, org); e != nil {
				return e
			}
		}
		r, e := tx.ExecContext(ctx, "UPDATE admission_instances SET state='terminated',drain_hash=NULL WHERE id=$1 AND state='draining' AND drain_hash=$2", id, core.Hash(proof))
		if e != nil {
			return e
		}
		n, _ := r.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		r, e = tx.ExecContext(ctx, "UPDATE admissions SET released=true WHERE instance_id=$1 AND NOT released", id)
		if e != nil {
			return e
		}
		released, e = r.RowsAffected()
		if e != nil {
			return e
		}
		rows, e = tx.QueryContext(ctx, "SELECT id,org_id,user_id,key_id,generation FROM admissions WHERE instance_id=$1", id)
		if e != nil {
			return e
		}
		var leases []Lease
		for rows.Next() {
			var l Lease
			if e = rows.Scan(&l.ID, &l.Principal.Scope.OrganizationID, &l.Principal.Scope.UserID, &l.Principal.KeyID, &l.Principal.Scope.Generation); e != nil {
				rows.Close()
				return e
			}
			leases = append(leases, l)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, l := range leases {
			if e = s.releaseAdmissionHealth(ctx, tx, l); e != nil {
				return e
			}
		}
		_, e = tx.ExecContext(ctx, "DELETE FROM mistral_waiters WHERE admission_id IN (SELECT id FROM admissions WHERE instance_id=$1)", id)
		return e
	})
	return
}

type AdmissionInstance struct {
	ID, State        string
	CreatedAt        time.Time
	ActiveAdmissions int64
}

func (s *Store) ListAdmissionInstances(ctx context.Context) (out []AdmissionInstance, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		rows, e := tx.QueryContext(ctx, "SELECT i.id,i.state,i.created_at,count(a.id) FILTER(WHERE NOT a.released) FROM admission_instances i LEFT JOIN admissions a ON a.instance_id=i.id GROUP BY i.id ORDER BY i.created_at")
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var i AdmissionInstance
			if e = rows.Scan(&i.ID, &i.State, &i.CreatedAt, &i.ActiveAdmissions); e != nil {
				return e
			}
			out = append(out, i)
		}
		return rows.Err()
	})
	return
}
