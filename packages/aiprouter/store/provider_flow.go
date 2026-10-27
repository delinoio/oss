// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"time"
)

var ErrBusy = errors.New("operation already claimed")
var ErrReauthentication = errors.New("provider credentials require reauthentication")

// WithScopeDispatch is for authenticated browser OAuth operations that have no
// inference key. The callback has the same narrow write boundary as WithDispatch.
func (s *Store) WithScopeDispatch(ctx context.Context, sc core.Scope, start func(context.Context) error) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		return start(ctx)
	})
}
func (s *Store) SaveProviderFlow(ctx context.Context, sc core.Scope, id string, value []byte, expires time.Time) error {
	return s.SaveProviderFlowBound(ctx, sc, id, "", "", "", value, expires)
}
func (s *Store) SaveProviderFlowBound(ctx context.Context, sc core.Scope, id, kind, providerID, expectedVersion string, value []byte, expires time.Time) error {
	if id == "" || len(value) > 1<<20 {
		return ErrConflict
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		if providerID != "" {
			if e := s.checkFlowTarget(ctx, tx, sc, kind, providerID, expectedVersion); e != nil {
				return e
			}
		}
		var valid bool
		var count int
		if e := tx.QueryRowContext(ctx, "SELECT $1::timestamptz>clock_timestamp() AND $1<=clock_timestamp()+interval '24 hours'", expires).Scan(&valid); e != nil {
			return e
		}
		if !valid {
			return ErrExpired
		}
		if _, e := tx.ExecContext(ctx, "DELETE FROM "+s.temp+".provider_flows WHERE org_id=$1 AND user_id=$2 AND expires_at<=clock_timestamp()", sc.OrganizationID, sc.UserID); e != nil {
			return e
		}
		if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM "+s.temp+".provider_flows WHERE org_id=$1 AND user_id=$2", sc.OrganizationID, sc.UserID).Scan(&count); e != nil {
			return e
		}
		if count >= 64 {
			return &LimitError{time.Minute}
		}
		v, e := s.keys.Encrypt(value, ownership(sc, "provider-flow", id))
		if e != nil {
			return e
		}
		res, e := tx.ExecContext(ctx, "INSERT INTO "+s.temp+".provider_flows(org_id,user_id,id,generation,value,expires_at,target_kind,target_id,expected_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT DO NOTHING", sc.OrganizationID, sc.UserID, id, sc.Generation, v, expires, kind, providerID, expectedVersion)
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
func (s *Store) ClaimProviderFlow(ctx context.Context, sc core.Scope, id string) (value []byte, claim string, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		var data, held []byte
		var valid bool
		e := tx.QueryRowContext(ctx, "SELECT value,claim_hash,expires_at>clock_timestamp() FROM "+s.temp+".provider_flows WHERE org_id=$1 AND user_id=$2 AND id=$3 AND generation=$4 AND status='pending' FOR UPDATE", sc.OrganizationID, sc.UserID, id, sc.Generation).Scan(&data, &held, &valid)
		if e != nil {
			return noRows(e)
		}
		if !valid {
			return ErrExpired
		}
		if held != nil {
			return ErrBusy
		}
		value, e = s.keys.Decrypt(data, ownership(sc, "provider-flow", id))
		if e != nil {
			return e
		}
		claim = core.Token()
		_, e = tx.ExecContext(ctx, "UPDATE "+s.temp+".provider_flows SET claim_hash=$4 WHERE org_id=$1 AND user_id=$2 AND id=$3", sc.OrganizationID, sc.UserID, id, core.Hash(claim))
		return e
	})
	return
}
func (s *Store) checkFlow(ctx context.Context, tx *sql.Tx, sc core.Scope, id, claim string) error {
	if _, e := s.guard(ctx, tx, sc); e != nil {
		return e
	}
	var ok bool
	e := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM "+s.temp+".provider_flows WHERE org_id=$1 AND user_id=$2 AND id=$3 AND generation=$4 AND claim_hash=$5 AND status='pending' AND expires_at>clock_timestamp())", sc.OrganizationID, sc.UserID, id, sc.Generation, core.Hash(claim)).Scan(&ok)
	if e != nil {
		return e
	}
	if !ok {
		return ErrConflict
	}
	var kind, target, version string
	if e := tx.QueryRowContext(ctx, "SELECT target_kind,target_id,expected_version FROM "+s.temp+".provider_flows WHERE org_id=$1 AND user_id=$2 AND id=$3", sc.OrganizationID, sc.UserID, id).Scan(&kind, &target, &version); e != nil {
		return e
	}
	if target != "" {
		return s.checkFlowTarget(ctx, tx, sc, kind, target, version)
	}
	return nil
}

// WithProviderFlowDispatch also fences cancellation between claim and send.
func (s *Store) WithProviderFlowDispatch(ctx context.Context, sc core.Scope, id, claim string, start func(context.Context) error) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkFlow(ctx, tx, sc, id, claim); e != nil {
			return e
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		return start(ctx)
	})
}
func (s *Store) CompleteProviderFlow(ctx context.Context, sc core.Scope, id, claim, providerID string, value []byte) error {
	return s.CompleteProviderFlowKind(ctx, sc, id, claim, "provider", providerID, value)
}
func (s *Store) CompleteProviderFlowKind(ctx context.Context, sc core.Scope, id, claim, kind, providerID string, value []byte) error {
	if kind == "" || providerID == "" {
		return ErrConflict
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkFlow(ctx, tx, sc, id, claim); e != nil {
			return e
		}
		var targetKind, targetID, expected string
		if e := tx.QueryRowContext(ctx, "SELECT target_kind,target_id,expected_version FROM "+s.temp+".provider_flows WHERE org_id=$1 AND user_id=$2 AND id=$3", sc.OrganizationID, sc.UserID, id).Scan(&targetKind, &targetID, &expected); e != nil {
			return e
		}
		if targetID != "" && (targetID != providerID || targetKind != kind) {
			return ErrConflict
		}
		if e := s.checkFlowTarget(ctx, tx, sc, kind, providerID, expected); e != nil {
			return e
		}
		v, e := s.keys.Encrypt(value, ownership(sc, kind, providerID))
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO private_data(org_id,user_id,kind,id,value,version) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(org_id,user_id,kind,id) DO UPDATE SET value=excluded.value,version=excluded.version", sc.OrganizationID, sc.UserID, kind, providerID, v, core.ID()); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE "+s.temp+".provider_flows SET status='completed',value=''::bytea,claim_hash=NULL,target_id=$4 WHERE org_id=$1 AND user_id=$2 AND id=$3", sc.OrganizationID, sc.UserID, id, providerID); e != nil {
			return e
		}
		return s.audit(ctx, tx, sc.OrganizationID, sc.UserID, "provider.configuration_changed", providerID)
	})
}

// ReleaseProviderFlow is only for a provider-confirmed pending result. Ambiguous
// exchanges retain their claim until canceled; elapsed time never reclaims it.
func (s *Store) ReleaseProviderFlow(ctx context.Context, sc core.Scope, id, claim string, value []byte) error {
	if len(value) > 1<<20 {
		return ErrConflict
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkFlow(ctx, tx, sc, id, claim); e != nil {
			return e
		}
		v, e := s.keys.Encrypt(value, ownership(sc, "provider-flow", id))
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE "+s.temp+".provider_flows SET value=$4,claim_hash=NULL WHERE org_id=$1 AND user_id=$2 AND id=$3", sc.OrganizationID, sc.UserID, id, v)
		return e
	})
}
func (s *Store) CancelProviderFlow(ctx context.Context, sc core.Scope, id string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, "UPDATE "+s.temp+".provider_flows SET status='canceled',value=''::bytea,claim_hash=NULL WHERE org_id=$1 AND user_id=$2 AND id=$3 AND status='pending'", sc.OrganizationID, sc.UserID, id)
		return e
	})
}

func (s *Store) checkFlowTarget(ctx context.Context, tx *sql.Tx, sc core.Scope, kind, id, expected string) error {
	var version string
	e := tx.QueryRowContext(ctx, "SELECT version FROM private_data WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4", sc.OrganizationID, sc.UserID, kind, id).Scan(&version)
	if e == sql.ErrNoRows && expected == "" {
		return nil
	}
	if e != nil && e != sql.ErrNoRows {
		return e
	}
	if expected == "" || version != expected || e == sql.ErrNoRows {
		return ErrConflict
	}
	return nil
}
func (s *Store) GetProviderFlow(ctx context.Context, sc core.Scope, id string) (value []byte, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		var data []byte
		if e := tx.QueryRowContext(ctx, "SELECT value FROM "+s.temp+".provider_flows WHERE org_id=$1 AND user_id=$2 AND id=$3 AND generation=$4 AND status='pending' AND expires_at>clock_timestamp()", sc.OrganizationID, sc.UserID, id, sc.Generation).Scan(&data); e != nil {
			return noRows(e)
		}
		var err error
		value, err = s.keys.Decrypt(data, ownership(sc, "provider-flow", id))
		return err
	})
	return
}

type FlowReceipt struct {
	Status, ProviderID string
	ExpiresAt          time.Time
}

func (s *Store) ProviderFlowReceipt(ctx context.Context, sc core.Scope, id string) (out FlowReceipt, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		return noRows(tx.QueryRowContext(ctx, "SELECT CASE WHEN expires_at<=clock_timestamp() THEN 'expired' WHEN status='pending' AND claim_hash IS NOT NULL THEN 'claimed' ELSE status END,target_id,expires_at FROM "+s.temp+".provider_flows WHERE org_id=$1 AND user_id=$2 AND id=$3 AND generation=$4", sc.OrganizationID, sc.UserID, id, sc.Generation).Scan(&out.Status, &out.ProviderID, &out.ExpiresAt))
	})
	return
}
