// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"testing"
	"time"
)

func automaticCreditFixture(t *testing.T, enabled, stale bool) (*subscriptionFixture, domain.SubscriptionQuotaBlock) {
	f := newQuotaFixture(t)
	block := domain.SubscriptionQuotaBlock{SessionID: f.input.SessionID, ExecutionID: f.input.ExecutionID, NativeThreadID: domain.NativeIdentity(domain.NewID()), NativeTurnID: domain.NativeIdentity(domain.NewID()), Reason: domain.CodexUsageLimitExceeded}
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.automatic", nil, func(tx *store.Tx) (any, error) {
		mr, e := tx.Get(domain.MachineKind, f.input.MachineID)
		if e != nil {
			return nil, e
		}
		machine, e := store.Decode[domain.Machine](mr)
		if e != nil {
			return nil, e
		}
		machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.CodexQuotaBlockV1)
		if _, e = tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine); e != nil {
			return nil, e
		}
		r, a, e := accountFromTx(tx, f.input.AccountID, 0)
		if e != nil {
			return nil, e
		}
		now := tx.ObservationTime()
		a.Subscription.Lease = &domain.SubscriptionLease{ID: domain.NewID(), OperationID: f.job, Revision: r.Revision + 1, Action: domain.SubscriptionExecute, MachineID: f.input.MachineID, InstanceID: f.instance, DeviceID: f.device, Epoch: f.service.subscriptionServerEpoch(), Generation: a.Subscription.Generation, StartedAt: now}
		if enabled {
			a.Subscription.AutomaticCreditConsent = &domain.AutomaticResetCreditConsent{Actor: domain.Principal{Type: domain.OwnerDevice}, ConnectionID: a.Connection.ID, Generation: a.Subscription.Generation, ConfirmedAt: now}
		}
		observed := now
		if stale {
			observed = now.Add(-6 * time.Minute)
		}
		a.Subscription.ResetCredits = &domain.SubscriptionResetCredits{ObservationID: domain.NewID(), ObservedAt: observed, AvailableCount: 2}
		if _, e = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); e != nil {
			return nil, e
		}
		jr, e := tx.Get(domain.JobKind, f.job)
		if e != nil {
			return nil, e
		}
		job, e := store.Decode[domain.Job](jr)
		if e != nil {
			return nil, e
		}
		input := f.input
		input.ConnectionID = a.Connection.ID
		input.Configuration.Subscription = true
		input.ConfigurationDigest, _ = input.Configuration.Digest()
		job.Input, _ = json.Marshal(input)
		if _, e = tx.PutJob(jr.ID, jr.Revision, f.input.SessionID, "", job); e != nil {
			return nil, e
		}
		sr, session, e := sessionRecord(tx, f.input.SessionID)
		if e != nil {
			return nil, e
		}
		session.InitialExecution.ConnectionID = a.Connection.ID
		session.InitialExecution.Configuration = input.Configuration
		session.InitialExecution.ConfigurationDigest = input.ConfigurationDigest
		session.Execution = &domain.ExecutionProgress{JobID: f.job, InputID: f.input.InputID, ExecutionID: f.input.ExecutionID, NativeThreadID: string(block.NativeThreadID), NativeTurnID: string(block.NativeTurnID), Outcome: domain.ExecutionFailed}
		_, e = tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.SessionID, "", session)
		return nil, e
	})
	if err != nil {
		t.Fatal(err)
	}
	return f, block
}
func automaticFixtureMutation(t *testing.T, f *subscriptionFixture, mutate func(*store.Tx, *domain.Account) error) {
	t.Helper()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.automatic.mutate", nil, func(tx *store.Tx) (any, error) {
		r, a, e := accountFromTx(tx, f.input.AccountID, 0)
		if e != nil {
			return nil, e
		}
		if e = mutate(tx, &a); e != nil {
			return nil, e
		}
		_, e = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return nil, e
	})
	if err != nil {
		t.Fatal(err)
	}
}
func admitFixture(t *testing.T, f *subscriptionFixture, b *domain.SubscriptionQuotaBlock, completed domain.ID) {
	automaticFixtureMutation(t, f, func(tx *store.Tx, a *domain.Account) error {
		return f.service.admitAutomaticCredit(tx, f.input.AccountID, a, b, completed)
	})
}
func TestAutomaticCreditDefaultOffAndOriginalOnceOnlyClaim(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[enabled], func(t *testing.T) {
			f, b := automaticCreditFixture(t, enabled, false)
			admitFixture(t, f, &b, "")
			_, a := f.record()
			if !enabled {
				if a.Subscription.Observation != nil || a.Subscription.AutomaticCreditEpisode != nil {
					t.Fatal("off admitted spending")
				}
				return
			}
			op := *a.Subscription.Observation
			if op.Action != domain.SubscriptionResetCredit || !op.NextCredit || op.AutomaticLeaseID != a.Subscription.Lease.ID {
				t.Fatal("original authority lost")
			}
			admitFixture(t, f, &b, "")
			_, a = f.record()
			if a.Subscription.Observation.ID != op.ID {
				t.Fatal("duplicate spent twice")
			}
			lease := a.Subscription.Lease
			f.claimObservation(string(op.ID), &pb.TakeSubscriptionResponse{LeaseId: string(lease.ID), LeaseRevision: lease.Revision, GenerationId: string(lease.Generation)})
			admitFixture(t, f, &b, "")
			_, a = f.record()
			if a.Subscription.Observation.ID != op.ID || a.Subscription.Observation.Phase != domain.SubscriptionObservationSending {
				t.Fatal("sending claim replayed")
			}
		})
	}
}
func TestAutomaticCreditStaleRefreshOnceAndRecoveredRedelivery(t *testing.T) {
	f, b := automaticCreditFixture(t, true, true)
	admitFixture(t, f, &b, "")
	_, a := f.record()
	refresh := a.Subscription.Observation.ID
	if a.Subscription.Observation.Action != domain.SubscriptionQuota {
		t.Fatal("stale inventory spent")
	}
	admitFixture(t, f, &b, "")
	_, a = f.record()
	if a.Subscription.Observation.ID != refresh {
		t.Fatal("refreshed twice")
	}
	automaticFixtureMutation(t, f, func(tx *store.Tx, a *domain.Account) error {
		a.Subscription.Observation.Phase = domain.SubscriptionObservationSucceeded
		a.Subscription.ResetCredits.ObservedAt = tx.ObservationTime()
		return f.service.admitAutomaticCredit(tx, f.input.AccountID, a, nil, refresh)
	})
	_, a = f.record()
	consume := a.Subscription.Observation.ID
	if consume == refresh || a.Subscription.Observation.Action != domain.SubscriptionResetCredit {
		t.Fatal("fresh inventory not consumed")
	}
	automaticFixtureMutation(t, f, func(_ *store.Tx, a *domain.Account) error {
		a.Subscription.AutomaticCreditEpisode = nil
		a.Subscription.Observation.Phase = domain.SubscriptionObservationSucceeded
		return nil
	})
	admitFixture(t, f, &b, "")
	_, a = f.record()
	if a.Subscription.Observation.ID != consume || a.Subscription.AutomaticCreditEpisode != nil {
		t.Fatal("redelivery rearmed original failed turn")
	}
}
func TestAutomaticCreditRateLimitNeedsSubscriptionExhaustion(t *testing.T) {
	f, b := automaticCreditFixture(t, true, false)
	b.Reason = domain.CodexRateLimitExceeded
	admitFixture(t, f, &b, "")
	_, a := f.record()
	if a.Subscription.Observation != nil || a.Subscription.AutomaticCreditEpisode != nil {
		t.Fatal("generic rate limit spent")
	}
}
func TestAutomaticCreditConsentExactConfirmationAndLogout(t *testing.T) {
	f := newQuotaFixture(t)
	r, a := f.record()
	q := &pb.SetAutomaticResetCreditConsentRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, ConnectionId: string(a.Connection.ID), GenerationId: string(a.Subscription.Generation), Enabled: true}
	if _, e := f.client.SetAutomaticResetCreditConsent(context.Background(), subscriptionRequest(f.service.Identity.Token, q)); e == nil {
		t.Fatal("unconfirmed accepted")
	}
	q.Mutation.RequestId = string(domain.NewID())
	q.Confirmed = true
	v, e := f.client.SetAutomaticResetCreditConsent(context.Background(), subscriptionRequest(f.service.Identity.Token, q))
	if e != nil {
		t.Fatal(e)
	}
	if replay, e := f.client.SetAutomaticResetCreditConsent(context.Background(), subscriptionRequest(f.service.Identity.Token, q)); e != nil || !replay.Msg.Replayed {
		t.Fatal("replay changed authority", e)
	}
	q.Mutation = &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: v.Msg.Account.Revision}
	q.GenerationId = string(domain.NewID())
	if _, e = f.client.SetAutomaticResetCreditConsent(context.Background(), subscriptionRequest(f.service.Identity.Token, q)); e == nil {
		t.Fatal("generation inherited consent")
	}
	f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	_, a = f.record()
	if a.Subscription.AutomaticCreditConsent != nil {
		t.Fatal("logout retained consent")
	}
}

func TestAutomaticCreditPublicationOriginalOwnerAndDisableAfterAdmission(t *testing.T) {
	f, b := automaticCreditFixture(t, true, false)
	_, a := f.record()
	lease := a.Subscription.Lease
	f.publishObservation("", &pb.TakeSubscriptionResponse{LeaseId: string(lease.ID), LeaseRevision: lease.Revision, GenerationId: string(lease.Generation)}, domain.SubscriptionObservationResult{QuotaBlock: &b})
	r, a := f.record()
	op := a.Subscription.Observation.ID
	_, err := f.client.SetAutomaticResetCreditConsent(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.SetAutomaticResetCreditConsentRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, ConnectionId: string(a.Connection.ID), GenerationId: string(a.Subscription.Generation)}))
	if err != nil {
		t.Fatal(err)
	}
	f.claimObservation(string(op), &pb.TakeSubscriptionResponse{LeaseId: string(lease.ID), LeaseRevision: lease.Revision, GenerationId: string(lease.Generation)})
	_, a = f.record()
	if a.Subscription.AutomaticCreditConsent != nil || a.Subscription.Observation.Phase != domain.SubscriptionObservationSending {
		t.Fatal("disable cancelled admitted settlement")
	}
}
func TestAutomaticCreditRejectsForeignAndRetiredProof(t *testing.T) {
	for _, change := range []string{"turn", "epoch", "cleanup", "mode"} {
		t.Run(change, func(t *testing.T) {
			f, b := automaticCreditFixture(t, true, false)
			automaticFixtureMutation(t, f, func(tx *store.Tx, a *domain.Account) error {
				switch change {
				case "epoch":
					a.Subscription.Lease.Epoch = domain.NewID()
				case "turn":
					b.NativeTurnID = domain.NativeIdentity(domain.NewID())
				case "cleanup":
					sr, s, e := sessionRecord(tx, b.SessionID)
					if e != nil {
						return e
					}
					s.Execution.CleanupVerified = true
					_, e = tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.SessionID, "", s)
					return e
				case "mode":
					jr, e := tx.Get(domain.JobKind, f.job)
					if e != nil {
						return e
					}
					j, e := store.Decode[domain.Job](jr)
					if e != nil {
						return e
					}
					var input domain.ExecutionJobInput
					if e = domain.Decode(j.Input, &input); e != nil {
						return e
					}
					input.Input.Mode = domain.PlanMode
					j.Input, _ = json.Marshal(input)
					_, e = tx.PutJob(jr.ID, jr.Revision, b.SessionID, "", j)
					return e
				}
				return nil
			})
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.reject", nil, func(tx *store.Tx) (any, error) {
				_, a, e := accountFromTx(tx, f.input.AccountID, 0)
				if e != nil {
					return nil, e
				}
				return nil, f.service.admitAutomaticCredit(tx, f.input.AccountID, &a, &b, "")
			})
			if err == nil {
				t.Fatal("foreign or retired authority accepted")
			}
			_, a := f.record()
			if a.Subscription.Observation != nil {
				t.Fatal("rejection persisted spending")
			}
		})
	}
}
func TestAutomaticCreditFailedRefreshAndFreshRateLimitProof(t *testing.T) {
	f, b := automaticCreditFixture(t, true, true)
	admitFixture(t, f, &b, "")
	_, a := f.record()
	op := a.Subscription.Observation.ID
	automaticFixtureMutation(t, f, func(tx *store.Tx, a *domain.Account) error {
		a.Subscription.Observation.Phase = domain.SubscriptionObservationFailed
		return f.service.admitAutomaticCredit(tx, f.input.AccountID, a, nil, op)
	})
	_, a = f.record()
	if a.Subscription.Observation.ID != op || a.Subscription.AutomaticCreditEpisode.OperationID != "" {
		t.Fatal("failed refresh spent or replaced its key")
	}
	f, b = automaticCreditFixture(t, true, false)
	b.Reason = domain.CodexRateLimitExceeded
	automaticFixtureMutation(t, f, func(tx *store.Tx, a *domain.Account) error {
		now := tx.ObservationTime()
		zero := 0.0
		a.ConfirmedExhausted = true
		a.Subscription.QuotaState = domain.Observed
		a.Subscription.QuotaObservedAt = &now
		a.Quota = []domain.QuotaWindow{{ID: "codex:primary", ComparisonGroup: "chatgpt", Blocking: true, Remaining: &zero, State: domain.Observed, ObservedAt: now}}
		return nil
	})
	admitFixture(t, f, &b, "")
	_, a = f.record()
	if a.Subscription.Observation == nil || a.Subscription.Observation.Action != domain.SubscriptionResetCredit {
		t.Fatal("fresh subscription proof did not qualify rate limit")
	}
}

func TestAutomaticCreditPortableExcludesStandingAuthority(t *testing.T) {
	f, b := automaticCreditFixture(t, true, false)
	admitFixture(t, f, &b, "")
	r, a := f.record()
	raw, e := portableDocument(domain.AccountKind, r.Data)
	if e != nil {
		t.Fatal(e)
	}
	var imported domain.Account
	if e = domain.Decode(raw, &imported); e != nil {
		t.Fatal(e)
	}
	if imported.Subscription != nil || imported.Connection != nil {
		t.Fatal("portable transfer retained spending authority")
	}
	omitted := a
	copyState := *a.Subscription
	omitted.Subscription = &copyState
	omitted.Subscription.AutomaticCreditConsent = nil
	omitted.Alias = "Old client edit"
	_, e = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.old-client", nil, func(tx *store.Tx) (any, error) {
		return nil, validateRelationships(tx, domain.AccountKind, r.ID, r.Revision, &omitted)
	})
	if e == nil {
		t.Fatal("legacy omission erased consent")
	}
}

func TestAutomaticCreditUncertainFinishClearsGenerationConsentAndReconcilesSameKey(t *testing.T) {
	f, b := automaticCreditFixture(t, true, false)
	admitFixture(t, f, &b, "")
	r, a := f.record()
	original := *a.Subscription.Observation
	l := a.Subscription.Lease
	lease := &pb.TakeSubscriptionResponse{LeaseId: string(l.ID), LeaseRevision: l.Revision, GenerationId: string(l.Generation)}
	f.claimObservation(string(original.ID), lease)
	f.publishObservation(string(original.ID), lease, domain.SubscriptionObservationResult{ConsumeUncertain: true, QuotaError: domain.Unavailable})
	bundle, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: r.ID, ID: l.Generation, Purpose: credentials.AccountLogin})
	if err != nil {
		t.Fatal(err)
	}
	defer clear(bundle)
	if _, err = f.finish(lease, bundle, true, false, true); err != nil {
		t.Fatal(err)
	}
	r, a = f.record()
	if a.Subscription.AutomaticCreditConsent != nil || a.Subscription.Generation == l.Generation || a.Subscription.Lease != nil || a.Subscription.Observation.Phase != domain.SubscriptionObservationUncertain {
		t.Fatal("finish retained consent or lost original uncertainty")
	}
	_, err = f.client.ReconcileSubscriptionCredit(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.ReconcileSubscriptionCreditRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, OperationId: string(original.ID), ConnectionId: string(a.Connection.ID), GenerationId: string(a.Subscription.Generation)}))
	if err != nil {
		t.Fatal(err)
	}
	_, a = f.record()
	op := a.Subscription.Observation
	if op.ID != original.ID || op.CreditID != original.CreditID || op.NextCredit != original.NextCredit || op.CreditsObservationID != original.CreditsObservationID || op.AutomaticBlock != nil || op.AutomaticEpisodeID != "" || op.AutomaticLeaseID != "" {
		t.Fatal("explicit reconciliation changed key/selector or reused retired automatic authority")
	}
	if _, err = f.take(&pb.RequestSubscriptionResponse{OperationId: string(original.ID)}, pb.SubscriptionAction_SUBSCRIPTION_ACTION_RESET_CREDIT); err != nil {
		t.Fatal("explicit same-key reconciliation lost its independent lane", err)
	}
}
