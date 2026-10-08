// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func activeUndiscoveredQuotaFixture(t *testing.T, missing bool) (*subscriptionFixture, domain.SubscriptionLease) {
	t.Helper()
	f := newQuotaFixture(t)
	var original domain.SubscriptionLease
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.direct.active.quota", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		original = domain.SubscriptionLease{ID: domain.NewID(), OperationID: domain.NewID(), Revision: r.Revision + 1, Action: domain.SubscriptionExecute, MachineID: f.input.MachineID, InstanceID: f.instance, DeviceID: f.device, Epoch: f.service.subscriptionServerEpoch(), Generation: a.Subscription.Generation, StartedAt: time.Now().UTC()}
		a.Subscription.Lease = &original
		if _, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		r, err = tx.Get(domain.MachineKind, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		m, err := store.Decode[domain.Machine](r)
		if err != nil {
			return nil, err
		}
		if missing {
			m.Installations = nil
		} else {
			m.Installations[0].Version = "0.159.2"
			m.Installations[0].ProtocolVerified = false
		}
		_, err = tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return f, original
}
func activeQuotaRequest(f *subscriptionFixture, machine domain.ID) *pb.RequestSubscriptionObservationRequest {
	r, a := f.record()
	return &pb.RequestSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(machine), Action: pb.SubscriptionObservationAction_SUBSCRIPTION_OBSERVATION_ACTION_QUOTA, ConnectionId: string(a.Connection.ID), GenerationId: string(a.Subscription.Generation)}
}
func TestActiveQuotaWithoutSavedInstallationUsesOriginalLease(t *testing.T) {
	for _, missing := range []bool{true, false} {
		for _, omitted := range []bool{true, false} {
			t.Run(map[bool]string{true: "missing", false: "unverified"}[missing]+map[bool]string{true: "-omitted", false: "-explicit"}[omitted], func(t *testing.T) {
				f, original := activeUndiscoveredQuotaFixture(t, missing)
				machine := original.MachineID
				if omitted {
					machine = ""
				}
				request := activeQuotaRequest(f, machine)
				response, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
				if err != nil {
					t.Fatal(err)
				}
				if replay, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, request)); err != nil || !replay.Msg.Replayed {
					t.Fatal("acceptance replay changed owner", err)
				}
				if omitted {
					_, a := f.record()
					if a.Subscription.ServerQuota == nil || !a.Subscription.ServerQuota.AccessOnly || a.Subscription.Observation != nil || !reflect.DeepEqual(*a.Subscription.Lease, original) {
						t.Fatal("omitted quota did not retain independent server ownership")
					}
					return
				}
				lease := &pb.TakeSubscriptionResponse{LeaseId: string(original.ID), LeaseRevision: original.Revision, GenerationId: string(original.Generation)}
				claim := f.claimObservation(response.Msg.OperationId, lease)
				if _, err := f.client.ClaimSubscriptionObservation(context.Background(), subscriptionRequest(f.workerToken, claim)); rpc.ClientError(err).Code != domain.RecoveryRequired {
					t.Fatal("replay granted second native read", err)
				}
				now, remaining := time.Now().UTC(), 0.4
				f.publishObservation(response.Msg.OperationId, lease, domain.SubscriptionObservationResult{Quota: &domain.SubscriptionQuotaObservation{ObservedAt: now, Windows: []domain.SubscriptionQuotaWindow{{ID: "codex:primary", Remaining: &remaining}}}})
				_, a := f.record()
				if !reflect.DeepEqual(*a.Subscription.Lease, original) || a.Subscription.Observation.MachineID != original.MachineID || !a.Subscription.QuotaObservedAt.Equal(now) {
					t.Fatal("quota changed execution ownership")
				}
				if _, err := f.take(&pb.RequestSubscriptionResponse{OperationId: response.Msg.OperationId}, pb.SubscriptionAction_SUBSCRIPTION_ACTION_QUOTA); rpc.ClientError(err).Code != domain.ResourceExhausted {
					t.Fatal("second native owner admitted", err)
				}
			})
		}
	}
}
func TestActiveQuotaBatchAndMaintenanceShareEligibility(t *testing.T) {
	for _, maintenance := range []bool{false, true} {
		t.Run(map[bool]string{true: "maintenance", false: "refresh-all"}[maintenance], func(t *testing.T) {
			f, original := activeUndiscoveredQuotaFixture(t, true)
			if maintenance {
				if err := f.service.queueDueSubscriptionQuotas(context.Background(), time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			} else {
				response, err := f.client.RefreshAllSubscriptionQuotas(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RefreshAllSubscriptionQuotasRequest{RequestId: string(domain.NewID())}))
				if err != nil || len(response.Msg.Accounts) != 1 || response.Msg.Accounts[0] != string(f.input.AccountID) {
					t.Fatal("active account excluded", response, err)
				}
			}
			_, a := f.record()
			if a.Subscription.ServerQuota == nil || !a.Subscription.ServerQuota.AccessOnly || a.Subscription.Observation != nil || !reflect.DeepEqual(*a.Subscription.Lease, original) {
				t.Fatal("batch replaced original owner")
			}
		})
	}
}
func TestActiveQuotaAdmissionRejectsChangedAuthority(t *testing.T) {
	for _, kind := range []string{"machine", "generation", "connection", "instance", "epoch", "revoked-worker", "disabled-machine", "missing-capability"} {
		t.Run(kind, func(t *testing.T) {
			f, original := activeUndiscoveredQuotaFixture(t, true)
			request := activeQuotaRequest(f, original.MachineID)
			if kind == "machine" {
				request.MachineId = string(domain.NewID())
			}
			if kind == "generation" {
				request.GenerationId = string(domain.NewID())
			}
			if kind == "connection" {
				request.ConnectionId = string(domain.NewID())
			}
			if kind != "machine" && kind != "generation" && kind != "connection" {
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.active.quota.deny", nil, func(tx *store.Tx) (any, error) {
					if kind == "instance" {
						return nil, tx.SetWorkerInstance(original.MachineID, domain.NewID(), time.Now().UTC())
					}
					if kind == "epoch" {
						r, a, err := accountFromTx(tx, f.input.AccountID, 0)
						if err != nil {
							return nil, err
						}
						a.Subscription.Lease.Epoch = domain.NewID()
						_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
						return nil, err
					}
					if kind == "revoked-worker" {
						r, err := tx.Get(domain.DeviceKind, original.DeviceID)
						if err != nil {
							return nil, err
						}
						d, err := store.Decode[domain.Device](r)
						if err != nil {
							return nil, err
						}
						d.Revoked = true
						at := time.Now().UTC()
						d.RevokedAt = &at
						_, err = tx.Put(domain.DeviceKind, r.ID, r.Revision, "", "", d)
						return nil, err
					}
					r, err := tx.Get(domain.MachineKind, original.MachineID)
					if err != nil {
						return nil, err
					}
					m, err := store.Decode[domain.Machine](r)
					if err != nil {
						return nil, err
					}
					if kind == "disabled-machine" {
						m.Disabled = true
					} else {
						m.WorkerCapabilities = []domain.WorkerCapability{domain.ManagedCodexSubscriptionsV1}
					}
					_, err = tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
					return nil, err
				})
				if err != nil {
					t.Fatal(err)
				}
				request = activeQuotaRequest(f, original.MachineID)
			}
			if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, request)); err == nil {
				t.Fatal("changed authority accepted")
			}
			var candidates []store.Record
			if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
				var err error
				candidates, err = subscriptionQuotaCandidates(tx, time.Now().UTC(), false, f.service.subscriptionServerEpoch())
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if len(candidates) != 1 {
				t.Fatal("server quota incorrectly depends on Worker authority")
			}
			_, a := f.record()
			if a.Subscription.Observation != nil {
				t.Fatal("refusal granted native work")
			}
		})
	}
}
func TestActiveQuotaDoesNotRelaxIdleOrResetCreditDiscovery(t *testing.T) {
	f, original := activeUndiscoveredQuotaFixture(t, true)
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		if observationMachine(tx, original.MachineID) == nil {
			t.Fatal("reset-credit discovery relaxed")
		}
		_, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return err
		}
		a.Subscription.Lease = nil
		if quotaObservationMachine(tx, a, original.MachineID, f.service.subscriptionServerEpoch()) == nil {
			t.Fatal("idle discovery relaxed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestActiveQuotaClaimAndPublicationRetainOriginalAuthority(t *testing.T) {
	for _, publish := range []bool{false, true} {
		for _, kind := range []string{"instance", "generation", "connection", "actor", "account-revoked"} {
			t.Run(map[bool]string{true: "publish-", false: "claim-"}[publish]+kind, func(t *testing.T) {
				f, original := activeUndiscoveredQuotaFixture(t, true)
				op := f.requestQuota()
				lease := &pb.TakeSubscriptionResponse{LeaseId: string(original.ID), LeaseRevision: original.Revision, GenerationId: string(original.Generation)}
				if publish {
					f.claimObservation(op.OperationId, lease)
				}
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.active.quota.changed", nil, func(tx *store.Tx) (any, error) {
					if kind == "instance" {
						return nil, tx.SetWorkerInstance(original.MachineID, domain.NewID(), time.Now().UTC())
					}
					r, a, err := accountFromTx(tx, f.input.AccountID, 0)
					if err != nil {
						return nil, err
					}
					switch kind {
					case "generation":
						a.Subscription.Generation = domain.NewID()
					case "connection":
						a.Connection.ID = domain.NewID()
					case "actor":
						a.Subscription.Observation.Actor = domain.Principal{Type: domain.ClientDevice, DeviceID: domain.NewID()}
					case "account-revoked":
						a.Health = domain.AccountFailed
						a.Subscription.RecoveryRequired = true
					}
					_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
					return nil, err
				})
				if err != nil {
					t.Fatal(err)
				}
				if publish {
					raw := []byte(`{"quota_error":"unavailable"}`)
					req := &pb.PublishSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: original.Revision}, LeaseId: string(original.ID), MachineId: string(original.MachineID), InstanceId: string(original.InstanceID), OperationId: op.OperationId, GenerationId: string(original.Generation), ObservationJson: raw}
					if _, err := f.client.PublishSubscriptionObservation(context.Background(), subscriptionRequest(f.workerToken, req)); err == nil {
						t.Fatal("changed authority published read")
					}
				} else {
					req := &pb.ClaimSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: original.Revision}, LeaseId: string(original.ID), MachineId: string(original.MachineID), InstanceId: string(original.InstanceID), OperationId: op.OperationId, GenerationId: string(original.Generation)}
					if _, err := f.client.ClaimSubscriptionObservation(context.Background(), subscriptionRequest(f.workerToken, req)); err == nil {
						t.Fatal("changed authority claimed native read")
					}
				}
			})
		}
	}
}
