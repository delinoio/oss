// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

const managedFixtureHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// managedTitleFixture is a synthetic Connect/SQLite fixture: a protected ChatGPT
// account with a settled login, an original successful direct-startup
// execution on its Worker, and the managed title queued by the production
// terminal path.
type managedTitleFixture struct {
	*subscriptionFixture
	original   domain.ExecutionJobInput
	connection domain.ID
	titleJob   domain.ID
	bundle     []byte
}

type managedTitleOptions struct {
	machineCapabilities []domain.WorkerCapability
	creator             domain.ID
	service             domain.SubscriptionService
}

func managedTitleCapabilities() []domain.WorkerCapability {
	return []domain.WorkerCapability{domain.ManagedCodexSubscriptionsV1, domain.AutomaticTitlesCodexV1, domain.ManagedCodexTitlesV1, domain.ExecutionStartupV1}
}

func newManagedTitleFixture(t *testing.T, options managedTitleOptions) *managedTitleFixture {
	t.Helper()
	f := &managedTitleFixture{subscriptionFixture: newSubscriptionFixture(t)}
	f.bundle = f.login()
	_, account := f.record()
	f.connection = account.Connection.ID
	if options.machineCapabilities == nil {
		options.machineCapabilities = managedTitleCapabilities()
	}
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.managed-title", nil, func(tx *store.Tx) (any, error) {
		mr, err := tx.Get(domain.MachineKind, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		machine, err := store.Decode[domain.Machine](mr)
		if err != nil {
			return nil, err
		}
		machine.WorkerCapabilities = options.machineCapabilities
		if _, err := tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine); err != nil {
			return nil, err
		}
		original := f.input
		original.Version = 4
		original.Installation = domain.Installation{}
		original.Startup = &domain.ExecutionStartupSelection{Harness: domain.Codex, ExplicitPath: "/controlled/native/codex"}
		original.ConnectionID = f.connection
		if options.service != "" {
			original.Configuration.SubscriptionService = options.service
		}
		digest, err := original.Configuration.Digest()
		if err != nil {
			return nil, err
		}
		original.ConfigurationDigest = digest
		f.original = original
		raw, err := json.Marshal(original)
		if err != nil {
			return nil, err
		}
		ready := &domain.ExecutionStartupObservation{State: domain.StartupReady, Phase: domain.StartupSettings, Harness: domain.Codex, NativeVersion: "0.159.2", ExecutableSHA256: managedFixtureHash, Protocol: domain.ProtocolFor(domain.Codex), CorrelationID: domain.NewID(), InputDelivery: domain.StartupNotSent}
		record := &domain.ExecutionStartupRecord{JobID: f.job, ExecutionID: original.ExecutionID, Ready: ready}
		completion, err := json.Marshal(domain.ExecutionCompletion{Version: 1, ExecutionID: original.ExecutionID, InputID: original.InputID, NativeThreadID: domain.NativeIdentity(domain.NewID()), NativeTurnID: domain.NativeIdentity(domain.NewID()), LastSequence: 3, Outcome: domain.ExecutionSucceeded, CleanupVerified: true})
		if err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		jr, err := tx.Get(domain.JobKind, f.job)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](jr)
		if err != nil {
			return nil, err
		}
		job.State, job.Input, job.Output, job.Startup, job.FinishedAt = domain.JobSucceeded, raw, completion, record, &now
		jr, err = tx.PutJob(jr.ID, jr.Revision, jr.SessionID, jr.ProjectID, job)
		if err != nil {
			return nil, err
		}
		digestBytes := bytes.Repeat([]byte{7}, 32)
		if err := tx.PutExecutionGrant(store.ExecutionGrant{JobID: f.job, Digest: digestBytes, ExecutionID: original.ExecutionID, MachineID: original.MachineID, InstanceID: f.instance, DeviceID: f.device, ServerEpoch: f.service.executionAuthority.epoch}); err != nil {
			return nil, err
		}
		sr, session, err := sessionRecord(tx, original.SessionID)
		if err != nil {
			return nil, err
		}
		session.Outcome, session.ActiveExecutionID, session.Dispatch = domain.ExecutionSucceeded, "", domain.DispatchPaused
		session.NameMode, session.NameOwner, session.NameGeneration = domain.AutomaticSessionName, domain.AutomaticNameOwner, 1
		session.TitleState, session.TitleReason, session.TitleOperationID, session.TitleJobID = domain.TitleWaiting, domain.TitleReasonNone, "", ""
		session.CreatedBy = options.creator
		if options.creator != "" {
			if _, err := tx.Put(domain.DeviceKind, options.creator, 0, "", "", domain.Device{Name: "Client", Type: domain.ClientDevice}); err != nil {
				return nil, err
			}
		}
		session.Name = "New session"
		initial := domain.InitialExecution{ID: original.ExecutionID, InputID: original.InputID, Configuration: original.Configuration, ConfigurationDigest: digest, InitialAccountID: original.AccountID, ConnectionID: original.ConnectionID, AcceptedAt: now}
		session.InitialExecution = &initial
		session.Startup = record
		if err := queueAutomaticSessionTitle(tx, sr, &session, jr, original, true); err != nil {
			return nil, err
		}
		f.titleJob = session.TitleJobID
		return tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session)
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *managedTitleFixture) session(t *testing.T) domain.Session {
	t.Helper()
	r, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.original.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Decode[domain.Session](r)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func (f *managedTitleFixture) titleRecord(t *testing.T) (store.Record, domain.Job) {
	t.Helper()
	r, err := f.service.Store.Get(context.Background(), domain.JobKind, f.titleJob)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Decode[domain.Job](r)
	if err != nil {
		t.Fatal(err)
	}
	return r, job
}

func (f *managedTitleFixture) claim() (store.Record, error) {
	return claimTitleJob(context.Background(), f.service, f.original.MachineID, f.instance, f.authorityFixture.device, f.titleJob)
}

func (f *managedTitleFixture) mutate(t *testing.T, name string, change func(tx *store.Tx) error) {
	t.Helper()
	if _, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture."+name, nil, func(tx *store.Tx) (any, error) { return nil, change(tx) }); err != nil {
		t.Fatal(err)
	}
}

func (f *managedTitleFixture) takeTitle(revision uint64) (*pb.TakeSubscriptionResponse, error) {
	response, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: revision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: string(f.titleJob), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (f *managedTitleFixture) finishLease(lease *pb.TakeSubscriptionResponse, bundle []byte, success, cleanup bool) (domain.ID, error) {
	request := domain.NewID()
	_, err := f.client.FinishSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(request), Id: string(f.input.AccountID), ExpectedRevision: lease.LeaseRevision}, LeaseId: lease.LeaseId, GenerationId: lease.GenerationId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), Bundle: bytes.Clone(bundle), Succeeded: success, CleanupConfirmed: cleanup}))
	return request, err
}

func (f *managedTitleFixture) report(record store.Record, output json.RawMessage, problem *domain.Error) error {
	request := &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(record.ID), ExpectedRevision: record.Revision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OutputJson: output}
	if problem != nil {
		request.Problem = &pb.ErrorDetail{Code: string(problem.Code)}
	}
	_, err := f.authorityFixture.client.ReportWork(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, request))
	return err
}

func (f *managedTitleFixture) result(finish domain.ID, title string, usage bool) json.RawMessage {
	value := domain.AuxiliaryTitleResult{Version: 2, OperationID: f.titleInput().OperationID, NameGeneration: 1, Title: title, CleanupVerified: true, ManagedFinish: finish}
	if usage {
		counts := reportedCounts(18)
		value.UsageRecord = &domain.ResponseUsageRecord{
			Purpose: domain.SessionTitleUsage, SessionID: f.original.SessionID, ExecutionID: f.original.ExecutionID, AccountID: f.original.AccountID, ConnectionID: f.original.ConnectionID,
			SubscriptionService: domain.SubscriptionChatGPT, ModelID: f.original.Configuration.ModelID, Harness: domain.Codex, Version: "0.159.2",
			ThreadID: string(domain.NewID()), TurnID: string(domain.NewID()), Sequence: 1,
			Usage: domain.NativeResponseUsage{ResponseDigest: strings.Repeat("b", 64), Counts: &counts, CostEvidence: domain.UsageCostMissing},
		}
	}
	raw, _ := json.Marshal(value)
	return raw
}

func (f *managedTitleFixture) titleInput() domain.AuxiliaryTitleInput {
	_, job := f.titleRecordQuiet()
	var input domain.AuxiliaryTitleInput
	_ = json.Unmarshal(job.Input, &input)
	return input
}

func (f *managedTitleFixture) titleRecordQuiet() (store.Record, domain.Job) {
	r, _ := f.service.Store.Get(context.Background(), domain.JobKind, f.titleJob)
	job, _ := store.Decode[domain.Job](r)
	return r, job
}

// runToFinish claims, leases and finishes a managed title with an unchanged
// bundle, returning the protected Finish request ID.
func (f *managedTitleFixture) runToFinish(t *testing.T) (store.Record, domain.ID) {
	t.Helper()
	record, err := f.claim()
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.takeTitle(record.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(lease.Bundle, f.bundle) {
		t.Fatal("the original protected generation was not delivered")
	}
	clear(lease.Bundle)
	finish, err := f.finishLease(lease, f.bundle, true, true)
	if err != nil {
		t.Fatal(err)
	}
	return record, finish
}

// Scenario 1: queue, claim, deliver protected credentials, return a valid
// native title, Finish cleanup, then report. One rename, one usage attribution.
func TestManagedTitleSucceedsOnceAfterProtectedFinish(t *testing.T) {
	f := newManagedTitleFixture(t, managedTitleOptions{})
	session := f.session(t)
	if session.TitleState != domain.TitleQueued || session.Name != "New session" || titleJobCount(t, f.service, f.original.SessionID) != 1 {
		t.Fatalf("first success did not queue exactly one managed title: %+v", session)
	}
	_, job := f.titleRecord(t)
	var input domain.AuxiliaryTitleInput
	if err := json.Unmarshal(job.Input, &input); err != nil || input.Version != 3 || input.Managed == nil || input.ProviderID != "" || input.ProviderProtocol != "" || input.Managed.IdentityCommitment == "" || input.Startup == nil || input.Startup.ExecutableSHA256 != managedFixtureHash {
		t.Fatalf("assignment is not the immutable version-3 managed branch: %+v %v", input, err)
	}
	record, finish := f.runToFinish(t)
	_, account := f.record()
	if account.Subscription.Lease != nil || account.Subscription.Generation != finish {
		t.Fatal("protected Finish did not settle the account on the staged generation")
	}
	output := f.result(finish, "Plan the migration", true)
	if err := f.report(record, output, nil); err != nil {
		t.Fatal(err)
	}
	session = f.session(t)
	if session.Name != "Plan the migration" || session.TitleState != domain.TitleSucceeded || session.NameOwner != domain.AutomaticNameOwner {
		t.Fatalf("title was not applied exactly once: %+v", session)
	}
	if _, job := f.titleRecord(t); job.State != domain.JobSucceeded {
		t.Fatal("title job did not succeed")
	}
	// The one-time inference claim never permits another credential delivery.
	if _, err := f.takeTitle(record.Revision); err == nil {
		t.Fatal("a completed title received protected credentials again")
	}
	// Exactly one session-title response exists: re-attributing the same native
	// response digest replays the stored row instead of inserting another.
	var usage domain.AuxiliaryTitleResult
	if err := json.Unmarshal(output, &usage); err != nil {
		t.Fatal(err)
	}
	f.mutate(t, "usage-replay", func(tx *store.Tx) error {
		_, replayed, err := tx.PutResponseUsage(domain.NewID(), *usage.UsageRecord)
		if err != nil || !replayed {
			t.Fatal("title usage was not attributed exactly once to the original subscription execution", replayed, err)
		}
		return nil
	})
}

// A primary job may lease the released account between the protected Finish and
// the title report. The immutable Finish receipt still proves the title.
func TestManagedTitlePublishesAfterLaterAccountUse(t *testing.T) {
	f := newManagedTitleFixture(t, managedTitleOptions{})
	record, finish := f.runToFinish(t)
	f.mutate(t, "later-primary-use", func(tx *store.Tx) error {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return err
		}
		a.Subscription.Generation = domain.NewID()
		a.Subscription.Lease = &domain.SubscriptionLease{ID: domain.NewID(), OperationID: domain.NewID(), Revision: 1, Action: domain.SubscriptionExecute, MachineID: f.input.MachineID, InstanceID: f.instance, DeviceID: f.authorityFixture.device, Epoch: domain.NewID(), Generation: a.Subscription.Generation, StartedAt: time.Now().UTC()}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return err
	})
	if err := f.report(record, f.result(finish, "Plan the migration", true), nil); err != nil {
		t.Fatal(err)
	}
	session := f.session(t)
	if session.Name != "Plan the migration" || session.TitleState != domain.TitleSucceeded {
		t.Fatalf("later primary account use discarded a proven title: %+v", session)
	}
}

// Scenario 2: held account keeps the title queued; primary work has priority.
func TestManagedTitleStaysQueuedWhileAccountIsBusy(t *testing.T) {
	f := newManagedTitleFixture(t, managedTitleOptions{})
	for _, name := range []string{"leased", "refreshing", "recovery-fenced", "primary-queued"} {
		t.Run(name, func(t *testing.T) {
			var release func()
			setAccount := func(change func(state *domain.SubscriptionState)) {
				f.mutate(t, "busy-"+name, func(tx *store.Tx) error {
					r, a, err := accountFromTx(tx, f.input.AccountID, 0)
					if err != nil {
						return err
					}
					change(a.Subscription)
					_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
					return err
				})
			}
			switch name {
			case "leased":
				setAccount(func(state *domain.SubscriptionState) {
					state.Lease = &domain.SubscriptionLease{ID: domain.NewID(), OperationID: domain.NewID(), Revision: 1, Action: domain.SubscriptionExecute, MachineID: f.input.MachineID, InstanceID: f.instance, DeviceID: f.authorityFixture.device, Epoch: domain.NewID(), Generation: state.Generation, StartedAt: time.Now().UTC()}
				})
				release = func() { setAccount(func(state *domain.SubscriptionState) { state.Lease = nil }) }
			case "refreshing":
				setAccount(func(state *domain.SubscriptionState) {
					state.Pending = &domain.SubscriptionOperation{ID: domain.NewID(), Action: domain.SubscriptionRefresh, MachineID: f.input.MachineID, Actor: domain.Principal{Type: domain.OwnerDevice}, Phase: domain.SubscriptionQueued}
				})
				release = func() { setAccount(func(state *domain.SubscriptionState) { state.Pending = nil }) }
			case "recovery-fenced":
				// A failed primary cleanup fences the same generation and identity and
				// marks the account failed; the reconcilable fence is not lost authority.
				var health domain.AccountHealth
				f.mutate(t, "fence", func(tx *store.Tx) error {
					r, a, err := accountFromTx(tx, f.input.AccountID, 0)
					if err != nil {
						return err
					}
					health = a.Health
					a.Subscription.RecoveryRequired, a.Health = true, domain.AccountFailed
					_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
					return err
				})
				release = func() {
					f.mutate(t, "unfence", func(tx *store.Tx) error {
						r, a, err := accountFromTx(tx, f.input.AccountID, 0)
						if err != nil {
							return err
						}
						a.Subscription.RecoveryRequired, a.Health = false, health
						_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
						return err
					})
				}
			case "primary-queued":
				primary := domain.NewID()
				f.mutate(t, "primary", func(tx *store.Tx) error {
					other := f.original
					other.ExecutionID = domain.NewID()
					raw, _ := json.Marshal(other)
					_, err := tx.PutJob(primary, 0, other.SessionID, "", domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobQueued, MachineID: other.MachineID, Input: raw, AcceptedAt: time.Now().UTC()})
					return err
				})
				release = func() {
					f.mutate(t, "primary-done", func(tx *store.Tx) error {
						r, err := tx.Get(domain.JobKind, primary)
						if err != nil {
							return err
						}
						job, err := store.Decode[domain.Job](r)
						if err != nil {
							return err
						}
						job.State = domain.JobCanceled
						_, err = tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job)
						return err
					})
				}
			}
			if _, err := f.claim(); domain.SafeError(err).Code != domain.ResourceExhausted {
				t.Fatal("a busy account admitted its title", err)
			}
			if session := f.session(t); session.TitleState != domain.TitleQueued {
				t.Fatalf("busy account retired the queued title: %+v", session)
			}
			release()
		})
	}
	record, err := f.claim()
	if err != nil {
		t.Fatal("released account did not admit exactly one title claim", err)
	}
	if session := f.session(t); session.TitleState != domain.TitleRunning || record.Revision == 0 {
		t.Fatal("admission did not mark the title running")
	}
	// A running title is never preempted: a second claim for the same job conflicts.
	if _, err := f.claim(); err == nil {
		t.Fatal("a running title was claimed twice")
	}
}

// Scenario 3: any changed identity is denied before credentials or input.
func TestManagedTitleDeniesChangedAuthorityBeforeCredentials(t *testing.T) {
	changes := map[string]func(f *managedTitleFixture, tx *store.Tx) error{
		"connection": func(f *managedTitleFixture, tx *store.Tx) error {
			r, a, err := accountFromTx(tx, f.input.AccountID, 0)
			if err != nil {
				return err
			}
			a.Connection.ID = domain.NewID()
			_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
			return err
		},
		"generation": func(f *managedTitleFixture, tx *store.Tx) error {
			r, a, err := accountFromTx(tx, f.input.AccountID, 0)
			if err != nil {
				return err
			}
			a.Subscription.Generation = domain.NewID()
			_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
			return err
		},
		"identity": func(f *managedTitleFixture, tx *store.Tx) error {
			r, a, err := accountFromTx(tx, f.input.AccountID, 0)
			if err != nil {
				return err
			}
			a.Subscription.IdentityCommitment = "different-identity"
			_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
			return err
		},
		"capability": func(f *managedTitleFixture, tx *store.Tx) error {
			r, err := tx.Get(domain.MachineKind, f.input.MachineID)
			if err != nil {
				return err
			}
			m, err := store.Decode[domain.Machine](r)
			if err != nil {
				return err
			}
			m.WorkerCapabilities = []domain.WorkerCapability{domain.ManagedCodexSubscriptionsV1, domain.AutomaticTitlesCodexV1, domain.ExecutionStartupV1}
			_, err = tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
			return err
		},
		"startup-hash": func(f *managedTitleFixture, tx *store.Tx) error {
			r, err := tx.Get(domain.JobKind, f.job)
			if err != nil {
				return err
			}
			job, err := store.Decode[domain.Job](r)
			if err != nil {
				return err
			}
			job.Startup.Ready.ExecutableSHA256 = strings.Repeat("c", 64)
			_, err = tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job)
			return err
		},
		"model": func(f *managedTitleFixture, tx *store.Tx) error {
			r, err := tx.Get(domain.JobKind, f.job)
			if err != nil {
				return err
			}
			job, err := store.Decode[domain.Job](r)
			if err != nil {
				return err
			}
			var input domain.ExecutionJobInput
			if err := json.Unmarshal(job.Input, &input); err != nil {
				return err
			}
			input.Configuration.NativeModel = "another-model"
			job.Input, _ = json.Marshal(input)
			_, err = tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job)
			return err
		},
		"server-epoch": func(f *managedTitleFixture, tx *store.Tx) error {
			f.service.executionAuthority.epoch = domain.NewID()
			return nil
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			f := newManagedTitleFixture(t, managedTitleOptions{})
			f.mutate(t, name, func(tx *store.Tx) error { return change(f, tx) })
			if _, err := f.claim(); err == nil {
				// An admitted claim must still be denied at Take.
				record, _ := f.titleRecordQuiet()
				if lease, err := f.takeTitle(record.Revision); err == nil {
					t.Fatalf("changed %s delivered protected credentials", name)
				} else if lease != nil && len(lease.Bundle) != 0 {
					t.Fatal("credentials leaked on denial")
				}
			}
			if claimed, err := f.service.Store.Get(context.Background(), domain.JobKind, f.titleJob); err != nil {
				t.Fatal(err)
			} else if job, _ := store.Decode[domain.Job](claimed); job.State == domain.JobSucceeded {
				t.Fatal("changed authority produced a title")
			}
			session := f.session(t)
			if session.Name != "New session" {
				t.Fatalf("changed authority renamed the session: %+v", session)
			}
		})
	}
}

// Scenario 3 (actor): a revoked creating device loses title authority.
func TestManagedTitleDeniesRevokedActor(t *testing.T) {
	client := domain.NewID()
	f := newManagedTitleFixture(t, managedTitleOptions{})
	f.mutate(t, "client", func(tx *store.Tx) error {
		_, err := tx.Put(domain.DeviceKind, client, 0, "", "", domain.Device{Name: "Client", Type: domain.ClientDevice, Revoked: true})
		return err
	})
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		if managedTitleActorValid(tx, client) == nil {
			t.Fatal("a revoked actor retained title authority")
		}
		if managedTitleActorValid(tx, "") != nil {
			t.Fatal("the local owner lost title authority")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// A creator revoked after the protected Finish but before the report cannot
// rename the session: RevokeDevice does not cancel title jobs, so publication
// revalidates the frozen actor and keeps the valid usage without the text.
func TestManagedTitleDiscardsTextWhenCreatorIsRevokedBeforePublication(t *testing.T) {
	client := domain.NewID()
	f := newManagedTitleFixture(t, managedTitleOptions{creator: client})
	record, finish := f.runToFinish(t)
	f.mutate(t, "revoke-creator", func(tx *store.Tx) error {
		r, err := tx.Get(domain.DeviceKind, client)
		if err != nil {
			return err
		}
		device, err := store.Decode[domain.Device](r)
		if err != nil {
			return err
		}
		device.Revoked = true
		_, err = tx.Put(domain.DeviceKind, r.ID, r.Revision, "", "", device)
		return err
	})
	output := f.result(finish, "Revoked title", true)
	if err := f.report(record, output, nil); err != nil {
		t.Fatal(err)
	}
	session := f.session(t)
	if session.Name != "New session" || session.TitleState != domain.TitleSkipped || session.TitleReason != domain.TitleReasonAuthorityLost {
		t.Fatalf("a revoked creator's title was published: %+v", session)
	}
	var usage domain.AuxiliaryTitleResult
	if err := json.Unmarshal(output, &usage); err != nil {
		t.Fatal(err)
	}
	f.mutate(t, "usage-kept", func(tx *store.Tx) error {
		if _, replayed, err := tx.PutResponseUsage(domain.NewID(), *usage.UsageRecord); err != nil || !replayed {
			t.Fatal("the discarded title lost its valid usage attribution", replayed, err)
		}
		return nil
	})
}

// A creator that stays authorized publishes normally.
func TestManagedTitlePublishesForAuthorizedClientCreator(t *testing.T) {
	f := newManagedTitleFixture(t, managedTitleOptions{creator: domain.NewID()})
	record, finish := f.runToFinish(t)
	if err := f.report(record, f.result(finish, "Client title", false), nil); err != nil {
		t.Fatal(err)
	}
	if session := f.session(t); session.Name != "Client title" || session.TitleState != domain.TitleSucceeded {
		t.Fatalf("an authorized client creator's title was discarded: %+v", session)
	}
}

// Missing managed capability and non-ChatGPT profiles use distinct reasons.
func TestManagedTitleUnsupportedReasonsAreDistinct(t *testing.T) {
	absent := newManagedTitleFixture(t, managedTitleOptions{machineCapabilities: []domain.WorkerCapability{domain.ManagedCodexSubscriptionsV1, domain.AutomaticTitlesCodexV1, domain.ExecutionStartupV1}})
	if session := absent.session(t); session.TitleState != domain.TitleUnsupported || session.TitleReason != domain.TitleReasonCapabilityAbsent || session.TitleJobID != "" {
		t.Fatalf("a Worker without the managed adapter did not report the missing capability: %+v", session)
	}
	profile := newManagedTitleFixture(t, managedTitleOptions{service: domain.SubscriptionOpenCodeGo})
	if session := profile.session(t); session.TitleState != domain.TitleUnsupported || session.TitleReason != domain.TitleReasonUnsupportedAgent || session.TitleJobID != "" {
		t.Fatalf("an excluded subscription profile reported the misleading capability reason: %+v", session)
	}
}

// Scenario 5: unconfirmed cleanup or Finish retains the recovery fence and
// publishes nothing.
func TestManagedTitleRequiresProtectedFinishReceipt(t *testing.T) {
	for _, mode := range []string{"cleanup-unconfirmed", "no-receipt", "foreign-receipt"} {
		t.Run(mode, func(t *testing.T) {
			f := newManagedTitleFixture(t, managedTitleOptions{})
			record, err := f.claim()
			if err != nil {
				t.Fatal(err)
			}
			lease, err := f.takeTitle(record.Revision)
			if err != nil {
				t.Fatal(err)
			}
			clear(lease.Bundle)
			finish := domain.NewID()
			switch mode {
			case "cleanup-unconfirmed":
				finish, _ = f.finishLease(lease, nil, false, false)
				_, account := f.record()
				if account.Subscription.Lease == nil || !account.Subscription.RecoveryRequired {
					t.Fatal("unconfirmed cleanup released ownership")
				}
			case "foreign-receipt":
				if finish, err = f.finishLease(lease, f.bundle, true, true); err != nil {
					t.Fatal(err)
				}
				finish = f.connection
			default:
				if _, err = f.finishLease(lease, f.bundle, true, true); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.report(record, f.result(finish, "Should not apply", false), nil); err != nil {
				t.Fatal(err)
			}
			session := f.session(t)
			if session.Name != "New session" || session.TitleState != domain.TitleUncertain {
				t.Fatalf("an unproven Finish published a title: %+v", session)
			}
			if _, job := f.titleRecord(t); job.State != domain.JobUncertain {
				t.Fatal("the title job did not retain its uncertain state")
			}
		})
	}
}

// Scenario 6: rename and Stop racing inference never overwrite or recreate.
func TestManagedTitleRacesPreserveManualRenameAndStop(t *testing.T) {
	for _, race := range []string{"rename", "stop"} {
		t.Run(race, func(t *testing.T) {
			f := newManagedTitleFixture(t, managedTitleOptions{})
			record, err := f.claim()
			if err != nil {
				t.Fatal(err)
			}
			lease, err := f.takeTitle(record.Revision)
			if err != nil {
				t.Fatal(err)
			}
			clear(lease.Bundle)
			f.mutate(t, race, func(tx *store.Tx) error {
				sr, session, err := sessionRecord(tx, f.original.SessionID)
				if err != nil {
					return err
				}
				if race == "rename" {
					session.Name, session.NameOwner, session.NameGeneration = "Manual name", domain.ManualNameOwner, session.NameGeneration+1
					session.TitleState, session.TitleReason = domain.TitleSkipped, domain.TitleReasonManualRename
				} else {
					if _, err := cancelSessionTitleJob(tx, session.TitleJobID); err != nil {
						return err
					}
					session.TitleState, session.TitleReason = domain.TitleSkipped, domain.TitleReasonCanceled
				}
				_, err = tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session)
				return err
			})
			finish, err := f.finishLease(lease, f.bundle, true, true)
			if err != nil {
				t.Fatal("custody must complete even after the title lost its session authority", err)
			}
			if err := f.report(record, f.result(finish, "Late title", false), nil); err != nil {
				t.Fatal(err)
			}
			session := f.session(t)
			if session.Name == "Late title" || session.TitleState == domain.TitleSucceeded {
				t.Fatalf("late title overwrote a rename or a Stop: %+v", session)
			}
			if race == "rename" && session.Name != "Manual name" {
				t.Fatal("manual rename was lost")
			}
			_, account := f.record()
			if account.Subscription.Lease != nil || account.Subscription.RecoveryRequired {
				t.Fatal("a racing title fenced the account")
			}
		})
	}
}

// getHookSecrets runs a hook before every vault read, modeling work that commits
// while TakeSubscription is reading the protected bundle.
type getHookSecrets struct {
	accountSecrets
	before func()
}

func (g *getHookSecrets) Get(ctx context.Context, ref credentials.Ref) ([]byte, error) {
	if g.before != nil {
		g.before()
	}
	return g.accountSecrets.Get(ctx, ref)
}

// A Stop that cancels the title after its lease commits but before the bundle
// is returned never delivers credentials, so the lease is released instead of
// fencing the protected account for recovery.
func TestManagedTitleCanceledBeforeDeliveryReleasesLease(t *testing.T) {
	f := newManagedTitleFixture(t, managedTitleOptions{})
	record, err := f.claim()
	if err != nil {
		t.Fatal(err)
	}
	var cancelErr error
	f.service.accountSecrets = &getHookSecrets{accountSecrets: f.secrets, before: func() {
		_, cancelErr = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.cancel-title", nil, func(tx *store.Tx) (any, error) {
			return cancelSessionTitleJob(tx, f.titleJob)
		})
	}}
	response, err := f.takeTitle(record.Revision)
	if cancelErr != nil {
		t.Fatal(cancelErr)
	}
	if err == nil || response != nil {
		t.Fatal("a canceled title received protected credentials")
	}
	_, account := f.record()
	if account.Subscription.Lease != nil || account.Subscription.RecoveryRequired || account.Health != domain.AccountReady {
		t.Fatalf("a canceled undelivered title fenced the account: %+v", account.Subscription)
	}
	f.service.accountSecrets = f.secrets
}

// Scenario 7: invalid, multiline or oversized output is bounded and unavailable
// usage fabricates no accounting.
func TestManagedTitleRejectsInvalidOutputAndAllowsMissingUsage(t *testing.T) {
	for _, title := range []string{"", "two\nlines", strings.Repeat("x", 257), " padded "} {
		f := newManagedTitleFixture(t, managedTitleOptions{})
		record, finish := f.runToFinish(t)
		if err := f.report(record, f.result(finish, title, false), nil); err != nil {
			t.Fatal(err)
		}
		if session := f.session(t); session.Name != "New session" || session.TitleState != domain.TitleFailed || session.TitleReason != domain.TitleReasonInvalidOutput {
			t.Fatalf("invalid output %q was not bounded: %+v", title, session)
		}
	}
	f := newManagedTitleFixture(t, managedTitleOptions{})
	record, finish := f.runToFinish(t)
	if err := f.report(record, f.result(finish, "No usage", false), nil); err != nil {
		t.Fatal(err)
	}
	if session := f.session(t); session.Name != "No usage" || session.TitleState != domain.TitleSucceeded {
		t.Fatalf("missing native usage blocked a valid title: %+v", session)
	}
}

// Known budget rejection retires the queued title before any credential use.
func TestManagedTitleBudgetRejectionSkipsBeforeCredentials(t *testing.T) {
	f := newManagedTitleFixture(t, managedTitleOptions{})
	f.mutate(t, "budget", func(tx *store.Tx) error {
		basis, err := tokenPricing(publicPrice("USD"))
		if err != nil {
			return err
		}
		if _, err = tx.PutPricing(f.original.Configuration.ModelID, 0, domain.NewID(), basis); err != nil {
			return err
		}
		counts := reportedCounts(18)
		if _, _, err = tx.PutResponseUsage(domain.NewID(), domain.ResponseUsageRecord{SessionID: f.original.SessionID, ExecutionID: f.original.ExecutionID, AccountID: f.original.AccountID, ConnectionID: f.original.ConnectionID, SubscriptionService: domain.SubscriptionChatGPT, ModelID: f.original.Configuration.ModelID, Harness: domain.Codex, Version: "0.159.2", ThreadID: string(domain.NewID()), TurnID: string(domain.NewID()), Sequence: 3, Usage: domain.NativeResponseUsage{ResponseDigest: strings.Repeat("d", 64), Counts: &counts, CostEvidence: domain.UsageCostMissing}}); err != nil {
			return err
		}
		sr, session, err := sessionRecord(tx, f.original.SessionID)
		if err != nil {
			return err
		}
		session.EstimatedCostBudget = &domain.EstimatedCostBudget{Currency: "USD", Threshold: "0.0000975"}
		_, err = tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session)
		return err
	})
	if _, err := f.claim(); err != nil {
		t.Fatal(err)
	}
	session := f.session(t)
	if session.TitleState != domain.TitleSkipped || session.TitleReason != domain.TitleReasonBudgetReached {
		t.Fatalf("a budget-rejected title was admitted: %+v", session)
	}
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		if claimed, err := tx.TitleInferenceClaimed(f.titleJob); err != nil || claimed {
			t.Fatal("budget rejection consumed the inference claim", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Scenario 8: managed assignments never obtain API relay authority, and the
// historical versions keep their validation.
func TestManagedTitleHasNoAPIRelayAuthority(t *testing.T) {
	f := newManagedTitleFixture(t, managedTitleOptions{})
	if _, err := f.claim(); err != nil {
		t.Fatal(err)
	}
	digest := bytes.Repeat([]byte{9}, 32)
	request := &pb.RegisterExecutionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.titleJob), ExpectedRevision: 2}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), CredentialDigest: digest}
	if _, err := f.authorityFixture.client.RegisterExecution(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, request)); err == nil {
		t.Fatal("a managed title registered an API relay credential")
	}
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		if claimed, err := tx.TitleInferenceClaimed(f.titleJob); err != nil || claimed {
			t.Fatal("API registration consumed the managed inference claim", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
