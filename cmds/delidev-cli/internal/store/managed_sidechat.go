// SPDX-License-Identifier: Apache-2.0
package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// ManagedSidechatFinish reads only the server-written protected Finish receipt.
// Public Fork reports carry its reference, never credential or lease authority.
func (t *Tx) ManagedSidechatFinish(id domain.ID) (domain.SubscriptionForkFinish, error) {
	var receipt struct {
		ID              domain.ID                      `json:"id"`
		CompletionID    domain.ID                      `json:"completion_id,omitempty"`
		ManagedSidechat *domain.SubscriptionForkFinish `json:"managed_sidechat,omitempty"`
	}
	if err := t.Authorize(); err != nil {
		return domain.SubscriptionForkFinish{}, err
	}
	var raw []byte
	if id.Validate() != nil {
		return domain.SubscriptionForkFinish{}, domain.SidechatUnavailable()
	}
	if err := t.tx.QueryRowContext(t.ctx, "SELECT result FROM receipts WHERE id=?", id).Scan(&raw); err != nil {
		return domain.SubscriptionForkFinish{}, domain.SidechatUnavailable()
	}
	if domain.Decode(raw, &receipt) != nil || receipt.ManagedSidechat == nil || receipt.ManagedSidechat.Finish != id || receipt.ManagedSidechat.Account != receipt.ID {
		return domain.SubscriptionForkFinish{}, domain.SidechatUnavailable()
	}
	return *receipt.ManagedSidechat, nil
}
