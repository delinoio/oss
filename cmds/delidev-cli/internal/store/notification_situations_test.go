// SPDX-License-Identifier: Apache-2.0
package store

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func TestSituationNotificationLegacyConversionAndDestructiveWriteRejection(t *testing.T) {
	for _, interactions := range []bool{false, true} {
		for _, terminals := range []bool{false, true} {
			s, _ := openTest(t)
			ctx := notificationOwner()
			_, err := s.Mutate(ctx, domain.NewID(), "fixture.notification.legacy", nil, func(tx *Tx) (any, error) {
				return tx.SetNotificationPreferences(domain.NotificationPreferences{Revision: 1, Interactions: interactions, Terminals: terminals})
			})
			if err != nil {
				t.Fatal(err)
			}
			var before domain.NotificationPreferences
			if err := s.Read(ctx, func(tx *Tx) error { var err error; before, err = tx.NotificationPreferences(); return err }); err != nil {
				t.Fatal(err)
			}
			if err := s.EnsureSituationNotificationPreferences(ctx); err != nil {
				t.Fatal(err)
			}
			if err := s.Read(ctx, func(tx *Tx) error {
				v, err := tx.NotificationPreferences()
				if err != nil {
					return err
				}
				if v.Revision != before.Revision || v.Situations == nil {
					t.Fatal(v)
				}
				p := v.Situations
				if p.Questions != interactions || p.Approvals != interactions || p.Succeeded != terminals || p.Failed != terminals || p.Stopped != terminals || !p.ServerLost || !p.WorkerUnavailable || !p.QuotaExhausted || !p.ScheduleStartFailed || !p.ScheduleOffline || p.ServerRestored || p.WorkerAvailable {
					t.Fatal(p)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			_, err = s.Mutate(ctx, domain.NewID(), "fixture.notification.destructive", nil, func(tx *Tx) (any, error) { return tx.SetNotificationPreferences(before) })
			if domain.SafeError(err).Code != domain.Unsupported {
				t.Fatal(err)
			}
		}
	}
}
func TestSituationNotificationGranularQuestionClaimAndConcurrentDisable(t *testing.T) {
	s, _ := openTest(t)
	ctx := notificationOwner()
	inbox := notificationFixture(t, s, "pending")
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.typed.question", nil, func(tx *Tx) (any, error) {
		r, err := tx.Get(domain.InboxKind, inbox)
		if err != nil {
			return nil, err
		}
		i, err := Decode[domain.InboxEntry](r)
		if err != nil {
			return nil, err
		}
		x, err := tx.Get(domain.InteractionKind, i.SourceID)
		if err != nil {
			return nil, err
		}
		v, err := Decode[domain.ExecutionInteraction](x)
		if err != nil {
			return nil, err
		}
		v.Type = domain.UserQuestionInteraction
		return tx.Put(x.Kind, x.ID, x.Revision, x.SessionID, x.ProjectID, v)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureSituationNotificationPreferences(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		rows, _, err := tx.NotificationCandidates(50)
		if err == nil && (len(rows) != 1 || rows[0].Kind != domain.QuestionNotification) {
			t.Fatal(rows)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.question.disabled", nil, func(tx *Tx) (any, error) {
		v, err := tx.NotificationPreferences()
		if err != nil {
			return nil, err
		}
		v.Situations.Questions = false
		return tx.SetNotificationPreferences(v)
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.question.claim", nil, func(tx *Tx) (any, error) {
		_, fresh, err := tx.ClaimNotification(inbox, domain.NewID())
		if fresh {
			t.Fatal("disabled question claimed")
		}
		return nil, err
	})
	if domain.SafeError(err).Code != domain.Conflict {
		t.Fatal(err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		rows, _, err := tx.NotificationCandidates(50)
		if len(rows) != 0 {
			t.Fatal(rows)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
