// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"github.com/delinoio/oss/packages/tairouter/core"
)

// Claims do not expire. A lost rotating exchange cannot safely reuse its token.
// Explicit management replacement changes the version and permits a new claim.
func (s *Store) ClaimPrivateRefresh(ctx context.Context, l Lease, kind, id string) (value []byte, version, claim string, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkLease(ctx, tx, l); e != nil {
			return e
		}
		sc := l.Principal.Scope
		var data []byte
		if e := tx.QueryRowContext(ctx, "SELECT value,version FROM private_data WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4", sc.OrganizationID, sc.UserID, kind, id).Scan(&data, &version); e != nil {
			return noRows(e)
		}
		if _, e := tx.ExecContext(ctx, "DELETE FROM private_refresh WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4 AND version<>$5", sc.OrganizationID, sc.UserID, kind, id, version); e != nil {
			return e
		}
		var state string
		e := tx.QueryRowContext(ctx, "SELECT state FROM private_refresh WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4", sc.OrganizationID, sc.UserID, kind, id).Scan(&state)
		if e == nil {
			if state == "uncertain" {
				return ErrReauthentication
			}
			return ErrBusy
		}
		if e != sql.ErrNoRows {
			return e
		}
		value, e = s.keys.Decrypt(data, ownership(sc, kind, id))
		if e != nil {
			return e
		}
		claim = core.Token()
		_, e = tx.ExecContext(ctx, "INSERT INTO private_refresh(org_id,user_id,kind,id,version,claim_hash,state) VALUES($1,$2,$3,$4,$5,$6,'claimed')", sc.OrganizationID, sc.UserID, kind, id, version, core.Hash(claim))
		return e
	})
	return
}
func (s *Store) checkRefresh(ctx context.Context, tx *sql.Tx, l Lease, kind, id, claim string) error {
	if e := s.checkLease(ctx, tx, l); e != nil {
		return e
	}
	sc := l.Principal.Scope
	var state string
	e := tx.QueryRowContext(ctx, "SELECT r.state FROM private_refresh r JOIN private_data d USING(org_id,user_id,kind,id) WHERE r.org_id=$1 AND r.user_id=$2 AND r.kind=$3 AND r.id=$4 AND r.claim_hash=$5 AND r.version=d.version", sc.OrganizationID, sc.UserID, kind, id, core.Hash(claim)).Scan(&state)
	if e == sql.ErrNoRows {
		return ErrConflict
	}
	if e != nil {
		return e
	}
	if state == "uncertain" {
		return ErrReauthentication
	}
	return nil
}
func (s *Store) WithPrivateRefreshDispatch(ctx context.Context, l Lease, kind, id, claim string, start func(context.Context) error) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkDispatchLease(ctx, tx, l); e != nil {
			return e
		}
		if e := s.checkRefresh(ctx, tx, l, kind, id, claim); e != nil {
			return e
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		return start(ctx)
	})
}
func (s *Store) ReleasePrivateRefresh(ctx context.Context, l Lease, kind, id, claim string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkRefresh(ctx, tx, l, kind, id, claim); e != nil {
			return e
		}
		sc := l.Principal.Scope
		_, e := tx.ExecContext(ctx, "DELETE FROM private_refresh WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4", sc.OrganizationID, sc.UserID, kind, id)
		return e
	})
}
func (s *Store) CompletePrivateRefresh(ctx context.Context, l Lease, kind, id, version, claim string, value []byte) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkRefresh(ctx, tx, l, kind, id, claim); e != nil {
			return e
		}
		sc := l.Principal.Scope
		v, e := s.keys.Encrypt(value, ownership(sc, kind, id))
		if e != nil {
			return e
		}
		res, e := tx.ExecContext(ctx, "UPDATE private_data SET value=$6,version=$7 WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4 AND version=$5", sc.OrganizationID, sc.UserID, kind, id, version, v, core.ID())
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		_, e = tx.ExecContext(ctx, "DELETE FROM private_refresh WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4", sc.OrganizationID, sc.UserID, kind, id)
		return e
	})
}

// MarkPrivateRefreshUncertain can persist uncertainty after key revocation. It
// cannot save credentials or grant authority; the original membership and claim
// must still match. This prevents a rotated token from being reused by another key.
func (s *Store) MarkPrivateRefreshUncertain(ctx context.Context, l Lease, kind, id, claim string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		sc := l.Principal.Scope
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		res, e := tx.ExecContext(ctx, "UPDATE private_refresh r SET state='uncertain' FROM private_data d WHERE r.org_id=$1 AND r.user_id=$2 AND r.kind=$3 AND r.id=$4 AND r.claim_hash=$5 AND d.org_id=r.org_id AND d.user_id=r.user_id AND d.kind=r.kind AND d.id=r.id AND d.version=r.version", sc.OrganizationID, sc.UserID, kind, id, core.Hash(claim))
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
func (s *Store) privateUsable(ctx context.Context, tx *sql.Tx, sc core.Scope, kind, id string) error {
	var state string
	e := tx.QueryRowContext(ctx, "SELECT r.state FROM private_refresh r JOIN private_data d USING(org_id,user_id,kind,id) WHERE r.org_id=$1 AND r.user_id=$2 AND r.kind=$3 AND r.id=$4 AND r.version=d.version", sc.OrganizationID, sc.UserID, kind, id).Scan(&state)
	if e == sql.ErrNoRows {
		return nil
	}
	if e != nil {
		return e
	}
	if state == "uncertain" {
		return ErrReauthentication
	}
	return ErrBusy
}

func (s *Store) checkScopeRefreshAuthority(ctx context.Context, tx *sql.Tx, sc core.Scope) error {
	_, e := s.guard(ctx, tx, sc)
	return e
}
func (s *Store) ClaimScopePrivateRefresh(ctx context.Context, sc core.Scope, kind, id string) (value []byte, version, claim string, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkScopeRefreshAuthority(ctx, tx, sc); e != nil {
			return e
		}
		var data []byte
		if e := tx.QueryRowContext(ctx, "SELECT value,version FROM private_data WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4", sc.OrganizationID, sc.UserID, kind, id).Scan(&data, &version); e != nil {
			return noRows(e)
		}
		if _, e := tx.ExecContext(ctx, "DELETE FROM private_refresh WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4 AND version<>$5", sc.OrganizationID, sc.UserID, kind, id, version); e != nil {
			return e
		}
		var state string
		e := tx.QueryRowContext(ctx, "SELECT state FROM private_refresh WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4", sc.OrganizationID, sc.UserID, kind, id).Scan(&state)
		if e == nil {
			if state == "uncertain" {
				return ErrReauthentication
			}
			return ErrBusy
		}
		if e != sql.ErrNoRows {
			return e
		}
		value, e = s.keys.Decrypt(data, ownership(sc, kind, id))
		if e != nil {
			return e
		}
		claim = core.Token()
		_, e = tx.ExecContext(ctx, "INSERT INTO private_refresh(org_id,user_id,kind,id,version,claim_hash,state) VALUES($1,$2,$3,$4,$5,$6,'claimed')", sc.OrganizationID, sc.UserID, kind, id, version, core.Hash(claim))
		return e
	})
	return
}
func (s *Store) checkScopeRefresh(ctx context.Context, tx *sql.Tx, sc core.Scope, kind, id, claim string) error {
	if e := s.checkScopeRefreshAuthority(ctx, tx, sc); e != nil {
		return e
	}
	var state string
	e := tx.QueryRowContext(ctx, "SELECT r.state FROM private_refresh r JOIN private_data d USING(org_id,user_id,kind,id) WHERE r.org_id=$1 AND r.user_id=$2 AND r.kind=$3 AND r.id=$4 AND r.claim_hash=$5 AND r.version=d.version", sc.OrganizationID, sc.UserID, kind, id, core.Hash(claim)).Scan(&state)
	if e == sql.ErrNoRows {
		return ErrConflict
	}
	if e != nil {
		return e
	}
	if state == "uncertain" {
		return ErrReauthentication
	}
	return nil
}
func (s *Store) WithScopePrivateRefreshDispatch(ctx context.Context, sc core.Scope, kind, id, claim string, start func(context.Context) error) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkScopeRefresh(ctx, tx, sc, kind, id, claim); e != nil {
			return e
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		return start(ctx)
	})
}
func (s *Store) ReleaseScopePrivateRefresh(ctx context.Context, sc core.Scope, kind, id, claim string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkScopeRefresh(ctx, tx, sc, kind, id, claim); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, "DELETE FROM private_refresh WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4", sc.OrganizationID, sc.UserID, kind, id)
		return e
	})
}
func (s *Store) CompleteScopePrivateRefresh(ctx context.Context, sc core.Scope, kind, id, version, claim string, value []byte) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkScopeRefresh(ctx, tx, sc, kind, id, claim); e != nil {
			return e
		}
		v, e := s.keys.Encrypt(value, ownership(sc, kind, id))
		if e != nil {
			return e
		}
		res, e := tx.ExecContext(ctx, "UPDATE private_data SET value=$6,version=$7 WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4 AND version=$5", sc.OrganizationID, sc.UserID, kind, id, version, v, core.ID())
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		_, e = tx.ExecContext(ctx, "DELETE FROM private_refresh WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4", sc.OrganizationID, sc.UserID, kind, id)
		return e
	})
}

// MarkScopePrivateRefreshUncertain can persist uncertainty after key revocation. It
// cannot save credentials or grant authority; the original membership and claim
// must still match. This prevents a rotated token from being reused by another key.
func (s *Store) MarkScopePrivateRefreshUncertain(ctx context.Context, sc core.Scope, kind, id, claim string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		res, e := tx.ExecContext(ctx, "UPDATE private_refresh r SET state='uncertain' FROM private_data d WHERE r.org_id=$1 AND r.user_id=$2 AND r.kind=$3 AND r.id=$4 AND r.claim_hash=$5 AND d.org_id=r.org_id AND d.user_id=r.user_id AND d.kind=r.kind AND d.id=r.id AND d.version=r.version", sc.OrganizationID, sc.UserID, kind, id, core.Hash(claim))
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

func (s *Store) WithAccountDispatch(ctx context.Context, l Lease, id, version string, start func(context.Context) error) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkDispatchLease(ctx, tx, l); e != nil {
			return e
		}
		sc := l.Principal.Scope
		if e := s.checkFlowTarget(ctx, tx, sc, "provider", id, version); e != nil {
			return e
		}
		var state string
		e := tx.QueryRowContext(ctx, "SELECT state FROM private_refresh WHERE org_id=$1 AND user_id=$2 AND kind='provider' AND id=$3 AND version=$4", sc.OrganizationID, sc.UserID, id, version).Scan(&state)
		if e == nil {
			if state == "uncertain" {
				return ErrReauthentication
			}
			return ErrBusy
		}
		if e != sql.ErrNoRows {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		return start(ctx)
	})
}

// MarkPrivateReauthentication records a definitive authentication rejection for
// the credentials used by this lease. A late response cannot quarantine a newer
// credential version. Explicit replacement is required to restore usability.
func (s *Store) MarkPrivateReauthentication(ctx context.Context, l Lease, kind, id, expectedVersion string) error {
	if kind == "" || id == "" || expectedVersion == "" {
		return ErrConflict
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkLease(ctx, tx, l); e != nil {
			return e
		}
		sc := l.Principal.Scope
		var current string
		e := tx.QueryRowContext(ctx, "SELECT version FROM private_data WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4", sc.OrganizationID, sc.UserID, kind, id).Scan(&current)
		if e == sql.ErrNoRows {
			return ErrConflict
		}
		if e != nil {
			return e
		}
		if current != expectedVersion {
			return ErrConflict
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO private_refresh(org_id,user_id,kind,id,version,claim_hash,state) VALUES($1,$2,$3,$4,$5,$6,'uncertain') ON CONFLICT(org_id,user_id,kind,id) DO UPDATE SET version=excluded.version,claim_hash=excluded.claim_hash,state='uncertain'", sc.OrganizationID, sc.UserID, kind, id, expectedVersion, core.Hash(core.Token()))
		return e
	})
}
