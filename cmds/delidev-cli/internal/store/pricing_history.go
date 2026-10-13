// SPDX-License-Identifier: Apache-2.0
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const pricingClearedPrefix = "pricing-cleared-v1:"

// pricingCleared records when an active price pointer stopped selecting one
// immutable version. Pricing versions already keep their activation instant
// (created_at); a clear is the only event that left no history before.
type pricingCleared struct {
	Version   uint32 `json:"version"`
	ClearedAt int64  `json:"cleared_at"`
}

func pricingClearedKey(id domain.ID) string { return pricingClearedPrefix + string(id) }

// recordPricingCleared retains the first clear instant for the version that was
// active. It is called inside the transaction that removes the pointer.
func (t *Tx) recordPricingCleared(model domain.ID) error {
	var id domain.ID
	err := t.tx.QueryRowContext(t.ctx, "SELECT pricing_id FROM active_pricing WHERE model_key=?", model).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return storageError(err)
	}
	body, err := json.Marshal(pricingCleared{Version: 1, ClearedAt: t.now.UnixMilli()})
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO NOTHING", pricingClearedKey(id), string(body))
	return storageError(err)
}

func (t *Tx) pricingClearedAt(id domain.ID) (int64, bool, error) {
	var raw string
	err := t.tx.QueryRowContext(t.ctx, "SELECT value FROM metadata WHERE key=?", pricingClearedKey(id)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, storageError(err)
	}
	var value pricingCleared
	if len(raw) > 256 || domain.Decode([]byte(raw), &value) != nil || value.Version != 1 || value.ClearedAt <= 0 {
		return 0, false, corrupt()
	}
	return value.ClearedAt, true, nil
}

// PricingAt selects the immutable version that was eligible for a model at the
// given instant from retained history only. It never runs the automatic price
// resolver, so a later refresh cannot change what was eligible earlier. A nil
// result means no price was eligible then.
func (t *Tx) PricingAt(model domain.ID, at time.Time) (*PricingVersion, error) {
	if err := domain.ValidateModelKey(model); err != nil {
		return nil, err
	}
	var id domain.ID
	err := t.tx.QueryRowContext(t.ctx, "SELECT id FROM pricing_versions WHERE model_key=? AND created_at<=? ORDER BY created_at DESC, revision DESC LIMIT 1", model, at.UnixMilli()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, storageError(err)
	}
	value, err := t.Pricing(id)
	if err != nil {
		return nil, err
	}
	if value.ModelID != model {
		return nil, corrupt()
	}
	cleared, found, err := t.pricingClearedAt(id)
	if err != nil {
		return nil, err
	}
	if found {
		if cleared <= at.UnixMilli() {
			return nil, nil
		}
		return &value, nil
	}
	// Clears before clear history existed left no instant. When the newest
	// version is no longer selected its clear time is unknown, so report no
	// eligible price rather than guess one.
	active, err := t.retainedActivePricing(model)
	if err != nil {
		return nil, err
	}
	if active == nil {
		var latest uint64
		if err = t.tx.QueryRowContext(t.ctx, "SELECT COALESCE(MAX(revision),0) FROM pricing_versions WHERE model_key=?", model).Scan(&latest); err != nil {
			return nil, storageError(err)
		}
		if latest == value.Revision {
			return nil, nil
		}
	}
	return &value, nil
}

// pricingChangedAfter reports whether any price was activated or cleared for the
// model after the instant, i.e. whether the current selection may differ from
// the one that was eligible then.
func (t *Tx) pricingChangedAfter(model domain.ID, at time.Time) (bool, error) {
	var count int
	err := t.tx.QueryRowContext(t.ctx, "SELECT (SELECT count(*) FROM pricing_versions WHERE model_key=? AND created_at>?)+(SELECT count(*) FROM pricing_versions p JOIN metadata m ON m.key=?||p.id WHERE p.model_key=? AND json_extract(m.value,'$.cleared_at')>?)", model, at.UnixMilli(), pricingClearedPrefix, model, at.UnixMilli()).Scan(&count)
	if err != nil {
		return false, storageError(err)
	}
	return count > 0, nil
}
