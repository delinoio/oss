// SPDX-License-Identifier: Apache-2.0
package store

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"log/slog"
)

func (t *Tx) CreateOperationalInbox(source domain.ID, value domain.InboxOperational) (Record, error) {
	if err := t.writeAllowed(); err != nil {
		return Record{}, err
	}
	existing, err := t.InboxBySource(domain.OperationalInbox, source)
	if err == nil {
		return existing, nil
	}
	if domain.SafeError(err).Code != domain.NotFound {
		return Record{}, err
	}
	sequence, err := t.notificationSequence()
	if err != nil {
		return Record{}, err
	}
	value.Sequence = sequence + 1
	entry := domain.InboxEntry{Source: domain.OperationalInbox, SourceID: source, ReadState: domain.InboxUnread, Operational: &value}
	if entry.Validate() != nil {
		return Record{}, notificationConflict()
	}
	if eligible, err := t.operationalSourceEligible(value); err != nil || !eligible {
		if err != nil {
			return Record{}, err
		}
		return Record{}, notificationConflict()
	}
	record, err := t.Put(domain.InboxKind, domain.NewID(), 0, "", "", entry)
	if err == nil {
		slog.InfoContext(t.ctx, "notification_operation_recorded", "kind", value.Kind, "source_id", source, "source_sequence", value.Sequence)
	}
	return record, err
}
func (t *Tx) operationalSourceEligible(v domain.InboxOperational) (bool, error) {
	switch v.Kind {
	case domain.WorkerUnavailableNotification, domain.WorkerAvailableNotification:
		record, err := t.Get(domain.MachineKind, v.MachineID)
		if domain.SafeError(err).Code == domain.NotFound {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		machine, err := Decode[domain.Machine](record)
		if err != nil {
			return false, err
		}
		if machine.Disabled {
			return false, nil
		}
		instance, _, err := t.WorkerInstance(v.MachineID)
		if err != nil || instance != v.InstanceID {
			return false, err
		}
		var authorized bool
		err = t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM entities WHERE kind='device' AND id=? AND json_extract(body,'$.type')='worker' AND json_extract(body,'$.machine_id')=? AND COALESCE(json_extract(body,'$.revoked'),0)=0)`, v.DeviceID, v.MachineID).Scan(&authorized)
		return authorized, storageError(err)
	case domain.QuotaExhaustedNotification:
		record, err := t.Get(domain.AccountKind, v.AccountID)
		if domain.SafeError(err).Code == domain.NotFound {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		account, err := Decode[domain.Account](record)
		if err != nil {
			return false, err
		}
		return account.SubscriptionService == domain.SubscriptionChatGPT && account.Connection != nil && account.Connection.ID == v.ConnectionID && account.Health == domain.AccountReady && account.Subscription != nil && !account.Subscription.RecoveryRequired, nil
	default:
		_, err := t.Get(domain.OccurrenceKind, v.OccurrenceID)
		if domain.SafeError(err).Code == domain.NotFound {
			return false, nil
		}
		return err == nil, err
	}
}
func (t *Tx) operationalNotificationCandidate(client string, p domain.NotificationPreferences, r Record) (domain.NotificationCandidate, bool, error) {
	var candidate domain.NotificationCandidate
	entry, err := Decode[domain.InboxEntry](r)
	if err != nil {
		return candidate, false, err
	}
	if entry.Validate() != nil || entry.Source != domain.OperationalInbox || r.SessionID != "" || r.ProjectID != "" {
		return candidate, false, notificationConflict()
	}
	if entry.ReadState != domain.InboxUnread || p.Situations == nil {
		return candidate, false, nil
	}
	v := *entry.Operational
	envelope, err := t.situationPreferences(client, p.Revision)
	if err != nil {
		return candidate, false, err
	}
	if envelope == nil || !envelope.Values.Enabled(v.Kind) || v.Sequence <= envelope.Checkpoints[v.Kind] {
		return candidate, false, nil
	}
	_, exists, err := t.metadataNotificationDelivery(client, r.ID)
	if err != nil || exists {
		return candidate, false, err
	}
	eligible, err := t.operationalSourceEligible(v)
	if err != nil || !eligible {
		return candidate, false, err
	}
	return domain.NotificationCandidate{InboxID: r.ID, Kind: v.Kind, MachineID: v.MachineID, AccountID: v.AccountID, OccurrenceID: v.OccurrenceID}, true, nil
}
func (t *Tx) operationalNotificationCandidates(client string, p domain.NotificationPreferences, limit int) ([]domain.NotificationCandidate, error) {
	if p.Situations == nil {
		return nil, nil
	}
	// Filtering checkpoint/kind in SQL bounds scans even when much history is
	// retained. Source eligibility is freshly checked against the exact target.
	envelope, err := t.situationPreferences(client, p.Revision)
	if err != nil {
		return nil, err
	}
	values := []domain.NotificationCandidate{}
	for _, kind := range domain.OperationalNotificationKinds {
		if !envelope.Values.Enabled(kind) {
			continue
		}
		var after domain.ID
		for {
			records, _, err := t.sessionPage(MaxPage, `SELECT `+recordColumns+` FROM entities WHERE kind='inbox' AND id>? AND json_extract(body,'$.source')='operational' AND json_extract(body,'$.operational.kind')=? AND CAST(json_extract(body,'$.operational.sequence') AS INTEGER)>? AND json_extract(body,'$.read_state')='unread' AND NOT EXISTS(SELECT 1 FROM metadata WHERE key='notification-delivery-v1:'||entities.id||':'||?) ORDER BY id LIMIT ?`, after, kind, envelope.Checkpoints[kind], client, MaxPage)
			if err != nil {
				return nil, err
			}
			for _, r := range records {
				v, eligible, err := t.operationalNotificationCandidate(client, p, r)
				if err != nil {
					return nil, err
				}
				if eligible {
					values = append(values, v)
					if len(values) >= limit {
						return values, nil
					}
				}
			}
			if len(records) < MaxPage {
				break
			}
			after = records[len(records)-1].ID
		}
	}
	return values, nil
}
func (t *Tx) deleteOperationalSourceInbox(kind domain.Kind, id domain.ID) error {
	field := ""
	switch kind {
	case domain.MachineKind:
		if _, err := t.tx.ExecContext(t.ctx, "DELETE FROM metadata WHERE key=?", "notification-worker-baseline-v1:"+string(id)); err != nil {
			return storageError(err)
		}
		field = "machine_id"
	case domain.AccountKind:
		field = "account_id"
	case domain.OccurrenceKind:
		field = "occurrence_id"
	case domain.InboxKind:
		return t.deleteNotificationMetadata(id)
	default:
		return nil
	}
	for {
		records, _, err := t.sessionPage(MaxPage, "SELECT "+recordColumns+" FROM entities WHERE kind='inbox' AND json_extract(body,'$.source')='operational' AND json_extract(body,'$.operational."+field+"')=? ORDER BY id LIMIT ?", id, MaxPage)
		if err != nil {
			return err
		}
		for _, r := range records {
			if err := t.Delete(domain.InboxKind, r.ID, r.Revision); err != nil {
				return err
			}
		}
		if len(records) < MaxPage {
			return nil
		}
	}
}

// The original occurrence is the durable deduplication identity. Post-start
// terminal failure and intentional skip reasons do not publish start failure.
func (t *Tx) publishScheduleNotification(r Record, v domain.ScheduleOccurrence) error {
	kind := domain.NotificationKind("")
	if v.State == domain.OccurrenceSkipped {
		switch v.Reason {
		case domain.ServerOfflineOccurrence:
			kind = domain.ScheduleServerOfflineNotification
		case domain.WorkerOfflineOccurrence:
			kind = domain.ScheduleWorkerOfflineNotification
		}
	} else if v.State == domain.OccurrenceFailed {
		if v.SessionID == "" {
			kind = domain.ScheduleStartFailedNotification
		} else {
			session, err := t.Get(domain.SessionKind, v.SessionID)
			if err != nil {
				return err
			}
			value, err := Decode[domain.Session](session)
			if err != nil {
				return err
			}
			if value.InitialExecution == nil {
				kind = domain.ScheduleStartFailedNotification
			}
		}
	}
	if kind == "" {
		return nil
	}
	_, err := t.CreateOperationalInbox(r.ID, domain.InboxOperational{Kind: kind, OccurrenceID: r.ID, ObservedAt: t.now})
	return err
}

// A fresh ordered observation is required. Unknown/sparse inputs establish no
// baseline and credit-reset observations intentionally never call this method.
func (t *Tx) ObserveQuotaNotification(before, after domain.Account, account, source domain.ID, observed domain.SubscriptionQuotaObservation) error {
	if before.Connection == nil || after.Connection == nil || before.Connection.ID != after.Connection.ID {
		return nil
	}
	if before.Subscription == nil || before.Subscription.QuotaObservedAt == nil || !observed.ObservedAt.After(*before.Subscription.QuotaObservedAt) {
		return nil
	}
	explicit := observed.SpendControlReached != nil && *observed.SpendControlReached
	for _, window := range observed.Windows {
		explicit = explicit || window.Remaining != nil && *window.Remaining == 0
	}
	if !domain.ConfirmedSubscriptionQuotaUsable(before, t.now) || !explicit || !after.ConfirmedExhausted {
		return nil
	}
	_, err := t.CreateOperationalInbox(source, domain.InboxOperational{Kind: domain.QuotaExhaustedNotification, AccountID: account, ConnectionID: after.Connection.ID, ObservedAt: observed.ObservedAt})
	return err
}

func (t *Tx) OperationalInboxTarget(v domain.InboxOperational) (Record, error) {
	if eligible, err := t.operationalSourceEligible(v); err != nil || !eligible {
		return Record{}, err
	}
	switch v.Kind {
	case domain.WorkerUnavailableNotification, domain.WorkerAvailableNotification:
		return t.Get(domain.MachineKind, v.MachineID)
	case domain.QuotaExhaustedNotification:
		return t.Get(domain.AccountKind, v.AccountID)
	default:
		return t.Get(domain.OccurrenceKind, v.OccurrenceID)
	}
}
