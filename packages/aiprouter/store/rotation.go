// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"github.com/delinoio/oss/packages/aiprouter/core"
)

// RotateEncryption runs during maintenance with both old and new keys loaded.
// It re-authenticates every ciphertext before committing. Keep retired keys for
// old backups. No caller may remove old keys before this transaction succeeds.
func (s *Store) RotateEncryption(ctx context.Context) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		for _, table := range []string{"private_data", s.temp + ".states", s.temp + ".devices", s.temp + ".mail_tokens", s.temp + ".login_states"} {
			if _, e := tx.ExecContext(ctx, "LOCK TABLE "+table+" IN ACCESS EXCLUSIVE MODE"); e != nil {
				return e
			}
		}
		type row struct {
			org, user, kind, id string
			generation          int64
			value               []byte
		}
		for _, table := range []string{"private_data", s.temp + ".states"} {
			rs, e := tx.QueryContext(ctx, "SELECT d.org_id,d.user_id,d.kind,d.id,m.generation,d.value FROM "+table+" d JOIN memberships m ON m.org_id=d.org_id AND m.user_id=d.user_id")
			if e != nil {
				return e
			}
			var rows []row
			for rs.Next() {
				var r row
				if e = rs.Scan(&r.org, &r.user, &r.kind, &r.id, &r.generation, &r.value); e != nil {
					rs.Close()
					return e
				}
				rows = append(rows, r)
			}
			e = rs.Err()
			rs.Close()
			if e != nil {
				return e
			}
			for _, r := range rows {
				kind := r.kind
				if table != "private_data" {
					kind = "state:" + kind
				}
				aad := ownership(core.Scope{OrganizationID: r.org, UserID: r.user, Generation: r.generation}, kind, r.id)
				p, e := s.keys.Decrypt(r.value, aad)
				if e != nil {
					return e
				}
				v, e := s.keys.Encrypt(p, aad)
				if e != nil {
					return e
				}
				if _, e = tx.ExecContext(ctx, "UPDATE "+table+" SET value=$5 WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4", r.org, r.user, r.kind, r.id, v); e != nil {
					return e
				}
			}
		}

		// Envelope version names can change ciphertext size. Recompute retained
		// bytes and apply the same oldest-first quotas before completing rotation.
		if _, e := tx.ExecContext(ctx, "UPDATE "+s.temp+".states SET size=octet_length(value)"); e != nil {
			return e
		}
		for _, kind := range []core.StateKind{core.Responses, core.Reasoning, core.Search} {
			_, count, max, e := kind.Limits()
			if e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, "DELETE FROM "+s.temp+".states WHERE (org_id,user_id,kind,id) IN (SELECT org_id,user_id,kind,id FROM (SELECT org_id,user_id,kind,id,row_number() OVER(PARTITION BY org_id,user_id ORDER BY created_at DESC,id DESC) n,sum(size) OVER(PARTITION BY org_id,user_id ORDER BY created_at DESC,id DESC) bytes FROM "+s.temp+".states WHERE kind=$1) ranked WHERE n>$2 OR bytes>$3)", kind, count, max); e != nil {
				return e
			}
		}
		for _, spec := range []struct{ table, column, prefix string }{{s.temp + ".devices", "id", "device/"}, {s.temp + ".mail_tokens", "invitation_id", "invitation/"}, {s.temp + ".login_states", "encode(hash,'hex')", "loginhash/"}} {
			rs, e := tx.QueryContext(ctx, "SELECT "+spec.column+",value FROM "+spec.table+" WHERE value IS NOT NULL")
			if e != nil {
				return e
			}
			var rows []row
			for rs.Next() {
				var r row
				if e = rs.Scan(&r.id, &r.value); e != nil {
					rs.Close()
					return e
				}
				rows = append(rows, r)
			}
			e = rs.Err()
			rs.Close()
			if e != nil {
				return e
			}
			for _, r := range rows {
				p, e := s.keys.Decrypt(r.value, spec.prefix+r.id)
				if e != nil {
					return e
				}
				v, e := s.keys.Encrypt(p, spec.prefix+r.id)
				if e != nil {
					return e
				}
				col := spec.column
				var id any = r.id
				if spec.prefix == "loginhash/" {
					col = "hash"
					id, e = hex.DecodeString(r.id)
					if e != nil {
						return e
					}
				}
				if _, e = tx.ExecContext(ctx, "UPDATE "+spec.table+" SET value=$2 WHERE "+col+"=$1", id, v); e != nil {
					return e
				}
			}
		}
		if e := s.rotateClientContexts(ctx, tx); e != nil {
			return e
		}
		return s.rotateProviderState(ctx, tx)
	})
}
