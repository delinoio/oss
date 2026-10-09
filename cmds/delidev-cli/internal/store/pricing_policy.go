// SPDX-License-Identifier: Apache-2.0
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const policyPrefix = "token-price-policy:"

func (t *Tx) PricingPolicy(model domain.ModelIdentity) (domain.PricingPolicy, error) {
	p := domain.PricingPolicy{Version: 1, Model: model, Mode: domain.AutomaticPricing}
	if e := model.Validate(); e != nil {
		return p, e
	}
	var raw []byte
	e := t.tx.QueryRowContext(t.ctx, "SELECT value FROM metadata WHERE key=?", policyPrefix+string(model.Key())).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		current, e := t.retainedActivePricing(model.Key())
		if e != nil {
			return p, e
		}
		if current != nil && current.Provenance == nil {
			p.Mode = domain.ManualPricing
		}
		return p, nil
	}
	if e != nil {
		return p, storageError(e)
	}
	if len(raw) > 4096 || domain.Decode(raw, &p) != nil || p.Validate() != nil || p.Model != model {
		return p, corrupt()
	}
	return p, nil
}
func (t *Tx) SetPricingPolicy(model domain.ModelIdentity, mode domain.PricingMode, expected uint64) (domain.PricingPolicy, error) {
	p, e := t.PricingPolicy(model)
	if e != nil {
		return p, e
	}
	if t.readOnly {
		return p, domain.Fail(domain.PermissionDenied, "Read transactions cannot change pricing policy.", "Use the authenticated policy mutation.")
	}
	if p.Revision != expected || expected >= 1<<63-1 {
		return p, domain.Fail(domain.Conflict, "The pricing policy changed.", "Read the current policy and preserve the original staged edit.")
	}
	p.Mode = mode
	p.Revision++
	if e = p.Validate(); e != nil {
		return p, e
	}
	raw, e := json.Marshal(p)
	if e != nil {
		return p, e
	}
	_, e = t.tx.ExecContext(t.ctx, "INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", policyPrefix+string(model.Key()), raw)
	return p, storageError(e)
}
func (t *Tx) ClearActivePricing(model domain.ModelIdentity) error {
	if t.readOnly {
		return domain.Fail(domain.PermissionDenied, "Read transactions cannot change pricing.", "Use a pricing mutation.")
	}
	_, e := t.tx.ExecContext(t.ctx, "DELETE FROM active_pricing WHERE model_key=?", model.Key())
	return storageError(e)
}
func (t *Tx) PricingIdentities() ([]domain.ModelIdentity, error) {
	seen := map[domain.ID]bool{}
	result := []domain.ModelIdentity{}
	add := func(m domain.ModelIdentity) error {
		if e := m.Validate(); e != nil {
			return e
		}
		if !seen[m.Key()] {
			seen[m.Key()] = true
			result = append(result, m)
		}
		if len(result) > 5000 {
			return domain.Fail(domain.ResourceExhausted, "Too many source pricing identities.", "Reduce unused source pricing policies.")
		}
		return nil
	}
	rows, e := t.tx.QueryContext(t.ctx, "SELECT value FROM metadata WHERE key LIKE 'token-price-policy:%' ORDER BY key LIMIT 5001")
	if e != nil {
		return nil, storageError(e)
	}
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return nil, storageError(e)
		}
		var p domain.PricingPolicy
		if domain.Decode(raw, &p) != nil || p.Validate() != nil {
			rows.Close()
			return nil, corrupt()
		}
		if e = add(p.Model); e != nil {
			rows.Close()
			return nil, e
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, storageError(e)
	}
	// Default revision-zero Automatic policies have no metadata row. Retained
	// active prices must remain discoverable after the last route is removed.
	rows, e = t.tx.QueryContext(t.ctx, "SELECT model_key FROM active_pricing ORDER BY model_key LIMIT 5001")
	if e != nil {
		return nil, storageError(e)
	}
	for rows.Next() {
		var key domain.ID
		if e = rows.Scan(&key); e != nil {
			rows.Close()
			return nil, storageError(e)
		}
		m, parseError := domain.ParseModelKey(key)
		if parseError != nil {
			rows.Close()
			return nil, corrupt()
		}
		if e = add(m); e != nil {
			rows.Close()
			return nil, e
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, storageError(e)
	}
	// Stream every Agent row; the bound applies to distinct identities, not
	// duplicate routes or raw records. The cursor retains one bounded body.
	rows, e = t.tx.QueryContext(t.ctx, "SELECT body FROM entities WHERE kind='agent' ORDER BY id")
	if e != nil {
		return nil, storageError(e)
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			return nil, storageError(e)
		}
		var a domain.Agent
		if domain.Decode(raw, &a) != nil || a.Validate() != nil {
			return nil, corrupt()
		}
		for _, route := range a.SourceRoutes() {
			if e = add(route.Model.ModelIdentity); e != nil {
				return nil, e
			}
		}
	}
	return result, storageError(rows.Err())
}
