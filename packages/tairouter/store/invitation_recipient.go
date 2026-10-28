// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"github.com/delinoio/oss/packages/tairouter/core"
)

const invitationColumns = "i.id,i.org_id,i.email,CASE WHEN i.status='pending' AND i.expires_at<=clock_timestamp() THEN 'expired' ELSE i.status END,i.expires_at,i.attempts,o.name,i.created_at,i.next_attempt,i.delivery_state"

func scanInvitation(row interface{ Scan(...any) error }, i *Invitation) error {
	return row.Scan(&i.ID, &i.OrganizationID, &i.Email, &i.Status, &i.ExpiresAt, &i.Attempts, &i.OrganizationName, &i.CreatedAt, &i.NextAttemptAt, &i.DeliveryState)
}

// User records are created only after verified-email OIDC authentication.
func (s *Store) GetInvitationForUser(ctx context.Context, userID, token string) (out Invitation, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		var i Invitation
		if err := scanInvitation(tx.QueryRowContext(ctx, "SELECT "+invitationColumns+" FROM invitations i JOIN organizations o ON o.id=i.org_id WHERE i.hash=$1", core.Hash(token)), &i); err != nil {
			return noRows(err)
		}
		var email string
		if err := tx.QueryRowContext(ctx, "SELECT email FROM users WHERE id=$1", userID).Scan(&email); err != nil {
			return noRows(err)
		}
		if emailMatch(email) != i.Email {
			return ErrInvitationEmailMismatch
		}
		out = i
		return nil
	})
	return
}
func (s *Store) ListMyInvitations(ctx context.Context, userID string, limit int, after string) (out []Invitation, next string, e error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	e = s.tx(ctx, func(tx *sql.Tx) error {
		var email string
		if err := tx.QueryRowContext(ctx, "SELECT email FROM users WHERE id=$1", userID).Scan(&email); err != nil {
			return noRows(err)
		}
		rows, err := tx.QueryContext(ctx, "SELECT "+invitationColumns+" FROM invitations i JOIN organizations o ON o.id=i.org_id WHERE i.email=$1 AND i.status='pending' AND i.expires_at>clock_timestamp() AND i.id>$2 ORDER BY i.id LIMIT $3", emailMatch(email), after, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var i Invitation
			if err = scanInvitation(rows, &i); err != nil {
				return err
			}
			out = append(out, i)
		}
		if len(out) > limit {
			out = out[:limit]
			next = out[len(out)-1].ID
		}
		return rows.Err()
	})
	return
}
