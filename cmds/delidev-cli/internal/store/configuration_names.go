// SPDX-License-Identifier: Apache-2.0
package store

import (
	"encoding/json"
	"log/slog"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// CheckConfigurationName runs in the same serialized transaction as publication.
// It reads live documents directly, so archived names remain reserved and deleted
// names are released without a persisted index or a bounded-page false negative.
func (t *Tx) CheckConfigurationName(kind domain.Kind, id domain.ID, body []byte) error {
	if kind != domain.ProjectKind && kind != domain.RepositoryKind {
		return nil
	}
	var incoming struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &incoming); err != nil {
		return err
	}
	key := domain.ConfigurationNameKey(incoming.Name)
	rows, err := t.tx.QueryContext(t.ctx, "SELECT body FROM entities WHERE kind=? AND id<>?", kind, id)
	if err != nil {
		return storageError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return storageError(err)
		}
		var existing struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &existing); err != nil {
			return err
		}
		if domain.ConfigurationNameKey(existing.Name) == key {
			slog.InfoContext(t.ctx, "configuration publication rejected", "kind", kind, "cause", domain.ConfigurationNameConflictCause)
			return domain.ConfigurationNameConflict()
		}
	}
	return storageError(rows.Err())
}
