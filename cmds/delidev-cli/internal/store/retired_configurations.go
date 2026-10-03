// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// RetiredConfiguration is an explicit history-only read. Get and Tx.Get never
// consult this table, so configuration, admission and receipt replay cannot
// recover retired authority from an old ID.
func (t *Tx) RetiredConfiguration(kind domain.Kind, id domain.ID) (Record, error) {
	if id.Validate() != nil || kind != domain.AccountKind && kind != domain.ProviderKind && kind != domain.ModelKind {
		return Record{}, domain.Fail(domain.NotFound, "No retired configuration exists.", "Read a historical account, provider or model ID.")
	}
	r, err := scan(t.tx.QueryRowContext(t.ctx, "SELECT "+recordColumns+" FROM retired_configurations WHERE id=? AND kind=?", id, kind))
	if errors.Is(err, sql.ErrNoRows) {
		return r, domain.Fail(domain.NotFound, "No retired configuration exists.", "Read the current configuration inventory.")
	}
	return r, storageError(err)
}
func (s *Store) RetiredConfiguration(ctx context.Context, kind domain.Kind, id domain.ID) (Record, error) {
	var r Record
	err := s.Read(ctx, func(tx *Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		var err error
		r, err = tx.RetiredConfiguration(kind, id)
		return err
	})
	return r, err
}
