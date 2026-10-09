// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
	"time"
)

func operationalFixture(t *testing.T, s *Store) domain.ID {
	t.Helper()
	r, v := scheduleFixture(t, s)
	o, _, _ := appendWaiting(t, s, r, v)
	return o.ID
}
func publishOperational(t *testing.T, s *Store, occ domain.ID) domain.ID {
	t.Helper()
	var id domain.ID
	_, err := s.Mutate(notificationOwner(), domain.NewID(), "fixture.notification.operation", nil, func(tx *Tx) (any, error) {
		r, err := tx.CreateOperationalInbox(occ, domain.InboxOperational{Kind: domain.ScheduleStartFailedNotification, OccurrenceID: occ, ObservedAt: tx.now})
		id = r.ID
		return r, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func notificationCandidates(t *testing.T, s *Store) []domain.NotificationCandidate {
	t.Helper()
	var rows []domain.NotificationCandidate
	err := s.Read(notificationOwner(), func(tx *Tx) error { var err error; rows, _, err = tx.NotificationCandidates(50); return err })
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
func TestSituationNotificationFutureCheckpointClaimDisableReenableAndCleanup(t *testing.T) {
	s, _ := openTest(t)
	ctx := notificationOwner()
	old := publishOperational(t, s, operationalFixture(t, s))
	if err := s.EnsureSituationNotificationPreferences(ctx); err != nil {
		t.Fatal(err)
	}
	if rows := notificationCandidates(t, s); len(rows) != 0 {
		t.Fatal("initialization replayed history", rows)
	}
	occurrence := operationalFixture(t, s)
	fresh := publishOperational(t, s, occurrence)
	if rows := notificationCandidates(t, s); len(rows) != 1 || rows[0].InboxID != fresh {
		t.Fatal(rows)
	}
	claim := domain.NewID()
	_, err := s.Mutate(ctx, claim, "fixture.notification.claim", nil, func(tx *Tx) (any, error) {
		v, may, err := tx.ClaimNotification(fresh, claim)
		if !may {
			t.Fatal("no fresh grant")
		}
		return v, err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.notification.competing", nil, func(tx *Tx) (any, error) {
		v, may, err := tx.ClaimNotification(fresh, domain.NewID())
		if may || v.ClaimID != claim {
			t.Fatal("duplicate grant", v)
		}
		return v, err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		if !enabled {
			publishOperational(t, s, operationalFixture(t, s))
		}
		_, err = s.Mutate(ctx, domain.NewID(), "fixture.notification.toggle", enabled, func(tx *Tx) (any, error) {
			p, err := tx.NotificationPreferences()
			if err != nil {
				return nil, err
			}
			p.Situations.ScheduleStartFailed = enabled
			return tx.SetNotificationPreferences(p)
		})
		if err != nil {
			t.Fatal(err)
		}
		if !enabled {
			publishOperational(t, s, operationalFixture(t, s))
		}
	}
	if rows := notificationCandidates(t, s); len(rows) != 0 {
		t.Fatal("reenabling replayed history", rows)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.notification.delete.source", nil, func(tx *Tx) (any, error) {
		r, err := tx.Get(domain.OccurrenceKind, occurrence)
		if err != nil {
			return nil, err
		}
		return nil, tx.Delete(r.Kind, r.ID, r.Revision)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		_, _, err := tx.metadataNotificationDelivery("owner", fresh)
		var count int
		if query := tx.tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM metadata WHERE key=?", notificationDeliveryPrefix+string(fresh)+":owner").Scan(&count); query != nil {
			return query
		}
		if count != 0 {
			t.Fatal("orphan claim")
		}
		_, read := tx.Get(domain.InboxKind, old)
		if read != nil {
			t.Fatal("unrelated retained history removed")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
func TestSituationNotificationRestorePreservesCurrentGenerationAndTypedClaim(t *testing.T) {
	s, root, ctx, in, _ := restoreFixture(t)
	if err := s.EnsureSituationNotificationPreferences(ctx); err != nil {
		t.Fatal(err)
	}
	occurrence := operationalFixture(t, s)
	id := publishOperational(t, s, occurrence)
	claim := domain.NewID()
	_, err := s.Mutate(ctx, claim, "fixture.notification.claim", nil, func(tx *Tx) (any, error) { v, _, err := tx.ClaimNotification(id, claim); return v, err })
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.notification.preference", nil, func(tx *Tx) (any, error) {
		p, err := tx.NotificationPreferences()
		if err != nil {
			return nil, err
		}
		p.Situations.ServerLost = false
		return tx.SetNotificationPreferences(p)
	})
	if err != nil {
		t.Fatal(err)
	}
	in.ExpectedRevision, err = s.RestoreRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.RestoreBackup(ctx, domain.NewID(), in); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err = s.Read(ctx, func(tx *Tx) error {
		p, err := tx.NotificationPreferences()
		if err != nil {
			return err
		}
		if p.Revision != 2 || p.Situations == nil || p.Situations.ServerLost {
			t.Fatal("restored stale preference generation", p)
		}
		var orphan int
		err = tx.tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM metadata WHERE key LIKE 'notification-delivery-v1:%' AND substr(key,length('notification-delivery-v1:')+1,36) NOT IN (SELECT id FROM entities WHERE kind='inbox')").Scan(&orphan)
		if orphan != 0 {
			t.Fatal("orphan restored claims")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if rows := notificationCandidates(t, s); len(rows) != 0 {
		t.Fatal("restore replayed operation", rows)
	}
}
func TestSituationNotificationWorkerBaselineEdgeRevocationAndRestart(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	machine, device, instance, epoch := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	now := time.Now().UTC().Truncate(time.Millisecond)
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.worker", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.MachineKind, machine, 0, "", "", domain.Machine{Name: "worker", OS: "linux", Architecture: "amd64", Version: "fixture"}); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.DeviceKind, device, 0, "", "", domain.Device{Name: "worker", Type: domain.WorkerDevice, MachineID: machine, PairedAt: now}); err != nil {
			return nil, err
		}
		return nil, tx.SetWorkerInstance(machine, instance, now.Add(-46*time.Second))
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ObserveWorkerNotifications(ctx, epoch, ""); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		var n int
		err := s.Read(ctx, func(tx *Tx) error {
			return tx.tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM entities WHERE kind='inbox' AND json_extract(body,'$.source')='operational'").Scan(&n)
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count() != 0 {
		t.Fatal("offline startup fabricated edge")
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.heartbeat", nil, func(tx *Tx) (any, error) { return nil, tx.SetWorkerInstance(machine, instance, time.Now().UTC()) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ObserveWorkerNotifications(ctx, epoch, ""); err != nil {
		t.Fatal(err)
	}
	if count() != 1 {
		t.Fatal("missing original recovery")
	}
	if _, err = s.ObserveWorkerNotifications(ctx, domain.NewID(), ""); err != nil {
		t.Fatal(err)
	}
	if count() != 1 {
		t.Fatal("restart replayed edge")
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.revoke", nil, func(tx *Tx) (any, error) {
		r, err := tx.Get(domain.DeviceKind, device)
		if err != nil {
			return nil, err
		}
		d, err := Decode[domain.Device](r)
		if err != nil {
			return nil, err
		}
		d.Revoked = true
		return tx.Put(r.Kind, r.ID, r.Revision, "", "", d)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ObserveWorkerNotifications(ctx, epoch, ""); err != nil {
		t.Fatal(err)
	}
	if count() != 1 {
		t.Fatal("revocation fabricated edge")
	}
}
