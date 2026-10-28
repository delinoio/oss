// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/delinoio/oss/packages/tairouter/core"
	"time"
)

func (s *Store) ClaimNormalQuotaCollection(ctx context.Context, l Lease, id, version string, ttl time.Duration) (string, error) {
	return s.ClaimQuotaCollection(ctx, l, id, version, "normal", ttl)
}
func (s *Store) CompleteNormalQuotaCollection(ctx context.Context, l Lease, id, version, claim string, gap time.Duration, update []byte) error {
	return s.CompleteQuotaCollection(ctx, l, id, version, "normal", claim, gap, update)
}

// ClaimQuotaCollection is only for bounded, read-only observation fetches. Its
// expiry must exceed the caller's entire network deadline, not each attempt.
func (s *Store) ClaimQuotaCollection(ctx context.Context, l Lease, id, version, purpose string, ttl time.Duration) (claim string, e error) {
	if purpose == "" || len(purpose) > 128 || ttl <= 0 || ttl > time.Minute {
		return "", ErrConflict
	}
	claim = core.Token()
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkNativeAccount(ctx, tx, l, id, version); e != nil {
			return e
		}
		sc := l.Principal.Scope
		r, e := tx.ExecContext(ctx, "INSERT INTO quota_collection_claims(org_id,user_id,id,version,purpose,generation,claim_hash,claim_until,next_at) VALUES($1,$2,$3,$4,$5,$6,$7,clock_timestamp()+$8*interval '1 second',clock_timestamp()) ON CONFLICT(org_id,user_id,id,version,purpose) DO UPDATE SET claim_hash=excluded.claim_hash,claim_until=excluded.claim_until,generation=excluded.generation WHERE quota_collection_claims.claim_until<=clock_timestamp() AND quota_collection_claims.next_at<=clock_timestamp()", sc.OrganizationID, sc.UserID, id, version, purpose, sc.Generation, core.Hash(claim), ttl.Seconds())
		if e != nil {
			return e
		}
		n, e := r.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrBusy
		}
		return nil
	})
	if e != nil {
		claim = ""
	}
	return
}
func (s *Store) CompleteQuotaCollection(ctx context.Context, l Lease, id, version, purpose, claim string, gap time.Duration, update []byte) error {
	if claim == "" || purpose == "" || gap <= 0 || gap > 24*time.Hour || len(update) > 16<<10 || (update != nil && !json.Valid(update)) {
		return ErrConflict
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkNativeAccount(ctx, tx, l, id, version); e != nil {
			return e
		}
		sc := l.Principal.Scope
		r, e := tx.ExecContext(ctx, "UPDATE quota_collection_claims SET claim_hash=NULL,claim_until=clock_timestamp(),next_at=clock_timestamp()+$8*interval '1 second' WHERE org_id=$1 AND user_id=$2 AND id=$3 AND version=$4 AND purpose=$5 AND generation=$6 AND claim_hash=$7 AND claim_until>clock_timestamp()", sc.OrganizationID, sc.UserID, id, version, purpose, sc.Generation, core.Hash(claim), gap.Seconds())
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
		if update == nil {
			_, e = tx.ExecContext(ctx, "UPDATE "+s.temp+".native_quotas SET expires_at=LEAST(expires_at,recorded_at+interval '30 minutes') WHERE org_id=$1 AND user_id=$2 AND id=$3 AND version=$4", sc.OrganizationID, sc.UserID, id, version)
			return e
		}
		b, e := s.keys.Encrypt(update, nativeQuotaAAD(sc, id, version))
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO "+s.temp+".native_quotas(org_id,user_id,id,generation,version,value,expires_at,recorded_at) VALUES($1,$2,$3,$4,$5,$6,clock_timestamp()+interval '30 minutes',clock_timestamp()) ON CONFLICT(org_id,user_id,id) DO UPDATE SET generation=excluded.generation,version=excluded.version,value=excluded.value,expires_at=excluded.expires_at,recorded_at=excluded.recorded_at", sc.OrganizationID, sc.UserID, id, sc.Generation, version, b)
		return e
	})
}
