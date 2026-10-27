// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"time"
)

type MistralProbe struct {
	Ready   bool
	Claim   string
	RetryAt time.Time
}

func mistralIdentity(digest, model string) string {
	b, _ := json.Marshal([]string{digest, model})
	return hex.EncodeToString(core.Hash(string(b)))
}
func (s *Store) ensureMistral(ctx context.Context, tx *sql.Tx, sc core.Scope, identity string) error {
	var exists bool
	if e := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM mistral_holds WHERE org_id=$1 AND user_id=$2 AND identity=$3)", sc.OrganizationID, sc.UserID, identity).Scan(&exists); e != nil {
		return e
	}
	if exists {
		return nil
	}
	_, e := tx.ExecContext(ctx, "DELETE FROM mistral_holds h WHERE org_id=$1 AND user_id=$2 AND hold_until<=clock_timestamp() AND probe_hash IS NULL AND NOT EXISTS(SELECT 1 FROM mistral_waiters w WHERE w.org_id=h.org_id AND w.user_id=h.user_id AND w.identity=h.identity)", sc.OrganizationID, sc.UserID)
	if e != nil {
		return e
	}
	var count int
	if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM mistral_holds WHERE org_id=$1 AND user_id=$2", sc.OrganizationID, sc.UserID).Scan(&count); e != nil {
		return e
	}
	if count >= 128 {
		return &LimitError{time.Minute}
	}
	_, e = tx.ExecContext(ctx, "INSERT INTO mistral_holds(org_id,user_id,identity,hold_until,active) VALUES($1,$2,$3,clock_timestamp(),false)", sc.OrganizationID, sc.UserID, identity)
	return e
}
func (s *Store) SetMistralHold(ctx context.Context, l Lease, id, version, digest, model string, until time.Time) error {
	if digest == "" || model == "" {
		return ErrConflict
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkLease(ctx, tx, l); e != nil {
			return e
		}
		sc := l.Principal.Scope
		if e := s.checkFlowTarget(ctx, tx, sc, "provider", id, version); e != nil {
			return e
		}
		identity := mistralIdentity(digest, model)
		if e := s.ensureMistral(ctx, tx, sc, identity); e != nil {
			return e
		}
		var valid bool
		if e := tx.QueryRowContext(ctx, "SELECT $1::timestamptz<=clock_timestamp()+interval '32 days'", until).Scan(&valid); e != nil {
			return e
		}
		if !valid {
			return ErrConflict
		}
		_, e := tx.ExecContext(ctx, "UPDATE mistral_holds SET active=true,revision=revision+1,hold_until=GREATEST(hold_until,$4) WHERE org_id=$1 AND user_id=$2 AND identity=$3", sc.OrganizationID, sc.UserID, identity, until)
		return e
	})
}
func (s *Store) JoinMistralHold(ctx context.Context, l Lease, id, version, digest, model string) (ticket string, e error) {
	if digest == "" || model == "" {
		return "", ErrConflict
	}
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkLease(ctx, tx, l); e != nil {
			return e
		}
		sc := l.Principal.Scope
		if e := s.checkFlowTarget(ctx, tx, sc, "provider", id, version); e != nil {
			return e
		}
		identity := mistralIdentity(digest, model)
		if e := s.ensureMistral(ctx, tx, sc, identity); e != nil {
			return e
		}
		e := tx.QueryRowContext(ctx, "SELECT id FROM mistral_waiters WHERE org_id=$1 AND user_id=$2 AND identity=$3 AND admission_id=$4", sc.OrganizationID, sc.UserID, identity, l.ID).Scan(&ticket)
		if e == nil {
			return nil
		}
		if e != sql.ErrNoRows {
			return e
		}
		var total, per int
		if e = tx.QueryRowContext(ctx, "SELECT count(*),count(*) FILTER(WHERE user_id=$2 AND identity=$3) FROM mistral_waiters WHERE org_id=$1", sc.OrganizationID, sc.UserID, identity).Scan(&total, &per); e != nil {
			return e
		}
		if total >= 512 || per >= 128 {
			return &LimitError{time.Second}
		}
		ticket = core.ID()
		_, e = tx.ExecContext(ctx, "INSERT INTO mistral_waiters(id,org_id,user_id,identity,admission_id,key_id,generation,provider_id,version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)", ticket, sc.OrganizationID, sc.UserID, identity, l.ID, l.Principal.KeyID, sc.Generation, id, version)
		return e
	})
	return
}
func (s *Store) mistralTicket(ctx context.Context, tx *sql.Tx, l Lease, ticket string) (identity string, e error) {
	if e = s.checkLease(ctx, tx, l); e != nil {
		return
	}
	sc := l.Principal.Scope
	var provider, version string
	e = tx.QueryRowContext(ctx, "SELECT identity,provider_id,version FROM mistral_waiters WHERE id=$1 AND org_id=$2 AND user_id=$3 AND admission_id=$4 AND key_id=$5 AND generation=$6", ticket, sc.OrganizationID, sc.UserID, l.ID, l.Principal.KeyID, sc.Generation).Scan(&identity, &provider, &version)
	if e != nil {
		return "", noRows(e)
	}
	e = s.checkFlowTarget(ctx, tx, sc, "provider", provider, version)
	return
}
func (s *Store) TryMistralProbe(ctx context.Context, l Lease, ticket string) (out MistralProbe, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		identity, e := s.mistralTicket(ctx, tx, l, ticket)
		if e != nil {
			return e
		}
		sc := l.Principal.Scope
		var active, due bool
		var held []byte
		e = tx.QueryRowContext(ctx, "SELECT active,hold_until,hold_until<=clock_timestamp(),probe_hash FROM mistral_holds WHERE org_id=$1 AND user_id=$2 AND identity=$3", sc.OrganizationID, sc.UserID, identity).Scan(&active, &out.RetryAt, &due, &held)
		if e != nil {
			return e
		}
		if !active {
			out.Ready = true
			return nil
		}
		if !due || held != nil {
			return nil
		}
		out.Ready = true
		out.Claim = core.Token()
		_, e = tx.ExecContext(ctx, "UPDATE mistral_holds SET probe_hash=$4,probe_ticket=$5,probe_revision=revision WHERE org_id=$1 AND user_id=$2 AND identity=$3", sc.OrganizationID, sc.UserID, identity, core.Hash(out.Claim), ticket)
		return e
	})
	return
}
func (s *Store) FinishMistralProbe(ctx context.Context, l Lease, ticket, claim string, retryAt *time.Time) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		identity, e := s.mistralTicket(ctx, tx, l, ticket)
		if e != nil {
			return e
		}
		sc := l.Principal.Scope
		var valid bool
		if e = tx.QueryRowContext(ctx, "SELECT $1::timestamptz IS NULL OR $1<=clock_timestamp()+interval '32 days'", retryAt).Scan(&valid); e != nil {
			return e
		}
		if !valid {
			return ErrConflict
		}
		res, e := tx.ExecContext(ctx, "UPDATE mistral_holds SET active=($6::timestamptz IS NOT NULL OR revision<>probe_revision),hold_until=CASE WHEN revision<>probe_revision THEN GREATEST(hold_until,COALESCE($6,hold_until)) ELSE COALESCE($6,clock_timestamp()) END,probe_hash=NULL,probe_ticket=NULL,probe_revision=NULL WHERE org_id=$1 AND user_id=$2 AND identity=$3 AND probe_hash=$4 AND probe_ticket=$5", sc.OrganizationID, sc.UserID, identity, core.Hash(claim), ticket, retryAt)
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

// Leaving never clears an in-flight probe. Its outcome can be uncertain even
// after the request disconnects; no other instance may infer permission to retry.
func (s *Store) LeaveMistralHold(ctx context.Context, l Lease, ticket string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		sc := l.Principal.Scope
		if e := s.lock(ctx, tx, sc.OrganizationID); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, "DELETE FROM mistral_waiters WHERE id=$1 AND org_id=$2 AND user_id=$3 AND admission_id=$4 AND key_id=$5 AND generation=$6", ticket, sc.OrganizationID, sc.UserID, l.ID, l.Principal.KeyID, sc.Generation)
		return e
	})
}
