// SPDX-License-Identifier: Apache-2.0
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func (t *Tx) AccountOAuthCredential(account, connection domain.ID) (domain.AccountOAuthCredential, bool, error) {
	var v domain.AccountOAuthCredential
	var raw []byte
	var revision uint64
	err := t.tx.QueryRowContext(t.ctx, "SELECT revision,body FROM account_oauth_credentials WHERE account_id=? AND connection_id=?", account, connection).Scan(&revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return v, false, nil
	}
	if err != nil {
		return v, false, storageError(err)
	}
	if domain.Decode(raw, &v) != nil || v.Validate() != nil || v.Revision != revision ||
		domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(v.AccountID), v.AccountID != account) ||
		domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(v.ConnectionID), v.ConnectionID != connection) {
		return v, false, corrupt()
	}
	return v, true, nil
}
func (t *Tx) PutAccountOAuthCredential(v domain.AccountOAuthCredential, expected uint64) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	if v.Validate() != nil || v.Revision != expected+1 {
		return corrupt()
	}
	raw, err := json.Marshal(v)
	if err != nil || len(raw) > 8192 {
		return corrupt()
	}
	if expected == 0 {
		_, err = t.tx.ExecContext(t.ctx, "INSERT INTO account_oauth_credentials(account_id,connection_id,revision,body) VALUES(?,?,?,?)", v.AccountID, v.ConnectionID, v.Revision, raw)
		return storageError(err)
	}
	r, err := t.tx.ExecContext(t.ctx, "UPDATE account_oauth_credentials SET revision=?,body=? WHERE account_id=? AND connection_id=? AND revision=?", v.Revision, raw, v.AccountID, v.ConnectionID, expected)
	if err != nil {
		return storageError(err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return storageError(err)
	}
	if n != 1 {
		return domain.Fail(domain.Conflict, "OAuth credential generation changed.", "Read the original connection.")
	}
	return nil
}
func (t *Tx) RetireAccountOAuthCredentials(account domain.ID) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	_, err := t.tx.ExecContext(t.ctx, `UPDATE account_oauth_credentials SET revision=revision+1,body=json_set(body,'$.revision',revision+1,'$.refresh_state','retired','$.cleanup',json('[]')) WHERE account_id=?`, account)
	return storageError(err)
}
