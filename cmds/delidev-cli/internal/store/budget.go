package store

import (
	"database/sql"
	"errors"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func (t *Tx) SessionEstimate(session domain.ID, currency domain.Currency) (domain.BudgetEvidence, error) {
	value := domain.BudgetEvidence{Currency: currency}
	if err := session.Validate(); err != nil {
		return value, err
	}
	err := t.tx.QueryRowContext(t.ctx, `SELECT known_amount,complete_responses,partial_responses,unavailable_responses FROM session_estimate_totals WHERE session_id=? AND currency=?`, session, currency).Scan(&value.KnownAmount, &value.CompleteResponses, &value.PartialResponses, &value.UnavailableResponses)
	if errors.Is(err, sql.ErrNoRows) {
		return value, value.Validate()
	}
	if err != nil {
		return value, storageError(err)
	}
	return value, value.Validate()
}
func (t *Tx) addSessionEstimate(session domain.ID, value domain.ResponseEstimate) error {
	if t.readOnly {
		return domain.Fail(domain.PermissionDenied, "Read transactions cannot mutate estimates.", "Use original response publication.")
	}
	total, err := t.SessionEstimate(session, value.Currency)
	if err != nil {
		return err
	}
	if err = total.Add(value); err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO session_estimate_totals(session_id,currency,known_amount,complete_responses,partial_responses,unavailable_responses) VALUES(?,?,?,?,?,?) ON CONFLICT(session_id,currency) DO UPDATE SET known_amount=excluded.known_amount,complete_responses=excluded.complete_responses,partial_responses=excluded.partial_responses,unavailable_responses=excluded.unavailable_responses`, session, total.Currency, total.KnownAmount, total.CompleteResponses, total.PartialResponses, total.UnavailableResponses)
	return storageError(err)
}

// Backfill only already retained immutable estimates, never current prices. Page
// the original ledger before writes to bound memory and close SQLite cursors.
func (t *Tx) backfillSessionEstimates() error {
	var after domain.ID
	for {
		rows, err := t.tx.QueryContext(t.ctx, "SELECT id,session_id,body FROM response_usage WHERE id>? ORDER BY id LIMIT 250", after)
		if err != nil {
			return storageError(err)
		}
		type original struct {
			id, session domain.ID
			body        []byte
		}
		var batch []original
		err = func() error {
			defer rows.Close()
			for rows.Next() {
				var value original
				if err := rows.Scan(&value.id, &value.session, &value.body); err != nil {
					return storageError(err)
				}
				batch = append(batch, value)
			}
			return storageError(rows.Err())
		}()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		for _, value := range batch {
			var record domain.ResponseUsageRecord
			if value.id.Validate() != nil || len(value.body) > 16<<10 || domain.Decode(value.body, &record) != nil || record.Validate() != nil || record.SessionID != value.session {
				return corrupt()
			}
			estimate, _, err := t.ResponseEstimate(value.id)
			if err != nil {
				return err
			}
			if err = t.addSessionEstimate(value.session, estimate); err != nil {
				return err
			}
			after = value.id
		}
	}
}
func (t *Tx) RequireSessionBudget(session domain.ID, budget *domain.EstimatedCostBudget) error {
	if budget == nil {
		return nil
	}
	if budget.Validate() != nil {
		return corrupt()
	}
	total, err := t.SessionEstimate(session, budget.Currency)
	if err != nil {
		return err
	}
	reached, err := budget.Reached(total)
	if err != nil {
		return err
	}
	if reached {
		return domain.BudgetReachedError()
	}
	return nil
}

func (t *Tx) OtherBudgetCurrencies(session domain.ID, selected domain.Currency) (uint64, error) {
	var count uint64
	err := t.tx.QueryRowContext(t.ctx, `SELECT COALESCE(SUM(complete_responses+partial_responses+unavailable_responses),0) FROM session_estimate_totals WHERE session_id=? AND currency<>'' AND currency<>?`, session, selected).Scan(&count)
	return count, storageError(err)
}
