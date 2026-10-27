// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"time"
)

type Member struct {
	UserID, Email string
	Role          core.Role
	Generation    int64
}

func (s *Store) ListMembers(ctx context.Context, sc core.Scope) (out []Member, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		r, e := s.guard(ctx, tx, sc)
		if e != nil {
			return e
		}
		if !elevated(r) {
			return ErrForbidden
		}
		rows, e := tx.QueryContext(ctx, "SELECT m.user_id,u.email,m.role,m.generation FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.org_id=$1 ORDER BY m.user_id", sc.OrganizationID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var m Member
			if e = rows.Scan(&m.UserID, &m.Email, &m.Role, &m.Generation); e != nil {
				return e
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return
}
func (s *Store) ListInvitations(ctx context.Context, sc core.Scope) (out []Invitation, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		r, e := s.guard(ctx, tx, sc)
		if e != nil {
			return e
		}
		if !elevated(r) {
			return ErrForbidden
		}
		rows, e := tx.QueryContext(ctx, "SELECT "+invitationColumns+" FROM invitations i JOIN organizations o ON o.id=i.org_id WHERE i.org_id=$1 AND i.expires_at>clock_timestamp() ORDER BY i.created_at DESC", sc.OrganizationID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var i Invitation
			if e = scanInvitation(rows, &i); e != nil {
				return e
			}
			out = append(out, i)
		}
		return rows.Err()
	})
	return
}
func (s *Store) OrganizationSettings(ctx context.Context, sc core.Scope) (out json.RawMessage, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		e := tx.QueryRowContext(ctx, "SELECT value FROM org_settings WHERE org_id=$1", sc.OrganizationID).Scan(&out)
		if e == sql.ErrNoRows {
			out = json.RawMessage(`{}`)
			return nil
		}
		return e
	})
	return
}

// Organization settings accept only nonsecret settings validated by the service.
func (s *Store) UpdateOrganizationSettings(ctx context.Context, sc core.Scope, value json.RawMessage) error {
	if !json.Valid(value) {
		return ErrConflict
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		r, e := s.guard(ctx, tx, sc)
		if e != nil {
			return e
		}
		if !elevated(r) {
			return ErrForbidden
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO org_settings(org_id,value) VALUES($1,$2) ON CONFLICT(org_id) DO UPDATE SET value=excluded.value", sc.OrganizationID, value)
		if e != nil {
			return e
		}
		return s.audit(ctx, tx, sc.OrganizationID, sc.UserID, "organization.settings_changed", sc.OrganizationID)
	})
}
func (s *Store) GetPrivateVersion(ctx context.Context, sc core.Scope, kind, id string) (value []byte, version string, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		if e := s.privateUsable(ctx, tx, sc, kind, id); e != nil {
			return e
		}
		var data []byte
		if e := tx.QueryRowContext(ctx, "SELECT value,version FROM private_data WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4", sc.OrganizationID, sc.UserID, kind, id).Scan(&data, &version); e != nil {
			return noRows(e)
		}
		var err error
		value, err = s.keys.Decrypt(data, ownership(sc, kind, id))
		return err
	})
	return
}

// CompareAndSwapPrivate is the refresh-completion path. A deleted or replaced
// connection cannot be recreated by a stale OAuth response. It never inserts.
func (s *Store) CompareAndSwapPrivate(ctx context.Context, l Lease, kind, id, version string, value []byte) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkLease(ctx, tx, l); e != nil {
			return e
		}
		sc := l.Principal.Scope
		if e := s.privateUsable(ctx, tx, sc, kind, id); e != nil {
			return e
		}
		v, e := s.keys.Encrypt(value, ownership(sc, kind, id))
		if e != nil {
			return e
		}
		res, e := tx.ExecContext(ctx, "UPDATE private_data SET value=$5,version=$7 WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4 AND version=$6", sc.OrganizationID, sc.UserID, kind, id, v, version, core.ID())
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}

func (s *Store) GetOrganization(ctx context.Context, sc core.Scope) (o Organization, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		r, e := s.guard(ctx, tx, sc)
		if e != nil {
			return e
		}
		o.Role = r
		return noRows(tx.QueryRowContext(ctx, "SELECT id,name FROM organizations WHERE id=$1", sc.OrganizationID).Scan(&o.ID, &o.Name))
	})
	return
}
func (s *Store) UpdateOrganization(ctx context.Context, sc core.Scope, name string) (o Organization, e error) {
	if name == "" || len(name) > 200 {
		return o, ErrConflict
	}
	e = s.tx(ctx, func(tx *sql.Tx) error {
		r, e := s.guard(ctx, tx, sc)
		if e != nil {
			return e
		}
		if !elevated(r) {
			return ErrForbidden
		}
		o.Role = r
		if e = tx.QueryRowContext(ctx, "UPDATE organizations SET name=$2 WHERE id=$1 RETURNING id,name", sc.OrganizationID, name).Scan(&o.ID, &o.Name); e != nil {
			return e
		}
		return s.audit(ctx, tx, sc.OrganizationID, sc.UserID, "organization.settings_changed", sc.OrganizationID)
	})
	return
}

type Transfer struct {
	ID, OrganizationID, FromUserID, ToUserID string
	ExpiresAt                                time.Time
}

func (s *Store) GetTransfer(ctx context.Context, sc core.Scope) (out Transfer, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		r, e := s.guard(ctx, tx, sc)
		if e != nil {
			return e
		}
		e = tx.QueryRowContext(ctx, "SELECT id,org_id,from_user,to_user,expires_at FROM transfers WHERE org_id=$1 AND expires_at>clock_timestamp()", sc.OrganizationID).Scan(&out.ID, &out.OrganizationID, &out.FromUserID, &out.ToUserID, &out.ExpiresAt)
		if e != nil {
			return noRows(e)
		}
		if !elevated(r) && out.ToUserID != sc.UserID {
			return ErrForbidden
		}
		return nil
	})
	return
}
func (s *Store) CancelTransferID(ctx context.Context, sc core.Scope, id string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		r, e := s.guard(ctx, tx, sc)
		if e != nil {
			return e
		}
		if r != core.Owner {
			return ErrForbidden
		}
		res, e := tx.ExecContext(ctx, "DELETE FROM transfers WHERE org_id=$1 AND id=$2", sc.OrganizationID, id)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrNotFound
		}
		return s.audit(ctx, tx, sc.OrganizationID, sc.UserID, "ownership.canceled", id)
	})
}
func (s *Store) ResendInvitation(ctx context.Context, sc core.Scope, id string) (i Invitation, e error) {
	var email string
	e = s.tx(ctx, func(tx *sql.Tx) error {
		r, e := s.guard(ctx, tx, sc)
		if e != nil {
			return e
		}
		if !elevated(r) {
			return ErrForbidden
		}
		return noRows(tx.QueryRowContext(ctx, "SELECT email FROM invitations WHERE org_id=$1 AND id=$2 AND status<>'accepted'", sc.OrganizationID, id).Scan(&email))
	})
	if e != nil {
		return i, e
	}
	return s.invite(ctx, sc, email, "invitation.resent")
}
func (s *Store) ConfirmDeviceStorage(ctx context.Context, id, secret string) error {
	var key string
	e := s.tx(ctx, func(tx *sql.Tx) error {
		return noRows(tx.QueryRowContext(ctx, "SELECT COALESCE(key_id,'') FROM "+s.temp+".devices WHERE id=$1 AND secret_hash=$2", id, core.Hash(secret)).Scan(&key))
	})
	if e != nil {
		return e
	}
	return s.ConfirmDevice(ctx, id, secret, key)
}
func (s *Store) RevokeOwnKey(ctx context.Context, raw, id string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		var org, user string
		var revoked bool
		if e := tx.QueryRowContext(ctx, "SELECT org_id,user_id,revoked FROM api_keys WHERE id=$1 AND hash=$2", id, core.Hash(raw)).Scan(&org, &user, &revoked); e != nil {
			if e == sql.ErrNoRows {
				return ErrForbidden
			}
			return e
		}
		if e := s.lock(ctx, tx, org); e != nil {
			return e
		}
		res, e := tx.ExecContext(ctx, "UPDATE api_keys SET revoked=true WHERE id=$1 AND hash=$2 AND NOT revoked", id, core.Hash(raw))
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return nil
		}
		return s.audit(ctx, tx, org, user, "key.revoked", id)
	})
}

// Management may inspect encrypted configuration for reconnect, but this method
// is not a credential source for provider I/O. Inference uses GetPrivateVersion.
func (s *Store) GetPrivateVersionForManagement(ctx context.Context, sc core.Scope, kind, id string) (value []byte, version string, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		var data []byte
		if e := tx.QueryRowContext(ctx, "SELECT value,version FROM private_data WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4", sc.OrganizationID, sc.UserID, kind, id).Scan(&data, &version); e != nil {
			return noRows(e)
		}
		var err error
		value, err = s.keys.Decrypt(data, ownership(sc, kind, id))
		return err
	})
	return
}

// CompareAndSwapScopePrivate preserves uncertain refresh state across metadata
// edits. Only explicit reconnect (CompleteProviderFlowBound) clears uncertainty.
func (s *Store) CompareAndSwapScopePrivate(ctx context.Context, sc core.Scope, kind, id, version string, value []byte) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		v, e := s.keys.Encrypt(value, ownership(sc, kind, id))
		if e != nil {
			return e
		}
		next := core.ID()
		res, e := tx.ExecContext(ctx, "UPDATE private_data SET value=$6,version=$7 WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4 AND version=$5", sc.OrganizationID, sc.UserID, kind, id, version, v, next)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		if _, e = tx.ExecContext(ctx, "UPDATE private_refresh SET version=$6,state='uncertain' WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4 AND version=$5", sc.OrganizationID, sc.UserID, kind, id, version, next); e != nil {
			return e
		}
		return s.audit(ctx, tx, sc.OrganizationID, sc.UserID, "provider.configuration_changed", id)
	})
}
