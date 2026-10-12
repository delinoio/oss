// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func newPaidAdmissionFixture(t *testing.T) (*subscriptionFixture, *pb.TakeSubscriptionResponse) {
	t.Helper()
	f := newSubscriptionFixture(t)
	clear(f.login())
	lease := takeSubscriptionExecutionFixture(t, f)
	t.Cleanup(func() { clear(lease.Bundle) })
	job, err := f.service.Store.Get(context.Background(), domain.JobKind, f.job)
	if err != nil {
		t.Fatal(err)
	}
	f.register.Mutation.ExpectedRevision = job.Revision
	changePaidAdmissionAccount(t, f, func(a *domain.Account, now time.Time) {
		yes, no, zero, balance := true, false, 0.0, "1250.50"
		a.ConfirmedExhausted = true
		a.Subscription.SpendControlReached, a.Subscription.SpendControlObservedAt = &no, &now
		a.Subscription.PaidCredits = []domain.SubscriptionPaidCreditBucket{{ID: "codex", HasCredits: &yes, Unlimited: &no, Balance: &balance, ObservedAt: now}}
		a.Quota = []domain.QuotaWindow{{ID: "codex:primary", Blocking: true, ComparisonGroup: "chatgpt", Remaining: &zero, ObservedAt: now, State: domain.Observed}}
	})
	return f, lease
}

func changePaidAdmissionAccount(t *testing.T, f *subscriptionFixture, change func(*domain.Account, time.Time)) {
	t.Helper()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.paid-admission-account", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		change(&a, tx.ObservationTime())
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func paidAdmissionDispatchCheck(f *subscriptionFixture) error {
	return f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		_, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return err
		}
		_, machine, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return err
		}
		_, err = checkedExecutionConfiguration(tx, session, machine, f.input)
		return err
	})
}

func TestPaidCreditExecutionDispatchAndNewRegistrationAgree(t *testing.T) {
	for _, test := range []struct {
		name     string
		change   func(*domain.Account, time.Time)
		admitted bool
	}{
		{"fresh has credits", nil, true},
		{"unlimited null balance", func(a *domain.Account, _ time.Time) {
			yes, no := true, false
			a.Subscription.PaidCredits[0].HasCredits = &no
			a.Subscription.PaidCredits[0].Unlimited = &yes
			a.Subscription.PaidCredits[0].Balance = nil
		}, true},
		{"no credits", func(a *domain.Account, _ time.Time) { no := false; a.Subscription.PaidCredits[0].HasCredits = &no }, false},
		{"other bucket", func(a *domain.Account, _ time.Time) { a.Subscription.PaidCredits[0].ID = "other" }, false},
		{"expired paid evidence", func(a *domain.Account, now time.Time) {
			a.Subscription.PaidCredits[0].ObservedAt = now.Add(-6 * time.Minute)
		}, false},
		{"future paid evidence", func(a *domain.Account, now time.Time) {
			a.Subscription.PaidCredits[0].ObservedAt = now.Add(time.Hour)
		}, false},
		{"spend reached", func(a *domain.Account, _ time.Time) { yes := true; a.Subscription.SpendControlReached = &yes }, false},
		{"spend missing", func(a *domain.Account, _ time.Time) {
			a.Subscription.SpendControlReached = nil
			a.Subscription.SpendControlObservedAt = nil
		}, false},
		{"spend expired", func(a *domain.Account, now time.Time) {
			at := now.Add(-6 * time.Minute)
			a.Subscription.SpendControlObservedAt = &at
		}, false},
		{"spend future", func(a *domain.Account, now time.Time) {
			at := now.Add(time.Hour)
			a.Subscription.SpendControlObservedAt = &at
		}, false},
		{"disabled", func(a *domain.Account, _ time.Time) { a.Enabled = false }, false},
		{"authentication failed", func(a *domain.Account, _ time.Time) { a.Health = domain.AccountFailed }, false},
		{"connection replaced", func(a *domain.Account, _ time.Time) { a.Connection.ID = domain.NewID() }, false},
		{"recovery required", func(a *domain.Account, _ time.Time) { a.Subscription.RecoveryRequired = true }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, _ := newPaidAdmissionFixture(t)
			if test.change != nil {
				changePaidAdmissionAccount(t, f, test.change)
			}
			if err := paidAdmissionDispatchCheck(f); (err == nil) != test.admitted {
				t.Fatal("dispatch disagrees with paid admission", err)
			}
			response, err := f.authorityFixture.client.RegisterExecution(context.Background(), subscriptionRequest(f.workerToken, f.register))
			if (err == nil) != test.admitted {
				t.Fatal("new registration disagrees with paid admission", err)
			}
			if test.admitted && response.Msg.ProxyPath != "" {
				t.Fatal("managed credits granted API relay authority")
			}
			_, account := f.record()
			if !account.ConfirmedExhausted || *account.Quota[0].Remaining != 0 {
				t.Fatal("admission changed included exhaustion")
			}
			if !test.admitted {
				if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error { _, err := tx.ExecutionGrantForJob(f.job); return err }); err == nil {
					t.Fatal("rejected new admission retained a grant")
				}
			}
		})
	}
}

func expirePaidAdmission(t *testing.T, f *subscriptionFixture) {
	t.Helper()
	changePaidAdmissionAccount(t, f, func(a *domain.Account, now time.Time) {
		stale := now.Add(-6 * time.Minute)
		a.Subscription.PaidCredits[0].ObservedAt = stale
		a.Subscription.SpendControlObservedAt = &stale
	})
}

func TestPaidCreditRegisteredGrantSurvivesExpiryAndExactReplay(t *testing.T) {
	f, lease := newPaidAdmissionFixture(t)
	f.registerGrant(t)
	expirePaidAdmission(t, f)
	if paidAdmissionDispatchCheck(f) == nil {
		t.Fatal("expired evidence authorized a fresh continuation")
	}
	if replay, err := f.authorityFixture.client.RegisterExecution(context.Background(), subscriptionRequest(f.workerToken, f.register)); err != nil || !replay.Msg.Replayed {
		t.Fatal("expiry rejected original registration receipt", err)
	}
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		grant, err := tx.ExecutionGrantForJob(f.job)
		if err != nil {
			return err
		}
		if _, err := f.service.executionAuthority.scope(tx, grant); err != nil {
			return err
		}
		grant.JobID = domain.NewID()
		if _, err := f.service.executionAuthority.scope(tx, grant); err == nil {
			t.Fatal("foreign grant inherited original authority")
		}
		return nil
	}); err != nil {
		t.Fatal("expiry revoked original publication scope", err)
	}
	// A new receipt may only recover the identical registered binding.
	duplicate := &pb.RegisterExecutionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: f.register.Mutation.Id, ExpectedRevision: f.register.Mutation.ExpectedRevision}, MachineId: f.register.MachineId, InstanceId: f.register.InstanceId, CredentialDigest: f.register.CredentialDigest}
	if _, err := f.authorityFixture.client.RegisterExecution(context.Background(), subscriptionRequest(f.workerToken, duplicate)); err != nil {
		t.Fatal("identical registered binding was treated as new admission", err)
	}
	digest := sha256.Sum256([]byte("distinct synthetic execution token"))
	duplicate.CredentialDigest = digest[:]
	duplicate.Mutation.RequestId = string(domain.NewID())
	if _, err := f.authorityFixture.client.RegisterExecution(context.Background(), subscriptionRequest(f.workerToken, duplicate)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("expiry allowed credential substitution", err)
	}
	if _, err := f.finish(lease, lease.Bundle, true, false, true); err != nil {
		t.Fatal("expiry prevented original confirmed cleanup", err)
	}
	_, account := f.record()
	if account.Subscription.Lease != nil || !account.ConfirmedExhausted {
		t.Fatal("cleanup failed or cleared included exhaustion")
	}
}

func TestPaidCreditRegisteredGrantRetainsLiveOwnershipFences(t *testing.T) {
	for _, scenario := range []string{"lease", "generation", "device"} {
		t.Run(scenario, func(t *testing.T) {
			f, _ := newPaidAdmissionFixture(t)
			f.registerGrant(t)
			expirePaidAdmission(t, f)
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.paid-admission-fence", scenario, func(tx *store.Tx) (any, error) {
				switch scenario {
				case "lease", "generation":
					r, a, err := accountFromTx(tx, f.input.AccountID, 0)
					if err != nil {
						return nil, err
					}
					if scenario == "lease" {
						a.Subscription.Lease.OperationID = domain.NewID()
					} else {
						a.Subscription.Generation = domain.NewID()
						a.Subscription.Lease = nil
					}
					return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
				case "device":
					r, err := tx.Get(domain.DeviceKind, f.device)
					if err != nil {
						return nil, err
					}
					device, err := store.Decode[domain.Device](r)
					if err != nil {
						return nil, err
					}
					at := tx.ObservationTime()
					device.Revoked, device.RevokedAt = true, &at
					return tx.Put(domain.DeviceKind, r.ID, r.Revision, "", "", device)
				}
				return nil, executionDenied()
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
				grant, err := tx.ExecutionGrantForJob(f.job)
				if err != nil {
					return err
				}
				_, err = f.service.executionAuthority.scope(tx, grant)
				return err
			}); err == nil {
				t.Fatal("registered binding bypassed live ownership fence")
			}
		})
	}
}

func TestPaidCreditExpiryPreservesTerminalPublicationAndNativeRejection(t *testing.T) {
	for _, outcome := range []domain.ExecutionOutcome{domain.ExecutionSucceeded, domain.ExecutionFailed} {
		t.Run(string(outcome), func(t *testing.T) {
			f, lease := newPaidAdmissionFixture(t)
			publication := publicationFixtureFromAuthority(t, f.authorityFixture)
			publication.revision = f.register.Mutation.ExpectedRevision
			f.registerGrant(t)
			expirePaidAdmission(t, f)
			publication.publish(t, publication.event(domain.ExecutionThreadBound, 1))
			publication.publish(t, publication.event(domain.ExecutionInputAccepted, 2))
			event := publication.event(domain.ExecutionTurnFinished, 3)
			event.Outcome = outcome
			if outcome == domain.ExecutionFailed {
				event.ProblemCode = domain.ResourceExhausted
			}
			terminal := publication.publish(t, event)
			if replay, err := publication.call(terminal); err != nil || !replay.Msg.Replayed {
				t.Fatal("expiry blocked original terminal receipt", err)
			}
			if _, err := f.finish(lease, lease.Bundle, true, false, true); err != nil {
				t.Fatal("terminal expiry blocked original native cleanup", err)
			}
			row, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.Decode[domain.Session](row)
			if err != nil || session.Outcome != outcome || session.ActiveExecutionID != f.input.ExecutionID || session.InitialExecution.InitialAccountID != f.input.AccountID {
				t.Fatal("native terminal substituted or resumed original execution", err)
			}
			_, account := f.record()
			if !account.ConfirmedExhausted || account.Subscription.Lease != nil {
				t.Fatal("native terminal changed exhaustion or leaked cleanup")
			}
		})
	}
}

func TestPaidCreditsAdmitInitialDispatchWithoutClearingExhaustion(t *testing.T) {
	for _, fresh := range []bool{true, false} {
		t.Run(map[bool]string{true: "fresh", false: "expired"}[fresh], func(t *testing.T) {
			f := newFirstDispatchFixture(t)
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.paid-initial-dispatch", fresh, func(tx *store.Tx) (any, error) {
				ar, account, err := accountFromTx(tx, domain.ID(f.account.Id), 0)
				if err != nil {
					return nil, err
				}
				yes, no, zero := true, false, 0.0
				observed := tx.ObservationTime()
				if !fresh {
					observed = observed.Add(-6 * time.Minute)
				}
				account.ProviderID, account.SubscriptionService = "", domain.SubscriptionChatGPT
				account.Type, account.Connection.Authentication, account.Validation = domain.SubscriptionAccount, domain.SubscriptionAuth, nil
				account.Catalog = nil
				account.ConfirmedExhausted = true
				account.Subscription = &domain.SubscriptionState{Generation: domain.NewID(), IdentityCommitment: strings.Repeat("a", 64), SpendControlReached: &no, SpendControlObservedAt: &observed, PaidCredits: []domain.SubscriptionPaidCreditBucket{{ID: "codex", HasCredits: &yes, Unlimited: &no, ObservedAt: observed}}}
				account.Quota = []domain.QuotaWindow{{ID: "codex:primary", ComparisonGroup: "chatgpt", Blocking: true, Remaining: &zero, ObservedAt: observed, State: domain.Observed}}
				if _, err = tx.Put(domain.AccountKind, ar.ID, ar.Revision, "", "", account); err != nil {
					return nil, err
				}
				if _, err = fixtureInlineSource(tx, domain.ID(f.agent.Id), "", domain.SubscriptionChatGPT); err != nil {
					return nil, err
				}
				mr, machine, err := activeMachine(tx, domain.ID(f.machine.Id))
				if err != nil {
					return nil, err
				}
				machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.ManagedCodexSubscriptionsV1)
				return tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
			})
			if err != nil {
				t.Fatal(err)
			}
			err = f.service.dispatchExecution(context.Background(), f.refresh(t))
			if (err == nil) != fresh {
				t.Fatal("initial dispatch disagrees with freshness", err)
			}
			session, decodeErr := store.Decode[domain.Session](f.refresh(t))
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			input := inputBody(t, currentCatalogResource(t, f.accountFixture, f.change.Input))
			if fresh {
				if session.InitialExecution == nil || session.InitialExecution.InitialAccountID != domain.ID(f.account.Id) || input.Delivery != domain.InputClaimed {
					t.Fatal("paid admission did not preserve original selection and claim")
				}
			} else if session.InitialExecution != nil || input.Delivery != domain.InputQueued {
				t.Fatal("expired admission consumed queued input")
			}
			row, getErr := f.service.Store.Get(context.Background(), domain.AccountKind, domain.ID(f.account.Id))
			if getErr != nil {
				t.Fatal(getErr)
			}
			account, decodeErr := store.Decode[domain.Account](row)
			if decodeErr != nil || !account.ConfirmedExhausted || *account.Quota[0].Remaining != 0 {
				t.Fatal("dispatch changed included exhaustion", decodeErr)
			}
		})
	}
}
