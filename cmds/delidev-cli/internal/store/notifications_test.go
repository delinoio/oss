package store

import (
	"context"

	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func notificationFixture(t *testing.T, s *Store, scenario string) domain.ID {
	t.Helper()
	inbox, session, source, execution := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.notification", nil, func(tx *Tx) (any, error) {
		v := domain.Session{Archive: domain.NotArchived, Dispatch: domain.DispatchClaimed, Recovery: domain.NoRecovery, ActiveExecutionID: execution, Name: "private session name"}
		x := domain.ExecutionInteraction{ExecutionID: execution, Closure: domain.InteractionOpen}
		i := domain.InboxEntry{Source: domain.InteractionInbox, SourceID: source, ReadState: domain.InboxUnread}
		switch scenario {
		case "archived":
			v.Archive = domain.Archived
		case "paused":
			v.Dispatch = domain.DispatchPaused
		case "recovery":
			v.Recovery = domain.NeedsRecovery
		case "replaced":
			v.ActiveExecutionID = domain.NewID()
		case "closed":
			x.Closure = domain.InteractionNativeClosed
		case "answered":
			x.Response = &domain.QuestionResponse{ID: domain.NewID()}
		case "approved":
			x.ApprovalResponse = &domain.ApprovalResponse{ID: domain.NewID()}
		case "read":
			i.ReadState = domain.InboxRead
		case "terminal", "terminal-read":
			i.Source = domain.ExecutionTerminalInbox
			i.Terminal = &domain.InboxTerminal{Outcome: domain.ExecutionFailed}
			if scenario == "terminal-read" {
				i.ReadState = domain.InboxRead
			}
		}
		if _, err := tx.Put(domain.SessionKind, session, 0, session, "", v); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.InteractionKind, source, 0, session, "", x); err != nil {
			return nil, err
		}
		return tx.Put(domain.InboxKind, inbox, 0, session, "", i)
	})
	if err != nil {
		t.Fatal(err)
	}
	return inbox
}
func notificationOwner() context.Context {
	return domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
}

func TestNotificationCandidatesRecheckCurrentSourceWithoutChangingReadState(t *testing.T) {
	s, _ := openTest(t)
	ctx := notificationOwner()
	eligible := map[domain.ID]bool{}
	for _, scenario := range []string{"pending", "read", "archived", "paused", "recovery", "replaced", "closed", "answered", "approved", "terminal", "terminal-read"} {
		id := notificationFixture(t, s, scenario)
		if scenario == "pending" || scenario == "read" {
			eligible[id] = true
		}
	}
	_, before, _ := s.Snapshot(ctx, Filter{Kind: domain.InboxKind, Limit: MaxPage})
	if err := s.Read(ctx, func(tx *Tx) error {
		rows, more, err := tx.NotificationCandidates(50)
		if err != nil {
			return err
		}
		if more || len(rows) != 2 {
			t.Fatalf("unexpected candidates: %+v %v", rows, more)
		}
		for _, v := range rows {
			if !eligible[v.InboxID] || v.Kind != domain.RequestNotification {
				t.Fatal("source validity/read state was confused")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, after, _ := s.Snapshot(ctx, Filter{Kind: domain.InboxKind, Limit: MaxPage})
	if before != after {
		t.Fatal("notification read changed the inbox")
	}
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.preferences", nil, func(tx *Tx) (any, error) {
		return tx.SetNotificationPreferences(domain.NotificationPreferences{Revision: 1, Interactions: false, Terminals: true})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		rows, more, err := tx.NotificationCandidates(1)
		if err == nil && (more || len(rows) != 1 || rows[0].Kind != domain.FailedNotification) {
			t.Fatal("terminal configuration/read state was ignored")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationClaimSurvivesRestartAndFailedPresentation(t *testing.T) {
	s, root := openTest(t)
	ctx := notificationOwner()
	id := notificationFixture(t, s, "pending")
	claim := domain.NewID()
	_, err := s.Mutate(ctx, claim, "fixture.claim", nil, func(tx *Tx) (any, error) {
		v, fresh, err := tx.ClaimNotification(id, claim)
		if err == nil && (!fresh || v.State != domain.NotificationClaimed) {
			t.Fatal("missing fresh durable claim")
		}
		return v, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.claim-again", nil, func(tx *Tx) (any, error) {
		v, fresh, err := tx.ClaimNotification(id, domain.NewID())
		if err == nil && (fresh || v.ClaimID != claim) {
			t.Fatal("reconnect repeated uncertain delivery")
		}
		return v, err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.report", nil, func(tx *Tx) (any, error) { return tx.ReportNotification(id, claim, domain.NotificationDenied) })
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		rows, _, err := tx.NotificationCandidates(50)
		if len(rows) != 0 {
			t.Fatal("failed presentation became an automatic retry")
		}
		inbox, e := tx.Get(domain.InboxKind, id)
		if e != nil {
			return e
		}
		v, e := Decode[domain.InboxEntry](inbox)
		if e == nil && (v.ReadState != domain.InboxUnread || inbox.Revision != 1) {
			t.Fatal("presentation changed inbox state")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.change-report", nil, func(tx *Tx) (any, error) { return tx.ReportNotification(id, claim, domain.NotificationSubmitted) })
	assertCode(t, err, domain.Conflict)
}

func TestNotificationBatchesHaveExplicitOverflow(t *testing.T) {
	s, _ := openTest(t)
	for range 3 {
		notificationFixture(t, s, "pending")
	}
	if err := s.Read(notificationOwner(), func(tx *Tx) error {
		values, more, err := tx.NotificationCandidates(2)
		if err == nil && (len(values) != 2 || !more) {
			t.Fatal("partial inventory was presented as complete")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, ctx := range []context.Context{context.Background(), domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice})} {
		assertCode(t, s.Read(ctx, func(tx *Tx) error { _, _, err := tx.NotificationCandidates(20); return err }), domain.PermissionDenied)
	}
}

func TestSubscriptionRecoveryInboxAndNotificationDeduplicateWithoutSessionAuthority(t *testing.T) {
	s, _ := openTest(t)
	ctx := notificationOwner()
	account, connection, source := domain.NewID(), domain.NewID(), domain.NewID()
	now := time.Now().UTC()
	var first Record
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.subscription.recovery", nil, func(tx *Tx) (any, error) {
		a := domain.Account{Alias: "private account alias", Type: domain.SubscriptionAccount, SubscriptionService: domain.SubscriptionChatGPT, Enabled: true, Health: domain.AccountReady, Connection: &domain.AccountConnection{ID: connection, Authentication: domain.SubscriptionAuth, ConnectedAt: now}, RecoveryNotifications: true}
		if _, err := tx.Put(domain.AccountKind, account, 0, "", "", a); err != nil {
			return nil, err
		}
		var err error
		first, err = tx.CreateSubscriptionRecoveryInbox(account, source, connection, now)
		if err != nil {
			return nil, err
		}
		duplicate, err := tx.CreateSubscriptionRecoveryInbox(account, source, connection, now)
		if err != nil || duplicate.ID != first.ID {
			return nil, domain.InvalidSubscriptionObservation()
		}
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.SessionID != "" || first.ProjectID != "" {
		t.Fatal("account recovery acquired session authority")
	}
	err = s.Read(ctx, func(tx *Tx) error {
		values, more, err := tx.NotificationCandidates(50)
		if err != nil {
			return err
		}
		if more || len(values) != 1 || values[0].Kind != domain.SubscriptionRecoveryNotification || values[0].AccountID != account || values[0].SessionID != "" {
			t.Fatal("account-scoped native candidate lost ownership", values)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	claim := domain.NewID()
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.subscription.notification.claim", nil, func(tx *Tx) (any, error) {
		delivery, created, err := tx.ClaimNotification(first.ID, claim)
		if err != nil {
			return nil, err
		}
		if !created || delivery.AccountID != account || delivery.SessionID != "" {
			t.Fatal("native claim changed account scope")
		}
		duplicate, created, err := tx.ClaimNotification(first.ID, domain.NewID())
		if err != nil {
			return nil, err
		}
		if created || duplicate.ClaimID != claim {
			t.Fatal("duplicate native presentation granted")
		}
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
