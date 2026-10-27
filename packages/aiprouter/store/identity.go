// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"strings"
)

type User struct{ ID, Issuer, Subject, Email string }
type Organization struct {
	ID, Name string
	Role     core.Role
}
type LoginState struct{ State, Binding, Nonce, Verifier string }

// RegisterIdentity accepts claims only after the server verifies the OIDC token.
func (s *Store) RegisterIdentity(ctx context.Context, issuer, subject, email string, verified bool) (u User, e error) {
	if issuer == "" || subject == "" || strings.TrimSpace(email) == "" || !verified {
		return u, ErrForbidden
	}
	e = s.tx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `INSERT INTO users(id,issuer,subject,email) VALUES($1,$2,$3,$4) ON CONFLICT(issuer,subject) DO UPDATE SET email=excluded.email RETURNING id,issuer,subject,email`, core.ID(), issuer, subject, email).Scan(&u.ID, &u.Issuer, &u.Subject, &u.Email)
	})
	return
}
func (s *Store) CreateSession(ctx context.Context, userID string) (raw string, e error) {
	raw = core.Token()
	e = s.tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "INSERT INTO sessions(hash,user_id) VALUES($1,$2)", core.Hash(raw), userID)
		return e
	})
	return
}
func (s *Store) AuthenticateSession(ctx context.Context, raw string) (u User, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		return noRows(tx.QueryRowContext(ctx, `WITH touched AS (UPDATE sessions SET last_seen=clock_timestamp() WHERE hash=$1 AND last_seen>clock_timestamp()-interval '24 hours' AND created_at>clock_timestamp()-interval '7 days' RETURNING user_id) SELECT u.id,u.issuer,u.subject,u.email FROM users u JOIN touched t ON t.user_id=u.id`, core.Hash(raw)).Scan(&u.ID, &u.Issuer, &u.Subject, &u.Email))
	})
	return
}
func (s *Store) Logout(ctx context.Context, raw string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "DELETE FROM sessions WHERE hash=$1", core.Hash(raw))
		return e
	})
}
func (s *Store) SaveLoginState(ctx context.Context, l LoginState) error {
	if l.State == "" || l.Binding == "" || l.Verifier == "" || l.Nonce == "" {
		return ErrForbidden
	}
	v, e := s.keys.Encrypt([]byte(l.Verifier), "loginhash/"+hex.EncodeToString(core.Hash(l.State)))
	if e != nil {
		return e
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "INSERT INTO "+s.temp+".login_states(hash,binding,nonce,value) VALUES($1,$2,$3,$4)", core.Hash(l.State), core.Hash(l.Binding), l.Nonce, v)
		return e
	})
}
func (s *Store) ConsumeLoginState(ctx context.Context, state, binding string) (l LoginState, e error) {
	l.State = state
	l.Binding = binding
	e = s.tx(ctx, func(tx *sql.Tx) error {
		var b []byte
		e := tx.QueryRowContext(ctx, "DELETE FROM "+s.temp+".login_states WHERE hash=$1 AND binding=$2 AND expires_at>clock_timestamp() RETURNING nonce,value", core.Hash(state), core.Hash(binding)).Scan(&l.Nonce, &b)
		if e != nil {
			return noRows(e)
		}
		p, e := s.keys.Decrypt(b, "loginhash/"+hex.EncodeToString(core.Hash(state)))
		l.Verifier = string(p)
		return e
	})
	return
}
func (s *Store) CreateOrganization(ctx context.Context, userID, name string) (o Organization, e error) {
	o = Organization{core.ID(), name, core.Owner}
	if strings.TrimSpace(name) == "" {
		return o, ErrConflict
	}
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "INSERT INTO organizations(id,name) VALUES($1,$2)", o.ID, name); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, "INSERT INTO memberships(org_id,user_id,role) VALUES($1,$2,'owner')", o.ID, userID); e != nil {
			return e
		}
		return s.audit(ctx, tx, o.ID, userID, "organization.created", o.ID)
	})
	return
}
func (s *Store) ListOrganizations(ctx context.Context, userID string) (out []Organization, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		rows, e := tx.QueryContext(ctx, "SELECT o.id,o.name,m.role FROM organizations o JOIN memberships m ON m.org_id=o.id WHERE m.user_id=$1 ORDER BY o.id", userID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var o Organization
			if e = rows.Scan(&o.ID, &o.Name, &o.Role); e != nil {
				return e
			}
			out = append(out, o)
		}
		return rows.Err()
	})
	return
}
func (s *Store) Membership(ctx context.Context, org, user string) (sc core.Scope, role core.Role, e error) {
	sc.OrganizationID = org
	sc.UserID = user
	e = s.tx(ctx, func(tx *sql.Tx) error {
		return noRows(tx.QueryRowContext(ctx, "SELECT generation,role FROM memberships WHERE org_id=$1 AND user_id=$2", org, user).Scan(&sc.Generation, &role))
	})
	return
}
func (s *Store) ChangeRole(ctx context.Context, sc core.Scope, user string, role core.Role) error {
	if role != core.Member && role != core.Administrator {
		return ErrForbidden
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		r, e := s.guard(ctx, tx, sc)
		if e != nil {
			return e
		}
		if r != core.Owner || user == sc.UserID {
			return ErrForbidden
		}
		res, e := tx.ExecContext(ctx, "UPDATE memberships SET role=$3 WHERE org_id=$1 AND user_id=$2 AND role<>'owner'", sc.OrganizationID, user, role)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return ErrNotFound
		}
		return s.audit(ctx, tx, sc.OrganizationID, sc.UserID, "membership.role_changed", user)
	})
}
func (s *Store) remove(ctx context.Context, tx *sql.Tx, org, user string) error {
	if _, e := tx.ExecContext(ctx, "UPDATE api_keys SET revoked=true WHERE org_id=$1 AND user_id=$2", org, user); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, "DELETE FROM transfers WHERE org_id=$1 AND (from_user=$2 OR to_user=$2)", org, user); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, "DELETE FROM "+s.temp+".devices WHERE org_id=$1 AND user_id=$2", org, user); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, "DELETE FROM memberships WHERE org_id=$1 AND user_id=$2", org, user)
	return e
}
func (s *Store) RemoveMember(ctx context.Context, sc core.Scope, user string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		r, e := s.guard(ctx, tx, sc)
		if e != nil {
			return e
		}
		var target core.Role
		if e = tx.QueryRowContext(ctx, "SELECT role FROM memberships WHERE org_id=$1 AND user_id=$2", sc.OrganizationID, user).Scan(&target); e != nil {
			return noRows(e)
		}
		if target == core.Owner || user != sc.UserID && (r == core.Member || r == core.Administrator && target != core.Member) {
			return ErrForbidden
		}
		if e = s.remove(ctx, tx, sc.OrganizationID, user); e != nil {
			return e
		}
		action := "membership.removed"
		if user == sc.UserID {
			action = "membership.left"
		}
		return s.audit(ctx, tx, sc.OrganizationID, sc.UserID, action, user)
	})
}
func (s *Store) RequestTransfer(ctx context.Context, sc core.Scope, target string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		r, e := s.guard(ctx, tx, sc)
		if e != nil {
			return e
		}
		if r != core.Owner || target == sc.UserID {
			return ErrForbidden
		}
		var ok bool
		if e = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM memberships WHERE org_id=$1 AND user_id=$2)", sc.OrganizationID, target).Scan(&ok); e != nil {
			return e
		}
		if !ok {
			return ErrNotFound
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO transfers(org_id,from_user,to_user,expires_at,id) VALUES($1,$2,$3,clock_timestamp()+interval '7 days',$4) ON CONFLICT(org_id) DO UPDATE SET from_user=excluded.from_user,to_user=excluded.to_user,expires_at=excluded.expires_at,id=excluded.id", sc.OrganizationID, sc.UserID, target, core.ID())
		if e != nil {
			return e
		}
		return s.audit(ctx, tx, sc.OrganizationID, sc.UserID, "ownership.nominated", target)
	})
}
func (s *Store) CancelTransfer(ctx context.Context, sc core.Scope) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		r, e := s.guard(ctx, tx, sc)
		if e != nil {
			return e
		}
		if r != core.Owner {
			return ErrForbidden
		}
		_, e = tx.ExecContext(ctx, "DELETE FROM transfers WHERE org_id=$1", sc.OrganizationID)
		if e != nil {
			return e
		}
		return s.audit(ctx, tx, sc.OrganizationID, sc.UserID, "ownership.canceled", sc.OrganizationID)
	})
}
func (s *Store) AcceptTransfer(ctx context.Context, sc core.Scope) error {
	return s.acceptTransfer(ctx, sc, "")
}
func (s *Store) AcceptTransferID(ctx context.Context, sc core.Scope, id string) error {
	if id == "" {
		return ErrNotFound
	}
	return s.acceptTransfer(ctx, sc, id)
}
func (s *Store) acceptTransfer(ctx context.Context, sc core.Scope, id string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		var from string
		if e := tx.QueryRowContext(ctx, "SELECT from_user FROM transfers WHERE org_id=$1 AND to_user=$2 AND expires_at>clock_timestamp() AND ($3='' OR id=$3)", sc.OrganizationID, sc.UserID, id).Scan(&from); e != nil {
			return noRows(e)
		}
		res, e := tx.ExecContext(ctx, "UPDATE memberships SET role='administrator' WHERE org_id=$1 AND user_id=$2 AND role='owner'", sc.OrganizationID, from)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrForbidden
		}
		if _, e = tx.ExecContext(ctx, "UPDATE memberships SET role='owner' WHERE org_id=$1 AND user_id=$2", sc.OrganizationID, sc.UserID); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM transfers WHERE org_id=$1", sc.OrganizationID); e != nil {
			return e
		}
		return s.audit(ctx, tx, sc.OrganizationID, sc.UserID, "ownership.accepted", sc.UserID)
	})
}
func (s *Store) DeleteOrganization(ctx context.Context, sc core.Scope) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		r, e := s.guard(ctx, tx, sc)
		if e != nil {
			return e
		}
		if r != core.Owner {
			return ErrForbidden
		}
		_, e = tx.ExecContext(ctx, "DELETE FROM organizations WHERE id=$1", sc.OrganizationID)
		return e
	})
}
func (s *Store) DeleteAccount(ctx context.Context, user string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		var locked string
		if e := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", user).Scan(&locked); e != nil {
			return noRows(e)
		}
		rows, e := tx.QueryContext(ctx, "SELECT org_id FROM memberships WHERE user_id=$1 ORDER BY org_id", user)
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
		var owns bool
		if e = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM memberships WHERE user_id=$1 AND role='owner')", user).Scan(&owns); e != nil {
			return e
		}
		if owns {
			return ErrForbidden
		}
		for _, org := range orgs {
			if e = s.remove(ctx, tx, org, user); e != nil {
				return e
			}
		}
		if _, e = tx.ExecContext(ctx, "UPDATE audits SET target_id='' WHERE target_id=$1", user); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM admissions WHERE user_id=$1", user); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM "+s.temp+".devices WHERE user_id=$1", user); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "DELETE FROM users WHERE id=$1", user)
		return e
	})
}
