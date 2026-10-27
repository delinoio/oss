// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/delinoio/oss/packages/aiprouter/core"
)

func readSettings(ctx context.Context, tx *sql.Tx, lock bool) (v Settings, e error) {
	q := "SELECT rpm,concurrency,usage_days,aggregate_days,audit_days,destinations,prices,revision FROM settings WHERE singleton"
	if lock {
		q += " FOR UPDATE"
	}
	var dest []byte
	e = tx.QueryRowContext(ctx, q).Scan(&v.RPM, &v.Concurrency, &v.UsageDays, &v.AggregateDays, &v.AuditDays, &dest, &v.Prices, &v.Revision)
	if e == nil {
		e = json.Unmarshal(dest, &v.DestinationRules)
		if e != nil {
			v.DestinationRules = nil
			e = json.Unmarshal(dest, &v.Destinations)
			if e == nil {
				for _, origin := range v.Destinations {
					v.DestinationRules = append(v.DestinationRules, AllowedDestination{Origin: origin})
				}
			}
		}
		if e == nil {
			v.DestinationRules, e = core.NormalizeDestinationRules(v.DestinationRules)
			v.Destinations = nil
			for _, r := range v.DestinationRules {
				v.Destinations = append(v.Destinations, r.Origin)
			}
		}
	}
	return
}
func (s *Store) writeSettings(ctx context.Context, tx *sql.Tx, i core.Identity, v Settings) (Settings, error) {
	if v.RPM < 1 || v.RPM > 6000 || v.Concurrency < 1 || v.Concurrency > 100 || v.UsageDays < 1 || v.UsageDays > 90 || v.AggregateDays < 30 || v.AggregateDays > 730 || v.AuditDays < 30 || v.AuditDays > 365 {
		return v, ErrConflict
	}
	if len(v.Prices) == 0 {
		v.Prices = json.RawMessage(`{}`)
	}
	if !json.Valid(v.Prices) {
		return v, ErrConflict
	}
	if _, e := tx.ExecContext(ctx, "UPDATE settings SET usage_history_floor=GREATEST(usage_history_floor,clock_timestamp()-LEAST(usage_days,$1)*interval '1 day')", v.UsageDays); e != nil {
		return v, e
	}
	if v.DestinationRules == nil {
		for _, origin := range v.Destinations {
			v.DestinationRules = append(v.DestinationRules, AllowedDestination{Origin: origin})
		}
	}
	rules, e := core.NormalizeDestinationRules(v.DestinationRules)
	if e != nil {
		return v, e
	}
	v.DestinationRules = rules
	v.Destinations = nil
	for _, r := range rules {
		v.Destinations = append(v.Destinations, r.Origin)
	}
	d, e := json.Marshal(rules)
	if e != nil {
		return v, e
	}
	e = tx.QueryRowContext(ctx, "UPDATE settings SET rpm=$1,concurrency=$2,usage_days=$3,aggregate_days=$4,audit_days=$5,destinations=$6,prices=$7,revision=revision+1 WHERE singleton RETURNING revision", v.RPM, v.Concurrency, v.UsageDays, v.AggregateDays, v.AuditDays, d, v.Prices).Scan(&v.Revision)
	if e != nil {
		return v, e
	}
	for _, p := range []struct {
		table, created string
		days           int
	}{{"usage", "created_at", v.UsageDays}, {"daily_usage", "day::timestamptz", v.AggregateDays}, {"audits", "created_at", v.AuditDays}} {
		if _, e = tx.ExecContext(ctx, "UPDATE "+p.table+" SET expires_at=LEAST(expires_at,"+p.created+"+$1*interval '1 day')", p.days); e != nil {
			return v, e
		}
	}
	var actor string
	e = tx.QueryRowContext(ctx, "SELECT id FROM users WHERE issuer=$1 AND subject=$2", i.Issuer, i.Subject).Scan(&actor)
	if e != nil && e != sql.ErrNoRows {
		return v, e
	}
	e = s.audit(ctx, tx, "", actor, "instance.settings_changed", "")
	return v, e
}
func (s *Store) UpdateSettingsCAS(ctx context.Context, i core.Identity, expected int64, v Settings) (out Settings, e error) {
	if !s.IsOperator(i) {
		return out, ErrForbidden
	}
	if expected < 1 {
		return out, ErrConflict
	}
	e = s.tx(ctx, func(tx *sql.Tx) error {
		old, e := readSettings(ctx, tx, true)
		if e != nil {
			return e
		}
		if old.Revision != expected {
			return ErrConflict
		}
		out, e = s.writeSettings(ctx, tx, i, v)
		return e
	})
	return
}
func (s *Store) PutPriceOverride(ctx context.Context, i core.Identity, expected int64, key string, value json.RawMessage) (Settings, error) {
	if key == "" || !json.Valid(value) {
		return Settings{}, ErrConflict
	}
	return s.mutatePrice(ctx, i, expected, key, value)
}
func (s *Store) DeletePriceOverride(ctx context.Context, i core.Identity, expected int64, key string) (Settings, error) {
	return s.mutatePrice(ctx, i, expected, key, nil)
}
func (s *Store) mutatePrice(ctx context.Context, i core.Identity, expected int64, key string, value json.RawMessage) (out Settings, e error) {
	if !s.IsOperator(i) {
		return out, ErrForbidden
	}
	if expected < 1 || key == "" {
		return out, ErrConflict
	}
	e = s.tx(ctx, func(tx *sql.Tx) error {
		v, e := readSettings(ctx, tx, true)
		if e != nil {
			return e
		}
		if v.Revision != expected {
			return ErrConflict
		}
		prices := map[string]json.RawMessage{}
		if e = json.Unmarshal(v.Prices, &prices); e != nil {
			return ErrConflict
		}
		if value == nil {
			delete(prices, key)
		} else {
			prices[key] = value
		}
		v.Prices, e = json.Marshal(prices)
		if e != nil {
			return e
		}
		out, e = s.writeSettings(ctx, tx, i, v)
		if e == nil {
			var actor string
			err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE issuer=$1 AND subject=$2", i.Issuer, i.Subject).Scan(&actor)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			action := "price.override_set"
			if value == nil {
				action = "price.override_deleted"
			}
			e = s.audit(ctx, tx, "", actor, action, key)
		}
		return e
	})
	return
}
