// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"github.com/delinoio/oss/packages/tairouter/core"
	"time"
)

type AuditFilter struct {
	Action, ActorID, After string
	Outcome                AuditOutcome
	Start, End             *time.Time
	Limit                  int
}

func (s *Store) QueryAudits(ctx context.Context, sc core.Scope, f AuditFilter) ([]Audit, string, error) {
	return s.queryAudits(ctx, sc, core.Identity{}, f, false)
}
func (s *Store) QueryInstanceAudits(ctx context.Context, i core.Identity, f AuditFilter) ([]Audit, string, error) {
	if !s.IsOperator(i) {
		return nil, "", ErrForbidden
	}
	return s.queryAudits(ctx, core.Scope{}, i, f, true)
}
func (s *Store) queryAudits(ctx context.Context, sc core.Scope, i core.Identity, f AuditFilter, instance bool) (out []Audit, next string, e error) {
	if e = validRange(f.Start, f.End); e != nil {
		return
	}
	if f.Limit < 1 || f.Limit > 1000 {
		f.Limit = 100
	}
	binding := f
	binding.After = ""
	aad := cursorContext("audit-page", []any{sc, i}, binding)
	c, e := s.cursor(f.After, aad)
	if e != nil {
		return nil, "", e
	}
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if !instance {
			r, e := s.guard(ctx, tx, sc)
			if e != nil {
				return e
			}
			if !elevated(r) {
				return ErrForbidden
			}
		}
		rows, e := tx.QueryContext(ctx, `SELECT id,COALESCE(org_id,''),COALESCE(actor_id,''),action,target_id,created_at,outcome FROM audits WHERE COALESCE(org_id,'')=$1 AND ($2='' OR action=$2 OR ($2='provider.configuration_changed' AND action='provider.connection_completed')) AND ($3='' OR actor_id=$3) AND ($4='' OR outcome=$4) AND ($5::timestamptz IS NULL OR created_at>=$5) AND ($6::timestamptz IS NULL OR created_at<$6) AND ($7='' OR (created_at,id)<($8,$7)) AND expires_at>clock_timestamp() ORDER BY created_at DESC,id DESC LIMIT $9`, sc.OrganizationID, f.Action, f.ActorID, f.Outcome, f.Start, f.End, c.ID, c.Time, f.Limit+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var a Audit
			if e = rows.Scan(&a.ID, &a.OrganizationID, &a.ActorID, &a.Action, &a.TargetID, &a.CreatedAt, &a.Outcome); e != nil {
				return e
			}
			out = append(out, a)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if len(out) > f.Limit {
			out = out[:f.Limit]
			last := out[len(out)-1]
			next, e = s.makeCursor(pageCursor{Time: last.CreatedAt, ID: last.ID}, aad)
		}
		return e
	})
	return
}
