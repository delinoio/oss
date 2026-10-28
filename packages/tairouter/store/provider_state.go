// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"github.com/delinoio/oss/packages/tairouter/core"
	"time"
)

type ProviderStatePolicy struct {
	TTL                          time.Duration
	Entries                      int
	MaxEntryBytes, MaxTotalBytes int64
}
type ProviderState struct {
	Data      []byte
	ExpiresAt time.Time
	Version   string
}

func stateAAD(p Principal, namespace, id string) string {
	return ownership(p.Scope, "provider-state:"+namespace+":"+p.KeyID, id)
}
func (s *Store) GetProviderState(ctx context.Context, p Principal, namespace, id string) (out ProviderState, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkProviderStateOwner(ctx, tx, p, namespace, id); e != nil {
			return e
		}
		var b []byte
		e := tx.QueryRowContext(ctx, "SELECT value,expires_at,version FROM "+s.temp+".provider_states WHERE org_id=$1 AND user_id=$2 AND key_id=$3 AND namespace=$4 AND id=$5 AND generation=$6 AND expires_at>clock_timestamp()", p.Scope.OrganizationID, p.Scope.UserID, p.KeyID, namespace, id, p.Scope.Generation).Scan(&b, &out.ExpiresAt, &out.Version)
		if e != nil {
			return noRows(e)
		}
		out.Data, e = s.keys.Decrypt(b, stateAAD(p, namespace, id))
		return e
	})
	return
}
func validStatePolicy(p ProviderStatePolicy) bool {
	return p.TTL > 0 && p.TTL <= 24*time.Hour && p.Entries > 0 && p.Entries <= 1000 && p.MaxEntryBytes > 0 && p.MaxEntryBytes <= 64<<20 && p.MaxTotalBytes > 0 && p.MaxTotalBytes <= 1<<30
}
func (s *Store) writeProviderState(ctx context.Context, tx *sql.Tx, p Principal, ns, id string, value ProviderState, policy ProviderStatePolicy) error {
	if ns == "" || id == "" || !validStatePolicy(policy) || int64(len(value.Data)) > policy.MaxEntryBytes {
		return ErrConflict
	}
	var now time.Time
	if e := tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&now); e != nil {
		return e
	}
	if value.ExpiresAt.IsZero() {
		value.ExpiresAt = now.Add(policy.TTL)
	}
	if !value.ExpiresAt.After(now) || value.ExpiresAt.After(now.Add(policy.TTL)) {
		return ErrExpired
	}
	b, e := s.keys.Encrypt(value.Data, stateAAD(p, ns, id))
	if e != nil {
		return e
	}
	if int64(len(b)) > policy.MaxTotalBytes {
		return ErrConflict
	}
	sc := p.Scope
	if _, e = tx.ExecContext(ctx, "INSERT INTO "+s.temp+".provider_states(org_id,user_id,key_id,namespace,id,generation,value,version,size,expires_at,quota_entries,quota_bytes,account_id,credential_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT(org_id,user_id,key_id,namespace,id) DO UPDATE SET value=excluded.value,version=excluded.version,size=excluded.size,created_at=clock_timestamp(),expires_at=excluded.expires_at,quota_entries=excluded.quota_entries,quota_bytes=excluded.quota_bytes,account_id=excluded.account_id,credential_version=excluded.credential_version", sc.OrganizationID, sc.UserID, p.KeyID, ns, id, sc.Generation, b, core.ID(), len(b), value.ExpiresAt, policy.Entries, policy.MaxTotalBytes, p.AccountID, p.CredentialVersion); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, "DELETE FROM "+s.temp+".provider_states WHERE org_id=$1 AND user_id=$2 AND namespace=$3 AND (expires_at<=clock_timestamp() OR (key_id,id) IN(SELECT key_id,id FROM (SELECT key_id,id,row_number() OVER(ORDER BY created_at DESC,id DESC,key_id DESC) n,sum(size) OVER(ORDER BY created_at DESC,id DESC,key_id DESC) bytes FROM "+s.temp+".provider_states WHERE org_id=$1 AND user_id=$2 AND namespace=$3) ranked WHERE n>$4 OR bytes>$5))", sc.OrganizationID, sc.UserID, ns, policy.Entries, policy.MaxTotalBytes)
	return e
}
func (s *Store) PutProviderState(ctx context.Context, p Principal, namespace, id string, value ProviderState, policy ProviderStatePolicy) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkProviderStateOwner(ctx, tx, p, namespace, id); e != nil {
			return e
		}
		return s.writeProviderState(ctx, tx, p, namespace, id, value, policy)
	})
}

// UpdateProviderState runs merge under the ownership transaction. It must not
// perform network I/O. nil input means authoritative absence or expiry only.
func (s *Store) UpdateProviderState(ctx context.Context, p Principal, namespace, id string, policy ProviderStatePolicy, merge func([]byte) ([]byte, error)) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkProviderStateOwner(ctx, tx, p, namespace, id); e != nil {
			return e
		}
		var b, old []byte
		e := tx.QueryRowContext(ctx, "SELECT value FROM "+s.temp+".provider_states WHERE org_id=$1 AND user_id=$2 AND key_id=$3 AND namespace=$4 AND id=$5 AND generation=$6 AND expires_at>clock_timestamp()", p.Scope.OrganizationID, p.Scope.UserID, p.KeyID, namespace, id, p.Scope.Generation).Scan(&b)
		if e != nil && e != sql.ErrNoRows {
			return e
		}
		if e == nil {
			old, e = s.keys.Decrypt(b, stateAAD(p, namespace, id))
			if e != nil {
				return e
			}
		}
		value, e := merge(old)
		if e != nil {
			return e
		}
		return s.writeProviderState(ctx, tx, p, namespace, id, ProviderState{Data: value}, policy)
	})
}

// checkProviderStateOwner holds the organization gate acquired by checkPrincipal
// across credential validation, state access, and the synchronous merge callback.
func (s *Store) checkProviderStateOwner(ctx context.Context, tx *sql.Tx, p Principal, ns, id string) error {
	if e := s.checkPrincipal(ctx, tx, p); e != nil {
		return e
	}
	if (ns == "tool-reasoning-replay" || ns == "compaction-reasoning") && p.AccountID == "" {
		return ErrForbidden
	}
	if (p.AccountID == "") != (p.CredentialVersion == "") {
		return ErrForbidden
	}
	if p.AccountID != "" {
		var valid bool
		if e := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM private_data WHERE org_id=$1 AND user_id=$2 AND kind='provider' AND id=$3 AND version=$4)", p.Scope.OrganizationID, p.Scope.UserID, p.AccountID, p.CredentialVersion).Scan(&valid); e != nil {
			return e
		}
		if !valid {
			return ErrForbidden
		}
		if e := s.privateUsable(ctx, tx, p.Scope, "provider", p.AccountID); e != nil {
			return e
		}
	}
	var account, version string
	e := tx.QueryRowContext(ctx, "SELECT account_id,credential_version FROM "+s.temp+".provider_states WHERE org_id=$1 AND user_id=$2 AND key_id=$3 AND namespace=$4 AND id=$5 AND expires_at>clock_timestamp()", p.Scope.OrganizationID, p.Scope.UserID, p.KeyID, ns, id).Scan(&account, &version)
	if e == sql.ErrNoRows {
		return nil
	}
	if e != nil {
		return e
	}
	if account != p.AccountID || version != p.CredentialVersion {
		return ErrForbidden
	}
	return nil
}
