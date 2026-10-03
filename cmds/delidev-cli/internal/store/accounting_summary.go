// SPDX-License-Identifier: Apache-2.0
package store

import (
	"sort"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func (t *Tx) grokAccountingSummary(f domain.UsageSelection, result *domain.UsageSummary, groups map[string]*domain.UsageGroup, models map[usageModelKey]*domain.UsageAnalyticsModel, days []domain.UsageAnalyticsDay) error {
	where := "r.kind=2 AND r.created_at>=? AND r.created_at<?"
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
	if f.SubscriptionService != "" {
		where += " AND 0"
	}
	if f.GeneralChat {
		where += " AND r.project_id=''"
	}
	rows, err := t.tx.QueryContext(t.ctx, "SELECT r.id,r.kind,r.session_id,r.project_id,r.execution_id,r.input_id,r.account_id,r.provider_id,r.model_id,r.body,r.created_at FROM native_accounting r WHERE "+where+" ORDER BY r.created_at,r.id LIMIT ?", append(args, maxUsageResponses+1)...)
	if err != nil {
		return storageError(err)
	}
	defer rows.Close()
	count := result.Totals.Responses
	for rows.Next() {
		count++
		if count > maxUsageResponses {
			return usageReadLimit()
		}
		var body []byte
		var created int64
		var index domain.GrokAccountingRecord
		if err := rows.Scan(&index.SourceReceipt, &index.Kind, &index.SessionID, &index.ProjectID, &index.ExecutionID, &index.InputID, &index.AccountID, &index.ProviderID, &index.ModelID, &body, &created); err != nil {
			return storageError(err)
		}
		var record domain.GrokAccountingRecord
		if len(body) > 16<<10 || domain.Decode(body, &record) != nil || record.Validate() != nil || index.SourceReceipt != record.SourceReceipt || index.Kind != record.Kind || index.SessionID != record.SessionID || index.ProjectID != record.ProjectID || index.ExecutionID != record.ExecutionID || index.InputID != record.InputID || index.AccountID != record.AccountID || index.ProviderID != record.ProviderID || index.ModelID != record.ModelID {
			return corrupt()
		}
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
		total := record.Terminal.TotalTokens
		result.Totals.AddAccounting(record.Kind, &total)
		group.Totals.AddAccounting(record.Kind, &total)
		if result.Analytics != nil {
			at := time.UnixMilli(created).UTC()
			day := sort.Search(len(days), func(i int) bool { return at.Before(days[i].Until) })
			if day >= len(days) || at.Before(days[day].From) {
				return corrupt()
			}
			days[day].Totals.AddAccounting(record.Kind, &total)
			modelKey := usageModelKey{Provider: record.ProviderID, Model: record.ModelID}
			model := models[modelKey]
			if model == nil {
				if len(models) >= domain.UsageModelGroupLimit {
					return usageReadLimit()
				}
				model = &domain.UsageAnalyticsModel{ProviderID: record.ProviderID, ModelID: record.ModelID}
				models[modelKey] = model
			}
			model.Totals.AddAccounting(record.Kind, &total)
		}
	}
	return storageError(rows.Err())
}
