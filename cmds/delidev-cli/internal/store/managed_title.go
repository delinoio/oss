// SPDX-License-Identifier: Apache-2.0
package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// ManagedTitleFinish reads only the server-written protected Finish receipt of
// a managed automatic title. The Worker's public title report carries its
// reference, never credential, lease or cleanup authority.
func (t *Tx) ManagedTitleFinish(id domain.ID) (domain.SubscriptionTitleFinish, error) {
	var receipt struct {
		ID           domain.ID                       `json:"id"`
		ManagedTitle *domain.SubscriptionTitleFinish `json:"managed_title,omitempty"`
	}
	denied := func() (domain.SubscriptionTitleFinish, error) {
		return domain.SubscriptionTitleFinish{}, domain.Fail(domain.RecoveryRequired, "The protected title authentication finish is unconfirmed.", "Retain the original title lease and cleanup evidence; do not repeat title inference.")
	}
	if err := t.Authorize(); err != nil {
		return domain.SubscriptionTitleFinish{}, err
	}
	if id.Validate() != nil {
		return denied()
	}
	var raw []byte
	if err := t.tx.QueryRowContext(t.ctx, "SELECT result FROM receipts WHERE id=?", id).Scan(&raw); err != nil {
		return denied()
	}
	if domain.Decode(raw, &receipt) != nil || receipt.ManagedTitle == nil || receipt.ManagedTitle.Finish != id || receipt.ManagedTitle.Account != receipt.ID {
		return denied()
	}
	return *receipt.ManagedTitle, nil
}

// ClaimedManagedTitleJobs pages the claimed protected (version-3) title jobs
// frozen to one subscription account, in job ID order. Server admission uses it
// to find titles that hold an account's custody window without a lease yet.
func (t *Tx) ClaimedManagedTitleJobs(account, after domain.ID, limit int) ([]Record, error) {
	if err := t.Authorize(); err != nil {
		return nil, err
	}
	if err := account.Validate(); err != nil {
		return nil, err
	}
	if after != "" {
		if err := after.Validate(); err != nil {
			return nil, err
		}
	}
	if limit < 1 || limit > MaxPage {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid managed title page bound.", "Use a bounded account-owned page.")
	}
	rows, err := t.tx.QueryContext(t.ctx, `SELECT e.id,e.kind,e.revision,e.session_id,e.project_id,e.body,e.created_at,e.updated_at
FROM jobs j JOIN entities e ON e.id=j.id
WHERE j.id>? AND j.state='claimed'
AND json_extract(e.body,'$.type')='generate-session-title'
AND json_extract(e.body,'$.input.version')=3
AND json_extract(e.body,'$.input.account_id')=?
ORDER BY j.id LIMIT ?`, after, account, limit)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	records := []Record{}
	for rows.Next() {
		record, err := scan(rows)
		if err != nil {
			return nil, storageError(err)
		}
		records = append(records, record)
	}
	return records, storageError(rows.Err())
}
