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

func (t *Tx) SessionBudgetEstimate(session domain.ID, currency domain.Currency) (domain.BudgetEvidence, error) {
	total, err := t.SessionEstimate(session, currency)
	if err != nil {
		return total, err
	}
	review, err := t.NativeCodeReviewEstimate(session, currency)
	if err != nil {
		return total, err
	}
	if err = total.MergeResponses(review); err != nil {
		return total, err
	}
	native, err := t.NativeSessionEstimate(session, currency)
	if err != nil {
		return total, err
	}
	err = total.MergeNative(native)
	return total, err
}

func (t *Tx) RequireSessionBudget(session domain.ID, budget *domain.EstimatedCostBudget) error {
	if budget == nil {
		return nil
	}
	if budget.Validate() != nil {
		return corrupt()
	}
	total, err := t.SessionBudgetEstimate(session, budget.Currency)
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
	if err != nil {
		return count, storageError(err)
	}
	var auxiliary uint64
	err = t.tx.QueryRowContext(t.ctx, "SELECT COALESCE(SUM(json_extract(value,'$.complete_responses')+json_extract(value,'$.partial_responses')+json_extract(value,'$.unavailable_responses')),0) FROM metadata WHERE key LIKE ? AND json_extract(value,'$.currency')<>'' AND json_extract(value,'$.currency')<>?", nativeReviewEstimatePrefix+string(session)+":%", selected).Scan(&auxiliary)
	if auxiliary > 1<<63-1-count {
		return 0, domain.NativeCodeReviewUnavailable()
	}
	return count + auxiliary, storageError(err)
}
