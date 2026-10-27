// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"time"
)

func (s *Store) checkNativeAccount(ctx context.Context, tx *sql.Tx, l Lease, id, version string) error {
	if e := s.checkLease(ctx, tx, l); e != nil {
		return e
	}
	if id == "" || version == "" {
		return ErrForbidden
	}
	sc := l.Principal.Scope
	var valid bool
	if e := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM private_data WHERE org_id=$1 AND user_id=$2 AND kind='provider' AND id=$3 AND version=$4)", sc.OrganizationID, sc.UserID, id, version).Scan(&valid); e != nil {
		return e
	}
	if !valid {
		return ErrForbidden
	}
	return s.privateUsable(ctx, tx, sc, "provider", id)
}
func nativeQuotaAAD(sc core.Scope, id, version string) string {
	return ownership(sc, "native-quota:"+version, id)
}
func (s *Store) readNativeQuota(ctx context.Context, tx *sql.Tx, l Lease, id, version string) ([]byte, error) {
	sc := l.Principal.Scope
	var b []byte
	e := tx.QueryRowContext(ctx, "SELECT value FROM "+s.temp+".native_quotas WHERE org_id=$1 AND user_id=$2 AND id=$3 AND version=$4 AND generation=$5 AND expires_at>clock_timestamp()", sc.OrganizationID, sc.UserID, id, version, sc.Generation).Scan(&b)
	if e != nil {
		return nil, noRows(e)
	}
	return s.keys.Decrypt(b, nativeQuotaAAD(sc, id, version))
}
func (s *Store) GetNativeQuota(ctx context.Context, l Lease, id, version string) (out []byte, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkNativeAccount(ctx, tx, l, id, version); e != nil {
			return e
		}
		var e error
		out, e = s.readNativeQuota(ctx, tx, l, id, version)
		return e
	})
	return
}

// UpdateNativeQuota serializes evidence merges across all keys of this owner.
// The callback compares per-window observation timestamps and must not use network I/O.
func (s *Store) UpdateNativeQuota(ctx context.Context, l Lease, id, version string, merge func([]byte) ([]byte, error)) error {
	if merge == nil {
		return ErrConflict
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkNativeAccount(ctx, tx, l, id, version); e != nil {
			return e
		}
		old, e := s.readNativeQuota(ctx, tx, l, id, version)
		if e != nil && e != ErrNotFound {
			return e
		}
		value, e := merge(old)
		if e != nil {
			return e
		}
		if len(value) > 16<<10 || !json.Valid(value) {
			return ErrConflict
		}
		sc := l.Principal.Scope
		b, e := s.keys.Encrypt(value, nativeQuotaAAD(sc, id, version))
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO "+s.temp+".native_quotas(org_id,user_id,id,generation,version,value,expires_at) VALUES($1,$2,$3,$4,$5,$6,clock_timestamp()+interval '24 hours') ON CONFLICT(org_id,user_id,id) DO UPDATE SET generation=excluded.generation,version=excluded.version,value=excluded.value,expires_at=excluded.expires_at,recorded_at=clock_timestamp()", sc.OrganizationID, sc.UserID, id, sc.Generation, version, b)
		return e
	})
}

type NativeAffinity struct {
	HoldSince                    time.Time
	DetourAccountID              string
	Successes                    int
	AccountID, CredentialVersion string
	Epoch                        int64
}

func validNativeAffinity(group, version, session string) bool {
	return group != "" && version != "" && session != "" && len(group) <= 512 && len(version) <= 512 && len(session) <= 512
}
func (s *Store) GetNativeAffinity(ctx context.Context, l Lease, group, groupVersion, sessionHash string) (out NativeAffinity, e error) {
	if !validNativeAffinity(group, groupVersion, sessionHash) {
		return out, ErrConflict
	}
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkLease(ctx, tx, l); e != nil {
			return e
		}
		sc := l.Principal.Scope
		if _, e := tx.ExecContext(ctx, "DELETE FROM "+s.temp+".native_affinities WHERE org_id=$1 AND user_id=$2 AND (expires_at<=clock_timestamp() OR generation<>$3)", sc.OrganizationID, sc.UserID, sc.Generation); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, "INSERT INTO "+s.temp+".native_affinities(org_id,user_id,key_id,generation,group_id,group_version,session_hash,epoch,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,nextval('native_affinity_epoch'),clock_timestamp()+interval '24 hours') ON CONFLICT DO NOTHING", sc.OrganizationID, sc.UserID, l.Principal.KeyID, sc.Generation, group, groupVersion, sessionHash)
		if e != nil {
			return e
		}
		var hold sql.NullTime
		e = tx.QueryRowContext(ctx, "SELECT account_id,credential_version,epoch,successes,hold_since,detour_account_id FROM "+s.temp+".native_affinities WHERE org_id=$1 AND user_id=$2 AND key_id=$3 AND group_id=$4 AND group_version=$5 AND session_hash=$6", sc.OrganizationID, sc.UserID, l.Principal.KeyID, group, groupVersion, sessionHash).Scan(&out.AccountID, &out.CredentialVersion, &out.Epoch, &out.Successes, &hold, &out.DetourAccountID)
		if e != nil {
			return e
		}
		if hold.Valid {
			out.HoldSince = hold.Time
		}
		if out.AccountID != "" {
			if e := s.checkNativeAccount(ctx, tx, l, out.AccountID, out.CredentialVersion); e != nil {
				if e != ErrForbidden && e != ErrBusy && e != ErrReauthentication {
					return e
				}
				if e = tx.QueryRowContext(ctx, "UPDATE "+s.temp+".native_affinities SET account_id='',credential_version='',successes=0,hold_since=NULL,detour_account_id='',epoch=nextval('native_affinity_epoch') WHERE org_id=$1 AND user_id=$2 AND key_id=$3 AND group_id=$4 AND group_version=$5 AND session_hash=$6 RETURNING epoch", sc.OrganizationID, sc.UserID, l.Principal.KeyID, group, groupVersion, sessionHash).Scan(&out.Epoch); e != nil {
					return e
				}
				out.AccountID = ""
				out.CredentialVersion = ""
				out.Successes = 0
				out.HoldSince = time.Time{}
				out.DetourAccountID = ""
			}
		}
		_, e = tx.ExecContext(ctx, "DELETE FROM "+s.temp+".native_affinities WHERE org_id=$1 AND user_id=$2 AND (key_id,group_id,group_version,session_hash) IN(SELECT key_id,group_id,group_version,session_hash FROM "+s.temp+".native_affinities WHERE org_id=$1 AND user_id=$2 ORDER BY updated_at DESC,epoch DESC OFFSET 1024)", sc.OrganizationID, sc.UserID)
		return e
	})
	return
}
func (s *Store) FinishNativeAffinity(ctx context.Context, l Lease, group, groupVersion, sessionHash, account, credentialVersion string, expectedEpoch int64, success bool) error {
	if !validNativeAffinity(group, groupVersion, sessionHash) || expectedEpoch <= 0 {
		return ErrConflict
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkNativeAccount(ctx, tx, l, account, credentialVersion); e != nil {
			return e
		}
		sc := l.Principal.Scope
		query := "UPDATE " + s.temp + ".native_affinities SET successes=CASE WHEN account_id=$8 AND credential_version=$9 THEN LEAST(successes::bigint+1,2147483647)::integer ELSE 1 END,account_id=$8,credential_version=$9,updated_at=clock_timestamp(),expires_at=clock_timestamp()+interval '24 hours' WHERE org_id=$1 AND user_id=$2 AND key_id=$3 AND group_id=$4 AND group_version=$5 AND session_hash=$6 AND epoch=$7 AND generation=$10 AND expires_at>clock_timestamp()"
		if !success {
			query = "UPDATE " + s.temp + ".native_affinities SET account_id='',credential_version='',successes=0,hold_since=NULL,detour_account_id='',epoch=nextval('native_affinity_epoch'),updated_at=clock_timestamp(),expires_at=clock_timestamp()+interval '24 hours' WHERE org_id=$1 AND user_id=$2 AND key_id=$3 AND group_id=$4 AND group_version=$5 AND session_hash=$6 AND epoch=$7 AND generation=$10 AND expires_at>clock_timestamp() AND (account_id='' OR (account_id=$8 AND credential_version=$9))"
		}
		r, e := tx.ExecContext(ctx, query, sc.OrganizationID, sc.UserID, l.Principal.KeyID, group, groupVersion, sessionHash, expectedEpoch, account, credentialVersion, sc.Generation)
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

// UpdateNativeAffinityHold changes only hold metadata for the live epoch.
func (s *Store) UpdateNativeAffinityHold(ctx context.Context, l Lease, group, version, session string, expectedEpoch int64, holdSince time.Time, detour string) error {
	if !validNativeAffinity(group, version, session) || expectedEpoch <= 0 || len(detour) > 512 {
		return ErrConflict
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkLease(ctx, tx, l); e != nil {
			return e
		}
		sc := l.Principal.Scope
		var account, credential string
		e := tx.QueryRowContext(ctx, "SELECT account_id,credential_version FROM "+s.temp+".native_affinities WHERE org_id=$1 AND user_id=$2 AND key_id=$3 AND group_id=$4 AND group_version=$5 AND session_hash=$6 AND epoch=$7 AND expires_at>clock_timestamp()", sc.OrganizationID, sc.UserID, l.Principal.KeyID, group, version, session, expectedEpoch).Scan(&account, &credential)
		if e == sql.ErrNoRows {
			return ErrConflict
		}
		if e != nil {
			return e
		}
		if account != "" {
			if e = s.checkNativeAccount(ctx, tx, l, account, credential); e != nil {
				return e
			}
		}
		var hold any
		if !holdSince.IsZero() {
			hold = holdSince
		}
		r, e := tx.ExecContext(ctx, "UPDATE "+s.temp+".native_affinities SET hold_since=$8,detour_account_id=$9 WHERE org_id=$1 AND user_id=$2 AND key_id=$3 AND group_id=$4 AND group_version=$5 AND session_hash=$6 AND epoch=$7 AND generation=$10 AND expires_at>clock_timestamp()", sc.OrganizationID, sc.UserID, l.Principal.KeyID, group, version, session, expectedEpoch, hold, detour, sc.Generation)
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
