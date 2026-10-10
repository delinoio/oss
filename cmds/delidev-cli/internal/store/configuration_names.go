// SPDX-License-Identifier: Apache-2.0
package store

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"log/slog"
)

// RequireConfigurationName runs under the shared writer transaction. Archived
// configuration still reserves names; deletion removes the live entity row.
func (t *Tx) RequireConfigurationName(kind domain.Kind, id domain.ID, name string) error {
	if kind != domain.ProjectKind && kind != domain.RepositoryKind {
		return nil
	}
	key := domain.ConfigurationNameKey(name)
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
		var value struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &value); err != nil {
			return storageError(err)
		}
		if domain.ConfigurationNameKey(value.Name) == key {
			slog.Warn("configuration_name_conflict", "kind", kind)
			return domain.NameConflict(kind)
		}
	}
	return storageError(rows.Err())
}

func (t *Tx) requireDocumentName(kind domain.Kind, id domain.ID, raw []byte) error {
	if kind != domain.ProjectKind && kind != domain.RepositoryKind {
		return nil
	}
	var value struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return storageError(err)
	}
	return t.RequireConfigurationName(kind, id, value.Name)
}
