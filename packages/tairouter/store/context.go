// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"github.com/delinoio/oss/packages/tairouter/core"
	"time"
)

type ClientContextBinding struct {
	Data                                  []byte
	AccountID, CredentialVersion, Receipt string
	ExpiresAt                             time.Time
}

func contextHash(session string) string {
	v := sha256.Sum256([]byte(session))
	return hex.EncodeToString(v[:])
}
func (s *Store) ConfigureClientContext(ctx context.Context, raw string, enabled bool) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		p, e := s.keyPrincipal(ctx, tx, raw)
		if e != nil {
			return e
		}
		if enabled {
			_, e = tx.ExecContext(ctx, "INSERT INTO client_context_capabilities(org_id,user_id,key_id,version) VALUES($1,$2,$3,$4) ON CONFLICT(key_id) DO NOTHING", p.Scope.OrganizationID, p.Scope.UserID, p.KeyID, core.ID())
		} else {
			_, e = tx.ExecContext(ctx, "DELETE FROM client_context_capabilities WHERE key_id=$1", p.KeyID)
		}
		if e != nil {
			return e
		}
		return s.audit(ctx, tx, p.Scope.OrganizationID, p.Scope.UserID, "client.context_configured", p.KeyID)
	})
}
func (s *Store) CaptureClientContext(ctx context.Context, l Lease, session, account, version string, data []byte) error {
	if session == "" || len(session) > 512 || len(data) > 16384 {
		return ErrConflict
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkLease(ctx, tx, l); e != nil {
			return e
		}
		sc := l.Principal.Scope
		var capability string
		e := tx.QueryRowContext(ctx, "SELECT version FROM client_context_capabilities WHERE key_id=$1 AND org_id=$2 AND user_id=$3", l.Principal.KeyID, sc.OrganizationID, sc.UserID).Scan(&capability)
		if e == sql.ErrNoRows {
			return nil
		}
		if e != nil {
			return e
		}
		if e = s.privateUsable(ctx, tx, sc, "provider", account); e != nil {
			return e
		}
		var valid bool
		if e = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM private_data WHERE org_id=$1 AND user_id=$2 AND kind='provider' AND id=$3 AND version=$4)", sc.OrganizationID, sc.UserID, account, version).Scan(&valid); e != nil {
			return e
		}
		if !valid {
			return ErrForbidden
		}
		hash := contextHash(session)
		// The first observed account owns the session. Later pool rotation cannot
		// overwrite its receipt or silently rebind context history to another account.
		if _, e = tx.ExecContext(ctx, "DELETE FROM "+s.temp+".client_context_bindings WHERE key_id=$1 AND expires_at<=clock_timestamp()", l.Principal.KeyID); e != nil {
			return e
		}
		var exists bool
		if e = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM "+s.temp+".client_context_bindings WHERE key_id=$1 AND session_hash=$2)", l.Principal.KeyID, hash).Scan(&exists); e != nil {
			return e
		}
		if exists {
			_, e = tx.ExecContext(ctx, "UPDATE "+s.temp+".client_context_bindings SET ambiguous=true WHERE key_id=$1 AND session_hash=$2 AND NOT confirmed AND (account_id<>$3 OR credential_version<>$4)", l.Principal.KeyID, hash, account, version)
			return e
		}
		var count int
		if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM "+s.temp+".client_context_bindings WHERE key_id=$1 AND expires_at>clock_timestamp()", l.Principal.KeyID).Scan(&count); e != nil {
			return e
		}
		if count >= 256 {
			return ErrConflict
		}
		value, e := s.keys.Encrypt(data, ownership(sc, "client-context:"+l.Principal.KeyID, hash))
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO "+s.temp+".client_context_bindings(org_id,user_id,key_id,session_hash,account_id,credential_version,capability_version,receipt,value,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,clock_timestamp()+interval '24 hours') ON CONFLICT(key_id,session_hash) DO NOTHING", sc.OrganizationID, sc.UserID, l.Principal.KeyID, hash, account, version, capability, core.ID(), value)
		return e
	})
}
func (s *Store) readClientContext(ctx context.Context, tx *sql.Tx, l Lease, session string, confirmed bool) (out ClientContextBinding, e error) {
	if session == "" || len(session) > 512 {
		return out, ErrConflict
	}
	if e = s.checkLease(ctx, tx, l); e != nil {
		return
	}
	return s.readContextProof(ctx, tx, l, session, confirmed)
}
func (s *Store) readContextProof(ctx context.Context, tx *sql.Tx, l Lease, session string, confirmed bool) (out ClientContextBinding, e error) {
	if session == "" || len(session) > 512 {
		return out, ErrConflict
	}
	sc := l.Principal.Scope
	e = tx.QueryRowContext(ctx, "SELECT b.value,b.account_id,b.credential_version,b.receipt,b.expires_at FROM "+s.temp+".client_context_bindings b JOIN client_context_capabilities c ON c.key_id=b.key_id AND c.version=b.capability_version JOIN private_data d ON d.org_id=b.org_id AND d.user_id=b.user_id AND d.kind='provider' AND d.id=b.account_id AND d.version=b.credential_version WHERE b.key_id=$1 AND b.session_hash=$2 AND b.org_id=$3 AND b.user_id=$4 AND b.expires_at>clock_timestamp() AND NOT b.ambiguous AND (NOT $5 OR b.confirmed)", l.Principal.KeyID, contextHash(session), sc.OrganizationID, sc.UserID, confirmed).Scan(&out.Data, &out.AccountID, &out.CredentialVersion, &out.Receipt, &out.ExpiresAt)
	if e != nil {
		return out, noRows(e)
	}
	if e = s.privateUsable(ctx, tx, sc, "provider", out.AccountID); e != nil {
		return
	}
	out.Data, e = s.keys.Decrypt(out.Data, ownership(sc, "client-context:"+l.Principal.KeyID, contextHash(session)))
	return
}
func (s *Store) ResolveClientContext(ctx context.Context, l Lease, session string) (out ClientContextBinding, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = s.readClientContext(ctx, tx, l, session, true)
		return err
	})
	return
}
func (s *Store) BindClientContext(ctx context.Context, raw, session string) (out ClientContextBinding, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		p, err := s.keyPrincipal(ctx, tx, raw)
		if err != nil {
			return err
		}
		// Registration does not consume an inference slot. Use current key authority,
		// then require an existing observed receipt rather than choosing an account.
		l := Lease{Principal: p}
		out, err = s.readClientContextKey(ctx, tx, l, session)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE "+s.temp+".client_context_bindings SET confirmed=true WHERE key_id=$1 AND session_hash=$2", p.KeyID, contextHash(session))
		return err
	})
	return
}

// The raw-key registration gate and admitted requests share the same proof checks.
func (s *Store) readClientContextKey(ctx context.Context, tx *sql.Tx, l Lease, session string) (ClientContextBinding, error) {
	// The raw-key gate already rechecks membership and revocation under this lock.
	return s.readContextProof(ctx, tx, l, session, false)
}
func (s *Store) WithClientContextDispatch(ctx context.Context, l Lease, session, receipt string, start func(context.Context) error) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		b, e := s.readClientContext(ctx, tx, l, session, true)
		if e != nil {
			return e
		}
		if b.Receipt != receipt {
			return ErrForbidden
		}
		return start(ctx)
	})
}

func (s *Store) rotateClientContexts(ctx context.Context, tx *sql.Tx) error {
	if _, e := tx.ExecContext(ctx, "LOCK TABLE "+s.temp+".client_context_bindings IN ACCESS EXCLUSIVE MODE"); e != nil {
		return e
	}
	rows, e := tx.QueryContext(ctx, "SELECT b.org_id,b.user_id,k.generation,b.key_id,b.session_hash,b.value FROM "+s.temp+".client_context_bindings b JOIN api_keys k ON k.id=b.key_id")
	if e != nil {
		return e
	}
	type item struct {
		sc      core.Scope
		key, id string
		value   []byte
	}
	var all []item
	for rows.Next() {
		var r item
		if e = rows.Scan(&r.sc.OrganizationID, &r.sc.UserID, &r.sc.Generation, &r.key, &r.id, &r.value); e != nil {
			rows.Close()
			return e
		}
		all = append(all, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, r := range all {
		aad := ownership(r.sc, "client-context:"+r.key, r.id)
		plain, e := s.keys.Decrypt(r.value, aad)
		if e != nil {
			return e
		}
		value, e := s.keys.Encrypt(plain, aad)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE "+s.temp+".client_context_bindings SET value=$3 WHERE key_id=$1 AND session_hash=$2", r.key, r.id, value); e != nil {
			return e
		}
	}
	return nil
}
