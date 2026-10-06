// SPDX-License-Identifier: Apache-2.0
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"time"
)

func (t *Tx) AccountOAuth(id domain.ID) (domain.AccountOAuthAttempt, error) {
	var a domain.AccountOAuthAttempt
	if id.Validate() != nil {
		return a, corrupt()
	}
	var raw []byte
	var revision uint64
	var state domain.AccountOAuthState
	var generation, start domain.ID
	err := t.tx.QueryRowContext(t.ctx, "SELECT revision,state,generation,start_request_id,body FROM account_oauth_attempts WHERE id=?", id).Scan(&revision, &state, &generation, &start, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return a, domain.Fail(domain.NotFound, "OAuth attempt was not found.", "Start an explicit new connection flow.")
	}
	if err != nil {
		return a, storageError(err)
	}
	if domain.Decode(raw, &a) != nil || a.Validate() != nil || a.ID != id || a.Revision != revision || a.State != state || a.Generation != generation || a.StartRequestID != start {
		return a, corrupt()
	}
	return a, nil
}
func (t *Tx) PutAccountOAuth(a domain.AccountOAuthAttempt, expected uint64) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	if a.Validate() != nil || a.Revision != expected+1 {
		return corrupt()
	}
	raw, err := json.Marshal(a)
	if err != nil || len(raw) > 8192 {
		return corrupt()
	}
	if expected == 0 {
		_, err = t.tx.ExecContext(t.ctx, "INSERT INTO account_oauth_attempts(id,revision,state,generation,start_request_id,body) VALUES(?,?,?,?,?,?)", a.ID, a.Revision, a.State, a.Generation, a.StartRequestID, raw)
		return storageError(err)
	}
	result, err := t.tx.ExecContext(t.ctx, "UPDATE account_oauth_attempts SET revision=?,state=?,generation=?,body=? WHERE id=? AND revision=?", a.Revision, a.State, a.Generation, raw, a.ID, expected)
	if err != nil {
		return storageError(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return storageError(err)
	}
	if n != 1 {
		return domain.Fail(domain.Conflict, "OAuth attempt revision changed.", "Read the original attempt before another explicit operation.")
	}
	return nil
}
func (t *Tx) AccountOAuthPending() (int, error) {
	var tokens int
	if err := t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM account_oauth_credentials WHERE json_extract(body,'$.refresh_state') IN ('claimed','recovery-required') OR json_array_length(json_extract(body,'$.cleanup'))>0`).Scan(&tokens); err != nil {
		return 0, storageError(err)
	}
	var n int
	err := t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM account_oauth_attempts WHERE state IN ('exchanging','saving','recovery-required') OR json_extract(body,'$.cleanup_pending')=1`).Scan(&n)
	if err != nil {
		return 0, storageError(err)
	}
	rows, err := t.tx.QueryContext(t.ctx, `SELECT id FROM account_oauth_attempts WHERE state='awaiting-authorization' AND COALESCE(json_extract(body,'$.cleanup_pending'),0)=0 LIMIT 33`)
	if err != nil {
		return 0, storageError(err)
	}
	var ids []domain.ID
	for rows.Next() {
		var id domain.ID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, storageError(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, storageError(err)
	}
	if len(ids) > 32 {
		return 0, corrupt()
	}
	now := time.Now().UTC()
	for _, id := range ids {
		a, err := t.AccountOAuth(id)
		if err != nil {
			return 0, err
		}
		if now.Before(a.ExpiresAt) {
			n++
		}
	}
	return n + tokens, nil
}

// A new lifetime cannot recover an old verifier or send a claimed exchange.
// Already sealed references remain recoverable only through explicit original
// local completion, never through startup, status or a copied database image.
func (t *Tx) InterruptAccountOAuth(generation domain.ID, now time.Time) error {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT id FROM account_oauth_attempts WHERE generation<>? AND state IN ('awaiting-authorization','exchanging','saving') LIMIT 33`, generation)
	if err != nil {
		return storageError(err)
	}
	ids := []domain.ID{}
	for rows.Next() {
		var id domain.ID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return storageError(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return storageError(err)
	}
	if len(ids) > 32 {
		return corrupt()
	}
	for _, id := range ids {
		a, err := t.AccountOAuth(id)
		if err != nil {
			return err
		}
		original := a.Revision
		a.Revision++
		a.UpdatedAt = now
		if a.State == domain.OAuthAwaiting {
			a.State = domain.OAuthInterrupted
		} else {
			a.State = domain.OAuthRecovery
		}
		a.Problem = domain.Fail(domain.RecoveryRequired, "The original OAuth process ended.", "Inspect the original attempt; an exchange is never repeated. Recover only its already protected local result.")
		if err := t.PutAccountOAuth(a, original); err != nil {
			return err
		}
	}
	return nil
}

func (t *Tx) ExpireAccountOAuth(now time.Time) error {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT id FROM account_oauth_attempts WHERE state='awaiting-authorization' LIMIT 33")
	if err != nil {
		return storageError(err)
	}
	ids := []domain.ID{}
	for rows.Next() {
		var id domain.ID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return storageError(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return storageError(err)
	}
	if len(ids) > 32 {
		return corrupt()
	}
	for _, id := range ids {
		a, err := t.AccountOAuth(id)
		if err != nil {
			return err
		}
		if now.Before(a.ExpiresAt) {
			continue
		}
		rev := a.Revision
		a.Revision++
		a.UpdatedAt = now
		a.State = domain.OAuthExpired
		if err := t.PutAccountOAuth(a, rev); err != nil {
			return err
		}
	}
	return nil
}
