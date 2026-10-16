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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// accessCreditNative stands in for the distinct access-only credit profile. It
// records every consumption it receives and can never write credentials back.
type accessCreditNative struct {
	mu            sync.Mutex
	sends         []domain.ServerCreditOperation
	auths         []codex.QuotaAuthentication
	opens         int
	outcome       domain.SubscriptionResetOutcome
	consumeError  error
	quotaError    error
	closeError    error
	observed      domain.SubscriptionQuotaObservation
	duringConsume func()
}

func (n *accessCreditNative) ConsumeExternalResetCredit(ctx context.Context, op domain.ServerCreditOperation, auth codex.QuotaAuthentication, checkpoint func(domain.SubscriptionResetOutcome) error) (domain.SubscriptionResetOutcome, domain.SubscriptionQuotaObservation, error, error) {
	if !op.AccessOnly || op.Validate() != nil || !op.SendClaimed || op.Phase != domain.SubscriptionObservationSending || auth.Access == "" || auth.Account == "" {
		return "", domain.SubscriptionQuotaObservation{}, domain.InvalidSubscriptionObservation(), nil
	}
	n.mu.Lock()
	n.sends = append(n.sends, op)
	n.auths = append(n.auths, auth)
	hook := n.duringConsume
	n.mu.Unlock()
	if hook != nil {
		hook()
	}
	if ctx.Err() != nil {
		return "", domain.SubscriptionQuotaObservation{}, ctx.Err(), nil
	}
	if n.consumeError != nil {
		return "", domain.SubscriptionQuotaObservation{}, n.consumeError, nil
	}
	if err := checkpoint(n.outcome); err != nil {
		return n.outcome, domain.SubscriptionQuotaObservation{}, err, domain.Fail(domain.Canceled, "skipped", "")
	}
	if n.quotaError != nil {
		return n.outcome, domain.SubscriptionQuotaObservation{}, nil, n.quotaError
	}
	return n.outcome, n.observed, nil, nil
}
func (n *accessCreditNative) Close() error { return n.closeError }
func (n *accessCreditNative) count() int   { n.mu.Lock(); defer n.mu.Unlock(); return len(n.sends) }

type accessCreditOptions struct {
	details  bool
	active   bool
	noRunner bool
}

// newAccessCreditFixture builds a Worker-settled ChatGPT account with a fresh
// reset-credit inventory and a System 101 native fixture.
func newAccessCreditFixture(t *testing.T, o accessCreditOptions) (*subscriptionFixture, *accessCreditNative, *pb.TakeSubscriptionResponse) {
	t.Helper()
	f := newQuotaFixture(t)
	var lease *pb.TakeSubscriptionResponse
	if o.active {
		lease = takeSubscriptionExecutionFixture(t, f)
	}
	now := time.Now().UTC()
	items := []domain.SubscriptionResetCreditDetail{{ID: "credit_1", ResetType: domain.CodexRateLimitsReset, Status: domain.SubscriptionCreditAvailable, GrantedAt: now.Add(-time.Hour)}}
	inventory := &domain.SubscriptionResetCredits{ObservationID: domain.NewID(), ObservedAt: now, AvailableCount: 2}
	if o.details {
		inventory.Credits = &items
	}
	mutateCreditAccount(t, f, func(a *domain.Account) { a.Subscription.ResetCredits = inventory })
	if o.noRunner {
		_, err := f.service.Store.Mutate(creditContext(), domain.NewID(), "fixture.credit.no-runner", nil, func(tx *store.Tx) (any, error) {
			r, err := tx.Get(domain.MachineKind, f.input.MachineID)
			if err != nil {
				return nil, err
			}
			return nil, tx.Delete(domain.MachineKind, r.ID, r.Revision)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	remaining := .7
	n := &accessCreditNative{outcome: domain.SubscriptionReset, observed: domain.SubscriptionQuotaObservation{ObservedAt: now, Windows: []domain.SubscriptionQuotaWindow{{ID: "codex:primary", Remaining: &remaining}}}}
	f.service.creditOpen = func(context.Context, nativeScope, domain.ID, codex.QuotaAuthentication, *slog.Logger) (serverCreditNative, error) {
		n.mu.Lock()
		n.opens++
		n.mu.Unlock()
		return n, nil
	}
	return f, n, lease
}

func rotateExecution(t *testing.T, f *subscriptionFixture, lease *pb.TakeSubscriptionResponse, rotation string) {
	t.Helper()
	raw := subscriptionTestBundle("fixture-account", rotation, time.Now().UTC())
	defer clear(raw)
	if _, err := f.finish(lease, raw, true, false, true); err != nil {
		t.Fatal(err)
	}
}

func referenceExists(t *testing.T, f *subscriptionFixture, generation domain.ID) bool {
	t.Helper()
	raw, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin})
	clear(raw)
	return err == nil
}

func TestAccessCreditManualLaneNeedsNoRunnerAndKeepsExecuteOwnership(t *testing.T) {
	for _, mode := range []string{"idle-worker-owned", "no-runner", "active-execute"} {
		for _, details := range []bool{true, false} {
			t.Run(mode+map[bool]string{true: "-exact", false: "-next"}[details], func(t *testing.T) {
				f, n, _ := newAccessCreditFixture(t, accessCreditOptions{details: details, active: mode == "active-execute", noRunner: mode == "no-runner"})
				_, before := f.record()
				req := requestServerCredit(f)
				requestAccessCredit(t, f, req)
				_, a := f.record()
				o := a.Subscription.ServerCredit
				if o == nil || !o.AccessOnly || a.Subscription.Observation != nil || o.ID != domain.ID(req.Mutation.RequestId) || o.Phase != domain.SubscriptionObservationQueued {
					t.Fatal("omitted selector did not queue the server access-only lane", o)
				}
				f.service.runServerCredit(creditContext(), f.input.AccountID)
				_, a = f.record()
				o = a.Subscription.ServerCredit
				if o.Phase != domain.SubscriptionObservationSucceeded || o.Outcome != domain.SubscriptionReset || !o.CleanupConfirmed || n.count() != 1 {
					t.Fatal("server consumption did not settle once", o)
				}
				if n.sends[0].ID != o.ID || n.sends[0].CreditID != req.CreditId || n.sends[0].NextCredit != req.NextCredit || n.auths[0].Account != "fixture-account" || n.auths[0].Plan != "plus" {
					t.Fatal("provider key, selector or sealed authentication changed", n.sends[0])
				}
				// Execute and credential ownership are untouched by the credit owner.
				if !reflect.DeepEqual(a.Subscription.Lease, before.Subscription.Lease) || a.Subscription.Generation != before.Subscription.Generation || a.Subscription.OwnerMachineID != before.Subscription.OwnerMachineID || a.Subscription.RecoveryRequired || a.Health != domain.AccountReady || a.Subscription.Observation != nil {
					t.Fatal("credit changed Execute or credential ownership", a.Subscription)
				}
				if a.Subscription.QuotaState != domain.Observed || len(a.Quota) != 1 {
					t.Fatal("independent post-consumption quota was not applied")
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
}

func TestAccessCreditExplicitMachineKeepsHistoricalWorkerLane(t *testing.T) {
	f, n, lease := newAccessCreditFixture(t, accessCreditOptions{details: true, active: true})
	req := requestServerCredit(f)
	req.MachineId = string(f.input.MachineID)
	requestAccessCredit(t, f, req)
	_, a := f.record()
	if a.Subscription.ServerCredit != nil || a.Subscription.Observation == nil || a.Subscription.Observation.MachineID != f.input.MachineID {
		t.Fatal("explicit machine left the historical Worker lane", a.Subscription)
	}
	f.claimObservation(req.Mutation.RequestId, lease)
	if n.count() != 0 {
		t.Fatal("server consumed an explicit-machine request")
	}
}

func TestAccessCreditWorkerCannotAdoptServerClaim(t *testing.T) {
	f, n, lease := newAccessCreditFixture(t, accessCreditOptions{details: true, active: true})
	req := requestServerCredit(f)
	requestAccessCredit(t, f, req)
	claim := &pb.ClaimSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: lease.LeaseRevision}, LeaseId: lease.LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: req.Mutation.RequestId, GenerationId: lease.GenerationId}
	if _, err := f.client.ClaimSubscriptionObservation(context.Background(), subscriptionRequest(f.workerToken, claim)); err == nil {
		t.Fatal("Worker adopted a server-owned claim")
	}
	raw := []byte(`{"outcome":"reset","quota_error":"unavailable"}`)
	publish := &pb.PublishSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: lease.LeaseRevision}, LeaseId: lease.LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: req.Mutation.RequestId, GenerationId: lease.GenerationId, ObservationJson: raw}
	if _, err := f.client.PublishSubscriptionObservation(context.Background(), subscriptionRequest(f.workerToken, publish)); err == nil {
		t.Fatal("Worker published a result for a server-owned claim")
	}
	_, a := f.record()
	if a.Subscription.ServerCredit.Phase != domain.SubscriptionObservationQueued || n.count() != 0 {
		t.Fatal("Worker interference changed the server claim")
	}
}

func TestAccessCreditGenerationRotationBeforeSendCancelsWithZeroSpend(t *testing.T) {
	f, n, lease := newAccessCreditFixture(t, accessCreditOptions{details: true, active: true})
	_, before := f.record()
	captured := before.Subscription.Generation
	requestAccessCredit(t, f, requestServerCredit(f))
	rotateExecution(t, f, lease, "second")
	if !referenceExists(t, f, captured) {
		t.Fatal("rotation retired the credit's captured reference")
	}
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	_, a := f.record()
	o := a.Subscription.ServerCredit
	if n.count() != 0 || n.opens != 0 || o.Phase != domain.SubscriptionObservationFailed || o.ErrorCode != domain.Canceled || o.EverSent || !o.CleanupConfirmed {
		t.Fatal("rotation before send spent or left a live claim", o)
	}
	if !referenceExists(t, f, captured) || !referenceExists(t, f, a.Subscription.Generation) {
		t.Fatal("settlement retired the captured reference instead of retaining it")
	}
	if a.Subscription.RecoveryRequired || a.Health != domain.AccountReady || a.Subscription.Lease != nil {
		t.Fatal("canceled credit changed Execute settlement")
	}
}

func TestAccessCreditPossibleSendSurvivesRotationAndReconcilesWithCapturedGeneration(t *testing.T) {
	f, n, lease := newAccessCreditFixture(t, accessCreditOptions{details: true, active: true})
	_, before := f.record()
	captured := before.Subscription.Generation
	n.consumeError = errors.New("synthetic lost response")
	n.outcome = ""
	n.duringConsume = func() { rotateExecution(t, f, lease, "second") }
	requestAccessCredit(t, f, requestServerCredit(f))
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	r, a := f.record()
	old := *a.Subscription.ServerCredit
	if old.Phase != domain.SubscriptionObservationUncertain || !old.EverSent || !old.CleanupConfirmed || n.count() != 1 {
		t.Fatal("possible send lost uncertainty", old)
	}
	if !referenceExists(t, f, captured) || len(protectedGenerations(a.Subscription)) != 1 {
		t.Fatal("uncertain credit lost its captured reference")
	}
	if a.Subscription.RecoveryRequired || a.Health != domain.AccountReady || a.Subscription.Generation == captured {
		t.Fatal("credit uncertainty failed or released independent Execute settlement")
	}
	// Restart, polling and receipt replay never resend.
	mutateCreditAccount(t, f, func(a *domain.Account) { a.Subscription.ServerCredit.AttemptEpoch = domain.NewID() })
	if err := f.service.initializeServerQuotas(creditContext()); err != nil {
		t.Fatal(err)
	}
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	if n.count() != 1 {
		t.Fatal("restart resent consumption")
	}
	// Explicit same-key reconciliation uses the retained captured generation.
	wrong := &pb.ReconcileSubscriptionCreditRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, OperationId: string(old.ID), ConnectionId: string(old.ConnectionID), GenerationId: string(a.Subscription.Generation)}
	r, a = f.record()
	wrong.Mutation.ExpectedRevision = r.Revision
	if _, err := f.client.ReconcileSubscriptionCredit(context.Background(), subscriptionRequest(f.service.Identity.Token, wrong)); err == nil {
		t.Fatal("a replacement generation reconciled the retained claim")
	}
	n.consumeError, n.outcome, n.duringConsume = nil, domain.SubscriptionAlreadyRedeemed, nil
	r, _ = f.record()
	reconcile := &pb.ReconcileSubscriptionCreditRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, OperationId: string(old.ID), ConnectionId: string(old.ConnectionID), GenerationId: string(old.Generation)}
	if _, err := f.client.ReconcileSubscriptionCredit(context.Background(), subscriptionRequest(f.service.Identity.Token, reconcile)); err != nil {
		t.Fatal(err)
	}
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	_, a = f.record()
	o := a.Subscription.ServerCredit
	if n.count() != 2 || n.sends[1].ID != old.ID || n.sends[1].CreditID != old.CreditID || o.Phase != domain.SubscriptionObservationSucceeded || o.Outcome != domain.SubscriptionAlreadyRedeemed {
		t.Fatal("reconciliation changed key or selection", o)
	}
	if !referenceExists(t, f, captured) || !referenceExists(t, f, a.Subscription.Generation) {
		t.Fatal("settled credit retired its captured reference instead of retaining it")
	}
	if replay, err := f.client.ReconcileSubscriptionCredit(context.Background(), subscriptionRequest(f.service.Identity.Token, reconcile)); err != nil || !replay.Msg.Replayed {
		t.Fatal("reconcile receipt replay failed", err)
	}
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	if n.count() != 2 {
		t.Fatal("reconcile replay resent")
	}
}

func TestAccessCreditCleanupUncertaintyNeverFailsOrReleasesExecute(t *testing.T) {
	f, n, lease := newAccessCreditFixture(t, accessCreditOptions{details: true, active: true})
	_, before := f.record()
	n.closeError = errors.New("synthetic cleanup failure")
	requestAccessCredit(t, f, requestServerCredit(f))
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	r, a := f.record()
	o := a.Subscription.ServerCredit
	if o.Phase != domain.SubscriptionObservationUncertain || o.CleanupConfirmed || a.Subscription.RecoveryRequired || a.Health != domain.AccountReady || !reflect.DeepEqual(a.Subscription.Lease, before.Subscription.Lease) {
		t.Fatal("cleanup uncertainty touched Execute", o)
	}
	reconcile := &pb.ReconcileSubscriptionCreditRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, OperationId: string(o.ID), ConnectionId: string(o.ConnectionID), GenerationId: string(o.Generation)}
	if _, err := f.client.ReconcileSubscriptionCredit(context.Background(), subscriptionRequest(f.service.Identity.Token, reconcile)); err == nil {
		t.Fatal("unconfirmed cleanup authorized a retry")
	}
	// The original Execute still finishes normally while the credit is fenced.
	rotateExecution(t, f, lease, "second")
	_, a = f.record()
	if a.Subscription.Lease != nil || a.Subscription.RecoveryRequired || a.Health != domain.AccountReady || !referenceExists(t, f, o.Generation) {
		t.Fatal("Execute settlement depended on credit cleanup or lost the retained reference")
	}
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	if n.count() != 1 {
		t.Fatal("terminal/uncertain work replayed")
	}
}

func TestAccessCreditQuotaFailureRetainsConsumptionAndPriorQuota(t *testing.T) {
	f, n, _ := newAccessCreditFixture(t, accessCreditOptions{details: true})
	n.quotaError = domain.Fail(domain.Unavailable, "Synthetic quota failure.", "")
	_, before := f.record()
	requestAccessCredit(t, f, requestServerCredit(f))
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	_, a := f.record()
	o := a.Subscription.ServerCredit
	if o.Phase != domain.SubscriptionObservationSucceeded || o.Outcome != domain.SubscriptionReset || o.QuotaErrorCode != domain.Unavailable || !reflect.DeepEqual(before.Quota, a.Quota) || a.Subscription.QuotaState != domain.ObservationFailed {
		t.Fatal("read failure erased consumption or last good quota", o)
	}
}

func TestAccessCreditRestartBeforeSendCancelsAndRetiresReference(t *testing.T) {
	f, n, lease := newAccessCreditFixture(t, accessCreditOptions{details: true, active: true})
	_, before := f.record()
	requestAccessCredit(t, f, requestServerCredit(f))
	rotateExecution(t, f, lease, "second")
	mutateCreditAccount(t, f, func(a *domain.Account) { a.Subscription.ServerCredit.AttemptEpoch = domain.NewID() })
	if err := f.service.initializeServerQuotas(creditContext()); err != nil {
		t.Fatal(err)
	}
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	_, a := f.record()
	o := a.Subscription.ServerCredit
	if n.count() != 0 || n.opens != 0 || o.Phase != domain.SubscriptionObservationFailed || o.ErrorCode != domain.Canceled || o.EverSent {
		t.Fatal("restart sent a queued claim", o)
	}
	if !referenceExists(t, f, before.Subscription.Generation) || a.Subscription.RecoveryRequired || a.Health != domain.AccountReady {
		t.Fatal("restart retired the obsolete reference or failed Execute settlement")
	}
}

func TestAccessCreditSendGateRejectsFutureGrantAndRevokedActor(t *testing.T) {
	t.Run("future-grant", func(t *testing.T) {
		f, n, _ := newAccessCreditFixture(t, accessCreditOptions{details: true})
		requestAccessCredit(t, f, requestServerCredit(f))
		mutateCreditAccount(t, f, func(a *domain.Account) {
			(*a.Subscription.ResetCredits.Credits)[0].GrantedAt = time.Now().Add(time.Hour)
		})
		f.service.runServerCredit(creditContext(), f.input.AccountID)
		_, a := f.record()
		if n.count() != 0 || a.Subscription.ServerCredit.Phase != domain.SubscriptionObservationFailed || a.Subscription.ServerCredit.EverSent {
			t.Fatal("future grant was spent early")
		}
	})
	t.Run("revoked-actor", func(t *testing.T) {
		f, n, _ := newAccessCreditFixture(t, accessCreditOptions{details: true})
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
	})
}

func TestAccessCreditRejectsNonDirectRouteBeforeNativeLaunch(t *testing.T) {
	f, n, _ := newAccessCreditFixture(t, accessCreditOptions{details: true})
	route := domain.NetworkRoute{ProfileID: domain.NewID(), ProfileRevision: 1, Profile: domain.NetworkProfile{ProxyDefinition: domain.ProxyDefinition{Name: "Credit proxy fixture", Mode: domain.ProxyHTTP, Host: "127.0.0.1", Port: 3128}}}
	_, err := f.service.Store.Mutate(creditContext(), domain.NewID(), "fixture.credit.route", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.NetworkRouteKind, domain.NewID(), 0, "", "", route)
	})
	if err != nil {
		t.Fatal(err)
	}
	requestAccessCredit(t, f, requestServerCredit(f))
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	_, a := f.record()
	if n.opens != 0 || n.count() != 0 || a.Subscription.ServerCredit.ErrorCode != domain.Unsupported || a.Subscription.ServerCredit.Phase != domain.SubscriptionObservationFailed {
		t.Fatal("unsupported route launched native", a.Subscription.ServerCredit)
	}
}

func TestAccessCreditFencesCompetingServerOwnersAndLifecycle(t *testing.T) {
	f, _, _ := newAccessCreditFixture(t, accessCreditOptions{details: true, active: true})
	requestAccessCredit(t, f, requestServerCredit(f))
	r, a := f.record()
	if serverQuotaReady(a) || !a.Subscription.AccessOnlyCredit() || a.Subscription.ExclusiveServerObservationActive() {
		t.Fatal("access-only credit is exclusive or permits competing quota")
	}
	logout := &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT}
	if _, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, logout)); err == nil {
		t.Fatal("logout bypassed credit fence")
	}
	if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, requestServerQuota(f))); err == nil {
		t.Fatal("quota bypassed credit fence")
	}
	if doctorSubscriptionState(a) != domain.DiagnosticUnavailable || failedCleanupCandidate(a) {
		t.Fatal("diagnostics or cleanup bypassed credit fence")
	}
}

func TestAccessCreditCapabilityIsAdvertisedWithHistoricalCapability(t *testing.T) {
	f, _, _ := newAccessCreditFixture(t, accessCreditOptions{})
	status, err := f.service.GetStatus(context.Background(), connect.NewRequest(&pb.GetStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	have := map[pb.SystemCapability]bool{}
	for _, c := range status.Msg.Capabilities {
		have[c] = true
	}
	if !have[pb.SystemCapability_SYSTEM_CAPABILITY_SERVER_SUBSCRIPTION_RESET_CREDITS_V2] || !have[pb.SystemCapability_SYSTEM_CAPABILITY_SERVER_SUBSCRIPTION_RESET_CREDITS_V1] || !have[pb.SystemCapability_SYSTEM_CAPABILITY_SERVER_SUBSCRIPTION_QUOTA_V2] {
		t.Fatal("capability negotiation lost an independent capability")
	}
}

// A valid provider outcome whose checkpoint fails must be recorded durably
// before the rotated credential is retired, so a failed retirement or crash
// cannot strand an ambiguous spend.
func TestAccessCreditRecordsOutcomeBeforeRetiringCredentialWhenCheckpointFails(t *testing.T) {
	f, n, lease := newAccessCreditFixture(t, accessCreditOptions{details: true, active: true})
	_, before := f.record()
	captured := before.Subscription.Generation
	n.duringConsume = func() {
		rotateExecution(t, f, lease, "second")
		// Deny only the in-flight checkpoint, which needs the send claim.
		mutateCreditAccount(t, f, func(a *domain.Account) { a.Subscription.ServerCredit.SendClaimed = false })
	}
	f.secrets.deleteError = domain.Fail(domain.Unavailable, "Fixture cleanup failure.", "")
	requestAccessCredit(t, f, requestServerCredit(f))
	f.service.runServerCredit(creditContext(), f.input.AccountID)
	_, a := f.record()
	o := a.Subscription.ServerCredit
	if n.count() != 1 || o == nil || !o.EverSent || o.Outcome != domain.SubscriptionReset {
		t.Fatal("the provider outcome was not durably recorded before retirement", o)
	}
	if !referenceExists(t, f, captured) {
		t.Fatal("the credential was retired before its outcome was durable")
	}
	if n.count() != 1 {
		t.Fatal("recovery resent consumption")
	}
}
