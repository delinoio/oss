// SPDX-License-Identifier: Apache-2.0
package main

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/pricing"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func usageOutcome(code string) v1.RequestOutcome {
	switch code {
	case "":
		return v1.RequestOutcome_REQUEST_OUTCOME_SUCCEEDED
	case "canceled", "cancelled":
		return v1.RequestOutcome_REQUEST_OUTCOME_CANCELED
	case "rate_limited":
		return v1.RequestOutcome_REQUEST_OUTCOME_RATE_LIMITED
	case "upstream_error", "upstream_status":
		return v1.RequestOutcome_REQUEST_OUTCOME_UPSTREAM_ERROR
	default:
		return v1.RequestOutcome_REQUEST_OUTCOME_GATEWAY_ERROR
	}
}
func appliedPrice(u store.Usage) *v1.AppliedPrice {
	var e pricing.Estimate
	if json.Unmarshal(u.Price, &e) != nil || e.Price.Model == "" {
		return nil
	}
	decimal := func(v float64) *string { s := strconv.FormatFloat(v, 'f', -1, 64); return &s }
	source := v1.PriceSource_PRICE_SOURCE_UNSPECIFIED
	switch e.Price.Source {
	case pricing.Catalog, pricing.Expected:
		source = v1.PriceSource_PRICE_SOURCE_BUNDLED
	case pricing.Operator:
		source = v1.PriceSource_PRICE_SOURCE_OPERATOR_OVERRIDE
	}
	tier := ""
	if e.Tier.Response != nil {
		tier = *e.Tier.Response
	} else if e.Tier.Requested != nil {
		tier = *e.Tier.Requested
	} else if e.Tier.Configured != nil {
		tier = *e.Tier.Configured
	}
	rates := &v1.PriceRates{Currency: "USD", Input: decimal(e.EffectiveRates.Input), Output: decimal(e.EffectiveRates.Output), CacheRead: decimal(e.EffectiveRates.CacheRead), CacheWrite: decimal(e.EffectiveRates.CacheWrite)}
	if r := e.EffectiveOptionalRates; r != nil {
		optional := func(v *float64) *string {
			if v == nil {
				return nil
			}
			return decimal(*v)
		}
		rates = &v1.PriceRates{Currency: r.Currency, Input: optional(r.Input), Output: optional(r.Output), CacheRead: optional(r.CacheRead), CacheWrite: optional(r.CacheWrite), CacheWriteShort: optional(r.CacheWriteShort), CacheWriteLong: optional(r.CacheWriteLong)}
	}
	if e.Price.ServiceTier != "" {
		tier = e.Price.ServiceTier
	} else if o := e.Tier.Outcome; o != nil {
		authoritative := o.ResponseTierAuthoritative == nil || *o.ResponseTierAuthoritative
		switch {
		case authoritative && o.Canonical == "priority" && o.Confirmation == "confirmed":
			tier = "priority"
		case authoritative && o.ResponseServiceTier != nil:
			tier = *o.ResponseServiceTier
		case o.Canonical == "priority" && (o.Confirmation == "assumed" || !authoritative && o.Confirmation == "confirmed"):
			tier = "priority"
		case o.FastOutcome == "unknown" && o.WireKind == "service-tier":
			tier = o.WireValue
		default:
			tier = ""
		}
	}
	return &v1.AppliedPrice{Rates: rates, Source: source, SourceRevision: e.SnapshotID, ProviderId: e.Price.Provider, ModelId: e.Price.Model, ServiceTier: tier, AppliedAt: timestamp(e.RecordedAt)}
}

var priceDecimal = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?$`)

func validatePriceOverride(p *v1.PriceOverride) error {
	if p == nil || p.ProviderId == "" || p.ModelId == "" || p.Rates == nil || p.Rates.Currency != "USD" {
		return invalid("provider, model and USD rates required")
	}
	for _, s := range []string{p.ProviderId, p.ModelId, p.ServiceTier} {
		if len(s) > 512 || strings.TrimSpace(s) != s || strings.ContainsAny(s, "\x00\r\n") {
			return invalid("invalid price selector")
		}
	}
	for _, raw := range []*string{p.Rates.Input, p.Rates.Output, p.Rates.CacheRead, p.Rates.CacheWrite, p.Rates.CacheWriteShort, p.Rates.CacheWriteLong} {
		if raw == nil {
			continue
		}
		if len(*raw) > 128 || !priceDecimal.MatchString(*raw) {
			return invalid("rates must be nonnegative decimal amounts")
		}
		value, ok := new(big.Rat).SetString(*raw)
		if !ok || value.Cmp(big.NewRat(1000000, 1)) > 0 {
			return invalid("rate exceeds supported maximum")
		}
	}
	return nil
}

// Read only immutable price metadata; these settings never contain provider secrets.
func storedPriceOverrides(raw json.RawMessage) (map[string]*v1.PriceOverride, error) {
	out := map[string]*v1.PriceOverride{}
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errors.New("invalid price configuration")
	}
	for id, p := range out {
		if validatePriceOverride(p) != nil || id == "" || id != p.Id {
			return nil, errors.New("invalid price configuration")
		}
	}
	return out, nil
}

func extendUsageFilter(f *store.UsageFilter, provider *string, outcome *v1.RequestOutcome) error {
	if provider != nil {
		if *provider == "" {
			return invalid("provider filter cannot be empty")
		}
		f.Provider = *provider
	}
	if outcome == nil {
		return nil
	}
	switch *outcome {
	case v1.RequestOutcome_REQUEST_OUTCOME_UNSPECIFIED:
		f.Outcome = store.OutcomeUnknown
	case v1.RequestOutcome_REQUEST_OUTCOME_SUCCEEDED:
		f.Outcome = store.OutcomeSucceeded
	case v1.RequestOutcome_REQUEST_OUTCOME_CANCELED:
		f.Outcome = store.OutcomeCanceled
	case v1.RequestOutcome_REQUEST_OUTCOME_UPSTREAM_ERROR:
		f.Outcome = store.OutcomeUpstreamError
	case v1.RequestOutcome_REQUEST_OUTCOME_GATEWAY_ERROR:
		f.Outcome = store.OutcomeGatewayError
	case v1.RequestOutcome_REQUEST_OUTCOME_RATE_LIMITED:
		f.Outcome = store.OutcomeRateLimited
	default:
		return invalid("invalid outcome filter")
	}
	return nil
}
func storedUsageOutcome(u store.Usage) v1.RequestOutcome {
	switch u.Outcome {
	case store.OutcomeUnknown, "":
		return v1.RequestOutcome_REQUEST_OUTCOME_UNSPECIFIED
	case store.OutcomeSucceeded:
		return v1.RequestOutcome_REQUEST_OUTCOME_SUCCEEDED
	case store.OutcomeCanceled:
		return v1.RequestOutcome_REQUEST_OUTCOME_CANCELED
	case store.OutcomeUpstreamError:
		return v1.RequestOutcome_REQUEST_OUTCOME_UPSTREAM_ERROR
	case store.OutcomeGatewayError:
		return v1.RequestOutcome_REQUEST_OUTCOME_GATEWAY_ERROR
	case store.OutcomeRateLimited:
		return v1.RequestOutcome_REQUEST_OUTCOME_RATE_LIMITED
	default:
		if u.ErrorCode == "" {
			return v1.RequestOutcome_REQUEST_OUTCOME_UNSPECIFIED
		}
		return usageOutcome(u.ErrorCode)
	}
}
func usageError(err error) error {
	if errors.Is(err, store.ErrRangeUnavailable) {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("exact partial-day usage is outside request retention; select complete UTC days"))
	}
	return rpcError(err)
}

func auditOutcome(outcome store.AuditOutcome) v1.AuditOutcome {
	switch outcome {
	case store.AuditSucceeded:
		return v1.AuditOutcome_AUDIT_OUTCOME_SUCCEEDED
	case store.AuditFailed:
		return v1.AuditOutcome_AUDIT_OUTCOME_FAILED
	case store.AuditDenied:
		return v1.AuditOutcome_AUDIT_OUTCOME_DENIED
	default:
		return v1.AuditOutcome_AUDIT_OUTCOME_UNSPECIFIED
	}
}

func managedPriceOverrides(raw json.RawMessage) ([]pricing.Override, error) {
	entries, err := storedPriceOverrides(raw)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for id := range entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]pricing.Override, 0, len(ids))
	for _, id := range ids {
		p := entries[id]
		rates := &pricing.OptionalRates{Currency: p.Rates.Currency}
		inputs := []*string{p.Rates.Input, p.Rates.Output, p.Rates.CacheRead, p.Rates.CacheWrite, p.Rates.CacheWriteShort, p.Rates.CacheWriteLong}
		outputs := []**float64{&rates.Input, &rates.Output, &rates.CacheRead, &rates.CacheWrite, &rates.CacheWriteShort, &rates.CacheWriteLong}
		for i, value := range inputs {
			if value != nil {
				n, err := strconv.ParseFloat(*value, 64)
				if err != nil {
					return nil, errors.New("invalid price configuration")
				}
				*outputs[i] = &n
			}
		}
		out = append(out, pricing.Override{Provider: p.ProviderId, Model: p.ModelId, ServiceTier: p.ServiceTier, OptionalRates: rates, SourceRef: p.Id})
	}
	return out, nil
}

func currentPricingSnapshot(ctx context.Context, s *store.Store, base *pricing.Snapshot) (*pricing.Snapshot, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	overrides, err := managedPriceOverrides(settings.Prices)
	if err != nil {
		return nil, err
	}
	// Rebuild even an empty collection: the caller's old snapshot may contain deleted overrides.
	return pricing.NewSnapshot(overrides)
}
