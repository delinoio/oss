// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/core"
)

// Private data is always encrypted, including routing and pool configuration.
// Callers must resolve embedded references with the same Scope, never by ID alone.
func (s *Store) PutPrivate(ctx context.Context, sc core.Scope, kind, id string, value []byte) error {
	if kind == "" || id == "" {
		return ErrConflict
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		v, e := s.keys.Encrypt(value, ownership(sc, kind, id))
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO private_data(org_id,user_id,kind,id,value,version) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(org_id,user_id,kind,id) DO UPDATE SET value=excluded.value,version=excluded.version", sc.OrganizationID, sc.UserID, kind, id, v, core.ID())
		if e != nil {
			return e
		}
		return s.audit(ctx, tx, sc.OrganizationID, sc.UserID, "provider.configuration_changed", id)
	})
}
func (s *Store) GetPrivate(ctx context.Context, sc core.Scope, kind, id string) (v []byte, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		if e := s.privateUsable(ctx, tx, sc, kind, id); e != nil {
			return e
		}
		var b []byte
		if e := tx.QueryRowContext(ctx, "SELECT value FROM private_data WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4", sc.OrganizationID, sc.UserID, kind, id).Scan(&b); e != nil {
			return noRows(e)
		}
		var err error
		v, err = s.keys.Decrypt(b, ownership(sc, kind, id))
		return err
	})
	return
}
func (s *Store) ListPrivate(ctx context.Context, sc core.Scope, kind string) (ids []string, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		rows, e := tx.QueryContext(ctx, "SELECT id FROM private_data WHERE org_id=$1 AND user_id=$2 AND kind=$3 ORDER BY id", sc.OrganizationID, sc.UserID, kind)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				return e
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	return
}
func (s *Store) DeletePrivate(ctx context.Context, sc core.Scope, kind, id string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, "DELETE FROM private_data WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4", sc.OrganizationID, sc.UserID, kind, id)
		if e != nil {
			return e
		}
		return s.audit(ctx, tx, sc.OrganizationID, sc.UserID, "provider.configuration_deleted", id)
	})
}
func (s *Store) PutState(ctx context.Context, sc core.Scope, kind core.StateKind, id string, value []byte) error {
	ttl, count, max, e := kind.Limits()
	if e != nil {
		return e
	}
	if int64(len(value)) > max {
		return ErrConflict
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		v, e := s.keys.Encrypt(value, ownership(sc, "state:"+string(kind), id))
		if e != nil {
			return e
		}
		if int64(len(v)) > max {
			return ErrConflict
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO "+s.temp+".states(org_id,user_id,kind,id,value,size,expires_at) VALUES($1,$2,$3,$4,$5,$6,clock_timestamp()+$7*interval '1 second') ON CONFLICT(org_id,user_id,kind,id) DO UPDATE SET value=excluded.value,size=excluded.size,created_at=clock_timestamp(),expires_at=excluded.expires_at", sc.OrganizationID, sc.UserID, kind, id, v, len(v), ttl.Seconds())
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "DELETE FROM "+s.temp+".states WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND (expires_at<=clock_timestamp() OR id IN (SELECT id FROM (SELECT id,row_number() OVER(ORDER BY created_at DESC,id DESC) AS n,sum(size) OVER(ORDER BY created_at DESC,id DESC) AS bytes FROM "+s.temp+".states WHERE org_id=$1 AND user_id=$2 AND kind=$3) q WHERE n>$4 OR bytes>$5))", sc.OrganizationID, sc.UserID, kind, count, max)
		return e
	})
}
func (s *Store) GetState(ctx context.Context, sc core.Scope, kind core.StateKind, id string) (v []byte, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		var b []byte
		if e := tx.QueryRowContext(ctx, "SELECT value FROM "+s.temp+".states WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4 AND expires_at>clock_timestamp()", sc.OrganizationID, sc.UserID, kind, id).Scan(&b); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return ErrContinuation
			}
			return e
		}
		var err error
		v, err = s.keys.Decrypt(b, ownership(sc, "state:"+string(kind), id))
		return err
	})
	return
}
func (s *Store) DeleteState(ctx context.Context, sc core.Scope, kind core.StateKind, id string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, "DELETE FROM "+s.temp+".states WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4", sc.OrganizationID, sc.UserID, kind, id)
		return e
	})
}
