// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/delinoio/oss/packages/tairouter/core"
	"github.com/delinoio/oss/packages/tairouter/inference"
)

// migrateLiveAliases runs with the service stopped for maintenance. The old kind
// is authenticated before conversion because changing only kind breaks the AAD.
func (s *Store) migrateLiveAliases(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "LOCK TABLE "+s.temp+".states IN ACCESS EXCLUSIVE MODE"); err != nil {
		return err
	}
	type legacy struct {
		org, user, id      string
		generation         int64
		value              []byte
		created, sqlExpiry time.Time
	}
	rs, err := tx.QueryContext(ctx, "SELECT d.org_id,d.user_id,d.id,m.generation,d.value,d.created_at,d.expires_at FROM "+s.temp+".states d JOIN memberships m ON m.org_id=d.org_id AND m.user_id=d.user_id WHERE d.kind=$1 AND d.id LIKE 'live/%' ORDER BY d.created_at,d.id", core.Responses)
	if err != nil {
		return err
	}
	var rows []legacy
	for rs.Next() {
		var r legacy
		if err = rs.Scan(&r.org, &r.user, &r.id, &r.generation, &r.value, &r.created, &r.sqlExpiry); err != nil {
			rs.Close()
			return err
		}
		rows = append(rows, r)
	}
	err = rs.Err()
	rs.Close()
	if err != nil {
		return err
	}
	for _, r := range rows {
		sc := core.Scope{OrganizationID: r.org, UserID: r.user, Generation: r.generation}
		plain, decryptErr := s.keys.Decrypt(r.value, ownership(sc, "state:"+string(core.Responses), r.id))
		var saved struct {
			Version int
			Binding inference.LiveBinding
		}
		parseErr := json.Unmarshal(plain, &saved)
		keyID, alias, hasKey := strings.Cut(strings.TrimPrefix(r.id, "live/"), "/")
		valid := decryptErr == nil && parseErr == nil && hasKey && keyID != "" && alias != "" && saved.Version == 1 &&
			saved.Binding.AccountID != "" && saved.Binding.UpstreamCallID != "" && saved.Binding.ServedCredentialVersion != "" &&
			saved.Binding.Target.Policy != nil && saved.Binding.Target.Provider == saved.Binding.AccountID && saved.Binding.ExpiresAt.After(time.Now()) &&
			!saved.Binding.ExpiresAt.After(time.Now().Add(24*time.Hour)) && !saved.Binding.ExpiresAt.After(r.sqlExpiry)
		if valid {
			var exists bool
			err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM api_keys k JOIN private_data p ON p.org_id=k.org_id AND p.user_id=k.user_id AND p.kind='provider' AND p.id=$5 AND p.version=$6 WHERE k.id=$1 AND k.org_id=$2 AND k.user_id=$3 AND k.generation=$4 AND NOT k.revoked AND (k.expires_at IS NULL OR k.expires_at>clock_timestamp()))", keyID, r.org, r.user, r.generation, saved.Binding.AccountID, saved.Binding.ServedCredentialVersion).Scan(&exists)
			if err != nil {
				return err
			}
			valid = exists
		}
		// Invalid or expired legacy aliases must disappear, not acquire a new deadline.
		if _, err = tx.ExecContext(ctx, "DELETE FROM "+s.temp+".states WHERE org_id=$1 AND user_id=$2 AND kind=$3 AND id=$4", r.org, r.user, core.Responses, r.id); err != nil {
			return err
		}
		if !valid {
			continue
		}
		ciphertext, encryptErr := s.keys.Encrypt(plain, ownership(sc, "state:"+string(core.LiveAlias), r.id))
		if encryptErr != nil {
			return encryptErr
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO "+s.temp+".states(org_id,user_id,kind,id,value,size,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING", r.org, r.user, core.LiveAlias, r.id, ciphertext, len(ciphertext), r.created, saved.Binding.ExpiresAt); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM "+s.temp+".states WHERE kind=$1 AND expires_at<=clock_timestamp()", core.LiveAlias); err != nil {
		return err
	}
	_, count, max, err := core.LiveAlias.Limits()
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM "+s.temp+".states WHERE (org_id,user_id,kind,id) IN (SELECT org_id,user_id,kind,id FROM (SELECT org_id,user_id,kind,id,row_number() OVER(PARTITION BY org_id,user_id ORDER BY created_at DESC,id DESC) n,sum(size) OVER(PARTITION BY org_id,user_id ORDER BY created_at DESC,id DESC) bytes FROM "+s.temp+".states WHERE kind=$1) ranked WHERE n>$2 OR bytes>$3)", core.LiveAlias, count, max); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO schema_versions(version) VALUES(14) ON CONFLICT DO NOTHING")
	return err
}
