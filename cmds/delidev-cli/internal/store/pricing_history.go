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
const pricingUnknownClearPrefix = "pricing-unknown-clear-v1:"

// pricingCleared records when an active price pointer stopped selecting one
// immutable version. Pricing versions already keep their activation instant
// (created_at); a clear is the only event that left no history before.
type pricingCleared struct {
	Version   uint32 `json:"version"`
	ClearedAt int64  `json:"cleared_at"`
}

// pricingUnknownClear retains the uncertainty of a legacy clear that has no
// recorded instant. Reactivation must not turn that unknown interval into an
// eligible historical price. It can be removed only if the original clear
// instant is independently recovered.
type pricingUnknownClear struct {
	Version uint32 `json:"version"`
}

func pricingClearedKey(id domain.ID) string      { return pricingClearedPrefix + string(id) }
func pricingUnknownClearKey(id domain.ID) string { return pricingUnknownClearPrefix + string(id) }

func (t *Tx) pricingUnknownClear(id domain.ID) (bool, error) {
	var raw []byte
	err := t.tx.QueryRowContext(t.ctx, "SELECT value FROM metadata WHERE key=?", pricingUnknownClearKey(id)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, storageError(err)
	}
	var value pricingUnknownClear
	if len(raw) > 256 || domain.Decode(raw, &value) != nil || value.Version != 1 {
		return false, corrupt()
	}
	return true, nil
}

// retainUnknownPricingClear runs before the next activation while the absent
// active pointer still proves that the latest unmarked version was cleared.
func (t *Tx) retainUnknownPricingClear(latestID domain.ID) error {
	if latestID == "" {
		return nil
	}
	if _, found, err := t.pricingClearedAt(latestID); err != nil {
		return err
	} else if found {
		return nil
	}
	if found, err := t.pricingUnknownClear(latestID); err != nil {
		return err
	} else if found {
		return nil
	}
	body, err := json.Marshal(pricingUnknownClear{Version: 1})
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO NOTHING", pricingUnknownClearKey(latestID), body)
	return storageError(err)
}

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
	unknown, err := t.pricingUnknownClear(id)
	if err != nil {
		return nil, err
	}
	if unknown {
		return nil, nil
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

func (t *Tx) pricingHistoryExists(model domain.ID) (bool, error) {
	var exists bool
	if err := t.tx.QueryRowContext(t.ctx, "SELECT EXISTS(SELECT 1 FROM pricing_versions WHERE model_key=?)", model).Scan(&exists); err != nil {
		return false, storageError(err)
	}
	return exists, nil
}
