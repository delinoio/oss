// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// CompactionHandoff contains only the physical route description, never credentials.
// The caller validates model length and current route/pool configuration before reuse.
type CompactionHandoff struct {
	AccountID, CredentialVersion string
	Data                         []byte
	Bindings                     []PrivateBinding
}

func validHandoffLane(lane string) bool { return lane != "" && len(lane) <= 512 }
func (s *Store) PutCompactionHandoff(ctx context.Context, l Lease, lane string, value CompactionHandoff) error {
	value.Bindings = append(append([]PrivateBinding(nil), dispatchBindings(ctx)...), value.Bindings...)
	if !validHandoffLane(lane) || len(value.Data) > 4096 || value.AccountID == "" || value.CredentialVersion == "" {
		return ErrConflict
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkNativeAccount(ctx, tx, l, value.AccountID, value.CredentialVersion); e != nil {
			return e
		}
		if e := s.checkNativeHealthBindings(ctx, tx, l, value.Bindings); e != nil {
			return e
		}
		raw, e := json.Marshal(value)
		if e != nil {
			return e
		}
		b, e := s.keys.Encrypt(raw, stateAAD(l.Principal, "compaction-handoff", lane))
		if e != nil {
			return e
		}
		sc := l.Principal.Scope
		_, e = tx.ExecContext(ctx, "INSERT INTO "+s.temp+".compaction_handoffs(org_id,user_id,key_id,id,generation,value,size,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,clock_timestamp()+interval '24 hours') ON CONFLICT(org_id,user_id,key_id,id) DO UPDATE SET generation=excluded.generation,value=excluded.value,size=excluded.size,last_used_at=clock_timestamp(),expires_at=excluded.expires_at", sc.OrganizationID, sc.UserID, l.Principal.KeyID, lane, sc.Generation, b, len(b))
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "DELETE FROM "+s.temp+".compaction_handoffs WHERE org_id=$1 AND user_id=$2 AND (expires_at<=clock_timestamp() OR (key_id,id) IN(SELECT key_id,id FROM "+s.temp+".compaction_handoffs WHERE org_id=$1 AND user_id=$2 ORDER BY last_used_at DESC,key_id DESC,id DESC OFFSET 2048))", sc.OrganizationID, sc.UserID)
		return e
	})
}
func (s *Store) GetCompactionHandoff(ctx context.Context, l Lease, lane string) (out CompactionHandoff, e error) {
	if !validHandoffLane(lane) {
		return out, ErrConflict
	}
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkLease(ctx, tx, l); e != nil {
			return e
		}
		sc := l.Principal.Scope
		var raw []byte
		e := tx.QueryRowContext(ctx, "SELECT value FROM "+s.temp+".compaction_handoffs WHERE org_id=$1 AND user_id=$2 AND key_id=$3 AND id=$4 AND generation=$5 AND expires_at>clock_timestamp()", sc.OrganizationID, sc.UserID, l.Principal.KeyID, lane, sc.Generation).Scan(&raw)
		if e != nil {
			return noRows(e)
		}
		b, e := s.keys.Decrypt(raw, stateAAD(l.Principal, "compaction-handoff", lane))
		if e != nil {
			return e
		}
		if e = json.Unmarshal(b, &out); e != nil {
			return e
		}
		if e = s.checkNativeAccount(ctx, tx, l, out.AccountID, out.CredentialVersion); e != nil {
			return e
		}
		if e := s.checkNativeHealthBindings(ctx, tx, l, out.Bindings); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE "+s.temp+".compaction_handoffs SET last_used_at=clock_timestamp(),expires_at=clock_timestamp()+interval '24 hours' WHERE org_id=$1 AND user_id=$2 AND key_id=$3 AND id=$4", sc.OrganizationID, sc.UserID, l.Principal.KeyID, lane)
		return e
	})
	if e != nil {
		out = CompactionHandoff{}
	}
	return
}
func (s *Store) ClearCompactionHandoff(ctx context.Context, l Lease, lane string) error {
	if !validHandoffLane(lane) {
		return ErrConflict
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkLease(ctx, tx, l); e != nil {
			return e
		}
		sc := l.Principal.Scope
		_, e := tx.ExecContext(ctx, "DELETE FROM "+s.temp+".compaction_handoffs WHERE org_id=$1 AND user_id=$2 AND key_id=$3 AND id=$4 AND generation=$5", sc.OrganizationID, sc.UserID, l.Principal.KeyID, lane, sc.Generation)
		return e
	})
}
