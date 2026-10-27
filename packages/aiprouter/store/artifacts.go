// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"github.com/delinoio/oss/packages/aiprouter/artifacts"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"time"
)

var _ artifacts.Repository = (*Store)(nil)

func artifactScope(o artifacts.Owner) core.Scope {
	return core.Scope{OrganizationID: o.OrganizationID, UserID: o.UserID, Generation: o.Generation}
}
func (s *Store) Reserve(ctx context.Context, o artifacts.Owner, items []artifacts.Artifact, _ time.Time) error {
	if len(items) == 0 || len(items) > 200 {
		return artifacts.ErrLimit
	}
	maxFile, maxBatch := int64(50<<20), int64(100<<20)
	video := items[0].ContentType == "video/mp4"
	if video {
		maxFile, maxBatch = 200<<20, 600<<20
	}
	var total int64
	for _, a := range items {
		if (a.ContentType == "video/mp4") != video {
			return artifacts.ErrInvalid
		}
		if !video {
			switch a.ContentType {
			case "image/png", "image/jpeg", "image/webp", "image/gif", "image/avif":
			default:
				return artifacts.ErrInvalid
			}
		}
		if a.Owner != o || a.Size < 1 || a.Size > maxFile || a.ID == "" || a.ObjectKey != "artifacts/"+a.ID {
			return artifacts.ErrInvalid
		}
		total += a.Size
	}
	if total > maxBatch {
		return artifacts.ErrLimit
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, artifactScope(o)); e != nil {
			return e
		}
		for _, a := range items {
			var valid bool
			if e := tx.QueryRowContext(ctx, "SELECT $1::timestamptz<=clock_timestamp()+interval '1 second' AND $2::timestamptz<=clock_timestamp()+interval '24 hours' AND $2>clock_timestamp() AND $3::timestamptz<=clock_timestamp()+interval '5 minutes' AND $3>clock_timestamp()", a.CreatedAt, a.ExpiresAt, a.UploadDeadline).Scan(&valid); e != nil {
				return e
			}
			if !valid {
				return artifacts.ErrInvalid
			}
			if _, e := tx.ExecContext(ctx, "INSERT INTO artifact_ids(id,object_key) VALUES($1,$2)", a.ID, a.ObjectKey); e != nil {
				return e
			}
			if _, e := tx.ExecContext(ctx, "INSERT INTO artifacts(id,org_id,user_id,object_key,size,content_type,created_at,expires_at,upload_deadline,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'pending')", a.ID, o.OrganizationID, o.UserID, a.ObjectKey, a.Size, a.ContentType, a.CreatedAt, a.ExpiresAt, a.UploadDeadline); e != nil {
				return e
			}
			if _, e := tx.ExecContext(ctx, "INSERT INTO object_deletes(id,object_key,not_before) VALUES($1,$2,$3)", a.ID, a.ObjectKey, a.UploadDeadline); e != nil {
				return e
			}
		}
		return nil
	})
}
func (s *Store) Publish(ctx context.Context, o artifacts.Owner, ids []string, _ time.Time) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, artifactScope(o)); e != nil {
			return e
		}
		for _, id := range ids {
			res, e := tx.ExecContext(ctx, "UPDATE artifacts SET state='active' WHERE org_id=$1 AND user_id=$2 AND id=$3 AND state='pending' AND expires_at>clock_timestamp() AND upload_deadline>clock_timestamp()", o.OrganizationID, o.UserID, id)
			if e != nil {
				return e
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				return artifacts.ErrNotFound
			}
			if _, e = tx.ExecContext(ctx, "DELETE FROM object_deletes WHERE id=$1", id); e != nil {
				return e
			}
		}
		_, e := tx.ExecContext(ctx, "DELETE FROM artifacts WHERE org_id=$1 AND user_id=$2 AND state='active' AND id IN(SELECT id FROM artifacts WHERE org_id=$1 AND user_id=$2 AND state='active' ORDER BY created_at DESC,id DESC OFFSET 200)", o.OrganizationID, o.UserID)
		return e
	})
}
func (s *Store) Get(ctx context.Context, o artifacts.Owner, id string, _ time.Time) (a artifacts.Artifact, e error) {
	a.Owner = o
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, artifactScope(o)); e != nil {
			return e
		}
		e := tx.QueryRowContext(ctx, "SELECT id,object_key,size,content_type,created_at,expires_at,upload_deadline FROM artifacts WHERE org_id=$1 AND user_id=$2 AND id=$3 AND state='active' AND expires_at>clock_timestamp()", o.OrganizationID, o.UserID, id).Scan(&a.ID, &a.ObjectKey, &a.Size, &a.ContentType, &a.CreatedAt, &a.ExpiresAt, &a.UploadDeadline)
		if e == sql.ErrNoRows {
			return artifacts.ErrNotFound
		}
		return e
	})
	return
}
func (s *Store) Tombstone(ctx context.Context, o artifacts.Owner, ids []string, _ time.Time) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, artifactScope(o)); e != nil {
			return e
		}
		for _, id := range ids {
			if _, e := tx.ExecContext(ctx, "DELETE FROM artifacts WHERE org_id=$1 AND user_id=$2 AND id=$3", o.OrganizationID, o.UserID, id); e != nil {
				return e
			}
		}
		return nil
	})
}
func (s *Store) Expire(ctx context.Context, _ time.Time) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "DELETE FROM artifacts WHERE expires_at<=clock_timestamp() OR (state='pending' AND upload_deadline<=clock_timestamp())")
		return e
	})
}
func (s *Store) PendingDeletes(ctx context.Context, _ time.Time, limit int) (out []artifacts.Deletion, e error) {
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	e = s.tx(ctx, func(tx *sql.Tx) error {
		rows, e := tx.QueryContext(ctx, "SELECT id,object_key,not_before FROM object_deletes WHERE not_before<=clock_timestamp() ORDER BY not_before LIMIT $1", limit)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var d artifacts.Deletion
			if e = rows.Scan(&d.ID, &d.ObjectKey, &d.NotBefore); e != nil {
				return e
			}
			out = append(out, d)
		}
		return rows.Err()
	})
	return
}
func (s *Store) CompleteDelete(ctx context.Context, d artifacts.Deletion, _ time.Time) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "DELETE FROM object_deletes WHERE id=$1 AND object_key=$2 AND not_before=$3 AND not_before<=clock_timestamp() AND NOT EXISTS(SELECT 1 FROM artifacts WHERE object_key=$2)", d.ID, d.ObjectKey, d.NotBefore)
		return e
	})
}
