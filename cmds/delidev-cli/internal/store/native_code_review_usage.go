// SPDX-License-Identifier: Apache-2.0
package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const nativeReviewUsagePrefix = "native-code-review-usage-v1:"

// NativeCodeReviewUsage preserves an independent auxiliary response and its
// original price. The legacy response table's closed purpose set stays intact.
type NativeCodeReviewUsage struct {
	Version   uint32                     `json:"version"`
	ID        domain.ID                  `json:"id"`
	JobID     domain.ID                  `json:"job_id"`
	CreatedAt time.Time                  `json:"observed_at"`
	Record    domain.ResponseUsageRecord `json:"record"`
	Estimate  domain.ResponseEstimate    `json:"estimate"`
	Pricing   *PricingVersion            `json:"pricing,omitempty"`
}

func nativeReviewUsageKey(record domain.ResponseUsageRecord) string {
	return nativeReviewUsagePrefix + string(record.AccountID) + ":" + string(record.ProviderID) + ":" + record.Usage.ResponseDigest
}

func (t *Tx) PutNativeCodeReviewUsage(job, id domain.ID, record domain.ResponseUsageRecord) (domain.ID, bool, error) {
	if err := t.writeAllowed(); err != nil {
		return "", false, err
	}
	if job.Validate() != nil || id.Validate() != nil || record.Validate() != nil || record.Purpose != domain.NativeCodeReviewUsage {
		return "", false, domain.NativeCodeReviewUnavailable()
	}
	if _, err := t.Get(domain.SessionKind, record.SessionID); err != nil {
		return "", false, err
	}
	var legacy string
	err := t.tx.QueryRowContext(t.ctx, "SELECT id FROM response_usage WHERE account_id=? AND provider_id=? AND response_digest=?", record.AccountID, record.ProviderID, record.Usage.ResponseDigest).Scan(&legacy)
	if err == nil {
		return "", false, domain.NativeCodeReviewUnavailable()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, storageError(err)
	}
	var raw string
	err = t.tx.QueryRowContext(t.ctx, "SELECT value FROM metadata WHERE key=?", nativeReviewUsageKey(record)).Scan(&raw)
	if err == nil {
		var previous NativeCodeReviewUsage
		if len(raw) > 32<<10 || domain.Decode([]byte(raw), &previous) != nil || previous.Version != 1 || previous.Record.Validate() != nil || previous.ID.Validate() != nil || previous.JobID != job || validateResponseEstimate(previous.Estimate, previous.Record, previous.Pricing) != nil {
			return "", false, domain.NativeCodeReviewUnavailable()
		}
		left, right := previous.Record, record
		left.Sequence, right.Sequence = 0, 0
		a, _ := json.Marshal(left)
		b, _ := json.Marshal(right)
		if !bytes.Equal(a, b) {
			return "", false, domain.NativeCodeReviewUnavailable()
		}
		return previous.ID, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, storageError(err)
	}
	var count int
	if err = t.tx.QueryRowContext(t.ctx, "SELECT count(*) FROM metadata WHERE key LIKE ? AND (json_extract(value,'$.job_id')=? OR json_extract(value,'$.id')=?)", nativeReviewUsagePrefix+"%", job, id).Scan(&count); err != nil {
		return "", false, storageError(err)
	}
	if count >= 128 {
		return "", false, domain.NativeCodeReviewUnavailable()
	}
	var existingID int
	if err = t.tx.QueryRowContext(t.ctx, "SELECT count(*) FROM metadata WHERE key LIKE ? AND json_extract(value,'$.id')=?", nativeReviewUsagePrefix+"%", id).Scan(&existingID); err != nil || existingID != 0 {
		return "", false, domain.NativeCodeReviewUnavailable()
	}
	value := NativeCodeReviewUsage{Version: 1, ID: id, JobID: job, CreatedAt: t.now, Record: record, Estimate: missingPriceEstimate()}
	if record.Attribution == "" {
		basis, err := t.ActivePricing(record.ModelID)
		if err != nil {
			return "", false, err
		}
		if basis != nil && basis.ProviderID == record.ProviderID && basis.SubscriptionService == record.SubscriptionService {
			value.Estimate, err = domain.EstimateObservedResponse(basis.ID, basis.Basis, record.Usage)
			if err != nil {
				return "", false, err
			}
			value.Pricing = basis
		}
	}
	body, err := json.Marshal(value)
	if err != nil || len(body) > 32<<10 {
		return "", false, domain.NativeCodeReviewUnavailable()
	}
	if _, err = t.tx.ExecContext(t.ctx, "INSERT INTO metadata(key,value) VALUES(?,?)", nativeReviewUsageKey(record), string(body)); err != nil {
		return "", false, storageError(err)
	}
	if err = t.addNativeCodeReviewEstimate(record.SessionID, value.Estimate); err != nil {
		return "", false, err
	}
	return id, false, nil
}

func (t *Tx) NativeCodeReviewUsage(job domain.ID) ([]NativeCodeReviewUsage, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT value FROM metadata WHERE key LIKE ? AND json_extract(value,'$.job_id')=? ORDER BY key LIMIT 129", nativeReviewUsagePrefix+"%", job)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	values := make([]NativeCodeReviewUsage, 0)
	for rows.Next() {
		var raw string
		var value NativeCodeReviewUsage
		if rows.Scan(&raw) != nil || len(raw) > 32<<10 || domain.Decode([]byte(raw), &value) != nil || value.Version != 1 || value.ID.Validate() != nil || value.JobID != job || value.Record.Validate() != nil || value.Record.Purpose != domain.NativeCodeReviewUsage || validateResponseEstimate(value.Estimate, value.Record, value.Pricing) != nil {
			return nil, domain.NativeCodeReviewUnavailable()
		}
		values = append(values, value)
		if len(values) > 128 {
			return nil, domain.NativeCodeReviewUnavailable()
		}
	}
	return values, storageError(rows.Err())
}

const nativeReviewEstimatePrefix = "native-code-review-estimate-v1:"

func (t *Tx) NativeCodeReviewEstimate(session domain.ID, currency domain.Currency) (domain.BudgetEvidence, error) {
	value := domain.BudgetEvidence{Currency: currency}
	var raw string
	err := t.tx.QueryRowContext(t.ctx, "SELECT value FROM metadata WHERE key=?", nativeReviewEstimatePrefix+string(session)+":"+string(currency)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return value, value.Validate()
	}
	if err != nil {
		return value, storageError(err)
	}
	if len(raw) > 2048 || domain.Decode([]byte(raw), &value) != nil || value.Currency != currency || value.Validate() != nil {
		return value, domain.NativeCodeReviewUnavailable()
	}
	return value, nil
}
func (t *Tx) addNativeCodeReviewEstimate(session domain.ID, estimate domain.ResponseEstimate) error {
	value, err := t.NativeCodeReviewEstimate(session, estimate.Currency)
	if err != nil {
		return err
	}
	if err = value.Add(estimate); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", nativeReviewEstimatePrefix+string(session)+":"+string(estimate.Currency), string(raw))
	return storageError(err)
}
func (t *Tx) DeleteNativeCodeReviewSessionUsage(session domain.ID) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	_, err := t.tx.ExecContext(t.ctx, "DELETE FROM metadata WHERE (key LIKE ? AND json_extract(value,'$.record.session_id')=?) OR key LIKE ?", nativeReviewUsagePrefix+"%", session, nativeReviewEstimatePrefix+string(session)+":%")
	return storageError(err)
}
