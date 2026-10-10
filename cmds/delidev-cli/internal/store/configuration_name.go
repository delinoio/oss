// SPDX-License-Identifier: Apache-2.0
package store

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"log/slog"
)

// RequireConfigurationName scans live same-kind configuration in the owning
// SQLite transaction. Archived Projects still reserve names; tombstones do not.
// Stream one bounded entity at a time rather than retaining a catalog payload.
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
		if err = rows.Scan(&raw); err != nil {
			return storageError(err)
		}
		var value struct {
			Name string `json:"name"`
		}
		if err = json.Unmarshal(raw, &value); err != nil {
			return storageError(err)
		}
		if domain.ConfigurationNameKey(value.Name) == key {
			slog.WarnContext(t.ctx, "configuration name collision", "kind", kind, "cause", domain.ConfigurationNameConflictCause)
			return domain.ConfigurationNameConflict(kind)
		}
	}
	return storageError(rows.Err())
}

func (t *Tx) requireConfigurationDocumentName(kind domain.Kind, id domain.ID, raw []byte) error {
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
