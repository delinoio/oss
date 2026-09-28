package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

func (t *Tx) CheckReviewCapacity(session domain.ID) error {
	if err := session.Validate(); err != nil {
		return err
	}
	var count uint64
	if err := t.tx.QueryRowContext(t.ctx, "SELECT COUNT(*) FROM entities WHERE kind=? AND session_id=?", domain.ReviewKind, session).Scan(&count); err != nil {
		return storageError(err)
	}
	if count >= 1000 {
		return domain.Fail(domain.ResourceExhausted, "This session has reached its 1,000 local review record limit.", "Remove unneeded comments before creating another comment or submission; accepted submission history is retained.")
	}
	return nil
}
