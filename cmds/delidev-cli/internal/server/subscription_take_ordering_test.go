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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/updates"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// takeOrderingFixture starts one queued login for the named service and returns
// the original operation. No native Worker or real account is launched.
func takeOrderingFixture(t *testing.T, service string) (*subscriptionFixture, *pb.RequestSubscriptionResponse) {
	t.Helper()
	if service == "claude" {
		f := newClaudeSubscriptionFixture(t)
		return f, claudeStart(t, f, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	}
	f := newSubscriptionFixture(t)
	return f, f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
}

// holdUnrelatedLease installs an execution lease owned by an operation that is
// unrelated to the queued lifecycle claim.
func holdUnrelatedLease(f *subscriptionFixture, change func(*domain.Account)) {
	f.t.Helper()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.take-ordering", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		if change != nil {
			change(&a)
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return nil, err
	})
	if err != nil {
		f.t.Fatal(err)
	}
}

func unrelatedExecutionLease(f *subscriptionFixture, a *domain.Account) {
	a.Subscription.Lease = &domain.SubscriptionLease{ID: domain.NewID(), OperationID: domain.NewID(), Revision: 1, Action: domain.SubscriptionExecute, MachineID: f.input.MachineID, InstanceID: f.instance, DeviceID: f.device, Epoch: f.service.subscriptionServerEpoch(), Generation: a.Subscription.Generation, StartedAt: time.Now().UTC()}
}

// A definitely unadmitted lifecycle claim must receive the no-grant proof even
// while an unrelated lease is active; only a live matching claim waits.
func TestSubscriptionLifecycleTakeClassifiesOriginalBeforeLeaseContention(t *testing.T) {
	for _, service := range []string{"chatgpt", "claude"} {
		for _, state := range []string{"absent", "replaced", "replaced-other-action", "replaced-other-machine", "canceled", "live", "wrong-action", "wrong-machine", "wrong-phase", "canceled-wrong-action"} {
			for _, leased := range []bool{false, true} {
				name := service + "/" + state
				if leased {
					name += "/leased"
				}
				t.Run(name, func(t *testing.T) {
					if state == "live" && !leased {
						t.Skip("an uncontended live claim is granted; covered by the grant tests")
					}
					f, op := takeOrderingFixture(t, service)
					holdUnrelatedLease(f, func(a *domain.Account) {
						p := a.Subscription.Pending
						switch state {
						case "absent":
							a.Subscription.Pending = nil
						case "replaced":
							p.ID = domain.NewID()
						case "replaced-other-action":
							p.ID = domain.NewID()
							p.Action = domain.SubscriptionRefresh
						case "replaced-other-machine":
							p.ID = domain.NewID()
							p.MachineID = domain.NewID()
						case "canceled":
							p.Canceled = true
						case "wrong-action":
							p.Action = domain.SubscriptionRefresh
						case "wrong-machine":
							p.MachineID = domain.NewID()
						case "wrong-phase":
							p.Phase = domain.SubscriptionClaimed
						case "canceled-wrong-action":
							p.Canceled = true
							p.Action = domain.SubscriptionRefresh
						}
						if leased {
							unrelatedExecutionLease(f, a)
						}
					})
					beforeRecord, before := f.record()
					response, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
					if response != nil {
						t.Fatal("refusal delivered protected bytes")
					}
					problem := domain.SafeError(rpc.ClientError(err))
					switch state {
					case "absent", "replaced", "replaced-other-action", "replaced-other-machine", "canceled":
						if problem.Code != domain.Canceled || problem.Cause != "subscription_take_not_admitted" {
							t.Fatalf("missing exact no-grant proof: %+v", problem)
						}
					case "live":
						if problem.Code != domain.ResourceExhausted || problem.Cause != "" {
							t.Fatalf("live claim behind a lease did not stay busy: %+v", problem)
						}
					default:
						if problem.Cause != "" || problem.Code == domain.Canceled {
							t.Fatalf("unproven refusal carried the no-grant proof: %+v", problem)
						}
						if !leased && problem.Code != domain.RecoveryRequired {
							t.Fatalf("matching claim with wrong fields was not denied: %+v", problem)
						}
					}
					afterRecord, after := f.record()
					if afterRecord.Revision != beforeRecord.Revision || !reflect.DeepEqual(before, after) {
						t.Fatal("refusal changed the replacement, lease or account state")
					}
				})
			}
		}
	}
}

// The no-grant proof is only valid after the Worker, installation and update
// fences. A failing fence must keep its own outcome even for a stale claim.
func TestSubscriptionLifecycleTakeNoGrantProofRequiresWorkerFences(t *testing.T) {
	for _, service := range []string{"chatgpt", "claude"} {
		for _, fence := range []string{"instance", "installation", "update", "recovery"} {
			for _, leased := range []bool{false, true} {
				name := service + "/" + fence
				if leased {
					name += "/leased"
				}
				t.Run(name, func(t *testing.T) {
					f, op := takeOrderingFixture(t, service)
					holdUnrelatedLease(f, func(a *domain.Account) {
						a.Subscription.Pending.ID = domain.NewID()
						a.Subscription.Pending.Action = domain.SubscriptionRefresh
						a.Subscription.RecoveryRequired = fence == "recovery"
						if leased {
							unrelatedExecutionLease(f, a)
						}
					})
					instance := f.instance
					switch fence {
					case "instance":
						instance = domain.NewID()
					case "installation", "update":
						_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.take-ordering-fence", nil, func(tx *store.Tx) (any, error) {
							if fence == "update" {
								return tx.Put(domain.UpdateKind, domain.NewID(), 0, "", "", updates.Operation{ServerID: f.service.Identity.ServerID, Actor: domain.Principal{Type: domain.OwnerDevice}, Component: updates.Worker, State: updates.Waiting, MachineID: f.input.MachineID, DeviceID: f.device})
							}
							r, m, err := activeMachine(tx, f.input.MachineID)
							if err != nil {
								return nil, err
							}
							m.Installations = nil
							return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
						})
						if err != nil {
							t.Fatal(err)
						}
					}
					r, before := f.record()
					response, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), InstanceId: string(instance), OperationId: op.OperationId, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN}))
					problem := domain.SafeError(rpc.ClientError(err))
					if response != nil || err == nil || problem.Cause != "" {
						t.Fatalf("failing fence supplied the no-grant proof: %+v", problem)
					}
					afterRecord, after := f.record()
					if afterRecord.Revision != r.Revision || !reflect.DeepEqual(before, after) {
						t.Fatal("refused claim changed account state")
					}
				})
			}
		}
	}
}

// ChatGPT additionally treats an exclusive server observation or a
// native-started server operation as contention. A stale claim still gets the
// proof; a live claim stays busy.
func TestSubscriptionLifecycleTakeServerContentionOrdering(t *testing.T) {
	for _, stale := range []bool{true, false} {
		name := "live"
		if stale {
			name = "stale"
		}
		t.Run(name, func(t *testing.T) {
			f := newSubscriptionFixture(t)
			op := f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
			holdUnrelatedLease(f, func(a *domain.Account) {
				if stale {
					a.Subscription.Pending.ID = domain.NewID()
				}
				a.Subscription.ServerOperation = &domain.ServerSubscriptionOperation{ID: domain.NewID(), NativeStarted: true}
			})
			beforeRecord, before := f.record()
			response, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
			problem := domain.SafeError(rpc.ClientError(err))
			if response != nil {
				t.Fatal("refusal delivered protected bytes")
			}
			if stale && (problem.Code != domain.Canceled || problem.Cause != "subscription_take_not_admitted") {
				t.Fatalf("stale claim lost the no-grant proof behind server contention: %+v", problem)
			}
			if !stale && (problem.Code != domain.ResourceExhausted || problem.Cause != "") {
				t.Fatalf("live claim did not stay busy behind server contention: %+v", problem)
			}
			afterRecord, after := f.record()
			if afterRecord.Revision != beforeRecord.Revision || !reflect.DeepEqual(before, after) {
				t.Fatal("refusal changed account state")
			}
		})
	}
}

// Execution Take keeps contention first and never receives the lifecycle proof.
func TestSubscriptionExecuteTakeKeepsContentionFirst(t *testing.T) {
	for _, service := range []string{"chatgpt", "claude"} {
		t.Run(service, func(t *testing.T) {
			f, op := takeOrderingFixture(t, service)
			holdUnrelatedLease(f, func(a *domain.Account) {
				a.Subscription.Pending = nil
				unrelatedExecutionLease(f, a)
			})
			r, _ := f.record()
			response, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), InstanceId: string(domain.NewID()), OperationId: op.OperationId, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE}))
			problem := domain.SafeError(rpc.ClientError(err))
			if response != nil || problem.Code != domain.ResourceExhausted || problem.Cause != "" {
				t.Fatalf("execute Take did not return contention first: %+v", problem)
			}
		})
	}
}
