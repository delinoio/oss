// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestNativeCompactionLargeOriginalAssignmentSettlesWithoutTruncation(t *testing.T) {
	for _, profile := range []struct {
		harness    domain.Harness
		managed    bool
		disconnect bool
	}{{domain.Codex, false, false}, {domain.OpenCode, false, false}, {domain.Codex, true, false}, {domain.Codex, false, true}} {
		harness, managed := profile.harness, profile.managed
		name := string(harness)
		if managed {
			name += "-subscription"
		}
		if profile.disconnect {
			name += "-disconnect"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			f := newFirstDispatchFixtureForHarness(t, harness, domain.ExecuteMode)
			prompt := strings.Repeat(`"`, domain.MaxPromptBytes)
			if _, err := sessionClient(f.accountFixture).EditQueuedInput(ctx, ownerRequest(f.identity, &pb.EditQueuedInputRequest{Mutation: acctMutation(f.change.Input, domain.NewID()), SessionId: f.change.Session.Id, Prompt: prompt})); err != nil {
				t.Fatal("valid original input rejected", err)
			}
			c := &continuationFixture{firstDispatchFixture: f, thread: domain.NewID()}
			if err := f.service.dispatchExecution(ctx, f.refresh(t)); err != nil {
				t.Fatal(err)
			}
			c.claim(t)
			if harness == domain.OpenCode {
				c.thread, c.turn = "ses_01960dcbe1faabcdefghijklmn", "msg_01960dcbe1faABCDEFGHIJKLMN"
				c.completeOpenCode(t, domain.ExecutionSucceeded)
			} else {
				c.complete(t, domain.ExecutionSucceeded)
			}
			f.workerStream.Close()
			if managed {
				configureLargeCompactionSubscription(t, c)
			}
			_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.large-compaction-capability", nil, func(tx *store.Tx) (any, error) {
				r, m, err := activeMachine(tx, f.selection.MachineID)
				if err != nil {
					return nil, err
				}
				capability := domain.CodexSessionCompactionV1
				if harness == domain.OpenCode {
					capability = domain.OpenCodeSessionCompactionV1
				}
				m.WorkerCapabilities = append(m.WorkerCapabilities, domain.NativeSessionCompactionV1, capability)
				return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
			})
			if err != nil {
				t.Fatal(err)
			}
			req := &pb.CompactSessionRequest{Mutation: acctMutation(resourceForTest(f.refresh(t)), domain.NewID())}
			r, err := sessionClient(f.accountFixture).CompactSession(ctx, ownerRequest(f.identity, req))
			if err != nil || len(r.Msg.Job.DocumentJson) <= 1<<20 {
				t.Fatal("large action admission", err)
			}
			var job domain.Job
			var input domain.SessionCompactionInput
			if domain.DecodeCompactionJob(r.Msg.Job.DocumentJson, &job) != nil || domain.DecodeCompactionInput(job.Input, &input) != nil || input.Assignment.Input.Prompt != prompt || input.Restore.Input.Prompt != prompt {
				t.Fatal("action lost original or restore evidence")
			}
			replay, err := sessionClient(f.accountFixture).CompactSession(ctx, ownerRequest(f.identity, req))
			if err != nil || !replay.Msg.Replayed || replay.Msg.Job.Id != r.Msg.Job.Id {
				t.Fatal("large action acceptance replay", err)
			}
			assertContext := func(state domain.JobState) {
				t.Helper()
				response, err := sessionClient(f.accountFixture).GetSessionContext(ctx, ownerRequest(f.identity, &pb.GetSessionContextRequest{SessionId: f.change.Session.Id}))
				var view sessionContextView
				if err != nil || domain.Decode(response.Msg.DocumentJson, &view) != nil || view.ManualAction == nil {
					t.Fatal("large action context unavailable", err)
				}
				var action struct {
					ActionID domain.ID       `json:"action_id"`
					State    domain.JobState `json:"state"`
				}
				if json.Unmarshal(view.ManualAction.Document, &action) != nil || action.ActionID != input.ActionID || action.State != state {
					t.Fatal("context lost accepted action", action)
				}
			}
			assertContext(domain.JobQueued)
			claimed := watchWorkAssignment(t, f.workerClient, f.workerIdentity, f.selection.MachineID, domain.ID(f.workerInstance))
			var assigned domain.Job
			if claimed.Id != r.Msg.Job.Id || domain.DecodeCompactionJob(claimed.DocumentJson, &assigned) != nil || !bytes.Equal(assigned.Input, job.Input) {
				t.Fatal("primary lane changed the original compaction input")
			}
			if harness == domain.Codex && !managed && !profile.disconnect {
				assertWorkerClaimExceptionBounds(t, claimed, domain.MaxCompactionInputBytes, domain.MaxCompactionJobBytes)
				malformed := bytes.Replace(claimed.DocumentJson, []byte(`"action_id":"`+string(input.ActionID)+`"`), []byte(`"action_id":""`), 1)
				record := store.Record{ID: domain.ID(claimed.Id), Kind: domain.JobKind, Revision: claimed.Revision}
				if _, _, err := decodeWorkerClaim(workerClaimJSON(t, record, malformed)); err == nil {
					t.Fatal("malformed enlarged compaction claim was accepted")
				}
			}
			reconnected := watchWorkAssignment(t, f.workerClient, f.workerIdentity, f.selection.MachineID, domain.ID(f.workerInstance))
			if reconnected.Id != claimed.Id || reconnected.Revision != claimed.Revision || !bytes.Equal(reconnected.DocumentJson, claimed.DocumentJson) {
				t.Fatal("reconnect changed the original compaction claim")
			}
			// Exercise every fresh authority reader at the admitted large bound.
			// Codex also uses a synchronized non-Direct route, without upstream I/O.
			routeID := domain.NewID()
			if harness == domain.Codex && !managed {
				identity, err := age.GenerateX25519Identity()
				if err != nil {
					t.Fatal(err)
				}
				keyID := domain.NewID()
				_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.large-compaction-route", nil, func(tx *store.Tx) (any, error) {
					_, err := tx.Put(domain.NetworkRouteKind, routeID, 0, "", "", domain.NetworkRoute{MachineID: f.selection.MachineID, Profile: domain.NetworkProfile{ProxyDefinition: domain.ProxyDefinition{Name: "Fixture", Mode: domain.ProxyHTTP, Host: "127.0.0.1", Port: 3128}}, Binding: &domain.WorkerNetworkBinding{DeviceID: f.workerDevice, PairingID: domain.NewID(), KeyID: keyID, Recipient: identity.Recipient().String(), Endpoint: f.endpoint.URL}})
					if err != nil {
						return nil, err
					}
					mr, machine, err := activeMachine(tx, f.selection.MachineID)
					if err != nil {
						return nil, err
					}
					machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.CodexAPIProxyV1)
					machine.Network = &domain.WorkerNetworkState{KeyID: keyID, Recipient: identity.Recipient().String(), InstanceID: domain.ID(f.workerInstance), RouteID: routeID, EffectiveGeneration: 1, NativeState: domain.WorkerRouteNotApplied, ObservedAt: time.Now().UTC()}
					return tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if managed {
				client := delidevv1connect.NewSubscriptionServiceClient(http.DefaultClient, f.endpoint.URL)
				taken, err := client.TakeSubscription(ctx, ownerRequest(f.workerIdentity, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(input.Assignment.AccountID), ExpectedRevision: claimed.Revision}, MachineId: f.machine.Id, InstanceId: f.workerInstance, OperationId: claimed.Id, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE}))
				if err != nil {
					t.Fatal("large action subscription lease", err)
				}
				clear(taken.Msg.Bundle)
				// The real authenticated Take path consumed this original large job.
				return
			}
			rawToken, err := security.RandomToken()
			if err != nil {
				t.Fatal(err)
			}
			token := apiproxy.TokenPrefix + rawToken
			digest := sha256.Sum256([]byte(token))
			_, err = f.workerClient.RegisterExecution(ctx, ownerRequest(f.workerIdentity, &pb.RegisterExecutionRequest{Mutation: acctMutation(claimed, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, CredentialDigest: digest[:]}))
			if err != nil {
				t.Fatal("large action registration", err)
			}
			if harness == domain.Codex {
				request := &pb.ReportWorkerNativeRouteRequest{Mutation: acctMutation(claimed, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, ExecutionId: string(input.ActionID), RouteId: string(routeID), Generation: 1, State: pb.WorkerNativeRouteState_WORKER_NATIVE_ROUTE_STATE_UNVERIFIED}
				if _, err := f.workerClient.ReportWorkerNativeRoute(ctx, ownerRequest(f.workerIdentity, request)); err != nil {
					t.Fatal("large action native route launch pin", err)
				}
				replay, err := f.workerClient.ReportWorkerNativeRoute(ctx, ownerRequest(f.workerIdentity, request))
				if err != nil || !replay.Msg.Replayed {
					t.Fatal("large action native route receipt", err)
				}
				request.Mutation.RequestId, request.State = string(domain.NewID()), pb.WorkerNativeRouteState_WORKER_NATIVE_ROUTE_STATE_OBSERVED
				if _, err := f.workerClient.ReportWorkerNativeRoute(ctx, ownerRequest(f.workerIdentity, request)); err != nil {
					t.Fatal("large action native route observation", err)
				}
			}
			lease, err := f.service.executionAuthority.Acquire(ctx, token)
			if err != nil {
				t.Fatal("large action authority", err)
			}
			defer lease.Release()
			if lease.ObserveHistory != nil {
				if err := lease.ObserveHistory(ctx, false); err != nil {
					t.Fatal("large action history observation", err)
				}
			}
			if harness == domain.Codex {
				id, attempted := domain.NewID(), true
				if err := lease.PublishDiagnostic(ctx, domain.RequestDiagnostic{ID: id, CorrelationID: id, SessionID: lease.Scope.SessionID, ExecutionID: lease.Scope.ExecutionID, AccountID: lease.Scope.AccountID, ConnectionID: lease.Scope.ConnectionID, ProviderID: lease.Scope.ProviderID, ModelID: lease.Scope.ModelID, Harness: harness, Source: domain.DiagnosticProxyHTTP, Operation: domain.DiagnosticCompact, State: domain.DiagnosticInProgress, Purpose: domain.ConversationUsage, ObservedAt: time.Now().UTC(), HTTPAttempted: &attempted}); err != nil {
					t.Fatal(err)
				}
				if err := lease.ObserveResponseUsage(ctx, id, domain.NativeResponseUsage{Source: domain.CompactionHTTPResponse, ResponseDigest: strings.Repeat("ab", 32), CostEvidence: domain.UsageCostMissing}); err != nil {
					t.Fatal("large action response usage", err)
				}
			}
			lease.Release()
			if profile.disconnect {
				account, err := f.service.Store.Get(ctx, domain.AccountKind, input.Assignment.AccountID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.accounts.DisconnectAccount(ctx, ownerRequest(f.identity, &pb.DisconnectAccountRequest{Mutation: acctMutation(resourceForTest(account), domain.NewID())})); err != nil {
					t.Fatal("large action disconnect", err)
				}
				if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
					canceled, err := tx.JobCancellationRequested(domain.ID(claimed.Id))
					if err != nil || !canceled {
						t.Fatal("large native action was not canceled", err)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			output := publicCompactionResult(input, domain.ID(claimed.Id), false)
			if harness == domain.Codex {
				output = domain.SessionCompactionResult{Version: 2, Harness: domain.Codex, ActionID: input.ActionID, ExecutionID: input.Assignment.ExecutionID, Outcome: domain.CompactionSucceeded, CleanupVerified: true, Checkpoint: output.Checkpoint, Codex: &domain.CodexCompactionResult{NativeThreadID: input.Completion.NativeThreadID, SourceNativeTurnID: input.Completion.NativeTurnID, NativeTurnID: domain.NativeIdentity(domain.NewID()), LiveItemID: "original-live-context", HistoryItemID: "item-0", HistoryDigest: strings.Repeat("ef", 32), Actions: 1, Acknowledged: true, LifecycleCompleted: true, ResponseUsages: []domain.NativeResponseUsage{}}}
			} else {
				output = domain.SessionCompactionResult{Version: 3, Harness: domain.OpenCode, ActionID: input.ActionID, ExecutionID: input.Assignment.ExecutionID, Outcome: domain.CompactionSucceeded, CleanupVerified: true, Checkpoint: output.Checkpoint, OpenCode: &domain.OpenCodeCompactionResult{NativeSessionID: input.Completion.NativeThreadID, SourceNativeInputID: input.Completion.NativeTurnID, UserID: "msg_01960dcbe1fcABCDEFGHIJKLMN", PartID: "prt_01960dcbe1fcABCDEFGHIJKLMN", SummaryID: "msg_01960dcbe1fdABCDEFGHIJKLMN", CompletedEventID: "evt_01960dcbe1fdABCDEFGHIJKLMN", HistoryDigest: strings.Repeat("ef", 32), Actions: 1, Acknowledged: true, LifecycleCompleted: true, Usages: []domain.OpenCodeUsageObservation{}}}
			}
			if err := output.Validate(); err != nil {
				t.Fatal("invalid native result fixture", err)
			}
			raw, _ := json.Marshal(output)
			// Controlled native result fixture exercises real authenticated
			// settlement; it does not establish native history/account acceptance.
			_, err = f.workerClient.ReportWork(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(claimed, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: raw}))
			if err != nil {
				t.Fatal("large action settlement was stranded", err)
			}
			if profile.disconnect {
				assertContext(domain.JobUncertain)
			} else {
				assertContext(domain.JobSucceeded)
			}
		})
	}
}

// Controlled protected-state fixture: configure a completed synthetic original
// under the managed identity before admission, without claiming account acceptance.
func configureLargeCompactionSubscription(t *testing.T, c *continuationFixture) {
	t.Helper()
	f, ctx := c.firstDispatchFixture, context.Background()
	bundle := subscriptionTestBundle("fixture-account", "large-compaction", time.Now().UTC())
	_, identity, err := subscription.Parse(bundle)
	if err != nil {
		t.Fatal(err)
	}
	identityJSON, _ := json.Marshal(identity)
	generation := domain.NewID()
	f.service.accountSecrets = &accountTestSecrets{values: map[credentials.Ref][]byte{{Owner: c.input.AccountID, ID: generation, Purpose: credentials.AccountLogin}: bundle}, removed: map[credentials.Ref]bool{}}
	t.Cleanup(func() { clear(bundle) })
	c.input.Configuration.ProviderID, c.input.Configuration.SubscriptionService, c.input.Configuration.Subscription = "", domain.SubscriptionChatGPT, true
	c.input.ConfigurationDigest, err = c.input.Configuration.Digest()
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.large-managed-compaction", nil, func(tx *store.Tx) (any, error) {
		ar, account, err := accountFromTx(tx, c.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		account.ProviderID, account.SubscriptionService, account.Type = "", domain.SubscriptionChatGPT, domain.SubscriptionAccount
		account.Validation, account.Catalog = nil, nil
		account.Connection.Authentication = domain.SubscriptionAuth
		account.Subscription = &domain.SubscriptionState{Generation: generation, IdentityCommitment: f.service.accountCommitment(f.service.Identity.ServerID, identityJSON), OwnerMachineID: c.input.MachineID}
		if _, err := tx.Put(domain.AccountKind, ar.ID, ar.Revision, "", "", account); err != nil {
			return nil, err
		}
		mr, err := tx.Get(domain.ModelKind, c.input.Configuration.ModelID)
		if err != nil {
			return nil, err
		}
		model, err := store.Decode[domain.Model](mr)
		if err != nil {
			return nil, err
		}
		model.ProviderID, model.SubscriptionService, model.SourceKind = "", domain.SubscriptionChatGPT, domain.SubscriptionModel
		if _, err := tx.Put(domain.ModelKind, mr.ID, mr.Revision, "", "", model); err != nil {
			return nil, err
		}
		sr, session, err := sessionRecord(tx, c.input.SessionID)
		if err != nil {
			return nil, err
		}
		session.InitialExecution.Configuration, session.InitialExecution.ConfigurationDigest = c.input.Configuration, c.input.ConfigurationDigest
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.SessionID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		machineRecord, machine, err := activeMachine(tx, c.input.MachineID)
		if err != nil {
			return nil, err
		}
		machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.ManagedCodexSubscriptionsV1)
		if _, err := tx.Put(domain.MachineKind, machineRecord.ID, machineRecord.Revision, "", "", machine); err != nil {
			return nil, err
		}
		jr, err := tx.Get(domain.JobKind, domain.ID(c.job.Id))
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](jr)
		if err != nil {
			return nil, err
		}
		job.Input, err = json.Marshal(c.input)
		if err != nil {
			return nil, err
		}
		return tx.PutJob(jr.ID, jr.Revision, jr.SessionID, jr.ProjectID, job)
	})
	if err != nil {
		t.Fatal(err)
	}
}
