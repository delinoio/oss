// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type serverQuotaFixture struct {
	*serverLoginFixture
	reads      atomic.Int32
	readError  error
	closeError error
	observed   domain.SubscriptionQuotaObservation
	duringRead func()
}

func (n *serverQuotaFixture) ReadManagedQuota(ctx context.Context, _ domain.ID) (domain.SubscriptionQuotaObservation, error) {
	n.reads.Add(1)
	if n.duringRead != nil {
		n.duringRead()
	}
	if ctx.Err() != nil {
		return domain.SubscriptionQuotaObservation{}, ctx.Err()
	}
	return n.observed, n.readError
}
func (n *serverQuotaFixture) Close([]byte) error { return n.closeError }
func newServerQuotaFixture(t *testing.T) (*subscriptionFixture, *serverQuotaFixture) {
	f := newSubscriptionFixture(t)
	// Remove the common fixture's Runner registration. This lane must work with
	// only its server-owned account, original protected generation and receipt.
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.server.quota.no-runner", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.MachineKind, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		return nil, tx.Delete(domain.MachineKind, r.ID, r.Revision)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	login := &serverLoginFixture{started: make(chan struct{}), finish: make(chan struct{}), bundle: subscriptionTestBundle("quota-server-account", "first", time.Now().UTC())}
	done := f.serverRun(login)
	awaitServerFixture(t, login.started)
	close(login.finish)
	awaitServerFixture(t, done)
	now := time.Now().UTC()
	primary, secondary := 0.2, 0.8
	n := &serverQuotaFixture{serverLoginFixture: login, observed: domain.SubscriptionQuotaObservation{ObservedAt: now, Windows: []domain.SubscriptionQuotaWindow{{ID: "codex:primary", Remaining: &primary}, {ID: "codex:secondary", Remaining: &secondary}}}}
	f.service.subscriptionOpen = func(context.Context, string, domain.ID, []byte, *slog.Logger) (serverSubscriptionNative, error) {
		return n, nil
	}
	f.service.quotaOpen = func(context.Context, string, domain.ID, codex.QuotaAuthentication, *slog.Logger) (serverQuotaNative, error) {
		return quotaNativeFixture{n}, nil
	}
	return f, n
}
func requestServerQuota(f *subscriptionFixture) *pb.RequestSubscriptionObservationRequest {
	r, a := f.record()
	return &pb.RequestSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SubscriptionObservationAction_SUBSCRIPTION_OBSERVATION_ACTION_QUOTA, ConnectionId: string(a.Connection.ID), GenerationId: string(a.Subscription.Generation)}
}
func TestServerQuotaIndividualAllMaintenanceAndReplay(t *testing.T) {
	for _, lane := range []string{"individual", "all", "maintenance"} {
		t.Run(lane, func(t *testing.T) {
			f, n := newServerQuotaFixture(t)
			ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
			_, a := f.record()
			if !serverQuotaReady(a) {
				t.Fatal("settled server login is not eligible")
			}
			req := requestServerQuota(f)
			switch lane {
			case "individual":
				if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, req)); err != nil {
					t.Fatal(err)
				}
			case "all":
				if _, err := f.client.RefreshAllSubscriptionQuotas(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RefreshAllSubscriptionQuotasRequest{RequestId: string(domain.NewID())})); err != nil {
					t.Fatal(err)
				}
			case "maintenance":
				if err := f.service.queueDueSubscriptionQuotas(ctx, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			}
			_, a = f.record()
			original := *a.Subscription.ServerQuota
			f.service.runServerQuota(ctx, f.input.AccountID)
			_, a = f.record()
			if a.Subscription.ServerQuota.Phase != domain.SubscriptionObservationSucceeded || !a.Subscription.ServerQuota.CleanupConfirmed || a.Subscription.Lease != nil || a.Subscription.OwnerMachineID != "" || len(a.Quota) != 2 || *a.Quota[0].Remaining != 0.2 || *a.Quota[1].Remaining != 0.8 || a.Subscription.QuotaObservedAt == nil {
				t.Fatal("server observation lost original ownership or windows", a.Subscription)
			}
			f.service.runServerQuota(ctx, f.input.AccountID)
			if lane == "individual" {
				replay, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, req))
				if err != nil || !replay.Msg.Replayed {
					t.Fatal("request replay failed", err)
				}
			}
			if err := f.service.finishServerQuota(ctx, f.input.AccountID, original, n.observed, ""); err != nil {
				t.Fatal("finish replay failed", err)
			}
			if err := f.service.queueDueSubscriptionQuotas(ctx, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if n.reads.Load() != 1 {
				t.Fatal("durable replay resent native read")
			}
		})
	}
}
func TestServerQuotaFailurePreservesSuccessAndCleanupFence(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmed", true: "uncertain"}[uncertain], func(t *testing.T) {
			f, n := newServerQuotaFixture(t)
			ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
			req := requestServerQuota(f)
			if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, req)); err != nil {
				t.Fatal(err)
			}
			f.service.runServerQuota(ctx, f.input.AccountID)
			_, old := f.record()
			n.readError = domain.Fail(domain.Unavailable, "Synthetic read failure.", "")
			if uncertain {
				n.closeError = errors.New("synthetic cleanup uncertainty")
			}
			req = requestServerQuota(f)
			if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, req)); err != nil {
				t.Fatal(err)
			}
			f.service.runServerQuota(ctx, f.input.AccountID)
			_, a := f.record()
			if !a.Subscription.QuotaObservedAt.Equal(*old.Subscription.QuotaObservedAt) || len(a.Quota) != 2 || *a.Quota[0].Remaining != 0.2 {
				t.Fatal("failure erased last success")
			}
			if uncertain {
				if a.Subscription.RecoveryRequired || a.Subscription.ServerQuota.Phase != domain.SubscriptionObservationUncertain {
					t.Fatal("uncertain cleanup lost fence")
				}
			} else {
				if a.Subscription.RecoveryRequired || a.Subscription.ServerQuota.Phase != domain.SubscriptionObservationFailed || !serverQuotaReady(a) {
					t.Fatal("confirmed read failure retained false recovery")
				}
			}
			f.service.runServerQuota(ctx, f.input.AccountID)
			if n.reads.Load() != 2 {
				t.Fatal("terminal read relaunched")
			}
		})
	}
}
func TestServerQuotaFencesWorkerLifecycleAndChangedGeneration(t *testing.T) {
	f, n := newServerQuotaFixture(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	req := requestServerQuota(f)
	accepted, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, req))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.take(&pb.RequestSubscriptionResponse{OperationId: accepted.Msg.OperationId}, pb.SubscriptionAction_SUBSCRIPTION_ACTION_QUOTA); err == nil {
		t.Fatal("Worker consumed server quota")
	}
	r, _ := f.record()
	logout := &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT}
	if _, err = f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, logout)); err == nil {
		t.Fatal("logout replaced queued server owner")
	}
	// Publication must not inherit another connection or generation, even after
	// independent native cleanup of the original read.
	n.duringRead = func() {
		replacement := domain.NewID()
		raw := subscriptionTestBundle("quota-server-account", "replacement", time.Now().UTC())
		defer clear(raw)
		if _, err := f.secrets.Put(ctx, credentials.Ref{Owner: f.input.AccountID, ID: replacement, Purpose: credentials.AccountLogin}, raw); err != nil {
			t.Fatal(err)
		}
		_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.server.quota.rotation", nil, func(tx *store.Tx) (any, error) {
			r, a, err := subscriptionAccount(tx, f.input.AccountID, 0)
			if err != nil {
				return nil, err
			}
			a.Subscription.Generation = replacement
			_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
			return nil, err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	f.service.runServerQuota(ctx, f.input.AccountID)
	_, a := f.record()
	if len(a.Quota) != 0 || a.Subscription.ServerQuota.Phase != domain.SubscriptionObservationFailed || a.Subscription.RecoveryRequired {
		t.Fatal("changed generation received stale quota or false recovery")
	}
}
func TestServerQuotaRestartNeverReplaysClaims(t *testing.T) {
	for _, phase := range []domain.SubscriptionObservationPhase{domain.SubscriptionObservationQueued, domain.SubscriptionObservationSending} {
		for _, cleanup := range []bool{false, true} {
			if phase == domain.SubscriptionObservationQueued && cleanup {
				continue
			}
			t.Run(string(phase)+map[bool]string{false: "-unconfirmed", true: "-confirmed"}[cleanup], func(t *testing.T) {
				f, n := newServerQuotaFixture(t)
				ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
				req := requestServerQuota(f)
				if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, req)); err != nil {
					t.Fatal(err)
				}
				_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.server.quota.previous-epoch", nil, func(tx *store.Tx) (any, error) {
					r, a, err := subscriptionAccount(tx, f.input.AccountID, 0)
					if err != nil {
						return nil, err
					}
					o := a.Subscription.ServerQuota
					o.Epoch = domain.NewID()
					o.Phase = phase
					o.CleanupConfirmed = cleanup
					_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
					return nil, err
				})
				if err != nil {
					t.Fatal(err)
				}
				if err = f.service.initializeServerQuotas(ctx); err != nil {
					t.Fatal(err)
				}
				f.service.runServerQuota(ctx, f.input.AccountID)
				_, a := f.record()
				if n.reads.Load() != 0 {
					t.Fatal("restart replayed original native claim")
				}
				wantRecovery := false
				if a.Subscription.RecoveryRequired != wantRecovery {
					t.Fatal("restart confused cleanup evidence")
				}
			})
		}
	}
}

func TestServerQuotaWaitsForFinalLoginReferenceCleanup(t *testing.T) {
	f := newSubscriptionFixture(t)
	f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	n := &serverLoginFixture{started: make(chan struct{}), finish: make(chan struct{}), bundle: subscriptionTestBundle("cleanup-fenced-server-account", "first", time.Now().UTC())}
	done := f.serverRun(n)
	awaitServerFixture(t, n.started)
	f.secrets.referenceError = domain.Fail(domain.Unavailable, "Synthetic final reference failure.", "")
	close(n.finish)
	awaitServerFixture(t, done)
	_, a := f.record()
	if a.Subscription.ServerQuotaGeneration != "" || serverQuotaReady(a) || !a.Subscription.RecoveryRequired {
		t.Fatal("login publication granted quota before protected cleanup")
	}
}
func TestServerQuotaShutdownJoinsAndSettlesCanceledRead(t *testing.T) {
	f, n := newServerQuotaFixture(t)
	ctx, cancel := context.WithCancel(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}))
	defer cancel()
	req := requestServerQuota(f)
	if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, req)); err != nil {
		t.Fatal(err)
	}
	n.duringRead = cancel
	f.service.runServerQuota(ctx, f.input.AccountID)
	_, a := f.record()
	if a.Subscription.ServerQuota.Phase != domain.SubscriptionObservationFailed || !a.Subscription.ServerQuota.CleanupConfirmed || a.Subscription.RecoveryRequired || len(a.Quota) != 0 {
		t.Fatal("shutdown confused confirmed cleanup with native uncertainty")
	}
	f.service.runServerQuota(context.Background(), f.input.AccountID)
	if n.reads.Load() != 1 {
		t.Fatal("canceled original read replayed")
	}
}
func TestServerQuotaUpgradeRequiresOriginalSettledGeneration(t *testing.T) {
	f, n := newServerQuotaFixture(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.server.quota.upgrade", nil, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.Subscription.ServerQuotaGeneration = ""
		a.Subscription.ServerOperation.Epoch = domain.NewID()
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.service.initializeServerQuotas(ctx); err != nil {
		t.Fatal(err)
	}
	_, a := f.record()
	if !serverQuotaReady(a) || n.reads.Load() != 0 {
		t.Fatal("upgrade fabricated read or lost confirmed generation")
	}
}

func TestServerQuotaOmittedMachineUsesIndependentServer(t *testing.T) {
	f := newQuotaFixture(t)
	lease := takeSubscriptionExecutionFixture(t, f)
	req := requestServerQuota(f)
	accepted, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, req))
	if err != nil {
		t.Fatal(err)
	}
	_, a := f.record()
	if a.Subscription.ServerQuota == nil || !a.Subscription.ServerQuota.AccessOnly || a.Subscription.Observation != nil || a.Subscription.Lease.ID != domain.ID(lease.LeaseId) {
		t.Fatal("omitted selector lost independent quota ownership")
	}
	if a.Subscription.ServerQuota.ID != domain.ID(accepted.Msg.OperationId) {
		t.Fatal("quota operation changed")
	}
}
func TestServerQuotaRevokedActorCannotPublishWindows(t *testing.T) {
	f, n := newServerQuotaFixture(t)
	owner := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	id := domain.NewID()
	_, err := f.service.Store.Mutate(owner, domain.NewID(), "fixture.server.quota.client", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.DeviceKind, id, 0, "", "", domain.Device{Name: "Quota fixture client", Type: domain.ClientDevice, PairedAt: time.Now().UTC()})
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: id})
	if _, err = f.service.RequestSubscriptionObservation(actor, connect.NewRequest(requestServerQuota(f))); err != nil {
		t.Fatal(err)
	}
	n.duringRead = func() {
		_, err := f.service.Store.Mutate(owner, domain.NewID(), "fixture.server.quota.revoke", nil, func(tx *store.Tx) (any, error) {
			r, err := tx.Get(domain.DeviceKind, id)
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
	}
	f.service.runServerQuota(owner, f.input.AccountID)
	_, a := f.record()
	if len(a.Quota) != 0 || a.Subscription.ServerQuota.Phase != domain.SubscriptionObservationFailed || a.Subscription.ServerQuota.ErrorCode != domain.Canceled || a.Subscription.RecoveryRequired {
		t.Fatal("revoked read published windows or false recovery")
	}
}
func TestServerQuotaRejectsNonDirectWithoutNativeFallback(t *testing.T) {
	f, n := newServerQuotaFixture(t)
	owner := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	route := domain.NetworkRoute{ProfileID: domain.NewID(), ProfileRevision: 1, Profile: domain.NetworkProfile{ProxyDefinition: domain.ProxyDefinition{Name: "Quota proxy fixture", Mode: domain.ProxyHTTP, Host: "127.0.0.1", Port: 3128}}}
	_, err := f.service.Store.Mutate(owner, domain.NewID(), "fixture.server.quota.route", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.NetworkRouteKind, domain.NewID(), 0, "", "", route)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, requestServerQuota(f))); err != nil {
		t.Fatal(err)
	}
	f.service.runServerQuota(owner, f.input.AccountID)
	_, a := f.record()
	if n.reads.Load() != 0 || a.Subscription.ServerQuota.Phase != domain.SubscriptionObservationFailed || a.Subscription.ServerQuota.ErrorCode != domain.Unsupported || a.Subscription.RecoveryRequired {
		t.Fatal("unsupported routing bypassed native authority")
	}
}

func TestServerQuotaWireOwnership(t *testing.T) {
	for _, field := range []string{"connection", "generation", "missing-connection", "missing-generation"} {
		t.Run(field, func(t *testing.T) {
			f, _ := newServerQuotaFixture(t)
			req := requestServerQuota(f)
			if field == "connection" {
				req.ConnectionId = string(domain.NewID())
			} else if field == "generation" {
				req.GenerationId = string(domain.NewID())
			} else if field == "missing-connection" {
				req.ConnectionId = ""
			} else {
				req.GenerationId = ""
			}
			_, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, req))
			if err == nil {
				t.Fatal("foreign wire owner was admitted")
			}
		})
	}
}
func TestServerQuotaProjectionFailureSettlesCleanedOwner(t *testing.T) {
	for _, count := range []int{63, 64} {
		t.Run(map[int]string{63: "partial", 64: "full"}[count], func(t *testing.T) {
			f, n := newServerQuotaFixture(t)
			ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
			_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.review.full.quota", nil, func(tx *store.Tx) (any, error) {
				r, a, e := subscriptionAccount(tx, f.input.AccountID, 0)
				if e != nil {
					return nil, e
				}
				remaining := 0.8
				for i := 0; i < count; i++ {
					a.Quota = append(a.Quota, domain.QuotaWindow{ID: string(domain.NewID()), ComparisonGroup: "chatgpt", Blocking: true, Remaining: &remaining, ObservedAt: time.Now().UTC(), State: domain.Observed})
				}
				_, e = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
				return nil, e
			})
			if err != nil {
				t.Fatal(err)
			}
			_, before := f.record()
			changed := 0.1
			n.observed.Windows = append([]domain.SubscriptionQuotaWindow{{ID: before.Quota[0].ID, Remaining: &changed}}, n.observed.Windows...)
			req := requestServerQuota(f)
			_, err = f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, req))
			if err != nil {
				t.Fatal(err)
			}
			f.service.runServerQuota(ctx, f.input.AccountID)
			_, a := f.record()
			if n.reads.Load() != 1 {
				t.Fatal("no native read")
			}
			if !reflect.DeepEqual(before.Quota, a.Quota) {
				t.Fatal("partial quota projection published")
			}
			if !a.Subscription.ServerQuota.CleanupConfirmed || a.Subscription.ServerQuota.Phase != domain.SubscriptionObservationFailed || !serverQuotaReady(a) {
				t.Fatal("projection failure did not settle confirmed cleanup")
			}
			f.service.runServerQuota(ctx, f.input.AccountID)
			if n.reads.Load() != 1 {
				t.Fatal("terminal native read retried")
			}
			if a.Subscription.ServerQuota.Active() {
				t.Fatalf("cleaned projection failure retains active %s fence", a.Subscription.ServerQuota.Phase)
			}
		})
	}
}

type quotaNativeFixture struct{ n *serverQuotaFixture }

func (q quotaNativeFixture) ReadExternalQuota(ctx context.Context, id domain.ID, auth codex.QuotaAuthentication) (domain.SubscriptionQuotaObservation, error) {
	if auth.Access == "" || auth.Account == "" {
		return domain.SubscriptionQuotaObservation{}, subscriptionDenied()
	}
	return q.n.ReadManagedQuota(ctx, id)
}
func (q quotaNativeFixture) Close() error { return q.n.closeError }
