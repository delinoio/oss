// SPDX-License-Identifier: Apache-2.0
package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

func (t *Tx) SessionDefaultSettings() (Record, domain.Settings, error) {
	records, _, err := t.sessionPage(2, "SELECT "+recordColumns+" FROM entities WHERE kind='settings' ORDER BY id LIMIT 2")
	if err != nil {
		return Record{}, domain.Settings{}, err
	}
	if len(records) > 1 {
		return Record{}, domain.Settings{}, domain.Fail(domain.RecoveryRequired, "Server settings ownership is ambiguous.", "Reconcile the original settings singleton.")
	}
	if len(records) == 0 {
		return Record{}, domain.DefaultSettings(), nil
	}
	value, err := Decode[domain.Settings](records[0])
	if err == nil {
		err = value.Validate()
	}
	return records[0], value, err
}
