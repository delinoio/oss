// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"github.com/delinoio/oss/packages/aiprouter/core"
)

func (s *Store) rotateProviderState(ctx context.Context, tx *sql.Tx) error {
	type row struct {
		sc                 core.Scope
		key, namespace, id string
		value              []byte
	}
	for _, table := range []string{s.temp + ".provider_flows", s.temp + ".provider_states", s.temp + ".native_quotas", s.temp + ".vertex_projects", s.temp + ".compaction_handoffs"} {
		if _, e := tx.ExecContext(ctx, "LOCK TABLE "+table+" IN ACCESS EXCLUSIVE MODE"); e != nil {
			return e
		}
		query := "SELECT org_id,user_id,generation,'' AS key,'' AS namespace,id,value FROM " + table + " WHERE status='pending'"
		if table == s.temp+".provider_states" || table == s.temp+".compaction_handoffs" {
			query = "SELECT org_id,user_id,generation,key_id,namespace,id,value FROM " + table
		}
		if table == s.temp+".native_quotas" {
			query = "SELECT org_id,user_id,generation,'',version,id,value FROM " + table
		}
		if table == s.temp+".vertex_projects" {
			query = "SELECT org_id,user_id,generation,'',version,id,value FROM " + table + " WHERE value IS NOT NULL"
		}
		rows, e := tx.QueryContext(ctx, query)
		if e != nil {
			return e
		}
		var all []row
		for rows.Next() {
			var r row
			if e = rows.Scan(&r.sc.OrganizationID, &r.sc.UserID, &r.sc.Generation, &r.key, &r.namespace, &r.id, &r.value); e != nil {
				rows.Close()
				return e
			}
			all = append(all, r)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, r := range all {
			aad := ownership(r.sc, "provider-flow", r.id)
			if r.key != "" {
				aad = stateAAD(Principal{Scope: r.sc, KeyID: r.key}, r.namespace, r.id)
			}
			if table == s.temp+".native_quotas" {
				aad = nativeQuotaAAD(r.sc, r.id, r.namespace)
			}
			if table == s.temp+".vertex_projects" {
				aad = vertexProjectAAD(r.sc, r.id, r.namespace)
			}
			p, e := s.keys.Decrypt(r.value, aad)
			if e != nil {
				return e
			}
			v, e := s.keys.Encrypt(p, aad)
			if e != nil {
				return e
			}
			q := "UPDATE " + table + " SET value=$4 WHERE org_id=$1 AND user_id=$2 AND id=$3"
			args := []any{r.sc.OrganizationID, r.sc.UserID, r.id, v}
			if table == s.temp+".vertex_projects" {
				q += " AND version=$5"
				args = append(args, r.namespace)
			}
			if r.key != "" {
				q = "UPDATE " + table + " SET value=$4,size=octet_length($4::bytea) WHERE org_id=$1 AND user_id=$2 AND id=$3 AND key_id=$5 AND namespace=$6"
				args = append(args, r.key, r.namespace)
			}
			if _, e = tx.ExecContext(ctx, q, args...); e != nil {
				return e
			}
		}
	}
	_, e := tx.ExecContext(ctx, "DELETE FROM "+s.temp+".provider_states WHERE (org_id,user_id,key_id,namespace,id) IN(SELECT org_id,user_id,key_id,namespace,id FROM(SELECT *,row_number() OVER(PARTITION BY org_id,user_id,namespace ORDER BY created_at DESC,id DESC,key_id DESC) n,sum(size) OVER(PARTITION BY org_id,user_id,namespace ORDER BY created_at DESC,id DESC,key_id DESC) bytes,min(quota_entries) OVER(PARTITION BY org_id,user_id,namespace) entries,min(quota_bytes) OVER(PARTITION BY org_id,user_id,namespace) maximum FROM "+s.temp+".provider_states) q WHERE n>entries OR bytes>maximum)")
	return e
}
