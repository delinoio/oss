package store

import (
	"sort"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const maxUsageResponses = 25000
const maxUsageGroups = 500
const maxUsageExecutions = 10000

func usageReadLimit() error {
	return domain.Fail(domain.ResourceExhausted, "The usage selection exceeds its read bound.", "Choose a shorter time range or a specific session, project, model or account; no partial total was returned.")
}

// UsageSummary reads one coherent, cancellable snapshot. It aggregates exact
// responses only, with server retention times and immutable event attribution.
func (t *Tx) UsageSummary(f domain.UsageSelection) (domain.UsageSummary, error) {
	result := domain.UsageSummary{Groups: []domain.UsageGroup{}}
	if err := f.Validate(); err != nil {
		return result, err
	}
	var dayBuckets []domain.UsageAnalyticsDay
	var modelGroups map[usageModelKey]*domain.UsageAnalyticsModel
	if f.Granularity == domain.UsageTimeGranularityDay {
		var err error
		dayBuckets, err = f.UsageDayBuckets()
		if err != nil {
			return result, err
		}
		result.Analytics = &domain.UsageAnalytics{Granularity: f.Granularity, TimeZone: f.TimeZone, Days: dayBuckets, Models: []domain.UsageAnalyticsModel{}}
		modelGroups = make(map[usageModelKey]*domain.UsageAnalyticsModel)
	}
	where := "r.purpose='conversation' AND r.created_at>=? AND r.created_at<?"
	args := []any{f.From.UnixMilli(), f.Until.UnixMilli()}
	for _, part := range []struct {
		column string
		value  domain.ID
	}{{"session_id", f.SessionID}, {"project_id", f.ProjectID}, {"account_id", f.AccountID}, {"provider_id", f.ProviderID}, {"model_id", f.ModelID}} {
		if part.value != "" {
			where += " AND r." + part.column + "=?"
			args = append(args, part.value)
		}
	}
	if f.GeneralChat {
		where += " AND r.project_id=''"
	}
	rows, err := t.tx.QueryContext(t.ctx, `SELECT r.body,r.created_at,e.body,COALESCE(e.pricing_id,''),COALESCE(p.model_id,''),COALESCE(p.provider_id,''),COALESCE(p.revision,0),COALESCE(p.body,''),COALESCE(p.created_at,0) FROM response_usage r LEFT JOIN response_estimates e ON e.usage_id=r.id LEFT JOIN pricing_versions p ON p.id=e.pricing_id WHERE `+where+" ORDER BY r.created_at,r.id LIMIT ?", append(args, maxUsageResponses+1)...)
	if err != nil {
		return result, storageError(err)
	}
	groups := map[string]*domain.UsageGroup{}
	observed := map[domain.ID]bool{}
	prices := map[domain.ID]*domain.PricingUsage{}
	// Close before the second query so SQLite never needs a nested statement on
	// an active result cursor. Nothing is published until both reads succeed.
	err = func() error {
		defer rows.Close()
		for rows.Next() {
			if result.Totals.Responses >= maxUsageResponses {
				return usageReadLimit()
			}
			var body, estimateBody, priceBody []byte
			var price PricingVersion
			var responseCreated, pricingCreated int64
			if err := rows.Scan(&body, &responseCreated, &estimateBody, &price.ID, &price.ModelID, &price.ProviderID, &price.Revision, &priceBody, &pricingCreated); err != nil {
				return storageError(err)
			}
			var record domain.ResponseUsageRecord
			if len(body) > 16<<10 || domain.Decode(body, &record) != nil || record.Validate() != nil {
				return corrupt()
			}
			var estimate domain.ResponseEstimate
			if len(estimateBody) > 16<<10 || domain.Decode(estimateBody, &estimate) != nil {
				return corrupt()
			}
			var basis *PricingVersion
			if price.ID != "" {
				retained := prices[price.ID]
				if retained == nil {
					if len(prices) >= maxUsageGroups {
						return usageReadLimit()
					}
					if price.ID.Validate() != nil || price.ModelID.Validate() != nil || price.ProviderID.Validate() != nil || price.Revision == 0 || len(priceBody) > 16<<10 || domain.Decode(priceBody, &price.Basis) != nil || price.Basis.Validate() != nil {
						return corrupt()
					}
					price.CreatedAt = time.UnixMilli(pricingCreated).UTC()
					retained = &domain.PricingUsage{Pricing: price}
					prices[price.ID] = retained
				}
				basis = &retained.Pricing
			}
			if err := validateResponseEstimate(estimate, record, basis); err != nil {
				return err
			}
			result.Estimates.Add(estimate)
			if basis != nil {
				prices[basis.ID].Add(estimate)
			}
			observed[record.ExecutionID] = true
			key := string(record.SessionID) + ":" + string(record.AccountID) + ":" + string(record.ProviderID) + ":" + string(record.ModelID)
			group := groups[key]
			if group == nil {
				if len(groups) >= maxUsageGroups {
					return usageReadLimit()
				}
				group = &domain.UsageGroup{SessionID: record.SessionID, ProjectID: record.ProjectID, AccountID: record.AccountID, ProviderID: record.ProviderID, ModelID: record.ModelID}
				groups[key] = group
			}
			if group.ProjectID != record.ProjectID {
				return corrupt()
			}
			group.Totals.Add(record.Usage.Counts)
			group.Estimates.Add(estimate)
			result.Totals.Add(record.Usage.Counts)
			if result.Analytics != nil {
				created := time.UnixMilli(responseCreated).UTC()
				day := sort.Search(len(dayBuckets), func(index int) bool { return created.Before(dayBuckets[index].Until) })
				if day >= len(dayBuckets) || created.Before(dayBuckets[day].From) {
					return corrupt()
				}
				dayBuckets[day].Totals.Add(record.Usage.Counts)
				key := usageModelKey{Provider: record.ProviderID, Model: record.ModelID}
				model := modelGroups[key]
				if model == nil {
					if len(modelGroups) >= maxUsageGroups || len(modelGroups) >= domain.UsageModelGroupLimit {
						return usageReadLimit()
					}
					model = &domain.UsageAnalyticsModel{ProviderID: record.ProviderID, ModelID: record.ModelID}
					modelGroups[key] = model
				}
				model.Totals.Add(record.Usage.Counts)
			}
		}
		return storageError(rows.Err())
	}()
	if err != nil {
		return domain.UsageSummary{}, err
	}
	if result.Analytics != nil {
		for _, model := range modelGroups {
			result.Analytics.Models = append(result.Analytics.Models, *model)
		}
		domain.SortUsageAnalyticsModels(result.Analytics.Models)
		if len(result.Analytics.Models) > domain.UsageRankedModelLimit {
			other := &domain.UsageOtherModels{}
			for _, model := range result.Analytics.Models[domain.UsageRankedModelLimit:] {
				if model.Totals.Total.MeasuredResponses == 0 || model.Totals.Total.KnownTotal == "" {
					continue
				}
				other.ModelCount++
				other.Totals.Merge(model.Totals)
			}
			if other.ModelCount > 0 {
				result.Analytics.OtherModels = other
			}
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result.Groups = append(result.Groups, *groups[key])
	}
	priceIDs := make([]domain.ID, 0, len(prices))
	for id := range prices {
		priceIDs = append(priceIDs, id)
	}
	sort.Slice(priceIDs, func(i, j int) bool { return priceIDs[i] < priceIDs[j] })
	for _, id := range priceIDs {
		result.Pricing = append(result.Pricing, *prices[id])
	}
	result.AcceptedExecutionsWithoutResponse, err = t.usageMissingExecutions(f, observed)
	if err != nil {
		return domain.UsageSummary{}, err
	}
	if f.AccountingProfile == domain.NativeInputAccountingV1 {
		native, err := t.nativeAccountingSummary(f)
		if err != nil {
			return domain.UsageSummary{}, err
		}
		result.NativeAccounting = &native
	}
	return result, nil
}

type usageModelKey struct {
	Provider domain.ID
	Model    domain.ID
}

// This is an explicit coverage indicator for executions accepted in the same
// server-time window, not proof of zero calls or complete child telemetry.
func (t *Tx) usageMissingExecutions(f domain.UsageSelection, observed map[domain.ID]bool) (uint32, error) {
	query := `SELECT json_extract(e.body,'$.input.execution_id'),json_extract(e.body,'$.input.session_id'),json_extract(e.body,'$.input.account_id') FROM entities e WHERE e.kind='job' AND json_extract(e.body,'$.type')='execute-session' AND e.created_at>=? AND e.created_at<? AND EXISTS(SELECT 1 FROM entities s WHERE s.kind='session' AND s.id=e.session_id)`
	args := []any{f.From.UnixMilli(), f.Until.UnixMilli()}
	for _, part := range []struct {
		column string
		value  domain.ID
	}{{"e.session_id", f.SessionID}, {"e.project_id", f.ProjectID}, {"json_extract(e.body,'$.input.account_id')", f.AccountID}, {"json_extract(e.body,'$.input.configuration.provider_id')", f.ProviderID}, {"json_extract(e.body,'$.input.configuration.model_id')", f.ModelID}} {
		if part.value != "" {
			query += " AND " + part.column + "=?"
			args = append(args, part.value)
		}
	}
	if f.GeneralChat {
		query += " AND e.project_id=''"
	}
	query += " LIMIT ?"
	args = append(args, maxUsageExecutions+1)
	rows, err := t.tx.QueryContext(t.ctx, query, args...)
	if err != nil {
		return 0, storageError(err)
	}
	defer rows.Close()
	var count, total uint32
	for rows.Next() {
		total++
		if total > maxUsageExecutions {
			return 0, usageReadLimit()
		}
		var execution, session, account domain.ID
		if err := rows.Scan(&execution, &session, &account); err != nil {
			return 0, storageError(err)
		}
		if execution.Validate() != nil || session.Validate() != nil || account.Validate() != nil {
			return 0, corrupt()
		}
		if !observed[execution] {
			count++
		}
	}
	return count, storageError(rows.Err())
}
