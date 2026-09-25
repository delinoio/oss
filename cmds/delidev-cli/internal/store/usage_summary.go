package store

import (
	"sort"

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
	where := "created_at>=? AND created_at<?"
	args := []any{f.From.UnixMilli(), f.Until.UnixMilli()}
	for _, part := range []struct {
		column string
		value  domain.ID
	}{{"session_id", f.SessionID}, {"project_id", f.ProjectID}, {"account_id", f.AccountID}, {"provider_id", f.ProviderID}, {"model_id", f.ModelID}} {
		if part.value != "" {
			where += " AND " + part.column + "=?"
			args = append(args, part.value)
		}
	}
	if f.GeneralChat {
		where += " AND project_id=''"
	}
	rows, err := t.tx.QueryContext(t.ctx, "SELECT body FROM response_usage WHERE "+where+" ORDER BY created_at,id LIMIT ?", append(args, maxUsageResponses+1)...)
	if err != nil {
		return result, storageError(err)
	}
	groups := map[string]*domain.UsageGroup{}
	observed := map[domain.ID]bool{}
	// Close before the second query so SQLite never needs a nested statement on
	// an active result cursor. Nothing is published until both reads succeed.
	err = func() error {
		defer rows.Close()
		for rows.Next() {
			if result.Totals.Responses >= maxUsageResponses {
				return usageReadLimit()
			}
			var body []byte
			if err := rows.Scan(&body); err != nil {
				return storageError(err)
			}
			var record domain.ResponseUsageRecord
			if len(body) > 16<<10 || domain.Decode(body, &record) != nil || record.Validate() != nil {
				return corrupt()
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
			result.Totals.Add(record.Usage.Counts)
		}
		return storageError(rows.Err())
	}()
	if err != nil {
		return domain.UsageSummary{}, err
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result.Groups = append(result.Groups, *groups[key])
	}
	result.AcceptedExecutionsWithoutResponse, err = t.usageMissingExecutions(f, observed)
	if err != nil {
		return domain.UsageSummary{}, err
	}
	return result, nil
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
