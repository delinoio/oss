// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// grokExecutionFixture drives Grok subscription execution leases over the real
// Connect handlers with Grok execution enabled and Worker capability 21
// negotiated by the selected Runner Device.
type grokExecutionFixture struct {
	*grokFixture
	generation domain.ID
}

func newGrokExecutionFixture(t *testing.T) *grokExecutionFixture {
	t.Helper()
	f := &grokExecutionFixture{grokFixture: newGrokFixture(t)}
	f.service.grokExecution = true
	f.setCapabilities(t, domain.ManagedGrokSubscriptionsV1)
	f.generation = f.login("synthetic-principal-1")
	return f
}

func (f *grokExecutionFixture) setCapabilities(t *testing.T, capabilities ...domain.WorkerCapability) {
	t.Helper()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.capabilities", nil, func(tx *store.Tx) (any, error) {
		mr, machine, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		machine.WorkerCapabilities = capabilities
		return tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// prepareJobs rewrites the fixture job as a Grok subscription execution and
// clones count extra claimed execute jobs that name the same account.
func (f *grokExecutionFixture) prepareJobs(count int) []memberJob {
	f.t.Helper()
	_, account := f.record()
	f.input.ConnectionID = account.Connection.ID
	c := &f.input.Configuration
	c.Harness, c.SubscriptionService, c.ProviderID, c.Subscription = domain.GrokBuild, domain.SubscriptionGrok, "", true
	c.ModelID = (domain.ModelIdentity{SubscriptionService: domain.SubscriptionGrok, NativeID: c.NativeModel}).Key()
	c.Options = domain.AgentOptions{Permission: domain.PermissionDefault}
	c.GrokContext = &domain.GrokModelContext{Tokens: 32000, Source: domain.UserDeclared}
	observed := time.Now().UTC()
	f.input.Installation = domain.Installation{Harness: domain.GrokBuild, State: domain.InstallationDetected, Version: domain.GrokSubscriptionBaselineVersion, ResolvedPath: "/controlled/native/grok", ProtocolVerified: true, ObservedAt: &observed, Protocol: &domain.ProtocolObservation{Protocol: domain.ProtocolFor(domain.GrokBuild), State: domain.ProtocolVerified}}
	f.input.ConfigurationDigest, _ = c.Digest()
	var jobs []memberJob
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.jobs", nil, func(tx *store.Tx) (any, error) {
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
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, memberJob{ID: updated.ID, Revision: updated.Revision})
		for i := 0; i < count; i++ {
			clone, err := tx.PutJob(domain.NewID(), 0, jr.SessionID, jr.ProjectID, job)
			if err != nil {
				return nil, err
			}
			jobs = append(jobs, memberJob{ID: clone.ID, Revision: clone.Revision})
		}
		return nil, nil
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return jobs
}

func (f *grokExecutionFixture) takeJob(job memberJob) (*pb.TakeSubscriptionResponse, error) {
	response, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: job.Revision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: string(job.ID), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func code(err error) domain.Code { return domain.SafeError(rpc.ClientError(err)).Code }

func (f *grokExecutionFixture) lease() *domain.SubscriptionLease {
	_, a := f.record()
	return a.Subscription.Lease
}

// stored returns the sealed envelope the server holds for the current generation.
func (f *grokExecutionFixture) stored(t *testing.T) []byte {
	t.Helper()
	_, a := f.record()
	raw, err := f.secrets.Get(context.Background(), credentials.Ref{Owner: f.input.AccountID, ID: a.Subscription.Generation, Purpose: credentials.AccountLogin})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// sealedCopy re-seals the delivered credential for the generation the Finish creates.
func (f *grokExecutionFixture) sealedCopy(t *testing.T, lease *pb.TakeSubscriptionResponse, generation domain.ID) []byte {
	t.Helper()
	bundle, err := subscription.OpenGrokBundle(lease.Bundle, f.service.Identity.ServerID, f.input.AccountID, domain.ID(lease.GenerationId))
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Clear()
	sealed, err := subscription.SealGrokBundle(f.service.Identity.ServerID, f.input.AccountID, generation, bundle)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func TestGrokExecuteTakeIsRefusedUnlessGrokExecutionIsEnabled(t *testing.T) {
	f := newGrokExecutionFixture(t)
	jobs := f.prepareJobs(0)
	f.service.grokExecution = false
	if _, err := f.takeJob(jobs[0]); err == nil || code(err) != domain.Unsupported {
		t.Fatalf("a disabled server admitted a Grok execute Take: %v", err)
	}
	if f.lease() != nil {
		t.Fatal("a refused Take created a lease")
	}
}

func TestGrokExecuteLeaseDeliversTheStoredEnvelopeAndSerializesTheAccount(t *testing.T) {
	f := newGrokExecutionFixture(t)
	jobs := f.prepareJobs(1)
	lease, err := f.takeJob(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(lease.Bundle, f.stored(t)) || lease.GenerationId != string(f.generation) || lease.LeaseRevision == 0 {
		t.Fatal("the execute lease did not deliver the stored generation")
	}
	if l := f.lease(); l == nil || l.Action != domain.SubscriptionExecute || l.Shared || l.OperationID != jobs[0].ID || l.Generation != f.generation {
		t.Fatal("the execute lease is not the exclusive singleton lease of the job")
	}
	// While the execute lease is active no second execution, login, refresh or
	// logout can take the account.
	if _, err := f.takeJob(jobs[1]); err == nil || code(err) != domain.ResourceExhausted {
		t.Fatalf("a second execution shared the account: %v", err)
	}
	if _, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH), pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH); err == nil || code(err) != domain.ResourceExhausted {
		t.Fatalf("a refresh was admitted during execution: %v", err)
	}
	if refs := f.references(); len(refs) != 1 || !refs[f.generation] {
		t.Fatal("the refused Takes changed the vault")
	}
}

func TestGrokExecuteFinishWithUnchangedCredentialKeepsTheGeneration(t *testing.T) {
	f := newGrokExecutionFixture(t)
	jobs := f.prepareJobs(1)
	lease, err := f.takeJob(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	next := domain.NewID()
	if _, err := f.finishWith(lease, next, f.sealedCopy(t, lease, next), true, false, true); err != nil {
		t.Fatalf("an unchanged credential was refused: %v", err)
	}
	_, a := f.record()
	if a.Subscription.Lease != nil || a.Subscription.Generation != f.generation || a.Subscription.RecoveryRequired || a.Health != domain.AccountReady {
		t.Fatal("an unchanged execution did not release the lease under the same generation")
	}
	if refs := f.references(); len(refs) != 1 || !refs[f.generation] {
		t.Fatal("an unchanged execution staged a new generation")
	}
	// The next waiting session now takes the same account.
	if _, err := f.takeJob(jobs[1]); err != nil {
		t.Fatalf("the serialized session did not take the released account: %v", err)
	}
}

func TestGrokExecuteFinishPublishesANativeRotation(t *testing.T) {
	f := newGrokExecutionFixture(t)
	jobs := f.prepareJobs(0)
	lease, err := f.takeJob(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	rotated := domain.NewID()
	if _, err := f.finishWith(lease, rotated, f.grokRotatedEnvelope(rotated), true, false, true); err != nil {
		t.Fatalf("a native rotation was refused: %v", err)
	}
	_, a := f.record()
	if a.Subscription.Lease != nil || a.Subscription.Generation != rotated || a.Subscription.IdentityCommitment != f.service.grokIdentityCommitment(grokServerBundle(t, "synthetic-principal-1")) {
		t.Fatal("the rotated credential was not published under the same identity")
	}
	if refs := f.references(); len(refs) != 1 || !refs[rotated] {
		t.Fatal("the superseded generation was retained")
	}
}

func TestGrokExecuteFinishRefusesAForeignOrRegressedCredential(t *testing.T) {
	f := newGrokExecutionFixture(t)
	jobs := f.prepareJobs(0)
	lease, err := f.takeJob(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	foreign := domain.NewID()
	if _, err := f.finishWith(lease, foreign, f.sealed(t, f.input.AccountID, foreign, "synthetic-principal-2"), true, false, true); err == nil {
		t.Fatal("another identity was published by an execution")
	}
	if _, a := f.record(); !a.Subscription.RecoveryRequired || a.Subscription.Generation != f.generation {
		t.Fatal("a foreign credential did not fence the lease under the current generation")
	}
	if refs := f.references(); len(refs) != 1 || !refs[f.generation] {
		t.Fatal("a refused Finish changed the vault")
	}
}

func TestGrokExecuteFinishReleasesWithoutAFenceWhenNothingUsableRemains(t *testing.T) {
	for name, test := range map[string]struct{ success bool }{"crash": {false}, "unreadable credential": {false}} {
		t.Run(name, func(t *testing.T) {
			f := newGrokExecutionFixture(t)
			jobs := f.prepareJobs(0)
			lease, err := f.takeJob(jobs[0])
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.finishWith(lease, domain.NewID(), nil, test.success, false, true); err != nil {
				t.Fatalf("confirmed cleanup after a failed run was refused: %v", err)
			}
			_, a := f.record()
			if a.Subscription.Lease != nil || a.Subscription.RecoveryRequired || a.Subscription.Generation != f.generation || a.Health != domain.AccountReady {
				t.Fatal("confirmed cleanup did not release the lease under the current generation")
			}
			if refs := f.references(); len(refs) != 1 || !refs[f.generation] {
				t.Fatal("a failed run changed the vault")
			}
		})
	}
}

func TestGrokExecuteFinishFencesUnconfirmedCleanup(t *testing.T) {
	f := newGrokExecutionFixture(t)
	jobs := f.prepareJobs(1)
	lease, err := f.takeJob(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.finishWith(lease, domain.NewID(), nil, true, false, false); err != nil {
		t.Fatal(err)
	}
	if _, a := f.record(); !a.Subscription.RecoveryRequired || a.Subscription.Lease == nil {
		t.Fatal("unconfirmed cleanup released the lease")
	}
	if _, err := f.takeJob(jobs[1]); err == nil {
		t.Fatal("a fenced account redistributed its credential")
	}
}

func TestGrokExecuteTakeRequiresTheExactClaimedGrokJobAndCapability(t *testing.T) {
	t.Run("capability 21 not negotiated", func(t *testing.T) {
		f := newGrokExecutionFixture(t)
		jobs := f.prepareJobs(0)
		f.setCapabilities(t, domain.ManagedCodexSubscriptionsV1)
		if _, err := f.takeJob(jobs[0]); err == nil || code(err) != domain.Unsupported {
			t.Fatalf("a Runner Device without Worker 21 took the lease: %v", err)
		}
	})
	t.Run("stale job revision", func(t *testing.T) {
		f := newGrokExecutionFixture(t)
		jobs := f.prepareJobs(0)
		jobs[0].Revision++
		if _, err := f.takeJob(jobs[0]); err == nil {
			t.Fatal("a stale job revision took the lease")
		}
	})
	t.Run("unknown job", func(t *testing.T) {
		f := newGrokExecutionFixture(t)
		jobs := f.prepareJobs(0)
		jobs[0].ID = domain.NewID()
		if _, err := f.takeJob(jobs[0]); err == nil {
			t.Fatal("an unknown job took the lease")
		}
	})
	t.Run("queued logout blocks new execution", func(t *testing.T) {
		f := newGrokExecutionFixture(t)
		jobs := f.prepareJobs(0)
		f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
		if _, err := f.takeJob(jobs[0]); err == nil {
			t.Fatal("execution started behind a queued logout")
		}
	})
	t.Run("below the subscription baseline", func(t *testing.T) {
		f := newGrokExecutionFixture(t)
		jobs := f.prepareJobs(0)
		f.setGrokInstallation(t, func(list *[]domain.Installation) { (*list)[0].Version = domain.GrokProtocolVersion })
		if _, err := f.takeJob(jobs[0]); err == nil || code(err) != domain.Unsupported {
			t.Fatalf("an installation below the baseline took the lease: %v", err)
		}
	})
	t.Run("not a Grok subscription configuration", func(t *testing.T) {
		f := newGrokExecutionFixture(t)
		jobs := f.prepareJobs(0)
		_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.api-job", nil, func(tx *store.Tx) (any, error) {
			jr, err := tx.Get(domain.JobKind, jobs[0].ID)
			if err != nil {
				return nil, err
			}
			job, err := store.Decode[domain.Job](jr)
			if err != nil {
				return nil, err
			}
			var input domain.ExecutionJobInput
			if err := json.Unmarshal(job.Input, &input); err != nil {
				return nil, err
			}
			input.Configuration.Subscription = false
			job.Input, _ = json.Marshal(input)
			return tx.PutJob(jr.ID, jr.Revision, jr.SessionID, jr.ProjectID, job)
		})
		if err != nil {
			t.Fatal(err)
		}
		_, a := f.record()
		_ = a
		if _, err := f.takeJob(memberJob{ID: jobs[0].ID, Revision: jobs[0].Revision + 1}); err == nil {
			t.Fatal("a non-subscription job took a subscription lease")
		}
	})
}

// A restart or a lost Worker instance fences the execute lease; only the
// original Worker's confirmed cleanup, with no success and no credential,
// releases it, and it never publishes a generation.
func TestGrokFencedExecuteLeaseIsReleasedOnlyByTheOriginalWorkersCleanup(t *testing.T) {
	f := newGrokExecutionFixture(t)
	jobs := f.prepareJobs(0)
	lease, err := f.takeJob(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.retainLostSubscriptionLeases(f.input.MachineID, f.instance, true); err != nil {
		t.Fatal(err)
	}
	if _, a := f.record(); !a.Subscription.RecoveryRequired || a.Subscription.Lease == nil {
		t.Fatal("the lost instance's execute lease was not fenced")
	}
	// A credential-bearing or successful Finish stays refused.
	next := domain.NewID()
	if _, err := f.finishWith(lease, next, f.sealedCopy(t, lease, next), true, false, true); err == nil {
		t.Fatal("a fenced lease accepted a credential")
	}
	if _, err := f.finishWith(lease, domain.NewID(), nil, true, false, true); err == nil {
		t.Fatal("a fenced lease accepted a successful Finish")
	}
	if _, err := f.finishWith(lease, domain.NewID(), nil, false, false, false); err == nil {
		t.Fatal("a fenced lease accepted unconfirmed cleanup")
	}
	if _, a := f.record(); a.Subscription.Lease == nil {
		t.Fatal("a refused cleanup released the lease")
	}
	// Another instance of the Worker is not the original owner.
	other := lease
	wrong, err := f.client.FinishSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: other.LeaseRevision}, LeaseId: other.LeaseId, GenerationId: other.GenerationId, MachineId: string(f.input.MachineID), InstanceId: string(domain.NewID()), CleanupConfirmed: true}))
	if err == nil || wrong != nil {
		t.Fatal("a different instance cleaned the lease")
	}
	if _, err := f.finishWith(lease, domain.NewID(), nil, false, false, true); err != nil {
		t.Fatalf("the original Worker's confirmed cleanup was refused: %v", err)
	}
	_, a := f.record()
	if a.Subscription.Lease != nil || a.Subscription.RecoveryRequired || a.Subscription.Generation != f.generation || a.Health != domain.AccountReady {
		t.Fatal("recovery did not release the account under its current generation")
	}
	if refs := f.references(); len(refs) != 1 || !refs[f.generation] {
		t.Fatal("recovery changed the vault")
	}
	// The fence canceled the account's jobs, so the recovered account is shown
	// usable through a fresh lifecycle lease.
	if _, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH), pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH); err != nil {
		t.Fatalf("the recovered account is not usable again: %v", err)
	}
}

func TestGrokFencedLifecycleLeaseCannotUseExecuteRecovery(t *testing.T) {
	f := newGrokExecutionFixture(t)
	lease, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH), pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.retainLostSubscriptionLeases(f.input.MachineID, f.instance, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.finishWith(lease, domain.NewID(), nil, false, false, true); err == nil {
		t.Fatal("execute recovery released a fenced refresh lease")
	}
}

func TestGrokExecutionCapabilitiesAreAdvertisedOnlyWhenEnabled(t *testing.T) {
	f := newManagedClaudeFixture(t)
	status := func() []pb.SystemCapability {
		response, err := f.service.GetStatus(context.Background(), connect.NewRequest(&pb.GetStatusRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		return response.Msg.Capabilities
	}
	if slices.Contains(status(), grokSystemExecutionReservation) {
		t.Fatal("System 40 is advertised by default")
	}
	loginAdvertised := slices.Contains(status(), grokSystemLoginReservation)
	f.service.grokExecution = true
	capabilities := status()
	if !slices.Contains(capabilities, grokSystemExecutionReservation) {
		t.Fatal("System 40 is not advertised when enabled")
	}
	if slices.Contains(capabilities, grokSystemLoginReservation) != loginAdvertised {
		t.Fatal("enabling execution changed the System 39 advertisement")
	}
}

// secondAccount clones the connected Grok account into an independent one with
// its own identity, generation and vault entry, plus one claimed execute job.
func (f *grokExecutionFixture) secondAccount(t *testing.T, jobs []memberJob) (domain.ID, memberJob) {
	t.Helper()
	second := domain.NewID()
	generation := domain.NewID()
	var job memberJob
	sealed := f.sealed(t, second, generation, "synthetic-principal-2")
	if _, err := f.secrets.Put(context.Background(), credentials.Ref{Owner: second, ID: generation, Purpose: credentials.AccountLogin}, sealed); err != nil {
		t.Fatal(err)
	}
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.second-account", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		clone := a
		sub := *a.Subscription
		sub.Generation, sub.Lease, sub.Pending = generation, nil, nil
		sub.IdentityCommitment = f.service.grokIdentityCommitment(grokServerBundle(t, "synthetic-principal-2"))
		clone.Subscription = &sub
		clone.Alias = "Second Grok"
		connection := *a.Connection
		connection.ID = domain.NewID()
		clone.Connection = &connection
		if _, err := tx.Put(domain.AccountKind, second, 0, "", "", clone); err != nil {
			return nil, err
		}
		jr, err := tx.Get(domain.JobKind, jobs[0].ID)
		if err != nil {
			return nil, err
		}
		stored, err := store.Decode[domain.Job](jr)
		if err != nil {
			return nil, err
		}
		var input domain.ExecutionJobInput
		if err := json.Unmarshal(stored.Input, &input); err != nil {
			return nil, err
		}
		input.AccountID, input.ConnectionID = second, connection.ID
		input.Configuration.Accounts = []domain.WeightedAccount{{ID: second, Weight: 1}}
		input.ConfigurationDigest, _ = input.Configuration.Digest()
		stored.Input, _ = json.Marshal(input)
		put, err := tx.PutJob(domain.NewID(), 0, jr.SessionID, jr.ProjectID, stored)
		if err != nil {
			return nil, err
		}
		job = memberJob{ID: put.ID, Revision: put.Revision}
		_ = r
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return second, job
}

func TestGrokExecuteRunsDifferentAccountsInParallelAndSerializesOneAccount(t *testing.T) {
	f := newGrokExecutionFixture(t)
	jobs := f.prepareJobs(1)
	second, secondJob := f.secondAccount(t, jobs)
	first, err := f.takeJob(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	response, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(second), ExpectedRevision: secondJob.Revision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: string(secondJob.ID), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE}))
	if err != nil {
		t.Fatalf("a different account waited for the first one: %v", err)
	}
	if response.Msg.LeaseId == first.LeaseId || bytes.Equal(response.Msg.Bundle, first.Bundle) {
		t.Fatal("two accounts shared one lease or credential")
	}
	// The first account stays exclusive while the second runs.
	if _, err := f.takeJob(jobs[1]); err == nil || code(err) != domain.ResourceExhausted {
		t.Fatalf("a second session shared the first account: %v", err)
	}
}

func TestGrokExecutionWorkerCapabilityIsAdmittedOnlyWhenEnabled(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	worker, paired := pairedWorker(t, ctx, Endpoint{URL: f.url}, f.service.Identity)
	client := delidevv1connect.NewWorkerServiceClient(http.DefaultClient, f.url)
	attach := func() (*connect.Response[pb.AttachWorkerResponse], error) {
		return client.AttachWorker(ctx, ownerRequest(worker, &pb.AttachWorkerRequest{ProtocolVersion: 2, RequestId: string(domain.NewID()), MachineId: paired.Machine.Id, InstanceId: string(domain.NewID()), Version: rpc.Version, Capabilities: []pb.WorkerCapability{grokWorkerReservation}}))
	}
	if _, err := attach(); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("a disabled server admitted Worker capability 21: %v", err)
	}
	f.service.grokExecution = true
	attached, err := attach()
	if err != nil {
		t.Fatalf("an enabled server rejected Worker capability 21: %v", err)
	}
	if !slices.Contains(attached.Msg.SupportedWorkerCapabilities, grokWorkerReservation) {
		t.Fatal("an enabled server did not advertise Worker capability 21")
	}
	var machine domain.Machine
	if err := json.Unmarshal(attached.Msg.Machine.DocumentJson, &machine); err != nil || !slices.Contains(machine.WorkerCapabilities, domain.ManagedGrokSubscriptionsV1) {
		t.Fatal("the machine did not record the negotiated Grok capability")
	}
}

// watched reports whether one watch pass for the fixture's Runner Device
// delivers the account document.
func (f *grokExecutionFixture) watched(t *testing.T) bool {
	t.Helper()
	return f.watchedWith(t, f.workerToken)
}

// watchedWith runs the same watch pass with the given Worker credential.
func (f *grokExecutionFixture) watchedWith(t *testing.T, token string) bool {
	t.Helper()
	// A current Worker also negotiates execution startup, so the watch needs no
	// Codex installation.
	f.setCapabilities(t, domain.ManagedGrokSubscriptionsV1, domain.ExecutionStartupV1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := f.client.WatchSubscription(ctx, subscriptionRequest(token, &pb.WatchSubscriptionRequest{MachineId: string(f.input.MachineID), InstanceId: string(f.instance)}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	found := false
	for stream.Receive() {
		account := stream.Msg().Account
		if account == nil {
			// An empty message ends one pass.
			return found
		}
		found = found || account.Id == string(f.input.AccountID)
	}
	t.Fatalf("the watch ended before a pass completed: %v", stream.Err())
	return false
}

func TestGrokWatchDeliversOnlyTheOriginalWorkersFencedExecuteLease(t *testing.T) {
	f := newGrokExecutionFixture(t)
	jobs := f.prepareJobs(0)
	if _, err := f.takeJob(jobs[0]); err != nil {
		t.Fatal(err)
	}
	if f.watched(t) {
		t.Fatal("a live execute lease was pushed to its Worker")
	}
	if err := f.service.retainLostSubscriptionLeases(f.input.MachineID, f.instance, true); err != nil {
		t.Fatal(err)
	}
	if !f.watched(t) {
		t.Fatal("the fenced execute lease was not delivered to the original Worker for cleanup")
	}
}

// pairSecondDevice stores a second Worker credential for the same Runner Device
// (machine) and returns its token.
func (f *grokExecutionFixture) pairSecondDevice(t *testing.T) string {
	t.Helper()
	token, err := security.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	device := domain.NewID()
	digest := sha256.Sum256([]byte(token))
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.second-device", device, func(tx *store.Tx) (any, error) {
		if _, err := tx.Put(domain.DeviceKind, device, 0, "", "", domain.Device{Name: "Second Worker", Type: domain.WorkerDevice, MachineID: f.input.MachineID}); err != nil {
			return nil, err
		}
		return struct{}{}, tx.PutCredential(device, digest[:])
	})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// The recovery Finish requires the exact device that owns the lease, so a second
// valid credential of the same machine must never be sent the fenced record: it
// could not settle it and would fail and reconnect forever.
func TestGrokWatchDeliversTheFencedLeaseOnlyToTheOwningDevice(t *testing.T) {
	f := newGrokExecutionFixture(t)
	jobs := f.prepareJobs(0)
	if _, err := f.takeJob(jobs[0]); err != nil {
		t.Fatal(err)
	}
	other := f.pairSecondDevice(t)
	if err := f.service.retainLostSubscriptionLeases(f.input.MachineID, f.instance, true); err != nil {
		t.Fatal(err)
	}
	if f.watchedWith(t, other) {
		t.Fatal("the fenced execute lease was delivered to another device of the same machine")
	}
	if !f.watched(t) {
		t.Fatal("the fenced execute lease was not delivered to its owning device")
	}
}

func TestGrokWatchNeverPushesLifecycleWorkOrFencedRefreshLeases(t *testing.T) {
	f := newGrokExecutionFixture(t)
	op := f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	if f.watched(t) {
		t.Fatal("queued Grok lifecycle work was pushed to a Worker")
	}
	if _, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH); err != nil {
		t.Fatal(err)
	}
	if err := f.service.retainLostSubscriptionLeases(f.input.MachineID, f.instance, false); err != nil {
		t.Fatal(err)
	}
	if f.watched(t) {
		t.Fatal("a fenced Grok refresh lease was pushed to a Worker")
	}
}

// A managed Grok subscription registers like the other native subscriptions: it
// needs the live execute lease and capability 21, and it advertises no API relay.
func TestGrokSubscriptionRegistrationRequiresTheExecuteLeaseAndAdvertisesNoRelay(t *testing.T) {
	f := newGrokExecutionFixture(t)
	jobs := f.prepareJobs(0)
	f.register.Mutation.ExpectedRevision = jobs[0].Revision
	if _, err := f.authorityFixture.client.RegisterExecution(context.Background(), subscriptionRequest(f.workerToken, f.register)); err == nil {
		t.Fatal("a Grok subscription registered without its execute lease")
	}
	lease, err := f.takeJob(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	clear(lease.Bundle)
	f.register.Mutation.RequestId = string(domain.NewID())
	registration, err := f.authorityFixture.client.RegisterExecution(context.Background(), subscriptionRequest(f.workerToken, f.register))
	if err != nil {
		t.Fatalf("the leased Grok subscription could not register: %v", err)
	}
	if registration.Msg.ProxyPath != "" {
		t.Fatal("a Grok subscription advertised an API proxy")
	}
	if _, err := f.service.executionAuthority.Acquire(context.Background(), f.token); err == nil {
		t.Fatal("the registration token granted upstream relay authority to a subscription")
	}
}
