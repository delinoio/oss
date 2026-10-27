// SPDX-License-Identifier: Apache-2.0
package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"time"
)

type NativeHealthLane string

const (
	NativeHealthTransient NativeHealthLane = "transient"
	NativeHealthQuota     NativeHealthLane = "quota"
	NativeHealthFamily    NativeHealthLane = "family"
)

type NativeHealthKey struct {
	AccountID, CredentialVersion, Model string
	Lane                                NativeHealthLane
}
type NativeHealth struct {
	Revision                                                               int64
	Failures                                                               int
	WindowStartedAt, CooldownSince, CooldownUntil, AvoidUntil, NextProbeAt time.Time
	ProbeEligible                                                          bool
	Payload                                                                json.RawMessage
	ProbeID                                                                string
	ProbeEpoch                                                             int64
	ProbeExpiresAt, LastProbeAt                                            time.Time
	// Now is database time for policy calculations; it is never persisted.
	Now time.Time `json:"-"`
}
type NativeHealthPermit struct{ ID string }

func validHealthKey(k NativeHealthKey) bool {
	return k.AccountID != "" && k.CredentialVersion != "" && ((k.Lane == NativeHealthFamily && (k.Model == "fable" || k.Model == "opus" || k.Model == "sonnet")) || (k.Lane == NativeHealthTransient && k.Model == "") || (k.Lane == NativeHealthQuota && (k.Model == "" || k.Model == "shared" || k.Model == "reserve")))
}
func (s *Store) healthAuthority(ctx context.Context, tx *sql.Tx, l Lease, k NativeHealthKey) error {
	if !validHealthKey(k) {
		return ErrConflict
	}
	return s.checkNativeAccount(ctx, tx, l, k.AccountID, k.CredentialVersion)
}
func healthArgs(l Lease, k NativeHealthKey) []any {
	sc := l.Principal.Scope
	return []any{sc.OrganizationID, sc.UserID, k.AccountID, k.CredentialVersion, k.Lane, k.Model}
}

const healthWhere = "org_id=$1 AND user_id=$2 AND account_id=$3 AND credential_version=$4 AND lane=$5 AND model=$6"

func (s *Store) readHealth(ctx context.Context, tx *sql.Tx, l Lease, k NativeHealthKey) (h NativeHealth, e error) {
	var raw []byte
	e = tx.QueryRowContext(ctx, "SELECT state,clock_timestamp() FROM native_health WHERE "+healthWhere, healthArgs(l, k)...).Scan(&raw, &h.Now)
	if e == sql.ErrNoRows {
		h.Revision = 1
		h.Payload = json.RawMessage(`{}`)
		e = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&h.Now)
		return
	}
	if e != nil {
		return
	}
	e = json.Unmarshal(raw, &h)
	return
}
func (s *Store) saveHealth(ctx context.Context, tx *sql.Tx, l Lease, k NativeHealthKey, h NativeHealth) error {
	if len(h.Payload) == 0 {
		h.Payload = json.RawMessage(`{}`)
	}
	if len(h.Payload) > 32<<10 || !json.Valid(h.Payload) || h.Failures < 0 {
		return ErrConflict
	}
	for _, deadline := range []time.Time{h.CooldownUntil, h.AvoidUntil, h.NextProbeAt} {
		if deadline.After(h.Now.Add(32 * 24 * time.Hour)) {
			return ErrConflict
		}
	}
	raw, e := json.Marshal(h)
	if e != nil {
		return e
	}
	args := append(healthArgs(l, k), l.Principal.Scope.Generation, raw)
	_, e = tx.ExecContext(ctx, "INSERT INTO native_health(org_id,user_id,account_id,credential_version,lane,model,generation,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(org_id,user_id,account_id,credential_version,lane,model) DO UPDATE SET state=excluded.state,updated_at=clock_timestamp()", args...)
	return e
}
func (s *Store) GetNativeHealth(ctx context.Context, l Lease, k NativeHealthKey) (out NativeHealth, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.healthAuthority(ctx, tx, l, k); e != nil {
			return e
		}
		var e error
		out, e = s.readHealth(ctx, tx, l, k)
		return e
	})
	return
}
func (s *Store) mergeHealth(ctx context.Context, tx *sql.Tx, l Lease, k NativeHealthKey, old NativeHealth, merge func(NativeHealth) (NativeHealth, error), clearProbe bool) error {
	if merge == nil {
		return ErrConflict
	}
	next, e := merge(old)
	if e != nil {
		return e
	}
	next.Now = old.Now
	next.Revision = old.Revision + 1
	// Policy cannot forge, release, or replace claim lineage.
	next.ProbeID = old.ProbeID
	next.ProbeEpoch = old.ProbeEpoch
	next.ProbeExpiresAt = old.ProbeExpiresAt
	next.LastProbeAt = old.LastProbeAt
	if k.Lane == NativeHealthFamily && !clearProbe && next.ProbeEligible && (!next.CooldownUntil.Equal(old.CooldownUntil) || !bytes.Equal(next.Payload, old.Payload)) {
		next.ProbeID = ""
		next.ProbeExpiresAt = time.Time{}
		next.ProbeEpoch = old.ProbeEpoch + 1
	}
	if clearProbe {
		next.ProbeID = ""
		next.ProbeExpiresAt = time.Time{}
		if k.Lane == NativeHealthQuota {
			next.LastProbeAt = old.Now
			if next.NextProbeAt.Before(old.Now.Add(5 * time.Minute)) {
				next.NextProbeAt = old.Now.Add(5 * time.Minute)
			}
		}
	}
	return s.saveHealth(ctx, tx, l, k, next)
}
func (s *Store) UpdateNativeHealth(ctx context.Context, l Lease, k NativeHealthKey, merge func(NativeHealth) (NativeHealth, error)) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.healthAuthority(ctx, tx, l, k); e != nil {
			return e
		}
		h, e := s.readHealth(ctx, tx, l, k)
		if e != nil {
			return e
		}
		return s.mergeHealth(ctx, tx, l, k, h, merge, false)
	})
}
func healthOrdinaryAllowed(h NativeHealth, k NativeHealthKey) bool {
	return !h.CooldownUntil.After(h.Now) && (k.Lane != NativeHealthTransient || !h.AvoidUntil.After(h.Now)) && (k.Lane != NativeHealthFamily || !h.ProbeEligible)
}
func (s *Store) AcquireNativeHealthPermit(ctx context.Context, l Lease, k NativeHealthKey, probe bool) (NativeHealthPermit, error) {
	return s.AcquireNativeHealthPermitForRecovery(ctx, l, k, probe, "", "")
}
func (s *Store) AcquireNativeHealthPermitForRecovery(ctx context.Context, l Lease, k NativeHealthKey, probe bool, poolKey string, class NativeRecoveryClass) (NativeHealthPermit, error) {
	return s.AcquireNativeHealthPermitForRecoveryBound(ctx, l, k, probe, poolKey, class, nil)
}
func (s *Store) AcquireNativeHealthPermitBound(ctx context.Context, l Lease, k NativeHealthKey, probe bool, bindings []NativeHealthBinding) (NativeHealthPermit, error) {
	return s.AcquireNativeHealthPermitForRecoveryBound(ctx, l, k, probe, "", "", bindings)
}
func (s *Store) AcquireNativeHealthPermitForRecoveryBound(ctx context.Context, l Lease, k NativeHealthKey, probe bool, poolKey string, class NativeRecoveryClass, bindings []NativeHealthBinding) (out NativeHealthPermit, e error) {
	bindings = append(append([]NativeHealthBinding(nil), dispatchBindings(ctx)...), bindings...)
	if (poolKey == "") != (class == "") || class != "" && class != NativeRecoveryInitial && class != NativeRecoveryRetry && class != NativeRecoveryProbe || class == NativeRecoveryProbe && !probe {
		return out, ErrConflict
	}
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.healthAuthority(ctx, tx, l, k); e != nil {
			return e
		}
		if e := s.checkDispatchLease(ctx, tx, l); e != nil {
			return e
		}
		if e := s.checkNativeHealthBindings(ctx, tx, l, bindings); e != nil {
			return e
		}
		h, e := s.readHealth(ctx, tx, l, k)
		if e != nil {
			return e
		}
		out.ID = core.ID()
		if probe {
			if h.NextProbeAt.After(h.Now) {
				return ErrBusy
			}
			if k.Lane == NativeHealthTransient {
				if h.ProbeID != "" && h.ProbeExpiresAt.After(h.Now) || h.LastProbeAt.Add(15*time.Second).After(h.Now) {
					return ErrBusy
				}
				h.ProbeExpiresAt = h.Now.Add(30 * time.Second)
			} else if k.Lane == NativeHealthFamily {
				if h.CooldownUntil.After(h.Now) || !h.ProbeEligible || h.ProbeID != "" {
					return ErrBusy
				}
				h.ProbeExpiresAt = time.Time{}
			} else {
				if !h.CooldownUntil.After(h.Now) || !h.ProbeEligible || h.ProbeID != "" || h.CooldownSince.Add(5*time.Minute).After(h.Now) || h.LastProbeAt.Add(5*time.Minute).After(h.Now) {
					return ErrBusy
				}
				h.ProbeExpiresAt = time.Time{}
			}
			h.ProbeEpoch++
			h.ProbeID = out.ID
			h.LastProbeAt = h.Now
		} else if !healthOrdinaryAllowed(h, k) {
			return ErrBusy
		}
		if class == NativeRecoveryProbe {
			if _, e = s.chargeNativeRecovery(ctx, tx, l, poolKey, class); e != nil {
				return e
			}
		}
		if e = s.saveHealth(ctx, tx, l, k, h); e != nil {
			return e
		}
		sc := l.Principal.Scope
		_, e = tx.ExecContext(ctx, "INSERT INTO native_health_permits(id,org_id,user_id,key_id,admission_id,generation,account_id,credential_version,lane,model,revision,probe_epoch,probe,status,recovery_pool,recovery_class,bindings) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'new',$14,$15,$16)", out.ID, sc.OrganizationID, sc.UserID, l.Principal.KeyID, l.ID, sc.Generation, k.AccountID, k.CredentialVersion, k.Lane, k.Model, h.Revision, h.ProbeEpoch, probe, poolKey, class, encodeHealthBindings(bindings))
		return e
	})
	if e != nil {
		out = NativeHealthPermit{}
	}
	return
}

type healthReceipt struct {
	bindings        []NativeHealthBinding
	pool            string
	class           NativeRecoveryClass
	key             NativeHealthKey
	revision, epoch int64
	probe           bool
	status          string
	created         time.Time
}

func (s *Store) healthReceipt(ctx context.Context, tx *sql.Tx, l Lease, p NativeHealthPermit) (r healthReceipt, e error) {
	sc := l.Principal.Scope
	var bindings []byte
	e = tx.QueryRowContext(ctx, "SELECT account_id,credential_version,lane,model,revision,probe_epoch,probe,status,created_at,recovery_pool,recovery_class,bindings FROM native_health_permits WHERE id=$1 AND org_id=$2 AND user_id=$3 AND key_id=$4 AND admission_id=$5 AND generation=$6", p.ID, sc.OrganizationID, sc.UserID, l.Principal.KeyID, l.ID, sc.Generation).Scan(&r.key.AccountID, &r.key.CredentialVersion, &r.key.Lane, &r.key.Model, &r.revision, &r.epoch, &r.probe, &r.status, &r.created, &r.pool, &r.class, &bindings)
	if e == sql.ErrNoRows {
		e = ErrConflict
	}
	if e == nil {
		e = json.Unmarshal(bindings, &r.bindings)
	}
	return
}
func checkHealthProbe(h NativeHealth, r healthReceipt, id string) error {
	if h.ProbeID != id || h.ProbeEpoch != r.epoch {
		return ErrConflict
	}
	if r.key.Lane == NativeHealthTransient && !h.ProbeExpiresAt.After(h.Now) {
		return ErrExpired
	}
	if h.Revision != r.revision {
		return ErrConflict
	}
	return nil
}

// WithNativeHealthDispatch reserves receipts durably before invoking the physical
// write gate. A failed transaction after reservation cannot make them replayable.
func (s *Store) WithNativeHealthDispatch(ctx context.Context, l Lease, permits []NativeHealthPermit, start func(context.Context) error) error {
	if len(permits) == 0 || len(permits) > 4 || start == nil {
		return ErrConflict
	}
	validate := func(tx *sql.Tx, want string) error {
		if e := s.checkDispatchLease(ctx, tx, l); e != nil {
			return e
		}
		seen := map[string]bool{}
		account := ""
		version := ""
		for _, p := range permits {
			if seen[p.ID] {
				return ErrConflict
			}
			seen[p.ID] = true
			r, e := s.healthReceipt(ctx, tx, l, p)
			if e != nil {
				return e
			}
			if e := s.checkNativeHealthBindings(ctx, tx, l, r.bindings); e != nil {
				return e
			}
			if r.status != want {
				return ErrConflict
			}
			if account != "" && (account != r.key.AccountID || version != r.key.CredentialVersion) {
				return ErrConflict
			}
			account = r.key.AccountID
			version = r.key.CredentialVersion
			if e = s.healthAuthority(ctx, tx, l, r.key); e != nil {
				return e
			}
			h, e := s.readHealth(ctx, tx, l, r.key)
			if e != nil {
				return e
			}
			if r.revision != h.Revision {
				return ErrConflict
			}
			if r.probe {
				if e = checkHealthProbe(h, r, p.ID); e != nil {
					return e
				}
			} else if !healthOrdinaryAllowed(h, r.key) || r.created.Add(24*time.Hour).Before(h.Now) {
				return ErrBusy
			}
		}
		return nil
	}
	if e := s.tx(ctx, func(tx *sql.Tx) error {
		if e := validate(tx, "new"); e != nil {
			return e
		}
		// Debit retries durably before the physical gate. If a send or its final
		// commit becomes ambiguous, capacity stays consumed rather than returning.
		charged := map[string]bool{}
		for _, p := range permits {
			r, e := s.healthReceipt(ctx, tx, l, p)
			if e != nil {
				return e
			}
			if r.class == NativeRecoveryRetry && !charged[r.pool] {
				if _, e = s.chargeNativeRecovery(ctx, tx, l, r.pool, r.class); e != nil {
					return e
				}
				charged[r.pool] = true
			}
		}
		for _, p := range permits {
			if _, e := tx.ExecContext(ctx, "UPDATE native_health_permits SET status='reserved' WHERE id=$1", p.ID); e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		return e
	}
	var sendErr error
	e := s.tx(ctx, func(tx *sql.Tx) error {
		if e := validate(tx, "reserved"); e != nil {
			return e
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		charged := map[string]bool{}
		for _, p := range permits {
			r, e := s.healthReceipt(ctx, tx, l, p)
			if e != nil {
				return e
			}
			if r.class == NativeRecoveryInitial {
				chargeKey := r.pool + "/" + string(r.class)
				if !charged[chargeKey] {
					if _, e = s.chargeNativeRecovery(ctx, tx, l, r.pool, r.class); e != nil {
						return e
					}
					charged[chargeKey] = true
				}
			}
		}
		sendErr = start(ctx)
		for _, p := range permits {
			if _, e := tx.ExecContext(ctx, "UPDATE native_health_permits SET status='dispatched' WHERE id=$1", p.ID); e != nil {
				return e
			}
		}
		// Commit the receipt even when the physical write returns an ambiguous error.
		return nil
	})
	return errors.Join(sendErr, e)
}
func (s *Store) FinishNativeHealthPermit(ctx context.Context, l Lease, p NativeHealthPermit, merge func(NativeHealth) (NativeHealth, error)) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkLease(ctx, tx, l); e != nil {
			return e
		}
		r, e := s.healthReceipt(ctx, tx, l, p)
		if e != nil {
			return e
		}
		if e = s.healthAuthority(ctx, tx, l, r.key); e != nil {
			return e
		}
		if e := s.checkNativeHealthBindings(ctx, tx, l, r.bindings); e != nil {
			return e
		}
		if r.status != "dispatched" {
			return ErrConflict
		}
		h, e := s.readHealth(ctx, tx, l, r.key)
		if e != nil {
			return e
		}
		if r.key.Lane == NativeHealthFamily && h.Revision != r.revision {
			return ErrConflict
		}
		if r.probe {
			if e = checkHealthProbe(h, r, p.ID); e != nil {
				return e
			}
		} else if r.created.Add(24 * time.Hour).Before(h.Now) {
			return ErrExpired
		}
		// Ordinary terminal samples merge into current state even if another sample
		// already advanced its revision. Probe completion remains lineage-fenced.
		if e = s.mergeHealth(ctx, tx, l, r.key, h, merge, r.probe); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE native_health_permits SET status='finished' WHERE id=$1", p.ID)
		return e
	})
}
func (s *Store) ReleaseNativeHealthPermit(ctx context.Context, l Lease, p NativeHealthPermit) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkLease(ctx, tx, l); e != nil {
			return e
		}
		r, e := s.healthReceipt(ctx, tx, l, p)
		if e != nil {
			return e
		}
		if e = s.healthAuthority(ctx, tx, l, r.key); e != nil {
			return e
		}
		if r.status == "released" {
			return nil
		}
		if r.status == "finished" {
			return ErrConflict
		}
		h, e := s.readHealth(ctx, tx, l, r.key)
		if e != nil {
			return e
		}
		if r.probe && h.ProbeID == p.ID && h.ProbeEpoch == r.epoch {
			h.ProbeID = ""
			h.ProbeExpiresAt = time.Time{}
			if r.key.Lane == NativeHealthQuota {
				h.LastProbeAt = h.Now
				if h.NextProbeAt.Before(h.Now.Add(5 * time.Minute)) {
					h.NextProbeAt = h.Now.Add(5 * time.Minute)
				}
			}
			if e = s.saveHealth(ctx, tx, l, r.key, h); e != nil {
				return e
			}
		}
		_, e = tx.ExecContext(ctx, "UPDATE native_health_permits SET status='released' WHERE id=$1", p.ID)
		return e
	})
}
