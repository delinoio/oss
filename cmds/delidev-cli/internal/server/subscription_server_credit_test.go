// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type serverCreditFixture struct {
	*serverQuotaFixture
	mu            sync.Mutex
	sends         []domain.ServerCreditOperation
	outcome       domain.SubscriptionResetOutcome
	consumeError  error
	duringConsume func()
}

func (n *serverCreditFixture) ConsumeServerResetCredit(ctx context.Context, op domain.ServerCreditOperation) (domain.SubscriptionResetOutcome, error) {
	if op.Validate() != nil || !op.SendClaimed || op.Phase != domain.SubscriptionObservationSending {
		return "", domain.InvalidSubscriptionObservation()
	}
	n.mu.Lock()
	n.sends = append(n.sends, op)
	n.mu.Unlock()
	if n.duringConsume != nil {
		n.duringConsume()
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return n.outcome, n.consumeError
}
func (n *serverCreditFixture) count() int { n.mu.Lock(); defer n.mu.Unlock(); return len(n.sends) }
func newServerCreditFixture(t *testing.T, details bool) (*subscriptionFixture, *serverCreditFixture) {
	t.Helper()
	f, q := newServerQuotaFixture(t)
	items := []domain.SubscriptionResetCreditDetail{{ID: "credit_1", ResetType: domain.CodexRateLimitsReset, Status: domain.SubscriptionCreditAvailable, GrantedAt: time.Now().UTC()}}
	q.observed.Credits = &domain.SubscriptionResetCredits{ObservationID: domain.NewID(), ObservedAt: q.observed.ObservedAt, AvailableCount: 2}
	if details {
		q.observed.Credits.Credits = &items
	}
	if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, requestServerQuota(f))); err != nil {
		t.Fatal(err)
	}
	f.service.runServerQuota(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), f.input.AccountID)
	n := &serverCreditFixture{serverQuotaFixture: q, outcome: domain.SubscriptionReset}
	f.service.subscriptionOpen = func(context.Context, string, domain.ID, []byte, *slog.Logger) (serverSubscriptionNative, error) {
		return n, nil
	}
	return f, n
}
func requestServerCredit(f *subscriptionFixture) *pb.RequestSubscriptionObservationRequest {
	r, a := f.record()
	credits := a.Subscription.ResetCredits
	req := &pb.RequestSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SubscriptionObservationAction_SUBSCRIPTION_OBSERVATION_ACTION_RESET_CREDIT, ConnectionId: string(a.Connection.ID), GenerationId: string(a.Subscription.Generation), CreditsObservationId: string(credits.ObservationID), Confirmed: true}
	if credits.Credits == nil {
		req.NextCredit = true
	} else {
		req.CreditId = "credit_1"
	}
	return req
}
func creditContext() context.Context {
	return domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
}
func acceptCredit(t *testing.T, f *subscriptionFixture, req *pb.RequestSubscriptionObservationRequest) {
	t.Helper()
	if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, req)); err != nil {
		t.Fatal(err)
	}
}
func mutateCreditAccount(t *testing.T, f *subscriptionFixture, fn func(*domain.Account)) {
	t.Helper()
	_, err := f.service.Store.Mutate(creditContext(), domain.NewID(), "fixture.credit.change", nil, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		fn(&a)
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestServerCreditConfirmedOriginalSelectionAndReplay(t *testing.T) {
	for _, details := range []bool{true, false} {
		t.Run(map[bool]string{true: "exact", false: "next"}[details], func(t *testing.T) {
			f, n := newServerCreditFixture(t, details)
			req := requestServerCredit(f)
			acceptCredit(t, f, req)
			f.service.runServerCredit(creditContext(), f.input.AccountID)
			_, a := f.record()
			o := a.Subscription.ServerCredit
			if o == nil || o.ID != domain.ID(req.Mutation.RequestId) || o.Phase != domain.SubscriptionObservationSucceeded || !o.CleanupConfirmed || o.Outcome != domain.SubscriptionReset || a.Subscription.OwnerMachineID != "" || a.Subscription.Lease != nil || n.count() != 1 || n.reads.Load() != 2 {
				t.Fatal("server consumption lost ownership or outcome", o)
			}
			if n.sends[0].ID != o.ID || n.sends[0].CreditID != req.CreditId || n.sends[0].NextCredit != req.NextCredit {
				t.Fatal("provider selector changed")
			}
			replay, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, req))
			if err != nil || !replay.Msg.Replayed {
				t.Fatal("receipt replay failed", err)
			}
			f.service.runServerCredit(creditContext(), f.input.AccountID)
			if n.count() != 1 {
				t.Fatal("replay resent consumption")
			}
		})
	}
}
func TestServerCreditLostResponseExplicitOriginalReconciliation(t *testing.T) {
	f, n := newServerCreditFixture(t, true)
	n.outcome = ""
	n.consumeError = errors.New("synthetic lost response")
	req := requestServerCredit(f)
	acceptCredit(t, f, req)
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	_, a := f.record()
	old := *a.Subscription.ServerCredit
	if old.Phase != domain.SubscriptionObservationUncertain || !old.CleanupConfirmed || a.Subscription.RecoveryRequired || serverQuotaReady(a) {
		t.Fatal("uncertainty lost original fenced owner", old)
	}
	mutateCreditAccount(t, f, func(a *domain.Account) { a.Subscription.ServerCredit.AttemptEpoch = domain.NewID() })
	if err := f.service.initializeServerQuotas(creditContext()); err != nil {
		t.Fatal(err)
	}
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	f.service.runServerQuota(creditContext(), f.input.AccountID)
	if n.count() != 1 {
		t.Fatal("restart/status replayed consumption")
	}
	r, a := f.record()
	n.outcome = domain.SubscriptionAlreadyRedeemed
	n.consumeError = nil
	reconcile := &pb.ReconcileSubscriptionCreditRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, OperationId: string(old.ID), ConnectionId: string(old.ConnectionID), GenerationId: string(old.Generation)}
	if _, err := f.client.ReconcileSubscriptionCredit(context.Background(), subscriptionRequest(f.service.Identity.Token, reconcile)); err != nil {
		t.Fatal(err)
	}
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	_, a = f.record()
	o := a.Subscription.ServerCredit
	if n.count() != 2 || n.sends[1].ID != old.ID || n.sends[1].CreditID != old.CreditID || o.Epoch != old.Epoch || o.Actor != old.Actor || o.Outcome != domain.SubscriptionAlreadyRedeemed || o.Phase != domain.SubscriptionObservationSucceeded {
		t.Fatal("reconciliation changed original key/authority", o)
	}
	replay, err := f.client.ReconcileSubscriptionCredit(context.Background(), subscriptionRequest(f.service.Identity.Token, reconcile))
	if err != nil || !replay.Msg.Replayed {
		t.Fatal("reconcile receipt replay failed", err)
	}
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	if n.count() != 2 {
		t.Fatal("reconcile replay resent native request")
	}
}
func TestServerCreditReadFailurePreservesConsumptionAndCleanupFence(t *testing.T) {
	for _, cleanup := range []bool{true, false} {
		t.Run(map[bool]string{true: "clean", false: "uncertain-cleanup"}[cleanup], func(t *testing.T) {
			f, n := newServerCreditFixture(t, true)
			_, before := f.record()
			n.readError = domain.Fail(domain.Unavailable, "Synthetic quota failure.", "")
			if !cleanup {
				n.closeError = errors.New("synthetic cleanup failure")
			}
			acceptCredit(t, f, requestServerCredit(f))
			f.service.runServerCredit(creditContext(), f.input.AccountID)
			r, a := f.record()
			o := a.Subscription.ServerCredit
			if o.Outcome != domain.SubscriptionReset || !reflect.DeepEqual(before.Quota, a.Quota) || !a.Subscription.QuotaObservedAt.Equal(*before.Subscription.QuotaObservedAt) || o.CleanupConfirmed != cleanup {
				t.Fatal("read failure erased outcome or last success", o)
			}
			if cleanup {
				if o.Phase != domain.SubscriptionObservationSucceeded || o.QuotaErrorCode != domain.Unavailable || a.Subscription.RecoveryRequired {
					t.Fatal("post-read error changed consumption")
				}
			} else {
				if o.Phase != domain.SubscriptionObservationUncertain || !a.Subscription.RecoveryRequired {
					t.Fatal("cleanup uncertainty lost recovery fence")
				}
				reconcile := &pb.ReconcileSubscriptionCreditRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, OperationId: string(o.ID), ConnectionId: string(o.ConnectionID), GenerationId: string(o.Generation)}
				if _, err := f.client.ReconcileSubscriptionCredit(context.Background(), subscriptionRequest(f.service.Identity.Token, reconcile)); err == nil {
					t.Fatal("unconfirmed cleanup authorized retry")
				}
			}
			f.service.runServerCredit(creditContext(), f.input.AccountID)
			if n.count() != 1 {
				t.Fatal("terminal/uncertain work replayed")
			}
		})
	}
}
func TestServerCreditConfirmationRejectsChangedOrUnavailableInventory(t *testing.T) {
	for _, change := range []string{"revision", "connection", "generation", "inventory", "stale", "future", "expired", "zero", "unknown", "missing-confirmation", "next-with-details", "missing-credit"} {
		t.Run(change, func(t *testing.T) {
			f, n := newServerCreditFixture(t, true)
			req := requestServerCredit(f)
			switch change {
			case "revision":
				req.Mutation.ExpectedRevision++
			case "connection":
				req.ConnectionId = string(domain.NewID())
			case "generation":
				req.GenerationId = string(domain.NewID())
			case "inventory":
				req.CreditsObservationId = string(domain.NewID())
			case "missing-confirmation":
				req.Confirmed = false
			case "next-with-details":
				req.CreditId = ""
				req.NextCredit = true
			case "missing-credit":
				req.CreditId = "not-available"
			default:
				mutateCreditAccount(t, f, func(a *domain.Account) {
					switch change {
					case "stale":
						a.Subscription.ResetCredits.ObservedAt = time.Now().Add(-6 * time.Minute)
					case "future":
						a.Subscription.ResetCredits.ObservedAt = time.Now().Add(time.Minute)
					case "expired":
						at := time.Now().Add(-time.Minute)
						(*a.Subscription.ResetCredits.Credits)[0].ExpiresAt = &at
					case "zero":
						a.Subscription.ResetCredits.AvailableCount = 0
					case "unknown":
						a.Subscription.ResetCredits = nil
					}
				})
				r, _ := f.record()
				req.Mutation.ExpectedRevision = r.Revision
			}
			if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, req)); err == nil {
				t.Fatal("invalid confirmation accepted")
			}
			f.service.runServerCredit(creditContext(), f.input.AccountID)
			if n.count() != 0 {
				t.Fatal("invalid inventory consumed")
			}
		})
	}
}
func TestServerCreditFencesLifecycleExecutionAndSavedInspection(t *testing.T) {
	f, n := newServerCreditFixture(t, true)
	req := requestServerCredit(f)
	acceptCredit(t, f, req)
	r, a := f.record()
	if serverQuotaReady(a) {
		t.Fatal("credit claim permits competing quota")
	}
	logout := &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT}
	if _, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, logout)); err == nil {
		t.Fatal("logout bypassed credit fence")
	}
	if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, requestServerQuota(f))); err == nil {
		t.Fatal("quota bypassed credit fence")
	}
	if doctorSubscriptionState(a) != domain.DiagnosticUnavailable {
		t.Fatal("saved-storage diagnostics bypassed credit fence")
	}
	if failedCleanupCandidate(a) {
		t.Fatal("cleanup admitted active server owner")
	}
	mutateCreditAccount(t, f, func(a *domain.Account) { a.Subscription.Generation = domain.NewID() })
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	_, a = f.record()
	if n.count() != 0 || a.Subscription.ServerCredit.Phase != domain.SubscriptionObservationFailed {
		t.Fatal("changed generation consumed")
	}
}
func TestServerCreditRejectsNonDirectBeforeNativeLaunch(t *testing.T) {
	f, n := newServerCreditFixture(t, true)
	opens := 0
	f.service.subscriptionOpen = func(context.Context, string, domain.ID, []byte, *slog.Logger) (serverSubscriptionNative, error) {
		opens++
		return n, nil
	}
	route := domain.NetworkRoute{ProfileID: domain.NewID(), ProfileRevision: 1, Profile: domain.NetworkProfile{ProxyDefinition: domain.ProxyDefinition{Name: "Credit proxy fixture", Mode: domain.ProxyHTTP, Host: "127.0.0.1", Port: 3128}}}
	_, err := f.service.Store.Mutate(creditContext(), domain.NewID(), "fixture.credit.route", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.NetworkRouteKind, domain.NewID(), 0, "", "", route)
	})
	if err != nil {
		t.Fatal(err)
	}
	acceptCredit(t, f, requestServerCredit(f))
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	_, a := f.record()
	if opens != 0 || n.count() != 0 || a.Subscription.ServerCredit.ErrorCode != domain.Unsupported || a.Subscription.ServerCredit.Phase != domain.SubscriptionObservationFailed {
		t.Fatal("unsupported route launched native")
	}
}
func TestServerCreditRevokedActorCannotSend(t *testing.T) {
	f, n := newServerCreditFixture(t, true)
	device := domain.NewID()
	_, err := f.service.Store.Mutate(creditContext(), domain.NewID(), "fixture.credit.client", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.DeviceKind, device, 0, "", "", domain.Device{Name: "Credit client", Type: domain.ClientDevice, PairedAt: time.Now().UTC()})
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: device})
	if _, err := f.service.RequestSubscriptionObservation(actor, connect.NewRequest(requestServerCredit(f))); err != nil {
		t.Fatal(err)
	}
	_, err = f.service.Store.Mutate(creditContext(), domain.NewID(), "fixture.credit.revoke", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.DeviceKind, device)
		if err != nil {
			return nil, err
		}
		d, err := store.Decode[domain.Device](r)
		if err != nil {
			return nil, err
		}
		d.Revoked = true
		return tx.Put(domain.DeviceKind, r.ID, r.Revision, "", "", d)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	if n.count() != 0 {
		t.Fatal("revoked actor consumed")
	}
}
func TestServerCreditShutdownJoinsOriginalNative(t *testing.T) {
	f, n := newServerCreditFixture(t, true)
	ctx, cancel := context.WithCancel(creditContext())
	n.duringConsume = cancel
	acceptCredit(t, f, requestServerCredit(f))
	f.service.runServerCredit(ctx, f.input.AccountID)
	_, a := f.record()
	if n.count() != 1 || a.Subscription.ServerCredit.Phase != domain.SubscriptionObservationUncertain || !a.Subscription.ServerCredit.CleanupConfirmed {
		t.Fatal("shutdown lost possible-send outcome")
	}
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	if n.count() != 1 {
		t.Fatal("shutdown replayed consumption")
	}
}

func TestServerCreditRestartBeforeExplicitReconciliationSendRetainsUncertainty(t *testing.T) {
	f, n := newServerCreditFixture(t, true)
	n.outcome = ""
	n.consumeError = errors.New("synthetic possible send")
	acceptCredit(t, f, requestServerCredit(f))
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	r, a := f.record()
	original := *a.Subscription.ServerCredit
	req := &pb.ReconcileSubscriptionCreditRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, OperationId: string(original.ID), ConnectionId: string(original.ConnectionID), GenerationId: string(original.Generation)}
	if _, err := f.client.ReconcileSubscriptionCredit(context.Background(), subscriptionRequest(f.service.Identity.Token, req)); err != nil {
		t.Fatal(err)
	}
	mutateCreditAccount(t, f, func(a *domain.Account) { a.Subscription.ServerCredit.AttemptEpoch = domain.NewID() })
	if err := f.service.initializeServerQuotas(creditContext()); err != nil {
		t.Fatal(err)
	}
	_, a = f.record()
	o := a.Subscription.ServerCredit
	if o.Phase != domain.SubscriptionObservationUncertain || !o.EverSent || !o.CleanupConfirmed || serverQuotaReady(a) || o.ID != original.ID || o.CreditID != original.CreditID {
		t.Fatal("restart released unresolved earlier consumption", o)
	}
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	if n.count() != 1 {
		t.Fatal("restart resent original key")
	}
}

func TestServerCreditClosedOutcomesRemainDistinct(t *testing.T) {
	for _, outcome := range []domain.SubscriptionResetOutcome{domain.SubscriptionReset, domain.SubscriptionAlreadyRedeemed, domain.SubscriptionNothingToReset, domain.SubscriptionNoCredit} {
		t.Run(string(outcome), func(t *testing.T) {
			f, n := newServerCreditFixture(t, true)
			n.outcome = outcome
			acceptCredit(t, f, requestServerCredit(f))
			f.service.runServerCredit(creditContext(), f.input.AccountID)
			_, a := f.record()
			if a.Subscription.ServerCredit.Outcome != outcome || a.Subscription.ServerCredit.Phase != domain.SubscriptionObservationSucceeded {
				t.Fatal("closed provider outcome collapsed")
			}
		})
	}
}
func TestServerCreditOmittedMachinePreservesOriginalActiveWorker(t *testing.T) {
	f := newQuotaFixture(t)
	mutateCreditAccount(t, f, func(a *domain.Account) {
		a.Subscription.ResetCredits = &domain.SubscriptionResetCredits{ObservationID: domain.NewID(), ObservedAt: time.Now().UTC(), AvailableCount: 2}
	})
	lease := takeSubscriptionExecutionFixture(t, f)
	req := requestServerCredit(f)
	acceptCredit(t, f, req)
	_, a := f.record()
	if a.Subscription.ServerCredit != nil || a.Subscription.Observation.MachineID != f.input.MachineID || a.Subscription.Lease.ID != domain.ID(lease.LeaseId) {
		t.Fatal("server lane stole original active Worker")
	}
	f.claimObservation(req.Mutation.RequestId, lease)
}
func TestServerCreditPendingServerOwnersRejectConsumption(t *testing.T) {
	for _, owner := range []string{"quota", "logout", "refresh", "recovery"} {
		t.Run(owner, func(t *testing.T) {
			f, n := newServerCreditFixture(t, true)
			switch owner {
			case "quota":
				if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, requestServerQuota(f))); err != nil {
					t.Fatal(err)
				}
			case "logout":
				f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
			case "refresh":
				f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
			case "recovery":
				mutateCreditAccount(t, f, func(a *domain.Account) { a.Subscription.RecoveryRequired = true; a.Health = domain.AccountFailed })
			}
			if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, requestServerCredit(f))); err == nil {
				t.Fatal("competing credential owner admitted credit")
			}
			f.service.runServerCredit(creditContext(), f.input.AccountID)
			if n.count() != 0 {
				t.Fatal("competing owner consumed")
			}
		})
	}
}
