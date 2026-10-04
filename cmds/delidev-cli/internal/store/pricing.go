package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type PricingVersion = domain.PricingVersion

func (t *Tx) Pricing(id domain.ID) (PricingVersion, error) {
	var value PricingVersion
	if err := id.Validate(); err != nil {
		return value, err
	}
	var body []byte
	var created int64
	serviceColumn := "subscription_service"
	if t.historicalPricingV1 {
		serviceColumn = "''"
	}
	err := t.tx.QueryRowContext(t.ctx, "SELECT model_id,provider_id,"+serviceColumn+",revision,body,created_at FROM pricing_versions WHERE id=?", id).Scan(&value.ModelID, &value.ProviderID, &value.SubscriptionService, &value.Revision, &body, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return value, domain.Fail(domain.NotFound, "The pricing version is unavailable.", "Read the selected model's current pricing or retain the original historical version.")
	}
	if err != nil {
		return value, storageError(err)
	}
	if value.ModelID.Validate() != nil || !value.ValidIdentity() || value.Revision == 0 || len(body) > 16<<10 || domain.Decode(body, &value.Basis) != nil || value.Basis.Validate() != nil {
		return value, corrupt()
	}
	value.ID, value.CreatedAt = id, time.UnixMilli(created).UTC()
	return value, nil
}
func (t *Tx) ActivePricing(model domain.ID) (*PricingVersion, error) {
	if err := model.Validate(); err != nil {
		return nil, err
	}
	var id domain.ID
	err := t.tx.QueryRowContext(t.ctx, "SELECT pricing_id FROM active_pricing WHERE model_id=?", model).Scan(&id)
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
	return &value, nil
}

// PutPricing appends a new immutable basis and atomically selects it for future
// observations. Model revisions, prior prices and estimates remain unchanged.
func (t *Tx) PutPricing(model domain.ID, expected uint64, id domain.ID, basis domain.TokenPricing) (PricingVersion, error) {
	var value PricingVersion
	if t.readOnly {
		return value, domain.Fail(domain.PermissionDenied, "Read transactions cannot mutate pricing.", "Use the pricing mutation.")
	}
	if id.Validate() != nil || basis.Validate() != nil {
		return value, domain.Fail(domain.InvalidArgument, "Invalid pricing version.", "Use a fresh UUID and validated explicit token rates.")
	}
	row, err := t.Get(domain.ModelKind, model)
	if err != nil {
		return value, err
	}
	selected, err := Decode[domain.Model](row)
	if err != nil {
		return value, err
	}
	if selected.SourceKind == domain.SubscriptionModel && (!selected.SubscriptionService.Valid() || selected.ProviderID != "") || selected.SourceKind != domain.SubscriptionModel && selected.ProviderID.Validate() != nil {
		return value, corrupt()
	}
	current, err := t.ActivePricing(model)
	if err != nil {
		return value, err
	}
	revision := uint64(0)
	if current != nil {
		revision = current.Revision
	}
	if expected != revision || revision >= uint64(1<<63-1) {
		return value, domain.Fail(domain.Conflict, "The selected pricing version changed.", "Read the current pricing and preserve your staged basis before submitting a new revision.")
	}
	body, err := json.Marshal(basis)
	if err != nil || len(body) > 16<<10 {
		return value, domain.Fail(domain.ResourceExhausted, "The pricing basis exceeds its bound.", "Shorten its source or explicit exclusions.")
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO pricing_versions(id,model_id,provider_id,subscription_service,revision,body,created_at) VALUES(?,?,?,?,?,?,?)", id, model, selected.ProviderID, selected.SubscriptionService, revision+1, body, t.now.UnixMilli())
	if err != nil {
		return value, storageError(err)
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO active_pricing(model_id,pricing_id) VALUES(?,?) ON CONFLICT(model_id) DO UPDATE SET pricing_id=excluded.pricing_id", model, id)
	if err != nil {
		return value, storageError(err)
	}
	// The event names the existing model at its unchanged configuration revision.
	// Pricing owns an independent revision and clients read it through UsageService.
	if err := t.event(row, Updated); err != nil {
		return value, err
	}
	t.touched[model] = true
	return t.Pricing(id)
}

func missingPriceEstimate() domain.ResponseEstimate {
	unavailable := domain.EstimateComponent{State: domain.ComponentMissingPrice}
	return domain.ResponseEstimate{Coverage: domain.EstimateUnavailable, Input: unavailable, CachedInput: unavailable, Output: unavailable}
}
func (t *Tx) snapshotResponseEstimate(id domain.ID, record domain.ResponseUsageRecord) error {
	basis, err := t.ActivePricing(record.ModelID)
	if err != nil {
		return err
	}
	estimate := missingPriceEstimate()
	var pricingID any
	// A model's current provider can differ from the immutable execution. Its new
	// rate card must never price an old-provider response under that reused UUID.
	if basis != nil && basis.ProviderID == record.ProviderID && basis.SubscriptionService == record.SubscriptionService {
		estimate, err = domain.EstimateObservedResponse(basis.ID, basis.Basis, record.Usage)
		if err != nil {
			return err
		}
		pricingID = basis.ID
	}
	body, err := json.Marshal(estimate)
	if err != nil {
		return storageError(err)
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO response_estimates(usage_id,pricing_id,body) VALUES(?,?,?)", id, pricingID, body)
	if err != nil {
		return storageError(err)
	}
	return t.addSessionEstimate(record.SessionID, estimate)
}

func (t *Tx) ResponseEstimate(id domain.ID) (domain.ResponseEstimate, *PricingVersion, error) {
	var value domain.ResponseEstimate
	var body []byte
	var pricing sql.NullString
	if err := id.Validate(); err != nil {
		return value, nil, err
	}
	err := t.tx.QueryRowContext(t.ctx, "SELECT pricing_id,body FROM response_estimates WHERE usage_id=?", id).Scan(&pricing, &body)
	if err != nil {
		return value, nil, storageError(err)
	}
	if len(body) > 16<<10 || domain.Decode(body, &value) != nil {
		return value, nil, corrupt()
	}
	if !pricing.Valid {
		err := validateResponseEstimate(value, domain.ResponseUsageRecord{}, nil)
		return value, nil, err
	}
	basis, err := t.Pricing(domain.ID(pricing.String))
	if err != nil {
		return value, nil, err
	}
	var record domain.ResponseUsageRecord
	if err := t.tx.QueryRowContext(t.ctx, "SELECT body FROM response_usage WHERE id=?", id).Scan(&body); err != nil {
		return value, nil, storageError(err)
	}
	if domain.Decode(body, &record) != nil || record.Validate() != nil {
		return value, nil, corrupt()
	}
	if err := validateResponseEstimate(value, record, &basis); err != nil {
		return value, nil, err
	}
	return value, &basis, nil
}

func validateResponseEstimate(value domain.ResponseEstimate, record domain.ResponseUsageRecord, basis *PricingVersion) error {
	expected := missingPriceEstimate()
	if basis != nil {
		if basis.ModelID != record.ModelID || basis.ProviderID != record.ProviderID || basis.SubscriptionService != record.SubscriptionService {
			return corrupt()
		}
		var err error
		expected, err = domain.EstimateObservedResponse(basis.ID, basis.Basis, record.Usage)
		if err != nil {
			return corrupt()
		}
	}
	left, _ := json.Marshal(expected)
	right, _ := json.Marshal(value)
	if string(left) != string(right) {
		return corrupt()
	}
	return nil
}
