// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const notificationPreferencePrefix = "notification-situations-v1:"
const notificationDeliveryPrefix = "notification-delivery-v1:"

// Metadata envelopes share the original SQLite transaction and managed image.
// They deliberately do not reinterpret legacy delivery CHECK values. No expiry
// or presentation result can release a retained reservation.
type situationPreferenceEnvelope struct {
	Version     int                                     `json:"version"`
	Revision    uint64                                  `json:"revision,string"`
	Values      domain.SituationNotificationPreferences `json:"values"`
	Checkpoints map[domain.NotificationKind]uint64      `json:"checkpoints"`
}

func (t *Tx) notificationMetadata(key string, value any) (bool, error) {
	var raw string
	err := t.tx.QueryRowContext(t.ctx, "SELECT value FROM metadata WHERE key=?", key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, storageError(err)
	}
	if len(raw) > 8192 || domain.Decode([]byte(raw), value) != nil {
		return false, notificationConflict()
	}
	return true, nil
}
func (t *Tx) putNotificationMetadata(key string, value any) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return storageError(err)
	}
	if len(raw) > 8192 {
		return notificationConflict()
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, string(raw))
	return storageError(err)
}
func (t *Tx) situationPreferences(client string, revision uint64) (*situationPreferenceEnvelope, error) {
	var v situationPreferenceEnvelope
	exists, err := t.notificationMetadata(notificationPreferencePrefix+client, &v)
	if err != nil || !exists {
		return nil, err
	}
	if v.Version != 1 || v.Revision == 0 || v.Revision >= 1<<63-1 || v.Revision != revision || len(v.Checkpoints) != len(domain.OperationalNotificationKinds) {
		return nil, notificationConflict()
	}
	for _, kind := range domain.OperationalNotificationKinds {
		if checkpoint, ok := v.Checkpoints[kind]; !ok || checkpoint >= 1<<63-1 {
			return nil, notificationConflict()
		}
	}
	return &v, nil
}
func (t *Tx) notificationSequence() (uint64, error) {
	var sequence uint64
	err := t.tx.QueryRowContext(t.ctx, "SELECT COALESCE((SELECT seq FROM sqlite_sequence WHERE name='events'),0)").Scan(&sequence)
	return sequence, storageError(err)
}

// Opt-in initialization preserves the original public revision and legacy
// receipts. Capture checkpoints before returning an acknowledged generation.
func (s *Store) EnsureSituationNotificationPreferences(ctx context.Context) error {
	s.gate.RLock()
	defer s.gate.RUnlock()
	if s.deletionFault {
		return domain.SessionDeletionPending()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError(err)
	}
	defer tx.Rollback()

	t := &Tx{tx: tx, ctx: ctx}
	if _, err := t.InitializeSituationNotificationPreferences(); err != nil {
		return err
	}
	return storageError(tx.Commit())
}

// Initialization and partial configuration can share one original mutation
// transaction. Receipt replay returns before this code and cannot reapply it.
func (t *Tx) InitializeSituationNotificationPreferences() (domain.NotificationPreferences, error) {
	current, err := t.NotificationPreferences()
	if err != nil {
		return current, err
	}
	if current.Situations != nil {
		return current, nil
	}
	client, err := t.notificationClient()
	if err != nil {
		return current, err
	}
	sequence, err := t.notificationSequence()
	if err != nil {
		return current, err
	}
	v := situationPreferenceEnvelope{Version: 1, Revision: current.Revision, Values: domain.DefaultSituationNotifications(current), Checkpoints: map[domain.NotificationKind]uint64{}}
	for _, kind := range domain.OperationalNotificationKinds {
		v.Checkpoints[kind] = sequence
	}
	if err := t.putNotificationMetadata(notificationPreferencePrefix+client, v); err != nil {
		return current, err
	}
	current.Situations = &v.Values
	slog.InfoContext(t.ctx, "notification_preference_generation_initialized", "revision", current.Revision, "checkpoint", sequence)
	return current, nil
}

func (t *Tx) saveSituationNotifications(client string, current, next domain.NotificationPreferences) error {
	previous, err := t.situationPreferences(client, current.Revision)
	if err != nil {
		return err
	}
	if next.Situations == nil {
		if previous != nil {
			return domain.Fail(domain.Unsupported, "This client cannot replace situation-specific preferences.", "Use a client supporting individual notification situations.")
		}
		return nil
	}
	sequence, err := t.notificationSequence()
	if err != nil {
		return err
	}
	value := situationPreferenceEnvelope{Version: 1, Revision: next.Revision, Values: *next.Situations, Checkpoints: map[domain.NotificationKind]uint64{}}
	for _, kind := range domain.OperationalNotificationKinds {
		value.Checkpoints[kind] = sequence
		if previous != nil && (previous.Values.Enabled(kind) || !next.Situations.Enabled(kind)) {
			value.Checkpoints[kind] = previous.Checkpoints[kind]
		}
	}
	return t.putNotificationMetadata(notificationPreferencePrefix+client, value)
}
func (t *Tx) exactNotificationCandidate(v domain.NotificationCandidate, prefs domain.NotificationPreferences) (domain.NotificationCandidate, bool, error) {
	if prefs.Situations == nil {
		return v, true, nil
	}
	if v.Kind == domain.RequestNotification {
		record, err := t.Get(domain.InboxKind, v.InboxID)
		if err != nil {
			return v, false, err
		}
		inbox, err := Decode[domain.InboxEntry](record)
		if err != nil {
			return v, false, err
		}
		source, err := t.Get(domain.InteractionKind, inbox.SourceID)
		if err != nil {
			return v, false, err
		}
		interaction, err := Decode[domain.ExecutionInteraction](source)
		if err != nil {
			return v, false, err
		}
		switch interaction.Type {
		case domain.UserQuestionInteraction:
			v.Kind = domain.QuestionNotification
		case domain.NativeApprovalInteraction:
			v.Kind = domain.ApprovalNotification
		default:
			return v, false, notificationConflict()
		}
	}
	if v.Kind == domain.SubscriptionRecoveryNotification {
		return v, true, nil
	}
	return v, prefs.Situations.Enabled(v.Kind), nil
}
func notificationDeliveryKey(client string, inbox domain.ID) string {
	return notificationDeliveryPrefix + string(inbox) + ":" + client
}
func (t *Tx) metadataNotificationDelivery(client string, inbox domain.ID) (domain.NotificationDelivery, bool, error) {
	var value struct {
		Version  int                         `json:"version"`
		Delivery domain.NotificationDelivery `json:"delivery"`
	}
	exists, err := t.notificationMetadata(notificationDeliveryKey(client, inbox), &value)
	if err != nil || !exists {
		return value.Delivery, exists, err
	}
	d := value.Delivery
	if value.Version != 1 || d.InboxID != inbox || d.ClaimID.Validate() != nil || !d.Kind.Valid() || d.State != domain.NotificationClaimed && !d.State.Reportable() {
		return d, false, notificationConflict()
	}
	if _, err := t.Get(domain.InboxKind, inbox); err != nil {
		return d, false, err
	}
	return d, true, nil
}
func (t *Tx) saveMetadataNotificationDelivery(client string, value domain.NotificationDelivery) error {
	return t.putNotificationMetadata(notificationDeliveryKey(client, value.InboxID), struct {
		Version  int                         `json:"version"`
		Delivery domain.NotificationDelivery `json:"delivery"`
	}{1, value})
}
func (t *Tx) deleteNotificationMetadata(inbox domain.ID) error {
	// UUID-prefixed ownership permits exact bounded source cleanup; immutable
	// tombstones/receipt digests keep deleted sources from publishing again.
	_, err := t.tx.ExecContext(t.ctx, "DELETE FROM metadata WHERE key LIKE ?", notificationDeliveryPrefix+string(inbox)+":%")
	return storageError(err)
}
func notificationSituationFilter(p domain.NotificationPreferences) (string, []any) {
	if p.Situations == nil {
		return "", nil
	}
	v := p.Situations
	return ` AND (json_extract(i.body,'$.source')='subscription-recovery' OR (json_extract(i.body,'$.source')='interaction' AND ((json_extract(x.body,'$.type')='user-question' AND ?) OR (json_extract(x.body,'$.type')='native-approval' AND ?))) OR (json_extract(i.body,'$.source')='execution-terminal' AND ((json_extract(i.body,'$.terminal.outcome')='succeeded' AND ?) OR (json_extract(i.body,'$.terminal.outcome')='failed' AND ?) OR (json_extract(i.body,'$.terminal.outcome')='stopped' AND ?))))`, []any{v.Questions, v.Approvals, v.Succeeded, v.Failed, v.Stopped}
}
func notificationEqual(a, b domain.NotificationPreferences) bool {
	if a.Revision != b.Revision || a.Interactions != b.Interactions || a.Terminals != b.Terminals {
		return false
	}
	if a.Situations == nil || b.Situations == nil {
		return a.Situations == nil && b.Situations == nil
	}
	return *a.Situations == *b.Situations
}
