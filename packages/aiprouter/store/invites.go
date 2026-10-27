// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"strings"
	"time"
)

type Invitation struct {
	DeliveryState                     string
	ID, OrganizationID, Email, Status string
	OrganizationName                  string
	CreatedAt, NextAttemptAt          time.Time
	ExpiresAt                         time.Time
	Attempts                          int
}
type Mail struct {
	Invitation
	Token, Claim string
}

// Email matching preserves the local part and folds only the domain.
func emailMatch(s string) string {
	a, b, ok := strings.Cut(strings.TrimSpace(s), "@")
	if !ok {
		return ""
	}
	return a + "@" + strings.ToLower(b)
}
func (s *Store) Invite(ctx context.Context, sc core.Scope, email string) (Invitation, error) {
	return s.invite(ctx, sc, email, "invitation.created")
}
func (s *Store) invite(ctx context.Context, sc core.Scope, email, action string) (i Invitation, e error) {
	email = emailMatch(email)
	if email == "" {
		return i, ErrConflict
	}
	i = Invitation{ID: core.ID(), OrganizationID: sc.OrganizationID, Email: email, Status: "pending", DeliveryState: "queued"}
	e = s.tx(ctx, func(tx *sql.Tx) error {
		r, e := s.guard(ctx, tx, sc)
		if e != nil {
			return e
		}
		if !elevated(r) {
			return ErrForbidden
		}
		var recent bool
		if e = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM invitations WHERE org_id=$1 AND email=$2 AND created_at>clock_timestamp()-interval '1 minute')", sc.OrganizationID, email).Scan(&recent); e != nil {
			return e
		}
		if recent {
			return &LimitError{time.Minute}
		}
		if _, e = tx.ExecContext(ctx, "UPDATE invitations SET status='superseded',claim=NULL WHERE org_id=$1 AND email=$2 AND status='pending'", sc.OrganizationID, email); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM "+s.temp+".mail_tokens WHERE invitation_id IN(SELECT id FROM invitations WHERE org_id=$1 AND email=$2)", sc.OrganizationID, email); e != nil {
			return e
		}
		raw := core.Token()
		value, e := s.keys.Encrypt([]byte(raw), "invitation/"+i.ID)
		if e != nil {
			return e
		}
		if e = tx.QueryRowContext(ctx, "INSERT INTO invitations(id,org_id,email,hash) VALUES($1,$2,$3,$4) RETURNING expires_at,created_at,next_attempt", i.ID, sc.OrganizationID, email, core.Hash(raw)).Scan(&i.ExpiresAt, &i.CreatedAt, &i.NextAttemptAt); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO "+s.temp+".mail_tokens(invitation_id,value) VALUES($1,$2)", i.ID, value); e != nil {
			return e
		}
		if e = tx.QueryRowContext(ctx, "SELECT name FROM organizations WHERE id=$1", sc.OrganizationID).Scan(&i.OrganizationName); e != nil {
			return e
		}
		return s.audit(ctx, tx, sc.OrganizationID, sc.UserID, action, i.ID)
	})
	return
}
func (s *Store) CancelInvite(ctx context.Context, sc core.Scope, id string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		r, e := s.guard(ctx, tx, sc)
		if e != nil {
			return e
		}
		if !elevated(r) {
			return ErrForbidden
		}
		if _, e = tx.ExecContext(ctx, "UPDATE invitations SET status='canceled',claim=NULL WHERE id=$1 AND org_id=$2 AND status='pending'", id, sc.OrganizationID); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM "+s.temp+".mail_tokens WHERE invitation_id IN(SELECT id FROM invitations WHERE id=$1 AND org_id=$2)", id, sc.OrganizationID); e != nil {
			return e
		}
		return s.audit(ctx, tx, sc.OrganizationID, sc.UserID, "invitation.canceled", id)
	})
}

var ErrInvitationEmailMismatch = fmt.Errorf("invitation email mismatch: %w", ErrForbidden)

func (s *Store) AcceptInvite(ctx context.Context, userID, raw string) (core.Scope, error) {
	return s.acceptInvite(ctx, userID, "hash", core.Hash(raw))
}
func (s *Store) AcceptInviteID(ctx context.Context, userID, id string) (core.Scope, error) {
	return s.acceptInvite(ctx, userID, "id::text", id)
}

// Both selectors use the same organization gate and pending-state transaction.
func (s *Store) acceptInvite(ctx context.Context, userID, column string, selector any) (sc core.Scope, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		var id, org, email string
		if e := tx.QueryRowContext(ctx, "SELECT id,org_id FROM invitations WHERE "+column+"=$1", selector).Scan(&id, &org); e != nil {
			return noRows(e)
		}
		if e := s.lock(ctx, tx, org); e != nil {
			return e
		}
		if e := tx.QueryRowContext(ctx, "SELECT email FROM invitations WHERE id=$1 AND status='pending' AND expires_at>clock_timestamp() FOR UPDATE", id).Scan(&email); e != nil {
			return noRows(e)
		}
		var actual string
		if e := tx.QueryRowContext(ctx, "SELECT email FROM users WHERE id=$1", userID).Scan(&actual); e != nil {
			return noRows(e)
		}
		if emailMatch(actual) != email {
			return ErrInvitationEmailMismatch
		}
		sc.OrganizationID = org
		sc.UserID = userID
		if _, e := tx.ExecContext(ctx, "INSERT INTO memberships(org_id,user_id,role) VALUES($1,$2,'member') ON CONFLICT DO NOTHING", org, userID); e != nil {
			return e
		}
		if e := tx.QueryRowContext(ctx, "SELECT generation FROM memberships WHERE org_id=$1 AND user_id=$2", org, userID).Scan(&sc.Generation); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, "UPDATE invitations SET status='accepted',claim=NULL WHERE id=$1", id); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, "DELETE FROM "+s.temp+".mail_tokens WHERE invitation_id=$1", id); e != nil {
			return e
		}
		return s.audit(ctx, tx, org, userID, "invitation.accepted", id)
	})
	return
}

// ClaimMail reserves a worker claim, not a send attempt. WithMailAttempt owns sends.
func (s *Store) ClaimMail(ctx context.Context) (m Mail, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		var id, org string
		if e := tx.QueryRowContext(ctx, "SELECT id,org_id FROM invitations WHERE status='pending' AND expires_at>clock_timestamp() AND next_attempt<=clock_timestamp() AND attempts<3 AND (claim IS NULL OR claim_until<=clock_timestamp()) ORDER BY next_attempt LIMIT 1").Scan(&id, &org); e != nil {
			return noRows(e)
		}
		if e := s.lock(ctx, tx, org); e != nil {
			return e
		}
		m.Claim = core.Token()
		return noRows(tx.QueryRowContext(ctx, "UPDATE invitations SET claim=$1,claim_until=clock_timestamp()+interval '2 minutes' WHERE id=$2 AND status='pending' AND expires_at>clock_timestamp() AND next_attempt<=clock_timestamp() AND attempts<3 AND (claim IS NULL OR claim_until<=clock_timestamp()) RETURNING id,org_id,email,status,expires_at,attempts", m.Claim, id).Scan(&m.ID, &m.OrganizationID, &m.Email, &m.Status, &m.ExpiresAt, &m.Attempts))
	})
	return
}

type MailOutcome int

const (
	MailSuccess MailOutcome = iota
	MailTransient
	MailPermanent
)

// WithMailAttempt holds the organization gate through bounded SMTP execution.
// An attempt reservation commits before I/O, so a crash cannot erase its count.
func (s *Store) WithMailAttempt(ctx context.Context, m Mail, send func(context.Context, Mail) MailOutcome) error {
	conn, e := s.db.Conn(ctx)
	if e != nil {
		return e
	}
	defer conn.Close()
	lock := s.schema + ":org:" + m.OrganizationID
	if _, e = conn.ExecContext(ctx, "SELECT pg_advisory_lock(hashtextextended($1,0))", lock); e != nil {
		return e
	}
	defer conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock(hashtextextended($1,0))", lock)
	tx, e := conn.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "SET LOCAL search_path TO "+s.schema); e != nil {
		return e
	}
	var data []byte
	var attempts int
	var expiry time.Time
	e = tx.QueryRowContext(ctx, "SELECT t.value,i.attempts,i.expires_at FROM invitations i JOIN "+s.temp+".mail_tokens t ON t.invitation_id=i.id WHERE i.id=$1 AND i.org_id=$2 AND i.claim=$3 AND i.status='pending' AND i.expires_at>clock_timestamp() AND i.next_attempt<=clock_timestamp() AND i.attempts<3 FOR UPDATE OF i", m.ID, m.OrganizationID, m.Claim).Scan(&data, &attempts, &expiry)
	if e != nil {
		return noRows(e)
	}
	var count int
	var next time.Time
	if e = tx.QueryRowContext(ctx, "SELECT count(*),COALESCE(min(attempted_at)+interval '1 hour',clock_timestamp()) FROM mail_attempts WHERE org_id=$1 AND attempted_at>clock_timestamp()-interval '1 hour'", m.OrganizationID).Scan(&count, &next); e != nil {
		return e
	}
	if count >= 100 {
		if _, e = tx.ExecContext(ctx, "UPDATE invitations SET next_attempt=$2,claim=NULL WHERE id=$1", m.ID, next); e != nil {
			return e
		}
		return tx.Commit()
	}
	p, e := s.keys.Decrypt(data, "invitation/"+m.ID)
	if e != nil {
		return e
	}
	m.Token = string(p)
	m.Attempts = attempts + 1
	if _, e = tx.ExecContext(ctx, "INSERT INTO mail_attempts(org_id) VALUES($1)", m.OrganizationID); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE invitations SET attempts=attempts+1 WHERE id=$1", m.ID); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	deadline := time.Now().Add(time.Minute)
	if expiry.Before(deadline) {
		deadline = expiry
	}
	sendCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if sendCtx.Err() != nil {
		return sendCtx.Err()
	}
	outcome := send(sendCtx, m)
	// Reuse the locked connection; a different connection would wait on this gate.
	tx, e = conn.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "SET LOCAL search_path TO "+s.schema); e != nil {
		return e
	}
	if e = s.finishMail(ctx, tx, m, outcome); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) finishMail(ctx context.Context, tx *sql.Tx, m Mail, outcome MailOutcome) error {
	status := "pending"
	delivery := "retry_pending"
	delay := time.Minute
	if m.Attempts >= 2 {
		delay = 5 * time.Minute
	}
	if outcome == MailSuccess {
		delivery = "sent"
		delay = 8 * 24 * time.Hour
	}
	if outcome == MailPermanent || outcome == MailTransient && m.Attempts >= 3 {
		status = "failed"
		delivery = "failed"
	}
	res, e := tx.ExecContext(ctx, "UPDATE invitations SET status=$3,delivery_state=$5,claim=NULL,claim_until=NULL,next_attempt=clock_timestamp()+$4*interval '1 second' WHERE id=$1 AND claim=$2 AND status='pending'", m.ID, m.Claim, status, delay.Seconds(), delivery)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrConflict
	}
	if status == "failed" || outcome == MailSuccess {
		_, e = tx.ExecContext(ctx, "DELETE FROM "+s.temp+".mail_tokens WHERE invitation_id=$1", m.ID)
	}
	return e
}
