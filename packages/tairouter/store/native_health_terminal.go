// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"time"
)

// ReleaseTerminalNativeHealthPermit is only for caller-confirmed terminal work.
// It grants no new authority and deliberately does not require current credentials.
func (s *Store) ReleaseTerminalNativeHealthPermit(ctx context.Context, l Lease, p NativeHealthPermit) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.lock(ctx, tx, l.Principal.Scope.OrganizationID); e != nil {
			return e
		}
		var owns bool
		sc := l.Principal.Scope
		if e := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM admissions WHERE id=$1 AND org_id=$2 AND user_id=$3 AND key_id=$4 AND generation=$5)", l.ID, sc.OrganizationID, sc.UserID, l.Principal.KeyID, sc.Generation).Scan(&owns); e != nil {
			return e
		}
		if !owns {
			return ErrForbidden
		}
		r, e := s.healthReceipt(ctx, tx, l, p)
		if e != nil {
			return e
		}
		return s.releaseTerminalHealthReceipt(ctx, tx, l, p, r)
	})
}
func (s *Store) releaseTerminalHealthReceipt(ctx context.Context, tx *sql.Tx, l Lease, p NativeHealthPermit, r healthReceipt) error {
	if r.status == "finished" || r.status == "released" {
		return nil
	}
	h, e := s.readHealth(ctx, tx, l, r.key)
	if e != nil {
		return e
	}
	if r.probe && h.ProbeID == p.ID && h.ProbeEpoch == r.epoch {
		h.ProbeID = ""
		h.ProbeExpiresAt = time.Time{}
		if r.key.Lane == NativeHealthQuota {
			h.LastProbeAt = h.Now
			if h.NextProbeAt.Before(h.Now.Add(5 * time.Minute)) {
				h.NextProbeAt = h.Now.Add(5 * time.Minute)
			}
		}
		if e = s.saveHealth(ctx, tx, l, r.key, h); e != nil {
			return e
		}
	}
	_, e = tx.ExecContext(ctx, "UPDATE native_health_permits SET status='released' WHERE id=$1", p.ID)
	return e
}
func (s *Store) releaseAdmissionHealth(ctx context.Context, tx *sql.Tx, l Lease) error {
	sc := l.Principal.Scope
	rows, e := tx.QueryContext(ctx, "SELECT id FROM native_health_permits WHERE admission_id=$1 AND org_id=$2 AND user_id=$3 AND key_id=$4 AND generation=$5 AND status NOT IN ('finished','released')", l.ID, sc.OrganizationID, sc.UserID, l.Principal.KeyID, sc.Generation)
	if e != nil {
		return e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		p := NativeHealthPermit{ID: id}
		r, e := s.healthReceipt(ctx, tx, l, p)
		if e != nil {
			return e
		}
		if e = s.releaseTerminalHealthReceipt(ctx, tx, l, p, r); e != nil {
			return e
		}
	}
	return nil
}
