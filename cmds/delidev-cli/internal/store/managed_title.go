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
