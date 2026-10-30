// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"github.com/delinoio/oss/packages/tairouter/core"
	"time"
)

type Key struct {
	ID, Name, Prefix string
	CreatedAt        time.Time
	LastUsedAt       *time.Time
	Device           bool
	ExpiresAt        *time.Time
	Revoked          bool
}
type Principal struct {
	// AccountID and CredentialVersion explicitly bind provider state to selected credentials.
	AccountID, CredentialVersion string
	Scope                        core.Scope
	KeyID                        string
}
type Lease struct {
	Principal Principal
	ID        string
}

func (s *Store) issueKey(ctx context.Context, tx *sql.Tx, sc core.Scope, name string, expires *time.Time) (k Key, raw string, e error) {
	k = Key{ID: core.ID(), Name: name, ExpiresAt: expires}
	raw = "aip_" + core.Token()
	k.Prefix = raw[:12]
	var valid bool
	if e = tx.QueryRowContext(ctx, "SELECT $1::timestamptz IS NULL OR $1>clock_timestamp()", expires).Scan(&valid); e != nil {
		return
	}
	if !valid {
		e = ErrExpired
		return
	}
	e = tx.QueryRowContext(ctx, "INSERT INTO api_keys(id,org_id,user_id,generation,name,hash,expires_at,prefix) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at", k.ID, sc.OrganizationID, sc.UserID, sc.Generation, name, core.Hash(raw), expires, k.Prefix).Scan(&k.CreatedAt)
	if e == nil {
		e = s.audit(ctx, tx, sc.OrganizationID, sc.UserID, "key.issued", k.ID)
	}
	return
}
func (s *Store) IssueKey(ctx context.Context, sc core.Scope, name string, expires *time.Time) (k Key, raw string, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		var err error
		k, raw, err = s.issueKey(ctx, tx, sc, name, expires)
		return err
	})
	return
}
func (s *Store) keyPrincipal(ctx context.Context, tx *sql.Tx, raw string) (p Principal, e error) {
	e = tx.QueryRowContext(ctx, "SELECT org_id,user_id,generation,id FROM api_keys WHERE hash=$1", core.Hash(raw)).Scan(&p.Scope.OrganizationID, &p.Scope.UserID, &p.Scope.Generation, &p.KeyID)
	if e != nil {
		return p, noRows(e)
	}
	e = s.checkPrincipal(ctx, tx, p)
	return
}
func (s *Store) checkPrincipal(ctx context.Context, tx *sql.Tx, p Principal) error {
	if _, e := s.guard(ctx, tx, p.Scope); e != nil {
		return e
	}
	var ok bool
	e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM api_keys WHERE id=$1 AND org_id=$2 AND user_id=$3 AND generation=$4 AND NOT revoked AND (expires_at IS NULL OR expires_at>clock_timestamp()) AND (handed_off OR handoff_deadline>clock_timestamp()))`, p.KeyID, p.Scope.OrganizationID, p.Scope.UserID, p.Scope.Generation).Scan(&ok)
	if e != nil {
		return e
	}
	if !ok {
		return ErrForbidden
	}
	return nil
}
func (s *Store) AuthenticateKey(ctx context.Context, raw string) (p Principal, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error { var err error; p, err = s.keyPrincipal(ctx, tx, raw); return err })
	return
}
func (s *Store) ListKeys(ctx context.Context, sc core.Scope) (out []Key, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		rows, e := tx.QueryContext(ctx, "SELECT id,name,expires_at,revoked,created_at,prefix,last_used_at,device FROM api_keys WHERE org_id=$1 AND user_id=$2 ORDER BY created_at", sc.OrganizationID, sc.UserID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var k Key
			if e = rows.Scan(&k.ID, &k.Name, &k.ExpiresAt, &k.Revoked, &k.CreatedAt, &k.Prefix, &k.LastUsedAt, &k.Device); e != nil {
				return e
			}
			out = append(out, k)
		}
		return rows.Err()
	})
	return
}
func (s *Store) RevokeKey(ctx context.Context, sc core.Scope, id string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		res, e := tx.ExecContext(ctx, "UPDATE api_keys SET revoked=true WHERE id=$1 AND org_id=$2 AND user_id=$3", id, sc.OrganizationID, sc.UserID)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return ErrNotFound
		}
		return s.audit(ctx, tx, sc.OrganizationID, sc.UserID, "key.revoked", id)
	})
}
func (s *Store) RevokeCurrentKey(ctx context.Context, raw string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		p, e := s.keyPrincipal(ctx, tx, raw)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE api_keys SET revoked=true WHERE id=$1", p.KeyID)
		if e != nil {
			return e
		}
		return s.audit(ctx, tx, p.Scope.OrganizationID, p.Scope.UserID, "key.revoked", p.KeyID)
	})
}

// Keep historical admission IDs for idempotency without scanning released history
// while holding the organization lock. statement_timestamp is an indexable bound
// captured after lock acquisition, unlike transaction time or volatile clock time.
const admissionCountsQuery = `SELECT recent.requests,
 (SELECT count(*) FROM admissions WHERE org_id=$1 AND user_id=$2 AND NOT released),
 COALESCE(EXTRACT(EPOCH FROM recent.first_at+interval '1 minute'-statement_timestamp()),1)
 FROM (SELECT count(*) AS requests,min(created_at) AS first_at FROM admissions
 WHERE org_id=$1 AND user_id=$2 AND created_at>statement_timestamp()-interval '1 minute') recent`

func (s *Store) Admit(ctx context.Context, raw string) (l Lease, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		p, e := s.keyPrincipal(ctx, tx, raw)
		if e != nil {
			return e
		}
		if e = s.guardInstance(ctx, tx, s.instanceID, true); e != nil {
			return e
		}
		var rpm, concurrent, n, active int
		var retry float64
		e = tx.QueryRowContext(ctx, `SELECT rpm,concurrency FROM settings WHERE singleton`).Scan(&rpm, &concurrent)
		if e != nil {
			return e
		}
		e = tx.QueryRowContext(ctx, admissionCountsQuery, p.Scope.OrganizationID, p.Scope.UserID).Scan(&n, &active, &retry)
		if e != nil {
			return e
		}
		if n >= rpm || active >= concurrent {
			if retry < 1 {
				retry = 1
			}
			return &LimitError{time.Duration(retry * float64(time.Second))}
		}
		l = Lease{p, core.ID()}
		_, e = tx.ExecContext(ctx, "INSERT INTO admissions(id,org_id,user_id,key_id,generation,instance_id) VALUES($1,$2,$3,$4,$5,$6)", l.ID, p.Scope.OrganizationID, p.Scope.UserID, p.KeyID, p.Scope.Generation, s.instanceID)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE api_keys SET last_used_at=clock_timestamp() WHERE id=$1", p.KeyID)
		return e
	})
	return
}
func (s *Store) checkLease(ctx context.Context, tx *sql.Tx, l Lease) error {
	if e := s.checkPrincipal(ctx, tx, l.Principal); e != nil {
		return e
	}
	var ok bool
	e := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM admissions WHERE id=$1 AND org_id=$2 AND user_id=$3 AND key_id=$4 AND generation=$5 AND NOT released)", l.ID, l.Principal.Scope.OrganizationID, l.Principal.Scope.UserID, l.Principal.KeyID, l.Principal.Scope.Generation).Scan(&ok)
	if e != nil {
		return e
	}
	if !ok {
		return ErrForbidden
	}
	var instance string
	if e := tx.QueryRowContext(ctx, "SELECT instance_id FROM admissions WHERE id=$1", l.ID).Scan(&instance); e != nil {
		return noRows(e)
	}
	return s.guardInstance(ctx, tx, instance, false)
}

// checkDispatchLease additionally fences new physical sends after drain. Ordinary
// lease validation remains available to finalize work that was already sent.
func (s *Store) checkDispatchLease(ctx context.Context, tx *sql.Tx, l Lease) error {
	if e := s.checkLease(ctx, tx, l); e != nil {
		return e
	}
	if e := s.checkNativeHealthBindings(ctx, tx, l, dispatchBindings(ctx)); e != nil {
		return e
	}
	var instance string
	if e := tx.QueryRowContext(ctx, "SELECT instance_id FROM admissions WHERE id=$1", l.ID).Scan(&instance); e != nil {
		return noRows(e)
	}
	return s.guardInstance(ctx, tx, instance, true)
}
func (s *Store) CheckDispatch(ctx context.Context, l Lease) error {
	return s.tx(ctx, func(tx *sql.Tx) error { return s.checkDispatchLease(ctx, tx, l) })
}

// WithDispatch serializes send initiation with revocation and membership deletion.
// start must initiate synchronously, respect ctx, and return before reading a stream.
// Never release a slot on a timeout alone: a remote request can still be executing.
func (s *Store) WithDispatch(ctx context.Context, l Lease, start func(context.Context) error) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkDispatchLease(ctx, tx, l); e != nil {
			return e
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		return start(ctx)
	})
}
func (s *Store) Release(ctx context.Context, l Lease) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.lock(ctx, tx, l.Principal.Scope.OrganizationID); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, "UPDATE admissions SET released=true WHERE id=$1 AND org_id=$2 AND user_id=$3 AND key_id=$4 AND generation=$5", l.ID, l.Principal.Scope.OrganizationID, l.Principal.Scope.UserID, l.Principal.KeyID, l.Principal.Scope.Generation)
		if e != nil {
			return e
		}
		if e = s.releaseAdmissionHealth(ctx, tx, l); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "DELETE FROM mistral_waiters WHERE admission_id=$1 AND org_id=$2 AND user_id=$3 AND key_id=$4 AND generation=$5", l.ID, l.Principal.Scope.OrganizationID, l.Principal.Scope.UserID, l.Principal.KeyID, l.Principal.Scope.Generation)
		return e
	})
}
