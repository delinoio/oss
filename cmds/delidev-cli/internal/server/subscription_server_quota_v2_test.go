// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func installV2QuotaFixture(f *subscriptionFixture) *serverQuotaFixture {
	remaining := .6
	n := &serverQuotaFixture{observed: domain.SubscriptionQuotaObservation{ObservedAt: time.Now().UTC(), Windows: []domain.SubscriptionQuotaWindow{{ID: "codex:primary", Remaining: &remaining}}}}
	f.service.quotaOpen = func(context.Context, string, domain.ID, codex.QuotaAuthentication, *slog.Logger) (serverQuotaNative, error) {
		return quotaNativeFixture{n}, nil
	}
	return n
}
func TestServerQuotaV2WorkerIndependentManualBatchMaintenance(t *testing.T) {
	for _, active := range []bool{false, true} {
		for _, lane := range []string{"manual", "all", "maintenance"} {
			t.Run(lane+map[bool]string{true: "-execute", false: "-worker-owned-idle"}[active], func(t *testing.T) {
				f := newQuotaFixture(t)
				n := installV2QuotaFixture(f)
				if active {
					takeSubscriptionExecutionFixture(t, f)
				}
				ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
				_, before := f.record()
				_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.quota.v2.remove-worker", nil, func(tx *store.Tx) (any, error) {
					r, err := tx.Get(domain.MachineKind, f.input.MachineID)
					if err != nil {
						return nil, err
					}
					return nil, tx.Delete(domain.MachineKind, r.ID, r.Revision)
				})
				if err != nil {
					t.Fatal(err)
				}
				switch lane {
				case "manual":
					_, err = f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, requestServerQuota(f)))
				case "all":
					_, err = f.client.RefreshAllSubscriptionQuotas(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RefreshAllSubscriptionQuotasRequest{RequestId: string(domain.NewID())}))
				case "maintenance":
					err = f.service.queueDueSubscriptionQuotas(ctx, time.Now().UTC())
				}
				if err != nil {
					t.Fatal(err)
				}
				f.service.runServerQuota(ctx, f.input.AccountID)
				_, after := f.record()
				if n.reads.Load() != 1 || after.Subscription.ServerQuota.Phase != domain.SubscriptionObservationSucceeded || after.Subscription.Observation != nil || !reflect.DeepEqual(after.Subscription.Lease, before.Subscription.Lease) || after.Subscription.OwnerMachineID != before.Subscription.OwnerMachineID || after.Subscription.Generation != before.Subscription.Generation || after.Health != before.Health {
					t.Fatal("quota modified execution ownership or required absent Worker")
				}
				f.service.runServerQuota(ctx, f.input.AccountID)
				if n.reads.Load() != 1 {
					t.Fatal("terminal replay resent quota")
				}
			})
		}
	}
}
func TestServerQuotaV2ExecutionRotationRetainsCapturedReference(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "joined", true: "uncertain"}[uncertain], func(t *testing.T) {
			f := newQuotaFixture(t)
			lease := takeSubscriptionExecutionFixture(t, f)
			n := installV2QuotaFixture(f)
			ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
			_, before := f.record()
			old := before.Subscription.Generation
			_, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, requestServerQuota(f)))
			if err != nil {
				t.Fatal(err)
			}
			if uncertain {
				n.closeError = errors.New("synthetic quota cleanup uncertainty")
			}
			n.duringRead = func() {
				raw := subscriptionTestBundle("fixture-account", "second", time.Now().UTC())
				defer clear(raw)
				if _, err := f.finish(lease, raw, true, false, true); err != nil {
					t.Fatal(err)
				}
				vault, err := f.service.secrets()
				if err != nil {
					t.Fatal(err)
				}
				retained, err := vault.Get(ctx, credentials.Ref{Owner: f.input.AccountID, ID: old, Purpose: credentials.AccountLogin})
				clear(retained)
				if err != nil {
					t.Fatal("rotation deleted observer reference", err)
				}
			}
			f.service.runServerQuota(ctx, f.input.AccountID)
			_, after := f.record()
			if after.Subscription.Generation == old || after.Subscription.Lease != nil || after.Subscription.RecoveryRequired || after.Health != domain.AccountReady || len(after.Quota) != 0 {
				t.Fatal("quota overwrote successful execution or published stale result")
			}
			if uncertain && (after.Subscription.ServerQuota.Phase != domain.SubscriptionObservationUncertain || serverQuotaReady(after)) {
				t.Fatal("independent cleanup fence lost")
			}
			f.service.runServerQuota(ctx, f.input.AccountID)
			if n.reads.Load() != 1 {
				t.Fatal("uncertain send replayed")
			}
		})
	}
}
func TestServerQuotaV2UncertainCleanupPreservesOriginalExecute(t *testing.T) {
	f := newQuotaFixture(t)
	takeSubscriptionExecutionFixture(t, f)
	n := installV2QuotaFixture(f)
	n.closeError = errors.New("synthetic uncertainty")
	_, before := f.record()
	_, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, requestServerQuota(f)))
	if err != nil {
		t.Fatal(err)
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	f.service.runServerQuota(ctx, f.input.AccountID)
	record, after := f.record()
	if !reflect.DeepEqual(after.Subscription.Lease, before.Subscription.Lease) || after.Health != domain.AccountReady || after.Subscription.RecoveryRequired || after.Subscription.ServerQuota.Phase != domain.SubscriptionObservationUncertain {
		t.Fatal("quota cleanup poisoned execution")
	}
	if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, requestServerQuota(f))); err == nil {
		t.Fatal("uncertain cleanup allowed another quota")
	}
	if _, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: record.Revision}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT})); err == nil {
		t.Fatal("uncertain quota released protected deletion")
	}
}

func TestServerQuotaV2RestartReconcilesOnlyOriginalCleanup(t *testing.T) {
	for _, evidence := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing-owner-index", true: "retained-owner-index"}[evidence], func(t *testing.T) {
			f, n := newServerQuotaFixture(t)
			ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
			_, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, requestServerQuota(f)))
			if err != nil {
				t.Fatal(err)
			}
			var op domain.ID
			_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.quota.previous-send", nil, func(tx *store.Tx) (any, error) {
				r, a, err := subscriptionAccount(tx, f.input.AccountID, 0)
				if err != nil {
					return nil, err
				}
				o := a.Subscription.ServerQuota
				o.Epoch = domain.NewID()
				o.Phase = domain.SubscriptionObservationSending
				op = o.ID
				_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
				return nil, err
			})
			if err != nil {
				t.Fatal(err)
			}
			if evidence {
				runtimeRoot := filepath.Join(f.service.Store.Root(), "subscription-runtime")
				// This synthetic retained empty owner index models a completed/pruned
				// original journal. Missing indexes must never stand in for this proof.
				for _, path := range []string{runtimeRoot, filepath.Join(runtimeRoot, "processes"), filepath.Join(runtimeRoot, "processes", string(op)), filepath.Join(runtimeRoot, "quota"), filepath.Join(runtimeRoot, "quota", string(op))} {
					if err := security.PrivateDir(path); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := f.service.initializeServerQuotas(ctx); err != nil {
				t.Fatal(err)
			}
			f.service.runServerQuota(ctx, f.input.AccountID)
			_, a := f.record()
			if n.reads.Load() != 0 || a.Subscription.RecoveryRequired || a.Subscription.ServerQuota.CleanupConfirmed != evidence {
				t.Fatal("restart confused cleanup with quota replay")
			}
			if evidence {
				if _, err := os.Lstat(filepath.Join(f.service.Store.Root(), "subscription-runtime", "quota", string(op))); !os.IsNotExist(err) {
					t.Fatal("original quota runtime remained")
				}
			} else if a.Subscription.ServerQuota.Phase != domain.SubscriptionObservationUncertain {
				t.Fatal("missing owner proof released obligation")
			}
		})
	}
}
