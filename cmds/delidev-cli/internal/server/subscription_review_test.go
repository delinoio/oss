// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func quotaReceiptCounter(t *testing.T, f *subscriptionFixture) func() int {
	t.Helper()
	path := filepath.ToSlash(filepath.Join(f.service.Store.Root(), "state.sqlite"))
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	uri := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return func() int {
		t.Helper()
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM receipts").Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
}
func TestQuotaMaintenanceSkipsEmptyAndAlreadyQueuedReceipts(t *testing.T) {
	for _, connected := range []bool{false, true} {
		t.Run(map[bool]string{false: "disconnected", true: "connected"}[connected], func(t *testing.T) {
			f := newSubscriptionFixture(t)
			if connected {
				f = newQuotaFixture(t)
			}
			count := quotaReceiptCounter(t, f)
			ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
			now := time.Now().UTC()
			before := count()
			var group sync.WaitGroup
			problems := make(chan error, 12)
			for i := 0; i < 12; i++ {
				group.Add(1)
				go func() { defer group.Done(); problems <- f.service.queueDueSubscriptionQuotas(ctx, now) }()
			}
			group.Wait()
			close(problems)
			for err := range problems {
				if err != nil {
					t.Fatal(err)
				}
			}
			expected := before
			if connected {
				expected++
			}
			if count() != expected {
				t.Fatal("initial maintenance receipt count is incorrect")
			}
			for i := 0; i < 20; i++ {
				if err := f.service.queueDueSubscriptionQuotas(ctx, now.Add(time.Duration(i)*time.Minute)); err != nil {
					t.Fatal(err)
				}
			}
			if count() != expected {
				t.Fatal("empty maintenance published durable receipts")
			}
		})
	}
}
func TestDeletedSubscriptionAccountRemovesRecoveryInboxAtomically(t *testing.T) {
	f := newSubscriptionFixture(t)
	target := domain.NewID()
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	var deletedEntry, retainedEntry domain.ID
	var revision uint64
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.recovery.account.delete", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.AccountKind, f.input.AccountID)
		if err != nil {
			return nil, err
		}
		a, err := store.Decode[domain.Account](r)
		if err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		a.RecoveryNotifications = true
		a.Health = domain.AccountReady
		a.Connection = &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.SubscriptionAuth, ConnectedAt: now}
		connected, err := tx.Put(domain.AccountKind, target, 0, "", "", a)
		if err != nil {
			return nil, err
		}
		for i := 0; i < store.MaxPage+1; i++ {
			entry, err := tx.CreateSubscriptionRecoveryInbox(target, domain.NewID(), a.Connection.ID, now)
			if err != nil {
				return nil, err
			}
			deletedEntry = entry.ID
		}
		other := domain.NewID()
		if _, err := tx.Put(domain.AccountKind, other, 0, "", "", a); err != nil {
			return nil, err
		}
		entry, err := tx.CreateSubscriptionRecoveryInbox(other, domain.NewID(), a.Connection.ID, now)
		if err != nil {
			return nil, err
		}
		retainedEntry = entry.ID
		a.Connection = nil
		a.Health = domain.AccountDisconnected
		disconnected, err := tx.Put(domain.AccountKind, target, connected.Revision, "", "", a)
		revision = disconnected.Revision
		return true, err
	})
	if err != nil {
		t.Fatal(err)
	}
	request := &pb.DeleteConfigurationRequest{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(target), ExpectedRevision: revision}}
	if _, err := f.service.DeleteConfiguration(ctx, connect.NewRequest(request)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.DeleteConfiguration(ctx, connect.NewRequest(request)); err != nil {
		t.Fatal("deletion replay changed receipt authority", err)
	}
	if _, err := f.service.GetInboxEntry(ctx, connect.NewRequest(&pb.GetInboxEntryRequest{Id: string(deletedEntry)})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal("deleted account retained orphan inbox", err)
	}
	page, err := f.service.ListInbox(ctx, connect.NewRequest(&pb.ListInboxRequest{PageSize: 50}))
	if err != nil || len(page.Msg.Entries) != 1 || page.Msg.Entries[0].Entry.Id != string(retainedEntry) {
		t.Fatal("deletion broke another account's Inbox", err)
	}
}

func TestCreditPublicationLossKeepsOriginalKeyReconciliation(t *testing.T) {
	f := newQuotaFixture(t)
	now := time.Now().UTC()
	inventory, key := domain.NewID(), domain.NewID()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.credit.inventory", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.AccountKind, f.input.AccountID)
		if err != nil {
			return nil, err
		}
		a, err := store.Decode[domain.Account](r)
		if err != nil {
			return nil, err
		}
		a.Subscription.ResetCredits = &domain.SubscriptionResetCredits{ObservationID: inventory, ObservedAt: now, AvailableCount: 2}
		a.ConfirmedExhausted = true
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		t.Fatal(err)
	}
	r, a := f.record()
	request := &pb.RequestSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(key), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), Action: pb.SubscriptionObservationAction_SUBSCRIPTION_OBSERVATION_ACTION_RESET_CREDIT, ConnectionId: string(a.Connection.ID), GenerationId: string(a.Subscription.Generation), NextCredit: true, CreditsObservationId: string(inventory), Confirmed: true}
	accepted, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.take(&pb.RequestSubscriptionResponse{OperationId: accepted.Msg.OperationId}, pb.SubscriptionAction_SUBSCRIPTION_ACTION_RESET_CREDIT)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(lease.Bundle)
	f.claimObservation(string(key), lease)
	// Publication never reaches the server. Independent unchanged-bundle/native
	// cleanup closes only the credential lease, preserving operation uncertainty.
	if _, err := f.finish(lease, lease.Bundle, true, false, true); err != nil {
		t.Fatal(err)
	}
	r, a = f.record()
	if a.Subscription.RecoveryRequired || a.Subscription.Lease != nil || a.Subscription.Observation.Phase != domain.SubscriptionObservationUncertain || !a.ConfirmedExhausted {
		t.Fatal("publication loss converted operation uncertainty into credential recovery or quota restoration")
	}
	if _, err := f.client.ReconcileSubscriptionCredit(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.ReconcileSubscriptionCreditRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, OperationId: string(key), ConnectionId: string(a.Connection.ID), GenerationId: string(a.Subscription.Generation)})); err != nil {
		t.Fatal(err)
	}
	_, a = f.record()
	if a.Subscription.Observation.ID != key || a.Subscription.Observation.Phase != domain.SubscriptionObservationQueued {
		t.Fatal("reconciliation replaced the official operation key")
	}
}

func TestQuotaPublicationLossSettlesReadAndAllowsNextObservation(t *testing.T) {
	f := newQuotaFixture(t)
	accepted := f.requestQuota()
	lease, err := f.take(&pb.RequestSubscriptionResponse{OperationId: accepted.OperationId}, pb.SubscriptionAction_SUBSCRIPTION_ACTION_QUOTA)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(lease.Bundle)
	f.claimObservation(accepted.OperationId, lease)
	if _, err := f.finish(lease, lease.Bundle, true, false, true); err != nil {
		t.Fatal(err)
	}
	_, a := f.record()
	if a.Subscription.Observation.Phase != domain.SubscriptionObservationFailed || a.Subscription.Observation.Active() || a.Subscription.QuotaState != domain.ObservationFailed || a.Subscription.RecoveryRequired || a.Subscription.Lease != nil {
		t.Fatal("lost quota read retained consumption uncertainty or credential ownership")
	}
	next := f.requestQuota()
	if next.OperationId == accepted.OperationId {
		t.Fatal("explicit fresh quota read reused failed observation")
	}
}
