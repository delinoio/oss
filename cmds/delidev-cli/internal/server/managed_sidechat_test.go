// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// This fixture changes synthetic completed-source records before acceptance.
// It verifies protected RPC ownership, never a real login or native execution.
func managedSidechatFixture(t *testing.T) (*continuationFixture, domain.ID, []byte) {
	t.Helper()
	f := newContinuationFixture(t, domain.ExecutionSucceeded)
	f.control(t, pb.SessionAction_SESSION_ACTION_STOP)
	f.secrets = &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
	f.service.accountSecrets = f.secrets
	generation := domain.NewID()
	bundle := subscriptionTestBundle("fixture-sidechat", "original", time.Now().UTC().Add(-time.Minute))
	_, err := f.secrets.Put(context.Background(), credentials.Ref{Owner: f.input.AccountID, ID: generation, Purpose: credentials.AccountLogin}, bundle)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.managed-sidechat", nil, func(tx *store.Tx) (any, error) {
		ar, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.Type, a.ProviderID, a.SubscriptionService = domain.SubscriptionAccount, "", domain.SubscriptionChatGPT
		a.Connection.Authentication, a.Validation, a.Catalog = domain.SubscriptionAuth, nil, nil
		a.Subscription = &domain.SubscriptionState{Generation: generation}
		if _, err := tx.Put(domain.AccountKind, ar.ID, ar.Revision, "", "", a); err != nil {
			return nil, err
		}
		f.input.Configuration.ModelID, err = fixtureInlineSource(tx, f.input.Configuration.AgentID, "", domain.SubscriptionChatGPT)
		if err != nil {
			return nil, err
		}
		f.input.Configuration.Subscription, f.input.Configuration.SubscriptionService, f.input.Configuration.ProviderID = true, domain.SubscriptionChatGPT, ""
		f.input.ConfigurationDigest, err = f.input.Configuration.Digest()
		if err != nil {
			return nil, err
		}
		jr, err := tx.Get(domain.JobKind, domain.ID(f.job.Id))
		if err != nil {
			return nil, err
		}
		j, err := store.Decode[domain.Job](jr)
		if err != nil {
			return nil, err
		}
		j.Input, _ = json.Marshal(f.input)
		if _, err := tx.PutJob(jr.ID, jr.Revision, jr.SessionID, jr.ProjectID, j); err != nil {
			return nil, err
		}
		sr, s, err := sessionRecord(tx, domain.ID(f.change.Session.Id))
		if err != nil {
			return nil, err
		}
		s.InitialExecution.Configuration, s.InitialExecution.ConfigurationDigest = f.input.Configuration, f.input.ConfigurationDigest
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.SessionID, sr.ProjectID, s); err != nil {
			return nil, err
		}
		r, m, err := activeMachine(tx, domain.ID(f.machine.Id))
		if err != nil {
			return nil, err
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.ManagedCodexSubscriptionsV1, domain.CodexReadOnlySidechatWorkerV1, domain.ManagedCodexSidechatV1)
		return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
	})
	if err != nil {
		t.Fatal(err)
	}
	return f, generation, bundle
}

func acceptManagedSidechat(t *testing.T, f *continuationFixture) (*pb.Resource, domain.ForkJobInput, *pb.ForkSessionRequest) {
	t.Helper()
	parent := f.refresh(t)
	req := &pb.ForkSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(parent.ID), ExpectedRevision: parent.Revision}, ExpectedTurnId: string(f.turn), Name: "Managed Sidechat", Purpose: pb.ForkPurpose_FORK_PURPOSE_SIDECHAT}
	accepted, err := sessionClient(f.accountFixture).ForkSession(context.Background(), ownerRequest(f.identity, req))
	if err != nil {
		t.Fatal(err)
	}
	job, input := forkClaimFixture(t, f, accepted.Msg.Job.Id)
	return job, input, req
}

func managedSidechatResult(t *testing.T, input domain.ForkJobInput, finish domain.ID) []byte {
	t.Helper()
	var prep workspace.PrepareRequest
	var source workspace.Manifest
	if domain.Decode(input.SourceAssignment.Preparation, &prep) != nil || domain.Decode(input.SourceAssignment.Manifest, &source) != nil {
		t.Fatal("source missing")
	}
	original := prep
	prep.SessionID, prep.ForkSourceID, prep.ForkProfile = input.ChildSessionID, input.SourceSessionID, workspace.CodexSidechatReferenceV1
	prep.SidechatSource = &workspace.SidechatSource{Preparation: original, Manifest: source}
	raw, _ := json.Marshal(prep)
	sourceRaw, _ := json.Marshal(source)
	manifest := source
	manifest.SessionID, manifest.InputDigest, manifest.CreatedAt = input.ChildSessionID, forkInputDigest(raw), time.Now().UTC()
	manifest.Reference = &workspace.SidechatReference{SessionID: source.SessionID, PreparationDigest: source.InputDigest, ManifestDigest: forkInputDigest(sourceRaw), DirectoryIdentity: strings.Repeat("a", 64), MetadataIdentity: strings.Repeat("b", 64)}
	for i := range manifest.Repositories {
		manifest.Repositories[i].Owned = false
	}
	result := forkResultFixture(t, input)
	result.Version, result.ManagedFinish, result.Preparation = input.Version, finish, raw
	result.Manifest, _ = json.Marshal(manifest)
	raw, _ = json.Marshal(result)
	return raw
}

func TestManagedSidechatServerCreditOwnerRejectsBeforeForkJob(t *testing.T) {
	f, generation, _ := managedSidechatFixture(t)
	credits := domain.SubscriptionResetCredits{ObservationID: domain.NewID(), ObservedAt: time.Now().UTC(), AvailableCount: 1}
	var accountRecord store.Record
	var connection domain.ID
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.sidechat-server-credit", nil, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.Subscription.ServerQuotaGeneration = generation
		a.Subscription.ResetCredits = &credits
		connection = a.Connection.ID
		accountRecord, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	creditID := domain.NewID()
	client := delidevv1connect.NewSubscriptionServiceClient(http.DefaultClient, f.endpoint.URL)
	credit := &pb.RequestSubscriptionObservationRequest{Mutation: &pb.Mutation{RequestId: string(creditID), Id: string(f.input.AccountID), ExpectedRevision: accountRecord.Revision}, Action: pb.SubscriptionObservationAction_SUBSCRIPTION_OBSERVATION_ACTION_RESET_CREDIT, ConnectionId: string(connection), GenerationId: string(generation), CreditsObservationId: string(credits.ObservationID), NextCredit: true, Confirmed: true}
	if _, err := client.RequestSubscriptionObservation(context.Background(), ownerRequest(f.identity, credit)); err != nil {
		t.Fatal("server credit acceptance", err)
	}
	jobCount := func() int {
		t.Helper()
		count := 0
		if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
			jobs, err := all(tx, domain.JobKind)
			count = len(jobs)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return count
	}
	before := jobCount()
	parent := f.refresh(t)
	req := &pb.ForkSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(parent.ID), ExpectedRevision: parent.Revision}, ExpectedTurnId: string(f.turn), Name: "Managed Sidechat", Purpose: pb.ForkPurpose_FORK_PURPOSE_SIDECHAT}
	if _, err := sessionClient(f.accountFixture).ForkSession(context.Background(), ownerRequest(f.identity, req)); err == nil {
		t.Fatal("Sidechat admitted while original server credit owns authentication")
	}
	if after := jobCount(); after != before {
		t.Fatalf("rejected Sidechat created a Fork job: before=%d after=%d", before, after)
	}
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		_, a, err := subscriptionAccount(tx, f.input.AccountID, 0)
		if err != nil {
			return err
		}
		if a.Subscription.ServerCredit == nil || a.Subscription.ServerCredit.ID != creditID || !a.Subscription.ServerCreditActive() || a.Subscription.ServerCredit.SendClaimed || a.Subscription.Lease != nil || a.Subscription.Generation != generation || a.Connection.ID != connection {
			t.Fatal("rejected Sidechat changed the original unsent credit owner")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestManagedSidechatProtectedFinishPrecedesPublication(t *testing.T) {
	for _, fault := range []string{"valid", "missing-finish", "unconfirmed-cleanup", "changed-generation", "missing-capability"} {
		t.Run(fault, func(t *testing.T) {
			f, generation, bundle := managedSidechatFixture(t)
			job, input, creation := acceptManagedSidechat(t, f)
			if input.SubscriptionGeneration != generation {
				t.Fatal("original generation not frozen")
			}
			client := delidevv1connect.NewSubscriptionServiceClient(http.DefaultClient, f.endpoint.URL)
			takeReq := &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: job.Revision}, MachineId: f.machine.Id, InstanceId: f.workerInstance, OperationId: job.Id, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE}
			taken, err := client.TakeSubscription(context.Background(), ownerRequest(f.workerIdentity, takeReq))
			if err != nil || !bytes.Equal(taken.Msg.Bundle, bundle) {
				t.Fatal("protected Sidechat Take", err)
			}
			if _, err := client.TakeSubscription(context.Background(), ownerRequest(f.workerIdentity, takeReq)); err == nil {
				t.Fatal("Take replay redistributed credentials")
			}
			var finish domain.ID
			if fault != "missing-finish" {
				finish = domain.NewID()
				rotated := subscriptionTestBundle("fixture-sidechat", "rotated", time.Now().UTC())
				req := &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(finish), Id: string(f.input.AccountID), ExpectedRevision: taken.Msg.LeaseRevision}, LeaseId: taken.Msg.LeaseId, GenerationId: taken.Msg.GenerationId, MachineId: f.machine.Id, InstanceId: f.workerInstance, Bundle: bytes.Clone(rotated), Succeeded: true, CleanupConfirmed: fault != "unconfirmed-cleanup"}
				if _, err := client.FinishSubscription(context.Background(), ownerRequest(f.workerIdentity, req)); err != nil {
					t.Fatal("protected Finish", err)
				}
				req.Bundle = bytes.Clone(rotated)
				if _, err := client.FinishSubscription(context.Background(), ownerRequest(f.workerIdentity, req)); err != nil {
					t.Fatal("Finish receipt replay", err)
				}
			}
			if fault == "changed-generation" || fault == "missing-capability" {
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.changed-sidechat-authority", fault, func(tx *store.Tx) (any, error) {
					if fault == "changed-generation" {
						r, a, err := accountFromTx(tx, f.input.AccountID, 0)
						if err != nil {
							return nil, err
						}
						a.Subscription.Generation = domain.NewID()
						return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
					}
					r, m, err := activeMachine(tx, domain.ID(f.machine.Id))
					if err != nil {
						return nil, err
					}
					m.WorkerCapabilities = nil
					return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			raw := managedSidechatResult(t, input, finish)
			_, err = f.workerClient.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: raw}))
			if err != nil {
				t.Fatal("Fork report", err)
			}
			result, err := sessionClient(f.accountFixture).GetSessionFork(context.Background(), ownerRequest(f.identity, &pb.GetSessionForkRequest{JobId: job.Id}))
			if err != nil {
				t.Fatal(err)
			}
			if fault != "valid" {
				if result.Msg.Session != nil {
					t.Fatal("unproven credentials published a child")
				}
				return
			}
			if result.Msg.Session == nil {
				t.Fatal("confirmed managed Sidechat not published", string(result.Msg.Job.DocumentJson))
			}
			var child domain.Session
			if domain.Decode(result.Msg.Session.DocumentJson, &child) != nil || !child.IsSidechat() || !child.Fork.Snapshot.Configuration.Subscription || child.Dispatch != domain.DispatchPaused || child.PendingInputs != 0 || child.Fork.Snapshot.InitialAccountID != f.input.AccountID {
				t.Fatal("child lost immutable managed authority")
			}
			replay, err := sessionClient(f.accountFixture).ForkSession(context.Background(), ownerRequest(f.identity, creation))
			if err != nil || replay.Msg.Session == nil || replay.Msg.Session.Id != result.Msg.Session.Id {
				t.Fatal("creation replay duplicated/lost child", err)
			}
		})
	}
}

func TestManagedSidechatRefusesIndependentForkAndChangedTakeAuthority(t *testing.T) {
	for _, fault := range []string{"independent", "generation", "capability", "instance"} {
		t.Run(fault, func(t *testing.T) {
			f, _, _ := managedSidechatFixture(t)
			if fault == "independent" {
				parent := f.refresh(t)
				_, err := sessionClient(f.accountFixture).ForkSession(context.Background(), ownerRequest(f.identity, &pb.ForkSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(parent.ID), ExpectedRevision: parent.Revision}, ExpectedTurnId: string(f.turn), Name: "Independent"}))
				if err == nil {
					t.Fatal("independent subscription Fork admitted")
				}
				return
			}
			job, _, _ := acceptManagedSidechat(t, f)
			if fault != "instance" {
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.take-fence", fault, func(tx *store.Tx) (any, error) {
					if fault == "generation" {
						r, a, err := accountFromTx(tx, f.input.AccountID, 0)
						if err != nil {
							return nil, err
						}
						a.Subscription.Generation = domain.NewID()
						return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
					}
					r, m, err := activeMachine(tx, domain.ID(f.machine.Id))
					if err != nil {
						return nil, err
					}
					m.WorkerCapabilities = nil
					return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			instance := f.workerInstance
			if fault == "instance" {
				instance = string(domain.NewID())
			}
			client := delidevv1connect.NewSubscriptionServiceClient(http.DefaultClient, f.endpoint.URL)
			_, err := client.TakeSubscription(context.Background(), ownerRequest(f.workerIdentity, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: job.Revision}, MachineId: f.machine.Id, InstanceId: instance, OperationId: job.Id, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE}))
			if err == nil {
				t.Fatal("changed original authority released credentials")
			}
		})
	}
}

func TestManagedIndependentForkProtectedFinishPrecedesPublication(t *testing.T) {
	for _, fault := range []string{"valid", "missing-finish", "unconfirmed-cleanup", "changed-generation", "missing-capability"} {
		t.Run(fault, func(t *testing.T) {
			f, generation, bundle := managedIndependentFixture(t)
			job, input, creation := acceptManagedIndependent(t, f)
			if input.SubscriptionGeneration != generation {
				t.Fatal("original generation not frozen")
			}
			client := delidevv1connect.NewSubscriptionServiceClient(http.DefaultClient, f.endpoint.URL)
			takeReq := &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: job.Revision}, MachineId: f.machine.Id, InstanceId: f.workerInstance, OperationId: job.Id, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE}
			taken, err := client.TakeSubscription(context.Background(), ownerRequest(f.workerIdentity, takeReq))
			if err != nil || !bytes.Equal(taken.Msg.Bundle, bundle) {
				t.Fatal("protected Sidechat Take", err)
			}
			if _, err := client.TakeSubscription(context.Background(), ownerRequest(f.workerIdentity, takeReq)); err == nil {
				t.Fatal("Take replay redistributed credentials")
			}
			var finish domain.ID
			if fault != "missing-finish" {
				finish = domain.NewID()
				rotated := subscriptionTestBundle("fixture-sidechat", "rotated", time.Now().UTC())
				req := &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(finish), Id: string(f.input.AccountID), ExpectedRevision: taken.Msg.LeaseRevision}, LeaseId: taken.Msg.LeaseId, GenerationId: taken.Msg.GenerationId, MachineId: f.machine.Id, InstanceId: f.workerInstance, Bundle: bytes.Clone(rotated), Succeeded: true, CleanupConfirmed: fault != "unconfirmed-cleanup"}
				if _, err := client.FinishSubscription(context.Background(), ownerRequest(f.workerIdentity, req)); err != nil {
					t.Fatal("protected Finish", err)
				}
				req.Bundle = bytes.Clone(rotated)
				if _, err := client.FinishSubscription(context.Background(), ownerRequest(f.workerIdentity, req)); err != nil {
					t.Fatal("Finish receipt replay", err)
				}
			}
			if fault == "changed-generation" || fault == "missing-capability" {
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.changed-sidechat-authority", fault, func(tx *store.Tx) (any, error) {
					if fault == "changed-generation" {
						r, a, err := accountFromTx(tx, f.input.AccountID, 0)
						if err != nil {
							return nil, err
						}
						a.Subscription.Generation = domain.NewID()
						return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
					}
					r, m, err := activeMachine(tx, domain.ID(f.machine.Id))
					if err != nil {
						return nil, err
					}
					m.WorkerCapabilities = nil
					return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			raw := managedIndependentResult(t, input, finish)
			_, err = f.workerClient.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: raw}))
			if err != nil {
				t.Fatal("Fork report", err)
			}
			result, err := sessionClient(f.accountFixture).GetSessionFork(context.Background(), ownerRequest(f.identity, &pb.GetSessionForkRequest{JobId: job.Id}))
			if err != nil {
				t.Fatal(err)
			}
			if fault != "valid" {
				if result.Msg.Session != nil {
					t.Fatal("unproven credentials published a child")
				}
				return
			}
			if result.Msg.Session == nil {
				t.Fatal("confirmed managed Sidechat not published", string(result.Msg.Job.DocumentJson))
			}
			var child domain.Session
			if domain.Decode(result.Msg.Session.DocumentJson, &child) != nil || child.IsSidechat() || !child.Fork.Snapshot.Configuration.Subscription || child.Dispatch != domain.DispatchPaused || child.PendingInputs != 0 || child.Fork.Snapshot.InitialAccountID != f.input.AccountID {
				t.Fatal("child lost immutable managed authority")
			}
			replay, err := sessionClient(f.accountFixture).ForkSession(context.Background(), ownerRequest(f.identity, creation))
			if err != nil || replay.Msg.Session == nil || replay.Msg.Session.Id != result.Msg.Session.Id {
				t.Fatal("creation replay duplicated/lost child", err)
			}
		})
	}
}

func managedIndependentFixture(t *testing.T) (*continuationFixture, domain.ID, []byte) {
	f, g, b := managedSidechatFixture(t)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.managed-fork-capability", nil, func(tx *store.Tx) (any, error) {
		r, m, e := activeMachine(tx, domain.ID(f.machine.Id))
		if e != nil {
			return nil, e
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.ManagedCodexForkV1)
		return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
	})
	if err != nil {
		t.Fatal(err)
	}
	return f, g, b
}
func acceptManagedIndependent(t *testing.T, f *continuationFixture) (*pb.Resource, domain.ForkJobInput, *pb.ForkSessionRequest) {
	parent := f.refresh(t)
	req := &pb.ForkSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(parent.ID), ExpectedRevision: parent.Revision}, ExpectedTurnId: string(f.turn), Name: "Managed independent"}
	result, err := sessionClient(f.accountFixture).ForkSession(context.Background(), ownerRequest(f.identity, req))
	if err != nil {
		t.Fatal(err)
	}
	job, input := forkClaimFixture(t, f, result.Msg.Job.Id)
	return job, input, req
}
func managedIndependentResult(t *testing.T, input domain.ForkJobInput, finish domain.ID) []byte {
	result := forkResultFixture(t, input)
	result.ManagedFinish = finish
	raw, _ := json.Marshal(result)
	return raw
}

func TestManagedIndependentForkAdmissionRequiresOwnCapabilityAndSettledAccount(t *testing.T) {
	for _, fault := range []string{"fork-capability", "subscription-capability", "recovery"} {
		t.Run(fault, func(t *testing.T) {
			f, _, _ := managedIndependentFixture(t)
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.managed-fork-admission", fault, func(tx *store.Tx) (any, error) {
				if fault == "recovery" {
					r, a, e := accountFromTx(tx, f.input.AccountID, 0)
					if e != nil {
						return nil, e
					}
					a.Subscription.RecoveryRequired = true
					return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
				}
				r, m, e := activeMachine(tx, domain.ID(f.machine.Id))
				if e != nil {
					return nil, e
				}
				filtered := []domain.WorkerCapability{}
				for _, capability := range m.WorkerCapabilities {
					if fault == "fork-capability" && capability == domain.ManagedCodexForkV1 || fault == "subscription-capability" && capability == domain.ManagedCodexSubscriptionsV1 {
						continue
					}
					filtered = append(filtered, capability)
				}
				m.WorkerCapabilities = filtered
				return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
			})
			if err != nil {
				t.Fatal(err)
			}
			parent := f.refresh(t)
			_, err = sessionClient(f.accountFixture).ForkSession(context.Background(), ownerRequest(f.identity, &pb.ForkSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(parent.ID), ExpectedRevision: parent.Revision}, ExpectedTurnId: string(f.turn), Name: "Refused"}))
			if err == nil {
				t.Fatal("unproved managed Fork admitted")
			}
		})
	}
}
