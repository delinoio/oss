// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"log/slog"
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
				if a.Subscription.ServerCredit != nil || a.Subscription.AutomaticCreditEpisode != nil {
					t.Fatal("off admitted spending")
				}
				return
			}
			op := *a.Subscription.ServerCredit
			if !op.AccessOnly || !op.NextCredit || op.Automatic == nil || op.Automatic.LeaseID != a.Subscription.Lease.ID || a.Subscription.Observation != nil {
				t.Fatal("original frozen server authority lost")
			}
			admitFixture(t, f, &b, "")
			_, a = f.record()
			if a.Subscription.ServerCredit.ID != op.ID {
				t.Fatal("duplicate spent twice")
			}
		})
	}
}
func TestAutomaticCreditStaleRefreshOnceAndRecoveredRedelivery(t *testing.T) {
	f, b := automaticCreditFixture(t, true, true)
	admitFixture(t, f, &b, "")
	_, a := f.record()
	refresh := a.Subscription.ServerQuota.ID
	if a.Subscription.ServerCredit != nil || a.Subscription.Observation != nil || a.Subscription.ServerQuota.AutomaticEpisodeID != a.Subscription.AutomaticCreditEpisode.ID {
		t.Fatal("stale inventory spent or used Worker")
	}
	admitFixture(t, f, &b, "")
	_, a = f.record()
	if a.Subscription.ServerQuota.ID != refresh {
		t.Fatal("refreshed twice")
	}
	automaticFixtureMutation(t, f, func(tx *store.Tx, a *domain.Account) error {
		a.Subscription.ServerQuota.Phase = domain.SubscriptionObservationSucceeded
		a.Subscription.ServerQuota.CleanupConfirmed = true
		a.Subscription.ResetCredits.ObservedAt = tx.ObservationTime()
		a.Subscription.Lease = nil
		return f.service.admitAutomaticCredit(tx, f.input.AccountID, a, nil, refresh)
	})
	_, a = f.record()
	consume := a.Subscription.ServerCredit.ID
	if consume == refresh || a.Subscription.Observation != nil {
		t.Fatal("fresh server inventory not consumed")
	}
	automaticFixtureMutation(t, f, func(_ *store.Tx, a *domain.Account) error {
		a.Subscription.AutomaticCreditEpisode = nil
		a.Subscription.ServerCredit.Phase = domain.SubscriptionObservationFailed
		a.Subscription.ServerCredit.CleanupConfirmed = true
		return nil
	})
	// Retained turn fences survive recovery. Original proof cannot be recreated
	// after cleanup, even if the old marker is delivered again.
	_, a = f.record()
	if len(a.Subscription.AutomaticCreditBlocks) != 1 {
		t.Fatal("original turn fence lost")
	}
}

func TestAutomaticCreditFrozenRefreshSurvivesOriginalWorkerFinish(t *testing.T) {
	f, block := automaticCreditFixture(t, true, true)
	_, before := f.record()
	lease := before.Subscription.Lease
	originalGeneration := lease.Generation
	oldBundle, err := f.secrets.Get(creditContext(), credentials.Ref{Owner: f.input.AccountID, ID: originalGeneration, Purpose: credentials.AccountLogin})
	if err != nil {
		t.Fatal(err)
	}
	defer clear(oldBundle)
	admitFixture(t, f, &block, "")
	_, admitted := f.record()
	quotaOperation := *admitted.Subscription.ServerQuota

	now := time.Now().UTC()
	quota := &serverQuotaFixture{observed: domain.SubscriptionQuotaObservation{
		ObservedAt: now,
		Windows:    []domain.SubscriptionQuotaWindow{{ID: "codex:primary"}},
		Credits:    &domain.SubscriptionResetCredits{ObservationID: domain.NewID(), ObservedAt: now, AvailableCount: 2},
	}}
	quota.duringRead = func() {
		if _, err := f.finish(&pb.TakeSubscriptionResponse{LeaseId: string(lease.ID), LeaseRevision: lease.Revision, GenerationId: string(lease.Generation)}, oldBundle, true, false, true); err != nil {
			t.Fatal("finish original Worker execution during frozen refresh", err)
		}
		// Finish creates the replacement generation; verify consent was cleared.
		_, current := f.record()
		if current.Subscription.Generation == originalGeneration || current.Subscription.AutomaticCreditConsent != nil {
			t.Fatal("fixture did not rotate and clear consent")
		}
	}
	f.service.quotaOpen = func(context.Context, string, domain.ID, codex.QuotaAuthentication, *slog.Logger) (serverQuotaNative, error) {
		return quotaNativeFixture{quota}, nil
	}
	f.service.runServerQuota(creditContext(), f.input.AccountID)
	_, after := f.record()
	if after.Subscription.ServerQuota.Phase != domain.SubscriptionObservationSucceeded || after.Subscription.AutomaticCreditEpisode.OperationID == "" || after.Subscription.ServerCredit == nil || after.Subscription.ServerCredit.Generation != originalGeneration {
		t.Fatal("frozen refresh did not finish and admit the original credit proof", after.Subscription)
	}
	if after.Subscription.ServerQuota.ID != quotaOperation.ID || after.Subscription.ServerQuota.AutomaticEpisodeID != after.Subscription.AutomaticCreditEpisode.ID {
		t.Fatal("refresh ownership changed")
	}
	if _, err := f.secrets.Get(creditContext(), credentials.Ref{Owner: f.input.AccountID, ID: originalGeneration, Purpose: credentials.AccountLogin}); err != nil {
		t.Fatal("captured credential was retired before the admitted credit used it", err)
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
	op := a.Subscription.ServerCredit.ID
	_, err := f.client.SetAutomaticResetCreditConsent(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.SetAutomaticResetCreditConsentRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, ConnectionId: string(a.Connection.ID), GenerationId: string(a.Subscription.Generation)}))
	if err != nil {
		t.Fatal(err)
	}
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	_, a = f.record()
	if a.Subscription.ServerCredit.ID != op || a.Subscription.ServerCredit.Phase != domain.SubscriptionObservationFailed || a.Subscription.ServerCredit.EverSent || a.Subscription.Observation != nil {
		t.Fatal("revoked consent sent or adopted Worker")
	}
}
func TestAutomaticCreditCleanupAndDisconnectAfterAdmissionDoNotRevoke(t *testing.T) {
	f, b := automaticCreditFixture(t, true, false)
	admitFixture(t, f, &b, "")
	_, a := f.record()
	proof := *a.Subscription.ServerCredit.Automatic
	automaticFixtureMutation(t, f, func(tx *store.Tx, a *domain.Account) error {
		a.Subscription.Lease = nil
		sr, session, err := sessionRecord(tx, b.SessionID)
		if err != nil {
			return err
		}
		session.Execution.CleanupVerified = true
		if _, err = tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.SessionID, "", session); err != nil {
			return err
		}
		mr, err := tx.Get(domain.MachineKind, f.input.MachineID)
		if err != nil {
			return err
		}
		return tx.Delete(domain.MachineKind, mr.ID, mr.Revision)
	})
	n := &serverCreditFixture{serverQuotaFixture: &serverQuotaFixture{}, outcome: domain.SubscriptionReset}
	f.service.creditOpen = func(context.Context, string, domain.ID, codex.QuotaAuthentication, *slog.Logger) (serverCreditAccessNative, error) {
		return creditAccessFixture{n}, nil
	}
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	_, a = f.record()
	if n.count() != 1 || a.Subscription.ServerCredit.Outcome != domain.SubscriptionReset || *a.Subscription.ServerCredit.Automatic != proof || a.Subscription.Observation != nil {
		t.Fatal("cleanup/disconnect revoked accepted immutable proof")
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
			if a.Subscription.Observation != nil || a.Subscription.ServerCredit != nil {
				t.Fatal("rejection persisted spending")
			}
		})
	}
}
func TestAutomaticCreditFailedRefreshAndFreshRateLimitProof(t *testing.T) {
	f, b := automaticCreditFixture(t, true, true)
	admitFixture(t, f, &b, "")
	_, a := f.record()
	op := a.Subscription.ServerQuota.ID
	automaticFixtureMutation(t, f, func(tx *store.Tx, a *domain.Account) error {
		a.Subscription.ServerQuota.Phase = domain.SubscriptionObservationFailed
		return f.service.admitAutomaticCredit(tx, f.input.AccountID, a, nil, op)
	})
	_, a = f.record()
	if a.Subscription.ServerQuota.ID != op || a.Subscription.AutomaticCreditEpisode.OperationID != "" {
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
	if a.Subscription.ServerCredit == nil || !a.Subscription.ServerCredit.AccessOnly {
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
	automaticFixtureMutation(t, f, func(_ *store.Tx, a *domain.Account) error {
		op := a.Subscription.ServerCredit
		p := op.Automatic
		a.Subscription.Observation = &domain.SubscriptionObservationOperation{ID: op.ID, Action: domain.SubscriptionResetCredit, MachineID: p.MachineID, Actor: op.Actor, ConnectionID: op.ConnectionID, Generation: op.Generation, Phase: domain.SubscriptionObservationQueued, RequestedAt: op.RequestedAt, CreditID: op.CreditID, NextCredit: op.NextCredit, CreditsObservationID: op.CreditsObservationID, AutomaticEpisodeID: p.EpisodeID, AutomaticLeaseID: p.LeaseID, AutomaticBlock: &p.Block}
		a.Subscription.ServerCredit = nil
		a.Subscription.AutomaticCreditEpisode.ServerProof = nil
		return nil
	})
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

func TestAutomaticCreditNotificationNeedsPriorUsableEdge(t *testing.T) {
	for _, baseline := range []string{"unknown", "stale", "usable"} {
		t.Run(baseline, func(t *testing.T) {
			f, b := automaticCreditFixture(t, true, false)
			if baseline != "unknown" {
				automaticFixtureMutation(t, f, func(tx *store.Tx, a *domain.Account) error {
					at := tx.ObservationTime().Add(-time.Second)
					if baseline == "stale" {
						at = at.Add(-6 * time.Minute)
					}
					positive := 0.5
					a.Subscription.QuotaState = domain.Observed
					a.Subscription.QuotaObservedAt = &at
					a.Quota = []domain.QuotaWindow{{ID: "codex:primary", ComparisonGroup: "chatgpt", Blocking: true, Remaining: &positive, State: domain.Observed, ObservedAt: at}}
					return nil
				})
			}
			admitFixture(t, f, &b, "")
			_, a := f.record()
			if a.Subscription.ServerCredit == nil {
				t.Fatal("notification baseline incorrectly blocked spending admission")
			}
			entries, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.InboxKind, Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, r := range entries {
				e, err := store.Decode[domain.InboxEntry](r)
				if err != nil {
					t.Fatal(err)
				}
				if e.Operational != nil && e.Operational.Kind == domain.QuotaExhaustedNotification {
					count++
				}
			}
			want := 0
			if baseline == "usable" {
				want = 1
			}
			if count != want {
				t.Fatalf("baseline %s notified %d, want %d", baseline, count, want)
			}
		})
	}
}

func TestAutomaticCreditConsentReplacementAndRestartCannotSend(t *testing.T) {
	for _, change := range []string{"reenable", "epoch"} {
		t.Run(change, func(t *testing.T) {
			f, b := automaticCreditFixture(t, true, false)
			admitFixture(t, f, &b, "")
			_, before := f.record()
			key := before.Subscription.ServerCredit.ID
			automaticFixtureMutation(t, f, func(tx *store.Tx, a *domain.Account) error {
				if change == "reenable" {
					c := *a.Subscription.AutomaticCreditConsent
					c.ConfirmedAt = tx.ObservationTime().Add(time.Nanosecond)
					a.Subscription.AutomaticCreditConsent = &c
				} else {
					a.Subscription.ServerCredit.AttemptEpoch = domain.NewID()
				}
				return nil
			})
			if change == "epoch" {
				if err := f.service.initializeServerCredits(creditContext()); err != nil {
					t.Fatal(err)
				}
			}
			f.service.runServerCredit(creditContext(), f.input.AccountID)
			_, after := f.record()
			if after.Subscription.ServerCredit.ID != key || after.Subscription.ServerCredit.EverSent || after.Subscription.ServerCredit.Phase != domain.SubscriptionObservationFailed || after.Subscription.Observation != nil {
				t.Fatal("replacement/restart consumed or transferred claim")
			}
		})
	}
}

func TestAutomaticCreditConsentRevocationCannotBeReauthorizedByExecuteFinish(t *testing.T) {
	f, block := automaticCreditFixture(t, true, false)
	admitFixture(t, f, &block, "")
	record, account := f.record()
	oldGeneration := account.Subscription.Generation
	lease := account.Subscription.Lease
	oldBundle, err := f.secrets.Get(creditContext(), credentials.Ref{Owner: record.ID, ID: oldGeneration, Purpose: credentials.AccountLogin})
	if err != nil {
		t.Fatal("read original Execute credential", err)
	}
	defer clear(oldBundle)
	setConsent := func(enabled bool) {
		t.Helper()
		record, account = f.record()
		req := &pb.SetAutomaticResetCreditConsentRequest{
			Mutation:     &pb.Mutation{RequestId: string(domain.NewID()), Id: string(record.ID), ExpectedRevision: record.Revision},
			ConnectionId: string(account.Connection.ID),
			GenerationId: string(account.Subscription.Generation),
			Enabled:      enabled,
			Confirmed:    enabled,
		}
		if _, err := f.client.SetAutomaticResetCreditConsent(context.Background(), subscriptionRequest(f.service.Identity.Token, req)); err != nil {
			t.Fatalf("change automatic-credit consent enabled=%t: %v", enabled, err)
		}
	}
	setConsent(false)
	setConsent(true)
	if _, err := f.finish(&pb.TakeSubscriptionResponse{LeaseId: string(lease.ID), LeaseRevision: lease.Revision, GenerationId: string(lease.Generation)}, oldBundle, true, false, true); err != nil {
		t.Fatal("finish original Execute after consent revocation", err)
	}
	native := &serverCreditFixture{serverQuotaFixture: &serverQuotaFixture{}, outcome: domain.SubscriptionReset}
	f.service.creditOpen = func(context.Context, string, domain.ID, codex.QuotaAuthentication, *slog.Logger) (serverCreditAccessNative, error) {
		return creditAccessFixture{native}, nil
	}
	f.service.runServerCredit(creditContext(), record.ID)
	_, after := f.record()
	if native.count() != 0 || after.Subscription.ServerCredit.EverSent || after.Subscription.ServerCredit.Phase != domain.SubscriptionObservationFailed || after.Subscription.AutomaticCreditEpisode.ServerProof != nil {
		t.Fatal("Execute Finish reauthorized an explicitly revoked automatic-credit proof", after.Subscription.ServerCredit)
	}
}

func TestAutomaticCreditStaleRefreshRunsOnlyOnServer(t *testing.T) {
	f, b := automaticCreditFixture(t, true, true)
	admitFixture(t, f, &b, "")
	_, before := f.record()
	refresh := before.Subscription.ServerQuota.ID
	automaticFixtureMutation(t, f, func(_ *store.Tx, a *domain.Account) error { a.Subscription.Lease = nil; return nil })
	now := time.Now().UTC()
	zero := 0.0
	q := &serverQuotaFixture{observed: domain.SubscriptionQuotaObservation{ObservedAt: now, Windows: []domain.SubscriptionQuotaWindow{{ID: "codex:primary", Remaining: &zero}}, Credits: &domain.SubscriptionResetCredits{ObservationID: domain.NewID(), ObservedAt: now, AvailableCount: 1}}}
	f.service.quotaOpen = func(context.Context, string, domain.ID, codex.QuotaAuthentication, *slog.Logger) (serverQuotaNative, error) {
		return quotaNativeFixture{q}, nil
	}
	n := &serverCreditFixture{serverQuotaFixture: q, outcome: domain.SubscriptionReset}
	f.service.creditOpen = func(context.Context, string, domain.ID, codex.QuotaAuthentication, *slog.Logger) (serverCreditAccessNative, error) {
		return creditAccessFixture{n}, nil
	}
	f.service.runServerQuota(creditContext(), f.input.AccountID)
	_, after := f.record()
	if after.Subscription.ServerQuota.ID != refresh || after.Subscription.ServerCredit == nil || after.Subscription.Observation != nil {
		t.Fatal("server refresh did not select frozen episode")
	}
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	f.service.runServerQuota(creditContext(), f.input.AccountID)
	_, after = f.record()
	if n.count() != 1 || q.reads.Load() != 2 || after.Subscription.ServerCredit.Outcome != domain.SubscriptionReset {
		t.Fatal("server refresh/spend replayed or failed")
	}
}
func TestAutomaticCreditZeroInventoryDoesNotFreezeFutureAuthority(t *testing.T) {
	f, b := automaticCreditFixture(t, true, false)
	automaticFixtureMutation(t, f, func(_ *store.Tx, a *domain.Account) error { a.Subscription.ResetCredits.AvailableCount = 0; return nil })
	admitFixture(t, f, &b, "")
	_, a := f.record()
	if a.Subscription.ServerCredit != nil || a.Subscription.ServerQuota != nil || a.Subscription.AutomaticCreditEpisode.ServerProof != nil {
		t.Fatal("zero inventory reserved delayed spending authority")
	}
	automaticFixtureMutation(t, f, func(tx *store.Tx, a *domain.Account) error {
		a.Subscription.Lease = nil
		a.Subscription.ResetCredits.AvailableCount = 1
		return f.service.admitAutomaticCredit(tx, f.input.AccountID, a, nil, domain.NewID())
	})
	_, a = f.record()
	if a.Subscription.ServerCredit != nil {
		t.Fatal("later inventory adopted unaccepted cleaned episode")
	}
}
