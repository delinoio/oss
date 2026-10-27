// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"regexp"
)

func vertexProjectAAD(sc core.Scope, id, version string) string {
	return ownership(sc, "vertex-project:"+version, id)
}
func (s *Store) readVertexProject(ctx context.Context, tx *sql.Tx, l Lease, id, version string) (string, error) {
	sc := l.Principal.Scope
	var raw []byte
	e := tx.QueryRowContext(ctx, "SELECT value FROM "+s.temp+".vertex_projects WHERE org_id=$1 AND user_id=$2 AND id=$3 AND version=$4 AND generation=$5 AND value IS NOT NULL AND expires_at>clock_timestamp()", sc.OrganizationID, sc.UserID, id, version, sc.Generation).Scan(&raw)
	if e != nil {
		return "", noRows(e)
	}
	b, e := s.keys.Decrypt(raw, vertexProjectAAD(sc, id, version))
	return string(b), e
}
func (s *Store) GetVertexProject(ctx context.Context, l Lease, id, version string) (project string, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkNativeAccount(ctx, tx, l, id, version); e != nil {
			return e
		}
		var e error
		project, e = s.readVertexProject(ctx, tx, l, id, version)
		return e
	})
	return
}

// ClaimVertexProject coordinates bounded ten-second read-only discovery. Only
// positive project numbers are cached; the fifteen-second claim fences late work.
func (s *Store) ClaimVertexProject(ctx context.Context, l Lease, id, version string) (project, claim string, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkNativeAccount(ctx, tx, l, id, version); e != nil {
			return e
		}
		sc := l.Principal.Scope
		var e error
		project, e = s.readVertexProject(ctx, tx, l, id, version)
		if e == nil {
			return nil
		}
		if e != ErrNotFound {
			return e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM "+s.temp+".vertex_projects WHERE org_id=$1 AND user_id=$2 AND ((value IS NULL AND claim_until<=clock_timestamp()) OR (expires_at<=clock_timestamp() AND claim_hash IS NULL))", sc.OrganizationID, sc.UserID); e != nil {
			return e
		}
		var exists bool
		if e = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM "+s.temp+".vertex_projects WHERE org_id=$1 AND user_id=$2 AND id=$3 AND version=$4)", sc.OrganizationID, sc.UserID, id, version).Scan(&exists); e != nil {
			return e
		}
		if exists {
			return ErrBusy
		}
		var count int
		if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM "+s.temp+".vertex_projects WHERE org_id=$1 AND user_id=$2", sc.OrganizationID, sc.UserID).Scan(&count); e != nil {
			return e
		}
		if count >= 64 {
			r, e := tx.ExecContext(ctx, "DELETE FROM "+s.temp+".vertex_projects WHERE (org_id,user_id,id,version) IN(SELECT org_id,user_id,id,version FROM "+s.temp+".vertex_projects WHERE org_id=$1 AND user_id=$2 AND claim_hash IS NULL ORDER BY created_at,id,version LIMIT 1)", sc.OrganizationID, sc.UserID)
			if e != nil {
				return e
			}
			n, e := r.RowsAffected()
			if e != nil {
				return e
			}
			if n == 0 {
				return ErrBusy
			}
		}
		claim = core.Token()
		_, e = tx.ExecContext(ctx, "INSERT INTO "+s.temp+".vertex_projects(org_id,user_id,id,version,generation,claim_hash,claim_until) VALUES($1,$2,$3,$4,$5,$6,clock_timestamp()+interval '15 seconds')", sc.OrganizationID, sc.UserID, id, version, sc.Generation, core.Hash(claim))
		return e
	})
	if e != nil {
		project = ""
		claim = ""
	}
	return
}
func (s *Store) CompleteVertexProject(ctx context.Context, l Lease, id, version, claim, project string) error {
	if claim == "" || (project != "" && !regexp.MustCompile(`^[0-9]{1,20}$`).MatchString(project)) {
		return ErrConflict
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkNativeAccount(ctx, tx, l, id, version); e != nil {
			return e
		}
		sc := l.Principal.Scope
		args := []any{sc.OrganizationID, sc.UserID, id, version, sc.Generation, core.Hash(claim)}
		q := "DELETE FROM " + s.temp + ".vertex_projects WHERE org_id=$1 AND user_id=$2 AND id=$3 AND version=$4 AND generation=$5 AND claim_hash=$6 AND claim_until>clock_timestamp()"
		if project != "" {
			b, e := s.keys.Encrypt([]byte(project), vertexProjectAAD(sc, id, version))
			if e != nil {
				return e
			}
			args = append(args, b)
			q = "UPDATE " + s.temp + ".vertex_projects SET value=$7,claim_hash=NULL,expires_at=clock_timestamp()+interval '24 hours',created_at=clock_timestamp() WHERE org_id=$1 AND user_id=$2 AND id=$3 AND version=$4 AND generation=$5 AND claim_hash=$6 AND claim_until>clock_timestamp()"
		}
		r, e := tx.ExecContext(ctx, q, args...)
		if e != nil {
			return e
		}
		n, e := r.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}
