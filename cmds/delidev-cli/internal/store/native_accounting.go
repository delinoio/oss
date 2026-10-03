// SPDX-License-Identifier: Apache-2.0
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// This is called only in the original source/event/receipt acceptance transaction.
// Source deduplication runs first; receipt replay never reaches this insert.
func (t *Tx) PutClaudeAccounting(source, input, session, project domain.ID, o domain.ClaudeUsageRecord) error {
	if t.readOnly {
		return domain.Fail(domain.PermissionDenied, "Read transactions cannot publish accounting.", "Use original correlated source publication.")
	}
	if o.Usage.Source != domain.ClaudeInputResultUsage {
		return nil
	}
	r, err := t.Get(domain.UsageKind, source)
	if err != nil {
		return err
	}
	retained, err := Decode[domain.ClaudeUsageRecord](r)
	if err != nil {
		return err
	}
	left, _ := json.Marshal(retained)
	right, _ := json.Marshal(o)
	if !t.touched[source] || r.Revision != 1 || !r.CreatedAt.Equal(t.now) || r.SessionID != session || r.ProjectID != project || string(left) != string(right) {
		return domain.Fail(domain.Conflict, "The native accounting source changed.", "Preserve the exact original source acceptance and assignment.")
	}
	u := domain.NativeAccountingUnit{Kind: domain.ClaudeMainLoopInput, SourceID: source, RequestID: t.requestID, InputID: input, SessionID: session, ProjectID: project, Observation: o}
	if u.Validate() != nil {
		return domain.Fail(domain.InvalidArgument, "Invalid native accounting ownership.", "Retain only the original correlated main-loop input result.")
	}
	// Preserve only main-loop primitives. Cumulative snapshots/native monetary
	// estimates remain in their original observation and never become accounting.
	var main *domain.ClaudeProviderUsage
	if m := o.Usage.Result.MainLoop; m != nil {
		main = &domain.ClaudeProviderUsage{Input: m.Input, CacheRead: m.CacheRead, CacheWrite: m.CacheWrite, Output: m.Output, OutputDetail: m.OutputDetail}
	}
	u.Observation.Usage.Result = &domain.ClaudeResultUsage{MainLoop: main}
	p, err := t.ActivePricing(o.ModelID)
	if err != nil {
		return err
	}
	e := domain.UnpricedNativeEstimate()
	var priceID any
	if p != nil && p.ProviderID == o.ProviderID {
		e, err = domain.EstimateNativeInput(u, *p)
		if err != nil {
			return err
		}
		priceID = p.ID
	}
	body, err := json.Marshal(u)
	if err != nil {
		return storageError(err)
	}
	estimate, err := json.Marshal(e)
	if err != nil {
		return storageError(err)
	}
	if len(body) > 16<<10 {
		return usageReadLimit()
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO native_accounting(id,source_id,session_id,project_id,execution_id,input_id,kind,account_id,provider_id,model_id,body,pricing_id,estimate,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, source, source, session, project, o.ExecutionID, input, u.Kind, o.AccountID, o.ProviderID, o.ModelID, body, priceID, estimate, t.now.UnixMilli())
	if err != nil {
		return storageError(err)
	}
	return t.addNativeSessionEstimate(session, e)
}

func (t *Tx) NativeSessionEstimate(session domain.ID, currency domain.Currency) (domain.BudgetEvidence, error) {
	v := domain.BudgetEvidence{Currency: currency}
	if err := session.Validate(); err != nil {
		return v, err
	}
	err := t.tx.QueryRowContext(t.ctx, `SELECT known_amount,complete_units,partial_units,unavailable_units FROM session_native_estimate_totals WHERE session_id=? AND currency=?`, session, currency).Scan(&v.KnownAmount, &v.CompleteResponses, &v.PartialResponses, &v.UnavailableResponses)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return v, storageError(err)
	}
	return v, v.Validate()
}
func (t *Tx) addNativeSessionEstimate(session domain.ID, e domain.NativeEstimate) error {
	v, err := t.NativeSessionEstimate(session, e.Currency)
	if err != nil {
		return err
	}
	if err = v.Add(domain.ResponseEstimate{Currency: e.Currency, KnownAmount: e.KnownAmount, Coverage: e.Coverage}); err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO session_native_estimate_totals(session_id,currency,known_amount,complete_units,partial_units,unavailable_units) VALUES(?,?,?,?,?,?) ON CONFLICT(session_id,currency) DO UPDATE SET known_amount=excluded.known_amount,complete_units=excluded.complete_units,partial_units=excluded.partial_units,unavailable_units=excluded.unavailable_units`, session, e.Currency, v.KnownAmount, v.CompleteResponses, v.PartialResponses, v.UnavailableResponses)
	return storageError(err)
}
func (t *Tx) OtherNativeBudgetCurrencies(session domain.ID, selected domain.Currency) (uint64, error) {
	var n uint64
	err := t.tx.QueryRowContext(t.ctx, `SELECT COALESCE(SUM(complete_units+partial_units+unavailable_units),0) FROM session_native_estimate_totals WHERE session_id=? AND currency<>'' AND currency<>?`, session, selected).Scan(&n)
	return n, storageError(err)
}

func (t *Tx) nativeAccountingSummary(f domain.UsageSelection, kind domain.AccountingUnitKind) (domain.NativeAccountingSummary, error) {
	v := domain.NativeAccountingSummary{Totals: domain.NativeAccountingTotals{Kind: kind}}
	buckets, err := f.UsageDayBuckets()
	if err != nil {
		return v, err
	}
	for _, b := range buckets {
		v.Days = append(v.Days, domain.NativeAccountingDay{FromUnixMS: b.From.UnixMilli(), UntilUnixMS: b.Until.UnixMilli(), Totals: domain.NativeAccountingTotals{Kind: kind}})
	}
	where := "created_at>=? AND created_at<?"
	args := []any{kind, f.From.UnixMilli(), f.Until.UnixMilli()}
	for _, p := range []struct {
		column string
		id     domain.ID
	}{{"session_id", f.SessionID}, {"project_id", f.ProjectID}, {"account_id", f.AccountID}, {"provider_id", f.ProviderID}, {"model_id", f.ModelID}} {
		if p.id != "" {
			where += " AND " + p.column + "=?"
			args = append(args, p.id)
		}
	}
	if f.SubscriptionService != "" {
		where += " AND 0"
	}
	if f.GeneralChat {
		where += " AND project_id=''"
	}
	rows, err := t.tx.QueryContext(t.ctx, `SELECT id,source_id,session_id,project_id,execution_id,input_id,account_id,provider_id,model_id,body,estimate,COALESCE(pricing_id,''),created_at FROM native_accounting WHERE kind=? AND `+where+` ORDER BY created_at,id LIMIT ?`, append(args, maxUsageResponses+1)...)
	if err != nil {
		return v, storageError(err)
	}
	// Close the bounded ledger cursor before reading prices on the same SQLite
	// connection. No partial aggregate can escape failed validation.
	type original struct {
		id, source, session, project, execution, input, account, provider, model domain.ID
		body, estimate                                                           []byte
		price                                                                    domain.ID
		created                                                                  int64
	}
	var originals []original
	err = func() error {
		defer rows.Close()
		for rows.Next() {
			if len(originals) >= maxUsageResponses {
				return usageReadLimit()
			}
			var o original
			if err := rows.Scan(&o.id, &o.source, &o.session, &o.project, &o.execution, &o.input, &o.account, &o.provider, &o.model, &o.body, &o.estimate, &o.price, &o.created); err != nil {
				return storageError(err)
			}
			originals = append(originals, o)
		}
		return storageError(rows.Err())
	}()
	if err != nil {
		return domain.NativeAccountingSummary{}, err
	}
	groups := map[string]*domain.NativeAccountingGroup{}
	models := map[string]*domain.NativeAccountingGroup{}
	prices := map[domain.ID]*domain.NativeAccountingPricing{}
	for _, o := range originals {
		if err := t.ctx.Err(); err != nil {
			return domain.NativeAccountingSummary{}, storageError(err)
		}
		var u domain.NativeAccountingUnit
		var e domain.NativeEstimate
		if len(o.body) > 16<<10 || len(o.estimate) > 16<<10 || domain.Decode(o.body, &u) != nil || u.Validate() != nil || domain.Decode(o.estimate, &e) != nil {
			return domain.NativeAccountingSummary{}, corrupt()
		}
		a := u.Attribution()
		if u.Kind != kind || u.SourceID != o.id || u.SourceID != o.source || u.SessionID != o.session || u.ProjectID != o.project || u.InputID != o.input || a.ExecutionID != o.execution || a.AccountID != o.account || a.ProviderID != o.provider || a.ModelID != o.model {
			return domain.NativeAccountingSummary{}, corrupt()
		}
		expected := domain.UnpricedNativeEstimate()
		var price *domain.NativeAccountingPricing
		if o.price != "" {
			price = prices[o.price]
			if price == nil {
				if len(prices) >= maxUsageGroups {
					return domain.NativeAccountingSummary{}, usageReadLimit()
				}
				p, err := t.Pricing(o.price)
				if err != nil {
					return domain.NativeAccountingSummary{}, err
				}
				price = &domain.NativeAccountingPricing{Pricing: p}
				prices[o.price] = price
			}
			expected, err = domain.EstimateNativeInput(u, price.Pricing)
			if err != nil {
				return domain.NativeAccountingSummary{}, corrupt()
			}
		}
		left, _ := json.Marshal(expected)
		right, _ := json.Marshal(e)
		if string(left) != string(right) {
			return domain.NativeAccountingSummary{}, corrupt()
		}
		v.Totals.Add(u, e)
		key := string(u.SessionID) + ":" + string(u.Attribution().AccountID) + ":" + string(u.Attribution().ProviderID) + ":" + string(u.Attribution().ModelID)
		g := groups[key]
		if g == nil {
			if len(groups) >= maxUsageGroups {
				return domain.NativeAccountingSummary{}, usageReadLimit()
			}
			g = &domain.NativeAccountingGroup{SessionID: u.SessionID, ProjectID: u.ProjectID, AccountID: u.Attribution().AccountID, ProviderID: u.Attribution().ProviderID, ModelID: u.Attribution().ModelID}
			groups[key] = g
		}
		if g.ProjectID != u.ProjectID {
			return domain.NativeAccountingSummary{}, corrupt()
		}
		g.Totals.Add(u, e)
		if price != nil {
			price.Totals.Add(u, e)
			price.Input.Add(e.Input)
			price.CacheRead.Add(e.CacheRead)
			price.CacheWrite.Add(e.CacheWrite)
			price.Output.Add(e.Output)
			price.Reasoning.Add(e.Reasoning)
		}
		if f.Granularity == domain.UsageTimeGranularityDay {
			i := sort.Search(len(buckets), func(i int) bool { return o.created < buckets[i].Until.UnixMilli() })
			if i >= len(buckets) || o.created < buckets[i].From.UnixMilli() {
				return domain.NativeAccountingSummary{}, corrupt()
			}
			v.Days[i].Totals.Add(u, e)
			key := string(u.Attribution().ProviderID) + ":" + string(u.Attribution().ModelID)
			m := models[key]
			if m == nil {
				if len(models) >= maxUsageGroups {
					return domain.NativeAccountingSummary{}, usageReadLimit()
				}
				m = &domain.NativeAccountingGroup{ProviderID: u.Attribution().ProviderID, ModelID: u.Attribution().ModelID}
				models[key] = m
			}
			m.Totals.Add(u, e)
		}
	}
	for _, g := range groups {
		v.Groups = append(v.Groups, *g)
	}
	sort.Slice(v.Groups, func(i, j int) bool {
		a, b := v.Groups[i], v.Groups[j]
		return string(a.SessionID)+string(a.AccountID)+string(a.ProviderID)+string(a.ModelID) < string(b.SessionID)+string(b.AccountID)+string(b.ProviderID)+string(b.ModelID)
	})
	for _, m := range models {
		v.Models = append(v.Models, *m)
	}
	sort.Slice(v.Models, func(i, j int) bool {
		a, b := v.Models[i], v.Models[j]
		return string(a.ProviderID)+string(a.ModelID) < string(b.ProviderID)+string(b.ModelID)
	})
	for _, p := range prices {
		v.Pricing = append(v.Pricing, *p)
	}
	sort.Slice(v.Pricing, func(i, j int) bool { return v.Pricing[i].Pricing.ID < v.Pricing[j].Pricing.ID })
	return v, nil
}

// Step publication has already proved original root message/part ownership.
// Assistant summaries and inherited history never reach this accounting insert.
func (t *Tx) PutOpenCodeAccounting(source, input, session, project domain.ID, o domain.OpenCodeUsageRecord) error {
	if o.Usage.Source != domain.OpenCodeStepUsage {
		return nil
	}
	r, err := t.Get(domain.UsageKind, source)
	if err != nil {
		return err
	}
	retained, err := Decode[domain.OpenCodeUsageRecord](r)
	if err != nil {
		return err
	}
	left, _ := json.Marshal(retained)
	right, _ := json.Marshal(o)
	if t.readOnly || !t.touched[source] || r.Revision != 1 || !r.CreatedAt.Equal(t.now) || r.SessionID != session || r.ProjectID != project || string(left) != string(right) {
		return corrupt()
	}
	u := domain.NativeAccountingUnit{Kind: domain.OpenCodeStep, SourceID: source, RequestID: t.requestID, InputID: input, SessionID: session, ProjectID: project, OpenCode: &o}
	if u.Validate() != nil {
		return corrupt()
	}
	p, err := t.ActivePricing(o.ModelID)
	if err != nil {
		return err
	}
	e := domain.UnpricedNativeEstimate()
	var priceID any
	if p != nil && p.ProviderID == o.ProviderID {
		e, err = domain.EstimateNativeInput(u, *p)
		if err != nil {
			return err
		}
		priceID = p.ID
	}
	body, err := json.Marshal(u)
	if err != nil {
		return storageError(err)
	}
	estimate, err := json.Marshal(e)
	if err != nil {
		return storageError(err)
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO native_accounting(id,source_id,session_id,project_id,execution_id,input_id,kind,account_id,provider_id,model_id,body,pricing_id,estimate,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, source, source, session, project, o.ExecutionID, input, u.Kind, o.AccountID, o.ProviderID, o.ModelID, body, priceID, estimate, t.now.UnixMilli())
	if err != nil {
		return storageError(err)
	}
	return t.addNativeSessionEstimate(session, e)
}
