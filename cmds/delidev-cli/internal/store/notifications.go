package store

import (
	"database/sql"
	"errors"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func (t *Tx) notificationClient() (string, error) {
	actor, ok := domain.PrincipalFrom(t.ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return "", domain.Fail(domain.PermissionDenied, "Only an owner or paired client can manage its notifications.", "Use the original client; Worker identity grants no presentation authority.")
	}
	if err := t.Authorize(); err != nil {
		return "", err
	}
	if actor.Type == domain.OwnerDevice {
		return "owner", nil
	}
	if err := actor.DeviceID.Validate(); err != nil {
		return "", err
	}
	return string(actor.DeviceID), nil
}

func (t *Tx) NotificationPreferences() (domain.NotificationPreferences, error) {
	v := domain.NotificationPreferences{Revision: 1, Interactions: true}
	client, err := t.notificationClient()
	if err != nil {
		return v, err
	}
	err = t.tx.QueryRowContext(t.ctx, `SELECT revision,interactions,terminals FROM notification_preferences WHERE client_id=?`, client).Scan(&v.Revision, &v.Interactions, &v.Terminals)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	return v, storageError(err)
}

func (t *Tx) SetNotificationPreferences(value domain.NotificationPreferences) (domain.NotificationPreferences, error) {
	if err := t.writeAllowed(); err != nil {
		return value, err
	}
	current, err := t.NotificationPreferences()
	if err != nil {
		return value, err
	}
	if value.Revision == 0 || value.Revision != current.Revision || value.Revision >= 1<<63-1 {
		return current, notificationConflict()
	}
	if value == current {
		return current, nil
	}
	client, err := t.notificationClient()
	if err != nil {
		return value, err
	}
	value.Revision++
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO notification_preferences(client_id,revision,interactions,terminals) VALUES(?,?,?,?) ON CONFLICT(client_id) DO UPDATE SET revision=excluded.revision,interactions=excluded.interactions,terminals=excluded.terminals`, client, value.Revision, value.Interactions, value.Terminals)
	return value, storageError(err)
}

// Eligibility is rechecked in the claim transaction. Read state cannot resolve
// interactions; read terminal notices may be omitted without losing history.
// Old or paused execution requests never gain authority from a retained inbox.
const notificationCandidatesSQL = `SELECT i.id,i.session_id,
CASE WHEN json_extract(i.body,'$.source')='subscription-recovery' THEN 'subscription-recovery' WHEN json_extract(i.body,'$.source')='interaction' THEN 'request' ELSE json_extract(i.body,'$.terminal.outcome') END,COALESCE(json_extract(i.body,'$.recovery.account_id'),'')
FROM entities i LEFT JOIN entities a ON a.id=json_extract(i.body,'$.recovery.account_id') AND a.kind='account' LEFT JOIN entities s ON s.id=i.session_id AND s.kind='session'
LEFT JOIN entities x ON x.id=json_extract(i.body,'$.source_id') AND x.kind='interaction'
WHERE i.kind='inbox' AND (json_extract(s.body,'$.archive')='active' OR json_extract(i.body,'$.source')='subscription-recovery')
AND NOT EXISTS(SELECT 1 FROM notification_deliveries d WHERE d.client_id=? AND d.inbox_id=i.id)
AND ((? AND json_extract(i.body,'$.source')='interaction'
AND x.session_id=i.session_id AND x.project_id=i.project_id
AND json_extract(x.body,'$.closure')='open'
AND json_extract(x.body,'$.response') IS NULL AND json_extract(x.body,'$.approval_response') IS NULL
AND json_extract(x.body,'$.execution_id')=json_extract(s.body,'$.active_execution_id')
AND json_extract(s.body,'$.dispatch')='claimed' AND json_extract(s.body,'$.recovery')='none')
OR (json_extract(i.body,'$.source')='subscription-recovery' AND i.session_id='' AND i.project_id='' AND json_extract(i.body,'$.read_state')='unread' AND json_extract(a.body,'$.recovery_notifications')=1 AND json_extract(a.body,'$.health')='ready' AND COALESCE(json_extract(a.body,'$.subscription.recovery_required'),0)=0 AND COALESCE(json_extract(a.body,'$.removal_required'),0)=0 AND json_extract(a.body,'$.connection.id')=json_extract(i.body,'$.recovery.connection_id'))
OR (? AND json_extract(i.body,'$.source')='execution-terminal' AND json_extract(i.body,'$.read_state')='unread'
AND json_extract(i.body,'$.terminal.outcome') IN ('succeeded','failed','stopped')))`

func (t *Tx) NotificationCandidates(limit int) ([]domain.NotificationCandidate, bool, error) {
	client, err := t.notificationClient()
	if err != nil {
		return nil, false, err
	}
	if limit < 1 || limit > 50 {
		return nil, false, domain.Fail(domain.InvalidArgument, "Invalid notification batch size.", "Request between 1 and 50 candidates.")
	}
	preferences, err := t.NotificationPreferences()
	if err != nil {
		return nil, false, err
	}
	rows, err := t.tx.QueryContext(t.ctx, notificationCandidatesSQL+` ORDER BY i.id LIMIT ?`, client, preferences.Interactions, preferences.Terminals, limit+1)
	if err != nil {
		return nil, false, storageError(err)
	}
	defer rows.Close()
	values := make([]domain.NotificationCandidate, 0, limit)
	for rows.Next() {
		var value domain.NotificationCandidate
		if err := rows.Scan(&value.InboxID, &value.SessionID, &value.Kind, &value.AccountID); err != nil {
			return nil, false, storageError(err)
		}
		if value.InboxID.Validate() != nil || (value.Kind == domain.SubscriptionRecoveryNotification && (value.AccountID.Validate() != nil || value.SessionID != "") || value.Kind != domain.SubscriptionRecoveryNotification && (value.SessionID.Validate() != nil || value.AccountID != "")) || !value.Kind.Valid() {
			return nil, false, notificationConflict()
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, storageError(err)
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	return values, more, nil
}

func (t *Tx) NotificationDelivery(inbox domain.ID) (domain.NotificationDelivery, error) {
	var value domain.NotificationDelivery
	client, err := t.notificationClient()
	if err != nil {
		return value, err
	}
	if err := inbox.Validate(); err != nil {
		return value, err
	}
	err = t.tx.QueryRowContext(t.ctx, `SELECT d.inbox_id,i.session_id,d.kind,d.claim_id,d.state,COALESCE(json_extract(i.body,'$.recovery.account_id'),'') FROM notification_deliveries d JOIN entities i ON i.id=d.inbox_id AND i.kind='inbox' WHERE d.client_id=? AND d.inbox_id=?`, client, inbox).Scan(&value.InboxID, &value.SessionID, &value.Kind, &value.ClaimID, &value.State, &value.AccountID)
	if errors.Is(err, sql.ErrNoRows) {
		return value, domain.Fail(domain.NotFound, "No notification claim is retained for this client and inbox entry.", "Refresh this client's notification state without adopting another client's claim.")
	}
	return value, storageError(err)
}

// This durable reservation precedes OS delivery. It has no expiry and cannot be
// reclaimed after reconnect, lost acknowledgment or native delivery uncertainty.
// A replay of the outer request is observation only, never another display grant.
func (t *Tx) ClaimNotification(inbox, claim domain.ID) (domain.NotificationDelivery, bool, error) {
	var value domain.NotificationDelivery
	if err := t.writeAllowed(); err != nil {
		return value, false, err
	}
	client, err := t.notificationClient()
	if err != nil {
		return value, false, err
	}
	if inbox.Validate() != nil || claim.Validate() != nil {
		return value, false, domain.Fail(domain.InvalidArgument, "Invalid notification claim identity.", "Use the original inbox entry and a fresh request identity.")
	}
	previous, err := t.NotificationDelivery(inbox)
	if err == nil {
		return previous, false, nil
	}
	if domain.SafeError(err).Code != domain.NotFound {
		return value, false, err
	}
	preferences, err := t.NotificationPreferences()
	if err != nil {
		return value, false, err
	}
	err = t.tx.QueryRowContext(t.ctx, notificationCandidatesSQL+` AND i.id=?`, client, preferences.Interactions, preferences.Terminals, inbox).Scan(&value.InboxID, &value.SessionID, &value.Kind, &value.AccountID)
	if errors.Is(err, sql.ErrNoRows) {
		return value, false, notificationConflict()
	}
	if err != nil {
		return value, false, storageError(err)
	}
	value.ClaimID, value.State = claim, domain.NotificationClaimed
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO notification_deliveries(client_id,inbox_id,claim_id,kind,state) VALUES(?,?,?,?,?)`, client, inbox, claim, value.Kind, value.State)
	return value, err == nil, storageError(err)
}

func (t *Tx) ReportNotification(inbox, claim domain.ID, state domain.NotificationState) (domain.NotificationDelivery, error) {
	var value domain.NotificationDelivery
	if err := t.writeAllowed(); err != nil {
		return value, err
	}
	if !state.Reportable() || claim.Validate() != nil {
		return value, domain.Fail(domain.InvalidArgument, "Invalid notification presentation report.", "Retain the original claim and a known native outcome without content or diagnostics.")
	}
	value, err := t.NotificationDelivery(inbox)
	if err != nil {
		return value, err
	}
	if value.ClaimID != claim || (value.State != domain.NotificationClaimed && value.State != state) {
		return value, notificationConflict()
	}
	client, err := t.notificationClient()
	if err != nil {
		return value, err
	}
	_, err = t.tx.ExecContext(t.ctx, `UPDATE notification_deliveries SET state=? WHERE client_id=? AND inbox_id=? AND claim_id=?`, state, client, inbox, claim)
	value.State = state
	return value, storageError(err)
}

func notificationConflict() error {
	return domain.Fail(domain.Conflict, "The notification source, preference or original claim has changed.", "Refresh the current inbox and this client's settings. Notification state cannot answer, resume or mark a request read.")
}
