// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"log/slog"
	"reflect"
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

// automaticServerFixture installs the System 101 credit and access-only quota
// natives for an automatic episode fixture.
func automaticServerFixture(t *testing.T, enabled, stale bool) (*subscriptionFixture, domain.SubscriptionQuotaBlock, *accessCreditNative, *serverQuotaFixture) {
	f, b := automaticCreditFixture(t, enabled, stale)
	credit := &accessCreditNative{outcome: domain.SubscriptionReset}
	zero := 0.0
	credit.observed = domain.SubscriptionQuotaObservation{ObservedAt: time.Now().UTC(), Windows: []domain.SubscriptionQuotaWindow{{ID: "codex:primary", Remaining: &zero}}}
	f.service.creditOpen = func(context.Context, nativeScope, domain.ID, codex.QuotaAuthentication, *slog.Logger) (serverCreditNative, error) {
		credit.mu.Lock()
		credit.opens++
		credit.mu.Unlock()
		return credit, nil
	}
	quota := installV2QuotaFixture(f)
	// The account is genuinely exhausted, so a refresh keeps the episode armed.
	quota.observed.Windows[0].Remaining = &zero
	quota.observed.Credits = &domain.SubscriptionResetCredits{ObservationID: domain.NewID(), ObservedAt: quota.observed.ObservedAt, AvailableCount: 2}
	return f, b, credit, quota
}

// workerClaimsServerOperation reports whether the Worker lease can claim the id.
func workerClaimsServerOperation(f *subscriptionFixture, operation domain.ID) bool {
	_, a := f.record()
	l := a.Subscription.Lease
	request := &pb.ClaimSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: l.Revision}, LeaseId: string(l.ID), MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: string(operation), GenerationId: string(l.Generation)}
	_, err := f.client.ClaimSubscriptionObservation(context.Background(), subscriptionRequest(f.workerToken, request))
	return err == nil
}
func TestAutomaticCreditDefaultOffAndOriginalOnceOnlyClaim(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[enabled], func(t *testing.T) {
			f, b, n, _ := automaticServerFixture(t, enabled, false)
			admitFixture(t, f, &b, "")
			_, a := f.record()
			if !enabled {
				if a.Subscription.ServerCredit != nil || a.Subscription.ServerQuota != nil || a.Subscription.Observation != nil || a.Subscription.AutomaticCreditEpisode != nil {
					t.Fatal("off admitted spending")
				}
				return
			}
			op := *a.Subscription.ServerCredit
			episode := a.Subscription.AutomaticCreditEpisode
			if !op.AccessOnly || op.Automatic == nil || !op.NextCredit || a.Subscription.Observation != nil || episode.ServerProof == nil || episode.OperationID != op.ID || op.Automatic.LeaseID != a.Subscription.Lease.ID || op.Automatic.Consent != *a.Subscription.AutomaticCreditConsent || op.Automatic.Consent.ConfirmedAt.IsZero() {
				t.Fatal("frozen server proof lost original authority", op)
			}
			if !reflect.DeepEqual(*episode.ServerProof, *op.Automatic) {
				t.Fatal("episode and operation proofs diverged")
			}
			admitFixture(t, f, &b, "")
			_, a = f.record()
			if a.Subscription.ServerCredit.ID != op.ID {
				t.Fatal("duplicate spent twice")
			}
			if workerClaimsServerOperation(f, op.ID) {
				t.Fatal("Worker adopted the server-owned automatic claim")
			}
			f.service.runServerCredit(creditContext(), f.input.AccountID)
			_, a = f.record()
			if n.count() != 1 || a.Subscription.ServerCredit.Phase != domain.SubscriptionObservationSucceeded || n.sends[0].ID != op.ID || a.Subscription.Observation != nil {
				t.Fatal("automatic claim did not consume exactly once on the server", n.count())
			}
			admitFixture(t, f, &b, "")
			f.service.runServerCredit(creditContext(), f.input.AccountID)
			_, a = f.record()
			if n.count() != 1 || a.Subscription.ServerCredit.ID != op.ID {
				t.Fatal("redelivery or replay sent twice")
			}
		})
	}
}
func TestAutomaticCreditStaleRefreshOnceAndRecoveredRedelivery(t *testing.T) {
	f, b, n, quota := automaticServerFixture(t, true, true)
	admitFixture(t, f, &b, "")
	_, a := f.record()
	refresh := *a.Subscription.ServerQuota
	if !refresh.AccessOnly || refresh.AutomaticEpisodeID != a.Subscription.AutomaticCreditEpisode.ID || a.Subscription.ServerCredit != nil || a.Subscription.Observation != nil {
		t.Fatal("stale inventory spent or used a Worker refresh", a.Subscription)
	}
	admitFixture(t, f, &b, "")
	_, a = f.record()
	if a.Subscription.ServerQuota.ID != refresh.ID {
		t.Fatal("refreshed twice")
	}
	f.service.runServerQuota(creditContext(), f.input.AccountID)
	_, a = f.record()
	consume := a.Subscription.ServerCredit
	if quota.reads.Load() != 1 || consume == nil || consume.ID == refresh.ID || a.Subscription.ServerQuota.Phase != domain.SubscriptionObservationSucceeded || consume.Automatic == nil || a.Subscription.AutomaticCreditEpisode.OperationID != consume.ID {
		t.Fatal("fresh inventory was not consumed from the same frozen episode", a.Subscription.ServerCredit)
	}
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	if n.count() != 1 {
		t.Fatal("deterministic single consumption failed")
	}
	automaticFixtureMutation(t, f, func(_ *store.Tx, a *domain.Account) error {
		a.Subscription.AutomaticCreditEpisode = nil
		return nil
	})
	admitFixture(t, f, &b, "")
	_, a = f.record()
	if a.Subscription.ServerCredit.ID != consume.ID || a.Subscription.AutomaticCreditEpisode != nil {
		t.Fatal("redelivery rearmed original failed turn")
	}
}

func TestAutomaticCreditSelectionIsDeterministicByExpiryGrantAndID(t *testing.T) {
	f, b, n, _ := automaticServerFixture(t, true, false)
	now := time.Now().UTC()
	soon, late := now.Add(time.Hour), now.Add(2*time.Hour)
	credits := []domain.SubscriptionResetCreditDetail{
		{ID: "z-late", ResetType: domain.CodexRateLimitsReset, Status: domain.SubscriptionCreditAvailable, GrantedAt: now.Add(-time.Hour), ExpiresAt: &late},
		{ID: "b-soon", ResetType: domain.CodexRateLimitsReset, Status: domain.SubscriptionCreditAvailable, GrantedAt: now.Add(-time.Hour), ExpiresAt: &soon},
		{ID: "a-soon", ResetType: domain.CodexRateLimitsReset, Status: domain.SubscriptionCreditAvailable, GrantedAt: now.Add(-time.Hour), ExpiresAt: &soon},
		{ID: "never", ResetType: domain.CodexRateLimitsReset, Status: domain.SubscriptionCreditAvailable, GrantedAt: now.Add(-2 * time.Hour)},
	}
	automaticFixtureMutation(t, f, func(_ *store.Tx, a *domain.Account) error {
		a.Subscription.ResetCredits.Credits = &credits
		return nil
	})
	admitFixture(t, f, &b, "")
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	if n.count() != 1 || n.sends[0].CreditID != "a-soon" || n.sends[0].NextCredit {
		t.Fatal("selection ignored earliest expiry, grant or ID", n.sends)
	}
}
func TestAutomaticCreditRateLimitNeedsSubscriptionExhaustion(t *testing.T) {
	f, b, _, _ := automaticServerFixture(t, true, false)
	b.Reason = domain.CodexRateLimitExceeded
	admitFixture(t, f, &b, "")
	_, a := f.record()
	if a.Subscription.ServerCredit != nil || a.Subscription.ServerQuota != nil || a.Subscription.AutomaticCreditEpisode != nil {
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

func TestAutomaticCreditPublicationAdmitsServerClaimAndConsentChangesCancelBeforeSend(t *testing.T) {
	for _, change := range []string{"disable", "replace"} {
		t.Run(change, func(t *testing.T) {
			f, b, n, _ := automaticServerFixture(t, true, false)
			_, a := f.record()
			lease := a.Subscription.Lease
			response := &pb.TakeSubscriptionResponse{LeaseId: string(lease.ID), LeaseRevision: lease.Revision, GenerationId: string(lease.Generation)}
			f.publishObservation("", response, domain.SubscriptionObservationResult{QuotaBlock: &b})
			r, a := f.record()
			op := a.Subscription.ServerCredit
			if op == nil || a.Subscription.Observation != nil || op.Automatic == nil {
				t.Fatal("Worker marker did not admit one server claim", a.Subscription)
			}
			if workerClaimsServerOperation(f, op.ID) {
				t.Fatal("Worker 52 received a consumption observation")
			}
			set := func(enabled bool) {
				_, a := f.record()
				r, _ := f.record()
				request := &pb.SetAutomaticResetCreditConsentRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, ConnectionId: string(a.Connection.ID), GenerationId: string(a.Subscription.Generation), Enabled: enabled, Confirmed: enabled}
				if _, err := f.client.SetAutomaticResetCreditConsent(context.Background(), subscriptionRequest(f.service.Identity.Token, request)); err != nil {
					t.Fatal(err)
				}
			}
			_ = r
			set(false)
			if change == "replace" {
				time.Sleep(2 * time.Millisecond)
				set(true)
			}
			f.service.runServerCredit(creditContext(), f.input.AccountID)
			_, a = f.record()
			if n.count() != 0 || n.opens != 0 || a.Subscription.ServerCredit.Phase != domain.SubscriptionObservationFailed || a.Subscription.ServerCredit.ErrorCode != domain.Canceled || a.Subscription.ServerCredit.EverSent {
				t.Fatal("consent change before send did not cancel with zero spend", a.Subscription.ServerCredit)
			}
		})
	}
}

func TestAutomaticCreditWorkerFinishAfterAdmissionDoesNotRevokeFrozenClaim(t *testing.T) {
	f, b, n, _ := automaticServerFixture(t, true, false)
	admitFixture(t, f, &b, "")
	r, a := f.record()
	original := *a.Subscription.ServerCredit
	captured := a.Subscription.Generation
	l := a.Subscription.Lease
	lease := &pb.TakeSubscriptionResponse{LeaseId: string(l.ID), LeaseRevision: l.Revision, GenerationId: string(l.Generation)}
	bundle, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: r.ID, ID: l.Generation, Purpose: credentials.AccountLogin})
	if err != nil {
		t.Fatal(err)
	}
	defer clear(bundle)
	if _, err = f.finish(lease, bundle, true, false, true); err != nil {
		t.Fatal(err)
	}
	_, a = f.record()
	if a.Subscription.AutomaticCreditConsent != nil || a.Subscription.Generation == captured || a.Subscription.AutomaticCreditEpisode.ServerFinishGeneration != a.Subscription.Generation || a.Subscription.Lease != nil {
		t.Fatal("Finish did not record the first successor generation", a.Subscription.AutomaticCreditEpisode)
	}
	if !referenceExists(t, f, captured) {
		t.Fatal("Finish retired the frozen claim's reference")
	}
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	_, a = f.record()
	o := a.Subscription.ServerCredit
	if n.count() != 1 || n.sends[0].ID != original.ID || o.Phase != domain.SubscriptionObservationSucceeded || !reflect.DeepEqual(o.Automatic, original.Automatic) {
		t.Fatal("Worker cleanup revoked the admitted claim", o)
	}
	if !referenceExists(t, f, captured) || !referenceExists(t, f, a.Subscription.Generation) {
		t.Fatal("settlement retired the captured reference instead of retaining it")
	}
}

func TestAutomaticCreditLaterRotationAndRestartCannotAdoptFrozenClaim(t *testing.T) {
	for _, change := range []string{"second-rotation", "restart"} {
		t.Run(change, func(t *testing.T) {
			f, b, n, _ := automaticServerFixture(t, true, false)
			admitFixture(t, f, &b, "")
			r, a := f.record()
			l := a.Subscription.Lease
			lease := &pb.TakeSubscriptionResponse{LeaseId: string(l.ID), LeaseRevision: l.Revision, GenerationId: string(l.Generation)}
			bundle, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: r.ID, ID: l.Generation, Purpose: credentials.AccountLogin})
			if err != nil {
				t.Fatal(err)
			}
			defer clear(bundle)
			if _, err = f.finish(lease, bundle, true, false, true); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "second-rotation":
				next := domain.NewID()
				if _, err := f.secrets.Put(context.Background(), credentials.Ref{Owner: r.ID, ID: next, Purpose: credentials.AccountLogin}, bundle); err != nil {
					t.Fatal(err)
				}
				automaticFixtureMutation(t, f, func(_ *store.Tx, a *domain.Account) error {
					a.Subscription.Generation = next
					return nil
				})
			case "restart":
				automaticFixtureMutation(t, f, func(_ *store.Tx, a *domain.Account) error {
					a.Subscription.ServerCredit.AttemptEpoch = domain.NewID()
					return nil
				})
				if err := f.service.initializeServerQuotas(creditContext()); err != nil {
					t.Fatal(err)
				}
			}
			f.service.runServerCredit(creditContext(), f.input.AccountID)
			_, a = f.record()
			if n.count() != 0 || n.opens != 0 || a.Subscription.ServerCredit.Phase != domain.SubscriptionObservationFailed || a.Subscription.ServerCredit.EverSent {
				t.Fatal("stale binding or restart sent a frozen claim", a.Subscription.ServerCredit)
			}
		})
	}
}

func TestAutomaticCreditUncertainConsumeReconcilesWithoutLiveEpisode(t *testing.T) {
	f, b, n, _ := automaticServerFixture(t, true, false)
	n.consumeError, n.outcome = errors.New("synthetic lost response"), ""
	admitFixture(t, f, &b, "")
	_, a := f.record()
	original := *a.Subscription.ServerCredit
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	r, a := f.record()
	if a.Subscription.ServerCredit.Phase != domain.SubscriptionObservationUncertain {
		t.Fatal("lost response did not stay uncertain")
	}
	// A later recovery observation may rearm the episode independently.
	automaticFixtureMutation(t, f, func(_ *store.Tx, a *domain.Account) error {
		a.Subscription.AutomaticCreditEpisode = nil
		a.Subscription.Lease = nil
		return nil
	})
	n.consumeError, n.outcome = nil, domain.SubscriptionReset
	r, _ = f.record()
	reconcile := &pb.ReconcileSubscriptionCreditRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, OperationId: string(original.ID), ConnectionId: string(original.ConnectionID), GenerationId: string(original.Generation)}
	if _, err := f.client.ReconcileSubscriptionCredit(context.Background(), subscriptionRequest(f.service.Identity.Token, reconcile)); err != nil {
		t.Fatal(err)
	}
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	_, a = f.record()
	o := a.Subscription.ServerCredit
	if n.count() != 2 || n.sends[1].ID != original.ID || o.Phase != domain.SubscriptionObservationSucceeded {
		t.Fatal("explicit same-key reconciliation needed the retired episode", o)
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
			if a.Subscription.Observation != nil || a.Subscription.ServerCredit != nil || a.Subscription.ServerQuota != nil {
				t.Fatal("rejection persisted spending")
			}
		})
	}
}
func TestAutomaticCreditFailedRefreshAndFreshRateLimitProof(t *testing.T) {
	f, b, _, _ := automaticServerFixture(t, true, true)
	admitFixture(t, f, &b, "")
	_, a := f.record()
	op := a.Subscription.ServerQuota.ID
	automaticFixtureMutation(t, f, func(tx *store.Tx, a *domain.Account) error {
		a.Subscription.ServerQuota.Phase = domain.SubscriptionObservationFailed
		return f.service.admitAutomaticCredit(tx, f.input.AccountID, a, nil, op)
	})
	_, a = f.record()
	if a.Subscription.ServerQuota.ID != op || a.Subscription.ServerCredit != nil || a.Subscription.AutomaticCreditEpisode.OperationID != "" {
		t.Fatal("failed refresh spent or replaced its key")
	}
	f, b, _, _ = automaticServerFixture(t, true, false)
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
	if a.Subscription.ServerCredit == nil || a.Subscription.ServerCredit.Automatic == nil {
		t.Fatal("fresh subscription proof did not qualify rate limit")
	}
}

func TestAutomaticCreditUnusableInventoryCannotSpend(t *testing.T) {
	for _, shape := range []string{"zero", "missing", "expired", "future-grant"} {
		t.Run(shape, func(t *testing.T) {
			f, b, n, _ := automaticServerFixture(t, true, false)
			automaticFixtureMutation(t, f, func(_ *store.Tx, a *domain.Account) error {
				now := time.Now().UTC()
				expired := now.Add(-time.Minute)
				switch shape {
				case "zero":
					a.Subscription.ResetCredits.AvailableCount = 0
				case "missing":
					a.Subscription.ResetCredits = nil
				case "expired":
					a.Subscription.ResetCredits.Credits = &[]domain.SubscriptionResetCreditDetail{{ID: "credit_1", ResetType: domain.CodexRateLimitsReset, Status: domain.SubscriptionCreditAvailable, GrantedAt: now.Add(-time.Hour), ExpiresAt: &expired}}
				case "future-grant":
					a.Subscription.ResetCredits.Credits = &[]domain.SubscriptionResetCreditDetail{{ID: "credit_1", ResetType: domain.CodexRateLimitsReset, Status: domain.SubscriptionCreditAvailable, GrantedAt: now.Add(time.Hour)}}
				}
				return nil
			})
			admitFixture(t, f, &b, "")
			f.service.runServerCredit(creditContext(), f.input.AccountID)
			_, a := f.record()
			if a.Subscription.ServerCredit != nil && shape != "missing" || n.count() != 0 {
				t.Fatal("unusable inventory spent", a.Subscription.ServerCredit)
			}
		})
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

// legacyWorkerAdmission rewrites a fresh admission into the Worker-owned
// observation shape accepted before System 101, which keeps its original lane.
func legacyWorkerAdmission(t *testing.T, f *subscriptionFixture) {
	t.Helper()
	automaticFixtureMutation(t, f, func(_ *store.Tx, a *domain.Account) error {
		st := a.Subscription
		credit, episode, block := st.ServerCredit, st.AutomaticCreditEpisode, st.AutomaticCreditEpisode.ServerProof.Block
		st.ServerCredit, episode.ServerProof = nil, nil
		st.Observation = &domain.SubscriptionObservationOperation{ID: credit.ID, Action: domain.SubscriptionResetCredit, MachineID: st.Lease.MachineID, Actor: credit.Actor, ConnectionID: credit.ConnectionID, Generation: credit.Generation, Phase: domain.SubscriptionObservationQueued, RequestedAt: credit.RequestedAt, CreditID: credit.CreditID, NextCredit: credit.NextCredit, CreditsObservationID: credit.CreditsObservationID, AutomaticEpisodeID: episode.ID, AutomaticLeaseID: st.Lease.ID, AutomaticBlock: &block}
		return nil
	})
}

func TestAutomaticCreditUncertainFinishClearsGenerationConsentAndReconcilesSameKey(t *testing.T) {
	f, b := automaticCreditFixture(t, true, false)
	admitFixture(t, f, &b, "")
	legacyWorkerAdmission(t, f)
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

func TestAutomaticCreditAndManualCreditShareOneOwnerAndKey(t *testing.T) {
	t.Run("manual-first", func(t *testing.T) {
		f, b, n, _ := automaticServerFixture(t, true, false)
		mutateCreditAccount(t, f, func(a *domain.Account) {
			a.Subscription.ResetCredits.Credits = nil
		})
		manual := requestServerCredit(f)
		requestAccessCredit(t, f, manual)
		admitFixture(t, f, &b, "")
		_, a := f.record()
		if a.Subscription.ServerCredit.ID != domain.ID(manual.Mutation.RequestId) || a.Subscription.ServerCredit.Automatic != nil || a.Subscription.AutomaticCreditEpisode == nil || a.Subscription.AutomaticCreditEpisode.OperationID != "" {
			t.Fatal("automatic admission replaced or duplicated the manual owner", a.Subscription.ServerCredit)
		}
		if n.count() != 0 {
			t.Fatal("race consumed")
		}
	})
	t.Run("automatic-first", func(t *testing.T) {
		f, b, n, _ := automaticServerFixture(t, true, false)
		admitFixture(t, f, &b, "")
		_, a := f.record()
		first := a.Subscription.ServerCredit.ID
		r, _ := f.record()
		manual := &pb.RequestSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SubscriptionObservationAction_SUBSCRIPTION_OBSERVATION_ACTION_RESET_CREDIT, ConnectionId: string(a.Connection.ID), GenerationId: string(a.Subscription.Generation), NextCredit: true, CreditsObservationId: string(a.Subscription.ResetCredits.ObservationID), Confirmed: true}
		if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, manual)); err == nil {
			t.Fatal("manual request replaced the admitted automatic claim")
		}
		_, a = f.record()
		if a.Subscription.ServerCredit.ID != first || n.count() != 0 {
			t.Fatal("second owner or key appeared")
		}
	})
}

// TestAutomaticCreditBindsTheReportingMemberLeaseOfASharedOwner covers a joined
// member that reports the quota block: the episode and frozen proof name that
// member's lease, job and revision rather than the founding owner's, and the
// claim lapses with the member instead of surviving under the founding job.
func TestAutomaticCreditBindsTheReportingMemberLeaseOfASharedOwner(t *testing.T) {
	f, b, _, _ := automaticServerFixture(t, true, false)
	var owner domain.ID
	var memberLease domain.ID = domain.NewID()
	founding := domain.NewID()
	automaticFixtureMutation(t, f, func(tx *store.Tx, a *domain.Account) error {
		now := tx.ObservationTime()
		owner = a.Subscription.Lease.ID
		a.Subscription.Lease.Shared = true
		a.Subscription.Lease.OperationID = founding
		a.Subscription.Members = []domain.SubscriptionMember{
			{LeaseID: owner, OperationID: founding, Revision: a.Subscription.Lease.Revision, State: domain.SubscriptionMemberRunning, AdmittedAt: now},
			{LeaseID: memberLease, OperationID: f.job, Revision: a.Subscription.Lease.Revision + 5, State: domain.SubscriptionMemberRunning, AdmittedAt: now},
		}
		return nil
	})
	admitFixture(t, f, &b, "")
	_, a := f.record()
	st := a.Subscription
	episode := st.AutomaticCreditEpisode
	if episode == nil || episode.LeaseID != memberLease || episode.LeaseID == owner {
		t.Fatal("the episode was not bound to the reporting member lease", episode)
	}
	proof := episode.ServerProof
	member, _ := st.Member(memberLease)
	if proof == nil || proof.LeaseID != memberLease || proof.OperationID != f.job || proof.OperationID == founding || proof.LeaseRevision != member.Revision {
		t.Fatal("the frozen proof names the founding owner's job, lease or revision", proof)
	}
	if st.ServerCredit == nil || st.ServerCredit.Automatic == nil || st.ServerCredit.Automatic.LeaseID != memberLease || !automaticEpisodeAuthority(st, episode, *proof, proof.Consent.Generation) {
		t.Fatal("the running reporting member did not authorize its own claim")
	}
	// A finished member holds no authority, and the founding job cannot adopt it.
	if !st.ReleaseMember(memberLease) {
		t.Fatal("fixture member vanished")
	}
	if automaticEpisodeAuthority(st, episode, *proof, proof.Consent.Generation) {
		t.Fatal("the automatic claim stayed authorized after the reporting member finished")
	}
	if lease, ok := automaticCreditLease(st, owner); !ok || lease.OperationID != founding {
		t.Fatal("the founding member must still resolve its own job", lease, ok)
	}
}
