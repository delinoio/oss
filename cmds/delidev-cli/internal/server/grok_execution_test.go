// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// grokExecWorker is one Worker device and instance with its claimed execute job.
type grokExecWorker struct {
	token                          string
	machine, instance, device, job domain.ID
	revision                       uint64
}

// prepareGrokExecution freezes a Grok subscription assignment for the connected
// account on the fixture's own Worker and grants it the given capabilities.
func (f *grokFixture) prepareGrokExecution(capabilities ...domain.WorkerCapability) grokExecWorker {
	f.t.Helper()
	_, account := f.record()
	c := &f.input.Configuration
	c.Harness, c.SubscriptionService, c.Subscription, c.ProviderID = domain.GrokBuild, domain.SubscriptionGrok, true, ""
	c.ModelID = (domain.ModelIdentity{SubscriptionService: domain.SubscriptionGrok, NativeID: c.NativeModel}).Key()
	c.Options = domain.AgentOptions{Permission: domain.PermissionDefault}
	c.GrokContext = &domain.GrokModelContext{Tokens: 48000, Source: domain.UserDeclared}
	f.input.ConnectionID = account.Connection.ID
	observed := time.Now().UTC()
	f.input.Installation = domain.Installation{Harness: domain.GrokBuild, State: domain.InstallationDetected, Version: domain.GrokSubscriptionBaselineVersion, ResolvedPath: "/controlled/native/grok", ProtocolVerified: true, ObservedAt: &observed, Protocol: &domain.ProtocolObservation{Protocol: domain.ProtocolFor(domain.GrokBuild), State: domain.ProtocolVerified}}
	digest, err := f.input.Configuration.Digest()
	if err != nil {
		f.t.Fatal(err)
	}
	f.input.ConfigurationDigest = digest
	worker := grokExecWorker{token: f.workerToken, machine: f.input.MachineID, instance: f.instance, job: f.job}
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok-execution", nil, func(tx *store.Tx) (any, error) {
		mr, machine, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		machine.WorkerCapabilities = capabilities
		if _, err := tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine); err != nil {
			return nil, err
		}
		sr, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		session.InitialExecution.ConnectionID = f.input.ConnectionID
		session.InitialExecution.Configuration = f.input.Configuration
		session.InitialExecution.ConfigurationDigest = f.input.ConfigurationDigest
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.SessionID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		jr, err := tx.Get(domain.JobKind, f.job)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](jr)
		if err != nil {
			return nil, err
		}
		job.Input, _ = json.Marshal(f.input)
		updated, err := tx.PutJob(jr.ID, jr.Revision, jr.SessionID, jr.ProjectID, job)
		worker.revision = updated.Revision
		return nil, err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return worker
}

// addGrokWorker registers an independent Worker device, instance and claimed job
// for the same frozen assignment on its own machine.
func (f *grokFixture) addGrokWorker(capabilities ...domain.WorkerCapability) grokExecWorker {
	f.t.Helper()
	token, err := security.RandomToken()
	if err != nil {
		f.t.Fatal(err)
	}
	w := grokExecWorker{token: token, machine: domain.NewID(), instance: domain.NewID(), device: domain.NewID(), job: domain.NewID()}
	input := f.input
	input.MachineID = w.machine
	observed := time.Now().UTC()
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok-worker", nil, func(tx *store.Tx) (any, error) {
		installation := f.input.Installation
		installation.ObservedAt = &observed
		if _, err := tx.Put(domain.MachineKind, w.machine, 0, "", "", domain.Machine{Name: "Second", OS: "linux", Architecture: "arm64", WorkerCapabilities: capabilities, Installations: []domain.Installation{installation}, LastSeen: observed}); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.DeviceKind, w.device, 0, "", "", domain.Device{Name: "Second Worker", Type: domain.WorkerDevice, MachineID: w.machine}); err != nil {
			return nil, err
		}
		digest := sha256.Sum256([]byte(w.token))
		if err := tx.PutCredential(w.device, digest[:]); err != nil {
			return nil, err
		}
		if err := tx.SetWorkerInstance(w.machine, w.instance, observed); err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(input)
		updated, err := tx.PutJob(w.job, 0, f.input.SessionID, "", domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobClaimed, MachineID: w.machine, InstanceID: w.instance, AssignedDeviceID: w.device, Input: raw, AcceptedAt: observed})
		w.revision = updated.Revision
		return nil, err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return w
}

func (f *grokFixture) takeExecution(w grokExecWorker, request domain.ID) (*pb.TakeSubscriptionResponse, error) {
	response, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(w.token, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(request), Id: string(f.input.AccountID), ExpectedRevision: w.revision}, MachineId: string(w.machine), InstanceId: string(w.instance), OperationId: string(w.job), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (f *grokFixture) finishExecution(w grokExecWorker, lease *pb.TakeSubscriptionResponse, request domain.ID, bundle []byte, success, refresh, cleanup bool) (*pb.FinishSubscriptionResponse, error) {
	response, err := f.client.FinishSubscription(context.Background(), subscriptionRequest(w.token, &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(request), Id: string(f.input.AccountID), ExpectedRevision: lease.LeaseRevision}, LeaseId: lease.LeaseId, GenerationId: lease.GenerationId, MachineId: string(w.machine), InstanceId: string(w.instance), Bundle: bytes.Clone(bundle), Succeeded: success, RefreshConfirmed: refresh, CleanupConfirmed: cleanup}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func requireProblem(t *testing.T, err error, code domain.Code, what string) {
	t.Helper()
	if err == nil || domain.SafeError(rpc.ClientError(err)).Code != code {
		t.Fatalf("%s: got %v, want %s", what, err, code)
	}
}

func TestGrokExecutionLeaseDeliversTheStoredEnvelopeAndReleasesAnUnchangedCredential(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	w := f.prepareGrokExecution(domain.ManagedGrokSubscriptionsV1)
	stored, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.takeExecution(w, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(lease.Bundle, stored) || lease.GenerationId != string(generation) || lease.LeaseRevision == 0 {
		t.Fatal("the execute lease did not deliver the stored envelope of the settled generation")
	}
	_, a := f.record()
	l := a.Subscription.Lease
	if l == nil || l.Action != domain.SubscriptionExecute || l.OperationID != w.job || l.Generation != generation || a.Subscription.Pending != nil {
		t.Fatal("the lease is not the exclusive execute lease of the job")
	}
	if _, err := f.finishExecution(w, lease, domain.NewID(), nil, true, false, true); err != nil {
		t.Fatal(err)
	}
	_, a = f.record()
	if a.Subscription.Lease != nil || a.Subscription.RecoveryRequired || a.Subscription.Generation != generation || a.Health != domain.AccountReady {
		t.Fatal("an unchanged credential did not release the lease on the same generation")
	}
	if refs := f.references(); len(refs) != 1 || !refs[generation] {
		t.Fatal("an unchanged credential changed the vault")
	}
}

func TestGrokExecutionRequiresTheNegotiatedWorkerCapability(t *testing.T) {
	f := newGrokFixture(t)
	f.login("synthetic-principal-1")
	for name, capabilities := range map[string][]domain.WorkerCapability{
		"none":         nil,
		"codex only":   {domain.ManagedCodexSubscriptionsV1},
		"claude only":  {domain.ManagedClaudeExecutionV1, domain.NativeClaudeSubscriptionsV1},
		"startup only": {domain.ExecutionStartupV1},
	} {
		t.Run(name, func(t *testing.T) {
			w := f.prepareGrokExecution(capabilities...)
			lease, err := f.takeExecution(w, domain.NewID())
			requireProblem(t, err, domain.Unsupported, "a Worker without capability 21 received the bundle")
			if lease != nil {
				t.Fatal("a bundle was delivered")
			}
			if _, a := f.record(); a.Subscription.Lease != nil || a.Subscription.RecoveryRequired {
				t.Fatal("a refused Take claimed the account")
			}
		})
	}
}

func TestGrokExecutionLeasesSerializeOneAccountAndRunDifferentAccountsInParallel(t *testing.T) {
	f := newGrokFixture(t)
	f.login("synthetic-principal-1")
	first := f.prepareGrokExecution(domain.ManagedGrokSubscriptionsV1)
	second := f.addGrokWorker(domain.ManagedGrokSubscriptionsV1)
	lease, err := f.takeExecution(first, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	// A second session on the same account receives the definite busy refusal that
	// the Worker retries; it never receives a second copy of the credential.
	other, err := f.takeExecution(second, domain.NewID())
	requireProblem(t, err, domain.ResourceExhausted, "a second session shared the exclusive account lease")
	if other != nil {
		t.Fatal("a second bundle was delivered")
	}
	if _, err := f.finishExecution(second, lease, domain.NewID(), nil, true, false, true); err == nil {
		t.Fatal("another Worker finished the original lease")
	}
	if _, err := f.finishExecution(first, lease, domain.NewID(), nil, true, false, true); err != nil {
		t.Fatal(err)
	}
	if next, err := f.takeExecution(second, domain.NewID()); err != nil {
		t.Fatal(err)
	} else if _, err := f.finishExecution(second, next, domain.NewID(), nil, true, false, true); err != nil {
		t.Fatal(err)
	}

	// A different account holds its own lease concurrently.
	g := newGrokFixture(t)
	g.login("synthetic-principal-2")
	gw := g.prepareGrokExecution(domain.ManagedGrokSubscriptionsV1)
	held, err := g.takeExecution(gw, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	if _, a := f.record(); a.Subscription.Lease != nil {
		t.Fatal("the first account was affected by the second account's lease")
	}
	again, err := f.takeExecution(first, domain.NewID())
	if err != nil {
		t.Fatal("two accounts did not run in parallel", err)
	}
	if _, err := f.finishExecution(first, again, domain.NewID(), nil, true, false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := g.finishExecution(gw, held, domain.NewID(), nil, true, false, true); err != nil {
		t.Fatal(err)
	}
}

func TestGrokExecutionRefreshReturnsTheRotatedBundleOnlyAfterIdentityVerification(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	w := f.prepareGrokExecution(domain.ManagedGrokSubscriptionsV1)

	// Another identity, an unrotated credential and a foreign generation are
	// refused and fence the account for a cleanup-only recovery. A bundle without
	// the refresh confirmation is a malformed report: refused without a fence, so
	// the genuine owner can still report correctly.
	for _, test := range []struct {
		name    string
		bundle  func(domain.ID) []byte
		refresh bool
		fenced  bool
	}{
		{"foreign identity", func(g domain.ID) []byte { return f.sealed(t, f.input.AccountID, g, "synthetic-principal-2") }, true, true},
		{"unrotated", func(g domain.ID) []byte { return f.sealed(t, f.input.AccountID, g, "synthetic-principal-1") }, true, true},
		{"no confirmation", func(g domain.ID) []byte { return f.grokRotatedEnvelope(g) }, false, false},
		{"wrong generation", func(domain.ID) []byte { return f.grokRotatedEnvelope(domain.NewID()) }, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			lease, err := f.takeExecution(w, domain.NewID())
			if err != nil {
				t.Fatal(err)
			}
			request := domain.NewID()
			if _, err := f.finishExecution(w, lease, request, test.bundle(request), true, test.refresh, true); err == nil {
				t.Fatal("an unverified credential was accepted")
			}
			_, a := f.record()
			if a.Subscription.Generation != generation || a.Subscription.Lease == nil || a.Subscription.RecoveryRequired != test.fenced {
				t.Fatal("a refused credential changed the generation, released the lease or mis-fenced")
			}
			if refs := f.references(); len(refs) != 1 || !refs[generation] {
				t.Fatal("a refused credential left a staged generation")
			}
			if test.fenced {
				if _, err := f.finishExecution(w, lease, domain.NewID(), nil, false, false, true); err != nil {
					t.Fatal(err)
				}
			} else if _, err := f.finishExecution(w, lease, domain.NewID(), nil, true, false, true); err != nil {
				t.Fatal(err)
			}
			if _, a := f.record(); a.Subscription.Lease != nil || a.Subscription.RecoveryRequired {
				t.Fatal("the owner could not settle the lease")
			}
		})
	}

	lease, err := f.takeExecution(w, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	rotated := domain.NewID()
	if _, err := f.finishExecution(w, lease, rotated, f.grokRotatedEnvelope(rotated), true, true, true); err != nil {
		t.Fatal(err)
	}
	_, a := f.record()
	want := f.service.grokIdentityCommitment(grokServerBundle(t, "synthetic-principal-1"))
	if a.Subscription.Generation != rotated || a.Subscription.IdentityCommitment != want || a.Subscription.Lease != nil || a.Subscription.RecoveryRequired || a.Health != domain.AccountReady {
		t.Fatal("the rotated credential was not published under the same identity")
	}
	if refs := f.references(); len(refs) != 1 || !refs[rotated] {
		t.Fatal("the superseded generation was not removed")
	}
	// The next execution receives the rotated generation.
	next, err := f.takeExecution(w, domain.NewID())
	if err != nil || next.GenerationId != string(rotated) {
		t.Fatal("the next execution did not receive the rotated generation", err)
	}
	if _, err := f.finishExecution(w, next, domain.NewID(), nil, true, false, true); err != nil {
		t.Fatal(err)
	}
}

// The settlement commit releases the lease before the superseded generation is
// removed. A removal failure after that commit must not fence the account: it
// holds no lease, so the fence would have no recovery path. The committed
// publication stands, the superseded generation stays vault-owned and is never
// granted, and the next execution receives the published generation.
func TestGrokExecutionPostCommitCleanupFailureNeitherFencesNorStrandsTheAccount(t *testing.T) {
	f := newGrokFixture(t)
	logs := f.captureLogs()
	generation := f.login("synthetic-principal-1")
	w := f.prepareGrokExecution(domain.ManagedGrokSubscriptionsV1)
	lease, err := f.takeExecution(w, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	f.secrets.afterPut = func() {
		f.secrets.mu.Lock()
		f.secrets.deleteError = domain.Fail(domain.Unavailable, "The vault removal failed.", "")
		f.secrets.mu.Unlock()
	}
	rotated := domain.NewID()
	if _, err := f.finishExecution(w, lease, rotated, f.grokRotatedEnvelope(rotated), true, true, true); err != nil {
		t.Fatalf("a failed removal of the superseded generation failed a committed settlement: %v", err)
	}
	_, a := f.record()
	if a.Subscription.Generation != rotated || a.Subscription.Lease != nil || a.Subscription.RecoveryRequired || a.Health != domain.AccountReady {
		t.Fatal("the committed publication was not kept or a settled lease was fenced")
	}
	if refs := f.references(); !refs[rotated] || !refs[generation] {
		t.Fatal("the published generation was retired, or the superseded generation vanished despite the failed removal")
	}
	logs.mu.Lock()
	text := logs.buf.String()
	logs.mu.Unlock()
	if !strings.Contains(text, "grok_subscription_cleanup_failed") {
		t.Fatal("the failed removal was not logged")
	}
	// The vault recovers; the account runs again on the published generation.
	f.secrets.mu.Lock()
	f.secrets.deleteError = nil
	f.secrets.mu.Unlock()
	next, err := f.takeExecution(w, domain.NewID())
	if err != nil || next.GenerationId != string(rotated) {
		t.Fatal("the next execution did not receive the published generation", err)
	}
	if _, err := f.finishExecution(w, next, domain.NewID(), nil, true, false, true); err != nil {
		t.Fatal(err)
	}
	if refs := f.references(); len(refs) != 1 || !refs[rotated] {
		t.Fatalf("the next completed operation did not remove the superseded generation: %v", refs)
	}
}

func TestGrokExecutionUnverifiedExitFencesThenCleanupOnlyRecoveryReleases(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	w := f.prepareGrokExecution(domain.ManagedGrokSubscriptionsV1)
	lease, err := f.takeExecution(w, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	// The Worker could not verify the native file (success false) but retired it.
	if _, err := f.finishExecution(w, lease, domain.NewID(), nil, false, false, true); err != nil {
		t.Fatal(err)
	}
	_, a := f.record()
	if !a.Subscription.RecoveryRequired || a.Subscription.Lease == nil || a.Subscription.Lease.CleanupUnconfirmed || a.Health != domain.AccountFailed {
		t.Fatal("an unverified exit did not fence the account for recovery")
	}
	if _, err := f.takeExecution(w, domain.NewID()); err == nil {
		t.Fatal("a fenced account granted another lease")
	}
	// Recovery accepts cleanup only: never success, a refresh or a bundle.
	if _, err := f.finishExecution(w, lease, domain.NewID(), nil, true, false, true); err == nil {
		t.Fatal("recovery accepted execution success")
	}
	if _, err := f.finishExecution(w, lease, domain.NewID(), f.grokRotatedEnvelope(domain.NewID()), false, true, true); err == nil {
		t.Fatal("recovery accepted a bundle")
	}
	if _, err := f.finishExecution(w, lease, domain.NewID(), nil, false, false, false); err == nil {
		t.Fatal("recovery accepted an unconfirmed cleanup report")
	}
	if _, err := f.finishExecution(w, lease, domain.NewID(), nil, false, false, true); err != nil {
		t.Fatal(err)
	}
	_, a = f.record()
	if a.Subscription.RecoveryRequired || a.Subscription.Lease != nil || a.Subscription.Generation != generation || a.Health != domain.AccountReady {
		t.Fatal("cleanup-only recovery did not release the account on its usable generation")
	}
}

func TestGrokExecutionUnconfirmedCleanupIsRetriedByTheOriginalOwnerOnly(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	w := f.prepareGrokExecution(domain.ManagedGrokSubscriptionsV1)
	other := f.addGrokWorker(domain.ManagedGrokSubscriptionsV1)
	lease, err := f.takeExecution(w, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.finishExecution(w, lease, domain.NewID(), nil, true, false, false); err != nil {
		t.Fatal(err)
	}
	_, a := f.record()
	if !a.Subscription.RecoveryRequired || a.Subscription.Lease == nil || !a.Subscription.Lease.CleanupUnconfirmed {
		t.Fatal("unconfirmed cleanup did not retain the lease")
	}
	if _, err := f.finishExecution(other, lease, domain.NewID(), nil, true, false, true); err == nil {
		t.Fatal("a foreign Worker settled the retained lease")
	}
	rotated := domain.NewID()
	if _, err := f.finishExecution(w, lease, rotated, f.grokRotatedEnvelope(rotated), true, true, true); err != nil {
		t.Fatal(err)
	}
	_, a = f.record()
	if a.Subscription.Lease != nil || a.Subscription.RecoveryRequired || a.Subscription.Generation != rotated || a.Health != domain.AccountReady {
		t.Fatal("the owner's retry did not release the lease with the verified refresh")
	}
	if refs := f.references(); len(refs) != 1 || !refs[rotated] || refs[generation] {
		t.Fatal("the retry left the superseded generation")
	}
}

// A server restart changes the epoch but leaves the unconfirmed-cleanup flag of a
// retained lease set. The pre-restart owner is then held to cleanup-only recovery:
// it cannot publish a rotated bundle or report success.
func TestGrokExecutionStaleEpochRetryOfAnUnconfirmedCleanupIsCleanupOnly(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	w := f.prepareGrokExecution(domain.ManagedGrokSubscriptionsV1)
	lease, err := f.takeExecution(w, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.finishExecution(w, lease, domain.NewID(), nil, true, false, false); err != nil {
		t.Fatal(err)
	}
	f.service.subscriptionServerEpoch()
	f.service.subscriptionEpoch = domain.NewID()
	if err := f.service.retainLostSubscriptionLeases("", "", false); err != nil {
		t.Fatal(err)
	}
	if _, a := f.record(); !a.Subscription.Lease.CleanupUnconfirmed || !a.Subscription.RecoveryRequired {
		t.Fatal("the restart cleared the unconfirmed-cleanup fence")
	}
	rotated := domain.NewID()
	if _, err := f.finishExecution(w, lease, rotated, f.grokRotatedEnvelope(rotated), true, true, true); err == nil {
		t.Fatal("a pre-restart owner published a rotated bundle")
	}
	if _, err := f.finishExecution(w, lease, domain.NewID(), nil, true, false, true); err == nil {
		t.Fatal("a pre-restart owner reported success")
	}
	if _, a := f.record(); a.Subscription.Lease == nil || a.Subscription.Generation != generation {
		t.Fatal("a refused stale report changed the account")
	}
	if _, err := f.finishExecution(w, lease, domain.NewID(), nil, false, false, true); err != nil {
		t.Fatalf("the cleanup-only report was refused: %v", err)
	}
	_, a := f.record()
	if a.Subscription.Lease != nil || a.Subscription.RecoveryRequired || a.Subscription.Generation != generation {
		t.Fatal("cleanup-only recovery did not release the lease on the original generation")
	}
}

func TestGrokExecutionCleanupOnlyRecoveryCrossesAnUnconfirmedCleanupFence(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	w := f.prepareGrokExecution(domain.ManagedGrokSubscriptionsV1)
	lease, err := f.takeExecution(w, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.finishExecution(w, lease, domain.NewID(), nil, true, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.finishExecution(w, lease, domain.NewID(), nil, false, false, true); err != nil {
		t.Fatal(err)
	}
	_, a := f.record()
	if a.Subscription.Lease != nil || a.Subscription.RecoveryRequired || a.Subscription.Generation != generation || a.Health != domain.AccountReady {
		t.Fatal("the owner's cleanup-only report did not release the unconfirmed-cleanup fence")
	}
}

func TestGrokExecutionReplayedTakeFencesAndNeverRedistributesTheBundle(t *testing.T) {
	f := newGrokFixture(t)
	f.login("synthetic-principal-1")
	w := f.prepareGrokExecution(domain.ManagedGrokSubscriptionsV1)
	request := domain.NewID()
	lease, err := f.takeExecution(w, request)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.takeExecution(w, request)
	if err == nil || replay != nil {
		t.Fatal("a replayed Take redistributed the bundle")
	}
	_, a := f.record()
	if !a.Subscription.RecoveryRequired || a.Subscription.Lease == nil || a.Health != domain.AccountFailed {
		t.Fatal("the replayed Take did not fence the unfinished lease")
	}
	if _, err := f.finishExecution(w, lease, domain.NewID(), nil, false, false, true); err != nil {
		t.Fatal(err)
	}
	if _, a := f.record(); a.Subscription.Lease != nil || a.Subscription.RecoveryRequired {
		t.Fatal("the original owner could not clean up the replay fence")
	}
}

func TestGrokExecutionServerRestartIsRecoveredByTheOriginalWorkerOnly(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	w := f.prepareGrokExecution(domain.ManagedGrokSubscriptionsV1)
	lease, err := f.takeExecution(w, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	// A restarted server has a new epoch and fences every lease of the old one.
	f.service.subscriptionServerEpoch()
	f.service.subscriptionEpoch = domain.NewID()
	if err := f.service.retainLostSubscriptionLeases("", "", false); err != nil {
		t.Fatal(err)
	}
	_, a := f.record()
	if !a.Subscription.RecoveryRequired || a.Subscription.Lease == nil || a.Subscription.Generation != generation {
		t.Fatal("the restart did not fence the lease under its generation")
	}
	other := f.addGrokWorker(domain.ManagedGrokSubscriptionsV1)
	// An execution report from the old epoch is refused; only cleanup is accepted,
	// and only from the original device and Runner.
	if _, err := f.finishExecution(w, lease, domain.NewID(), nil, true, false, true); err == nil {
		t.Fatal("a stale-epoch execution report was accepted")
	}
	if _, err := f.finishExecution(other, lease, domain.NewID(), nil, false, false, true); err == nil {
		t.Fatal("a foreign Worker recovered the lease")
	}
	if _, err := f.finishExecution(w, lease, domain.NewID(), nil, false, false, true); err != nil {
		t.Fatal(err)
	}
	if _, a := f.record(); a.Subscription.Lease != nil || a.Subscription.RecoveryRequired || a.Health != domain.AccountReady {
		t.Fatal("the original Worker's cleanup did not release the account")
	}
}

func TestGrokExecutionRecoveryDoesNotRestoreReadinessOnAnUnusableGeneration(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	w := f.prepareGrokExecution(domain.ManagedGrokSubscriptionsV1)
	lease, err := f.takeExecution(w, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.retainLostSubscriptionLeases(w.machine, w.instance, true); err != nil {
		t.Fatal(err)
	}
	delete(f.secrets.values, credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin})
	if _, err := f.finishExecution(w, lease, domain.NewID(), nil, false, false, true); err != nil {
		t.Fatal(err)
	}
	if _, a := f.record(); a.Subscription.Lease != nil || a.Health != domain.AccountFailed {
		t.Fatal("recovery restored readiness on a credential that no longer opens")
	}
}

func TestGrokExecutionRefusesNewWorkWhileLoggingOutAndLogoutWaitsForTheLease(t *testing.T) {
	f := newGrokFixture(t)
	generation := f.login("synthetic-principal-1")
	w := f.prepareGrokExecution(domain.ManagedGrokSubscriptionsV1)
	lease, err := f.takeExecution(w, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	// Accept the server-owned logout while the execution lease is held.
	r, _ := f.record()
	if _, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT})); err != nil {
		t.Fatal(err)
	}
	_, a := f.record()
	if !grokAccountLoggingOut(a) || a.Subscription.Lease == nil || a.Subscription.ServerOperation == nil || a.Subscription.ServerOperation.NativeStarted {
		t.Fatal("the logout did not wait behind the execution lease")
	}
	second := f.addGrokWorker(domain.ManagedGrokSubscriptionsV1)
	_, err = f.takeExecution(second, domain.NewID())
	requireProblem(t, err, domain.Unavailable, "new execution was not refused with the typed logging-out problem")
	// The original execution still settles; the account stays revoked for the
	// queued logout and the generation is untouched.
	if _, err := f.finishExecution(w, lease, domain.NewID(), nil, true, false, true); err != nil {
		t.Fatal(err)
	}
	_, a = f.record()
	if a.Subscription.Lease != nil || a.Health != domain.AccountRevoked || a.Subscription.Generation != generation || !grokLogoutCandidate(a) {
		t.Fatal("the execution exit did not leave the logout runnable")
	}
}

func TestGrokExecutionRefusesForeignAndCanceledAssignments(t *testing.T) {
	f := newGrokFixture(t)
	f.login("synthetic-principal-1")
	w := f.prepareGrokExecution(domain.ManagedGrokSubscriptionsV1)
	other := f.addGrokWorker(domain.ManagedGrokSubscriptionsV1)
	// Another Worker presenting this job never receives the bundle.
	foreign := w
	foreign.token, foreign.machine, foreign.instance = other.token, other.machine, other.instance
	if lease, err := f.takeExecution(foreign, domain.NewID()); err == nil || lease != nil {
		t.Fatal("a job claimed by another Worker received the bundle")
	}
	// A job whose cancellation was requested never receives it either.
	if _, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok-cancel-job", nil, func(tx *store.Tx) (any, error) {
		return nil, tx.RequestJobCancellation(w.job)
	}); err != nil {
		t.Fatal(err)
	}
	if lease, err := f.takeExecution(w, domain.NewID()); err == nil || lease != nil {
		t.Fatal("a canceled job received the bundle")
	}
	if _, a := f.record(); a.Subscription.Lease != nil || a.Subscription.RecoveryRequired {
		t.Fatal("a refused assignment claimed the account")
	}
}

func TestGrokCapabilitiesAreAdvertisedOnlyBehindTheSwitch(t *testing.T) {
	f := newGrokFixture(t)
	status := func() []pb.SystemCapability {
		response, err := f.service.GetStatus(context.Background(), connect.NewRequest(&pb.GetStatusRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		return response.Msg.Capabilities
	}
	if containsSystemCapability(status(), grokSystemExecutionReservation) {
		t.Fatal("System 40 is advertised by default")
	}
	grokManagedExecutionAdvertised = true
	t.Cleanup(func() { grokManagedExecutionAdvertised = false })
	if !containsSystemCapability(status(), grokSystemExecutionReservation) {
		t.Fatal("System 40 is not advertised once the switch is on")
	}
	if !containsSystemCapability(status(), grokSystemLoginReservation) {
		t.Fatal("the execution switch disabled independent server CLI login")
	}
}

func containsSystemCapability(values []pb.SystemCapability, wanted pb.SystemCapability) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func TestGrokWorkerCapabilityIsNegotiatedOnlyBehindTheSwitch(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	worker, paired := pairedWorker(t, ctx, Endpoint{URL: f.url}, f.service.Identity)
	client := delidevv1connect.NewWorkerServiceClient(http.DefaultClient, f.url)
	instance := domain.NewID()
	attach := func(capabilities ...pb.WorkerCapability) (*connect.Response[pb.AttachWorkerResponse], error) {
		return client.AttachWorker(ctx, ownerRequest(worker, &pb.AttachWorkerRequest{ProtocolVersion: 2, RequestId: string(domain.NewID()), MachineId: paired.Machine.Id, InstanceId: string(instance), Version: rpc.Version, Capabilities: capabilities}))
	}
	if _, err := attach(grokWorkerReservation); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("Worker capability 21 was not rejected as unknown by default: %v", err)
	}
	grokManagedExecutionAdvertised = true
	t.Cleanup(func() { grokManagedExecutionAdvertised = false })
	supported, err := attach(pb.WorkerCapability_WORKER_CAPABILITY_REPOSITORY_CLONE_V1)
	if err != nil || !slices.Contains(supported.Msg.SupportedWorkerCapabilities, grokWorkerReservation) {
		t.Fatal("the server did not echo Worker capability 21 once the switch is on", err)
	}
	if _, err := attach(grokWorkerReservation); err != nil {
		t.Fatal("a Worker that negotiated capability 21 was rejected", err)
	}
	var machine domain.Machine
	err = f.service.Store.Read(ctx, func(tx *store.Tx) error {
		_, value, err := activeMachine(tx, domain.ID(paired.Machine.Id))
		machine = value
		return err
	})
	if err != nil || !slices.Contains(machine.WorkerCapabilities, domain.ManagedGrokSubscriptionsV1) {
		t.Fatal("the negotiated capability was not retained", err)
	}
}

func (f *grokFixture) dispatchCheck() error {
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

func (f *grokFixture) registration(w grokExecWorker) *pb.RegisterExecutionRequest {
	digest := sha256.Sum256([]byte("managed-grok-registration-" + string(w.job)))
	return &pb.RegisterExecutionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(w.job), ExpectedRevision: w.revision}, MachineId: string(w.machine), InstanceId: string(w.instance), CredentialDigest: digest[:]}
}

func TestGrokDispatchAndRegistrationRequireTheNegotiatedCapabilityAndTheLease(t *testing.T) {
	f := newGrokFixture(t)
	f.login("synthetic-principal-1")
	// Without the capability, dispatch refuses the managed account.
	w := f.prepareGrokExecution()
	if err := f.dispatchCheck(); err == nil {
		t.Fatal("dispatch admitted a managed Grok account on a Worker without capability 21")
	}
	w = f.prepareGrokExecution(domain.ManagedGrokSubscriptionsV1)
	if err := f.dispatchCheck(); err != nil {
		t.Fatal("dispatch refused a managed Grok account on a capable Worker:", err)
	}
	// Registration (publication authority) needs the original execute lease.
	if _, err := f.authorityFixture.client.RegisterExecution(context.Background(), subscriptionRequest(w.token, f.registration(w))); err == nil {
		t.Fatal("registration succeeded without an execute lease")
	}
	lease, err := f.takeExecution(w, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	response, err := f.authorityFixture.client.RegisterExecution(context.Background(), subscriptionRequest(w.token, f.registration(w)))
	if err != nil {
		t.Fatal("registration failed under the original lease:", err)
	}
	if response.Msg.ProxyPath != "" {
		t.Fatal("a managed subscription was granted API relay authority")
	}
	// The relay itself never serves a managed subscription.
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		grant, err := tx.ExecutionGrantForJob(w.job)
		if err != nil {
			return err
		}
		scope, err := f.service.executionAuthority.scope(tx, grant)
		if err != nil {
			return err
		}
		if scope.SubscriptionService != domain.SubscriptionGrok || scope.Harness != domain.GrokBuild {
			t.Fatal("the registered scope lost its Grok subscription identity")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A lost capability revokes the publication scope of the running execution.
	if _, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok-capability-lost", nil, func(tx *store.Tx) (any, error) {
		mr, machine, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		machine.WorkerCapabilities = nil
		return tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		grant, err := tx.ExecutionGrantForJob(w.job)
		if err != nil {
			return err
		}
		_, err = f.service.executionAuthority.scope(tx, grant)
		return err
	}); err == nil {
		t.Fatal("a Worker that lost capability 21 kept its publication scope")
	}
	if _, err := f.finishExecution(w, lease, domain.NewID(), nil, true, false, true); err != nil {
		t.Fatal(err)
	}
}

func TestGrokDispatchRefusesNewWorkWhileLoggingOut(t *testing.T) {
	f := newGrokFixture(t)
	f.login("synthetic-principal-1")
	f.prepareGrokExecution(domain.ManagedGrokSubscriptionsV1)
	r, _ := f.record()
	if _, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT})); err != nil {
		t.Fatal(err)
	}
	if err := f.dispatchCheck(); err == nil || domain.SafeError(err).Code != domain.Unavailable || err.Error() == "" {
		t.Fatal("dispatch did not return the typed logging-out refusal", err)
	}
}

// A Worker is offered a Grok account only to clean up the private home of its own
// interrupted execution lease, and only with the negotiated capability.
func TestGrokWatchOffersOnlyTheOriginalWorkersInterruptedLease(t *testing.T) {
	f := newGrokFixture(t)
	f.login("synthetic-principal-1")
	w := f.prepareGrokExecution(domain.ManagedGrokSubscriptionsV1, domain.ExecutionStartupV1)
	if _, err := f.takeExecution(w, domain.NewID()); err != nil {
		t.Fatal(err)
	}
	offered := func(token string, machine, instance domain.ID) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stream, err := f.client.WatchSubscription(ctx, subscriptionRequest(token, &pb.WatchSubscriptionRequest{MachineId: string(machine), InstanceId: string(instance)}))
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		for stream.Receive() {
			if stream.Msg().Account == nil {
				return false
			}
			return true
		}
		return false
	}
	if offered(w.token, w.machine, w.instance) {
		t.Fatal("a healthy execution lease was offered to its Worker")
	}
	if err := f.service.retainLostSubscriptionLeases(w.machine, w.instance, true); err != nil {
		t.Fatal(err)
	}
	if !offered(w.token, w.machine, w.instance) {
		t.Fatal("the original Worker was not offered its fenced execution lease")
	}
	other := f.addGrokWorker(domain.ManagedGrokSubscriptionsV1, domain.ExecutionStartupV1)
	if offered(other.token, other.machine, other.instance) {
		t.Fatal("a foreign Worker was offered the lease")
	}
	if _, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok-capability-lost", nil, func(tx *store.Tx) (any, error) {
		mr, machine, err := activeMachine(tx, w.machine)
		if err != nil {
			return nil, err
		}
		machine.WorkerCapabilities = []domain.WorkerCapability{domain.ManagedCodexSubscriptionsV1, domain.ExecutionStartupV1}
		return tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
	}); err != nil {
		t.Fatal(err)
	}
	if offered(w.token, w.machine, w.instance) {
		t.Fatal("a Worker without capability 21 was offered a Grok account")
	}
}
