// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func publicCompactionFixture(t *testing.T) (*firstDispatchFixture, *publicationFixture) {
	t.Helper()
	return publicCompactionOutcomeFixture(t, domain.ExecutionSucceeded)
}

func publicCompactionOutcomeFixture(t *testing.T, outcome domain.ExecutionOutcome) (*firstDispatchFixture, *publicationFixture) {
	t.Helper()
	f := newFirstDispatchFixtureForHarness(t, domain.ClaudeCode, domain.ExecuteMode)
	if e := f.service.dispatchExecution(context.Background(), f.refresh(t)); e != nil {
		t.Fatal(e)
	}
	c := &continuationFixture{firstDispatchFixture: f}
	c.claim(t)
	pf := &publicationFixture{authorityFixture: &authorityFixture{service: f.service, http: nil, client: f.workerClient, workerToken: f.workerIdentity.Token, job: domain.ID(c.job.Id), device: f.workerDevice, instance: domain.ID(f.workerInstance), input: c.input}, revision: c.job.Revision, thread: c.input.SessionID, turn: "123e4567-e89b-42d3-a456-426614174000"}
	terminal := publishClaudeTerminalFixture(t, pf, true)
	if outcome == domain.ExecutionFailed {
		terminal.Outcome = outcome
		terminal.ClaudeTerminal.Reason, terminal.ClaudeTerminal.Error, terminal.ClaudeTerminal.Command = domain.ClaudeAPIError, true, domain.ClaudeCommandCancelled
	}
	completion := publishClaudeRootCompletionFixture(t, pf, terminal)
	completion.Outcome = outcome
	raw, _ := json.Marshal(completion)
	_, e := f.workerClient.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(c.job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: raw}))
	if e != nil {
		t.Fatal(e)
	}
	f.workerStream.Close()
	return f, pf
}

func TestCompactionPreservesOriginalClaudeStartupAfterDiscoveryChanges(t *testing.T) {
	ctx := context.Background()
	f, source := publicCompactionFixture(t)
	machineRecord, err := f.service.Store.Get(ctx, domain.MachineKind, domain.ID(f.machine.Id))
	if err != nil {
		t.Fatal(err)
	}
	machine, err := store.Decode[domain.Machine](machineRecord)
	if err != nil {
		t.Fatal(err)
	}
	for index := range machine.Installations {
		if machine.Installations[index].Harness == domain.ClaudeCode {
			machine.Installations[index].ResolvedPath = "/replacement/claude"
		}
	}
	if _, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.replace-claude-installation", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.MachineKind, machineRecord.ID, machineRecord.Revision, "", "", machine)
	}); err != nil {
		t.Fatal(err)
	}
	before := f.refresh(t)
	accepted, err := sessionClient(f.accountFixture).CompactSession(ctx, ownerRequest(f.identity, &pb.CompactSessionRequest{Mutation: acctMutation(resourceForTest(before), domain.NewID())}))
	if err != nil {
		t.Fatal(err)
	}
	var job domain.Job
	var input domain.SessionCompactionInput
	if domain.Decode(accepted.Msg.Job.DocumentJson, &job) != nil || domain.DecodeCompactionInput(job.Input, &input) != nil || !reflect.DeepEqual(input.Assignment.Startup, source.input.Startup) || !reflect.DeepEqual(input.Assignment.Installation, domain.Installation{}) {
		t.Fatal("discovery replaced the original startup selection")
	}
}

func publicCompactionResult(i domain.SessionCompactionInput, job domain.ID, failed bool) domain.SessionCompactionResult {
	r := domain.SessionCompactionResult{Version: 1, ActionID: i.ActionID, ExecutionID: i.Assignment.ExecutionID, OuterKind: domain.ClaudeResultSuccess, Outcome: domain.CompactionSucceeded, CommandEchoID: string(i.ActionID), ResultID: string(domain.NewID()), CommandCompletedID: string(domain.NewID()), IdleID: string(domain.NewID()), CleanupVerified: true, Checkpoint: domain.SessionCompactionRef{JobID: job, ActionID: i.ActionID, ExecutionID: i.Assignment.ExecutionID, CheckpointDigest: strings.Repeat("ab", 32), NativeDigest: strings.Repeat("cd", 32), RequiresResume: failed}}
	if failed {
		r.Outcome = domain.CompactionFailed
		return r
	}
	r.BoundaryID, r.SummaryID = string(domain.NewID()), string(domain.NewID())
	zero := domain.ClaudeProgressCount("0")
	r.Boundary = &domain.ClaudeCompactionBoundary{Trigger: domain.ClaudeManualCompaction, Before: "100", After: &zero, DurationMS: &zero, Messages: &domain.ClaudePreservedMessages{Anchor: r.SummaryID, IDs: []string{string(domain.NewID())}}}
	r.Summary = &domain.SessionCompactionSummary{BoundaryID: r.BoundaryID, Text: "Original native summary"}
	return r
}

func TestPublicCompactionAtomicReceiptAndFIFO(t *testing.T) {
	testPublicCompactionAtomicReceiptAndFIFO(t, domain.ClaudeCode)
}
func TestPublicCodexCompactionAtomicReceiptAndFIFO(t *testing.T) {
	testPublicCompactionAtomicReceiptAndFIFO(t, domain.Codex)
}
func TestPublicOpenCodeCompactionAtomicReceiptAndFIFO(t *testing.T) {
	testPublicCompactionAtomicReceiptAndFIFO(t, domain.OpenCode)
}
func testPublicCompactionAtomicReceiptAndFIFO(t *testing.T, harness domain.Harness) {
	for _, scenario := range []string{"success", "failed-compact-outer-success", "queued-stop", "queued-archive", "claimed-stop", "claimed-archive", "claimed-disconnect", "lost-report", "foreign-result"} {
		if harness != domain.ClaudeCode && scenario == "failed-compact-outer-success" {
			continue
		}
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			var f *firstDispatchFixture
			var pf *publicationFixture
			if harness == domain.Codex || harness == domain.OpenCode {
				c := newContinuationFixtureProfile(t, domain.ExecutionSucceeded, harness)
				f = c.firstDispatchFixture
				pf = &publicationFixture{authorityFixture: &authorityFixture{service: f.service, client: f.workerClient, workerToken: f.workerIdentity.Token, job: domain.ID(c.job.Id), device: f.workerDevice, instance: domain.ID(f.workerInstance), input: c.input}, revision: c.job.Revision, thread: c.thread, turn: c.turn}
				f.workerStream.Close()
				_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.codex-compaction-capability", nil, func(tx *store.Tx) (any, error) {
					r, m, err := activeMachine(tx, f.selection.MachineID)
					if err != nil {
						return nil, err
					}
					nativeCapability := domain.CodexSessionCompactionV1
					if harness == domain.OpenCode {
						nativeCapability = domain.OpenCodeSessionCompactionV1
					}
					m.WorkerCapabilities = append(m.WorkerCapabilities, domain.NativeSessionCompactionV1, nativeCapability)
					return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
				})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				f, pf = publicCompactionFixture(t)
			}
			client := sessionClient(f.accountFixture)
			before := f.refresh(t)
			prior, _ := store.Decode[domain.Session](before)
			req := &pb.CompactSessionRequest{Mutation: acctMutation(resourceForTest(before), domain.NewID())}
			accepted, e := client.CompactSession(ctx, ownerRequest(f.identity, req))
			if e != nil {
				t.Fatal(e)
			}
			replay, e := client.CompactSession(ctx, ownerRequest(f.identity, req))
			if e != nil || !replay.Msg.Replayed || replay.Msg.Job.Id != accepted.Msg.Job.Id {
				t.Fatal("receipt did not retain one job", e)
			}
			row := f.refresh(t)
			pending, _ := store.Decode[domain.Session](row)
			raw, _ := json.Marshal(domain.SessionInput{Prompt: "Later queued input", Mode: domain.ExecuteMode})
			_, e = client.EnqueueInput(ctx, ownerRequest(f.identity, &pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: string(row.ID), DocumentJson: raw}))
			if e != nil {
				t.Fatal(e)
			}
			if pending.Dispatch != domain.DispatchPaused || pending.ExecutionSelection() != prior.ExecutionSelection() || pending.CompactionJobID != domain.ID(accepted.Msg.Job.Id) {
				t.Fatal("acceptance replaced execution")
			}
			if e := f.service.dispatchExecution(ctx, f.refresh(t)); e == nil {
				t.Fatal("FIFO overtook pending action")
			}
			var job domain.Job
			domain.Decode(accepted.Msg.Job.DocumentJson, &job)
			var input domain.SessionCompactionInput
			if domain.Decode(job.Input, &input) != nil || input.Validate() != nil {
				t.Fatal("invalid action assignment")
			}
			if scenario == "queued-stop" || scenario == "queued-archive" {
				action := pb.SessionAction_SESSION_ACTION_STOP
				if scenario == "queued-archive" {
					action = pb.SessionAction_SESSION_ACTION_ARCHIVE
				}
				sr := f.refresh(t)
				_, e := client.ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(resourceForTest(sr), domain.NewID()), Action: action}))
				if e != nil {
					t.Fatal(e)
				}
				r := f.refresh(t)
				state, _ := store.Decode[domain.Session](r)
				jr, _ := f.service.Store.Get(ctx, domain.JobKind, domain.ID(accepted.Msg.Job.Id))
				saved, _ := store.Decode[domain.Job](jr)
				if state.CompactionJobID != "" || saved.State != domain.JobCanceled || state.Recovery != domain.NoRecovery || state.Outcome != prior.Outcome || state.PendingInputs != 1 {
					t.Fatal("queued cancellation lost safe predecessor")
				}
				return
			}
			// Claim deterministically under the exact same Worker instance. The public
			// native fixture separately exercises real WatchWork/process ownership.
			claimed, e := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.claim-compaction", nil, func(tx *store.Tx) (any, error) {
				r, e := tx.Get(domain.JobKind, domain.ID(accepted.Msg.Job.Id))
				if e != nil {
					return nil, e
				}
				job.State, job.InstanceID, job.AssignedDeviceID = domain.JobClaimed, pf.instance, pf.device
				return tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job)
			})
			if e != nil {
				t.Fatal(e)
			}
			var claim store.Record
			if json.Unmarshal(claimed.Data, &claim) != nil {
				t.Fatal("invalid claim")
			}
			rawToken, e := security.RandomToken()
			if e != nil {
				t.Fatal(e)
			}
			token := apiproxy.TokenPrefix + rawToken
			digest := sha256.Sum256([]byte(token))
			_, e = f.workerClient.RegisterExecution(ctx, ownerRequest(f.workerIdentity, &pb.RegisterExecutionRequest{Mutation: acctMutation(resourceForTest(claim), domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, CredentialDigest: digest[:]}))
			if e != nil {
				t.Fatal("action relay registration", e)
			}
			lease, e := f.service.executionAuthority.Acquire(ctx, token)
			expected := []apiproxy.Operation{apiproxy.MessageCreate}
			if harness == domain.Codex {
				expected = []apiproxy.Operation{apiproxy.ResponseCreate, apiproxy.ResponseCompact}
			} else if harness == domain.OpenCode {
				expected = []apiproxy.Operation{apiproxy.ChatCompletion}
			}
			if lease != nil {
				defer lease.Release()
			}
			if e != nil || lease.Scope.ExecutionID != input.ActionID || lease.Scope.AccountID != input.Assignment.AccountID || lease.Scope.Harness != harness || !reflect.DeepEqual(lease.Scope.Operations, expected) {
				t.Fatal("manual action changed relay authority", e)
			}
			diagnosticOperation := domain.DiagnosticMessage
			if harness == domain.Codex {
				diagnosticOperation = domain.DiagnosticCompact
			}
			if harness == domain.OpenCode {
				diagnosticOperation = domain.DiagnosticChat
			}
			diagnosticID := domain.NewID()
			attempted := false
			if err := lease.PublishDiagnostic(ctx, domain.RequestDiagnostic{ID: diagnosticID, CorrelationID: diagnosticID, SessionID: lease.Scope.SessionID, ExecutionID: lease.Scope.ExecutionID, AccountID: lease.Scope.AccountID, ConnectionID: lease.Scope.ConnectionID, ProviderID: lease.Scope.ProviderID, ModelID: lease.Scope.ModelID, Harness: lease.Scope.Harness, Source: domain.DiagnosticProxyHTTP, Operation: diagnosticOperation, State: domain.DiagnosticInProgress, Purpose: domain.ConversationUsage, ObservedAt: time.Now().UTC(), HTTPAttempted: &attempted}); err != nil {
				t.Fatal("manual compaction could not publish its initial diagnostic", err)
			}
			lease.Release()
			canceled := strings.HasPrefix(scenario, "claimed-")
			if canceled {
				if scenario == "claimed-disconnect" {
					account, err := f.service.Store.Get(ctx, domain.AccountKind, input.Assignment.AccountID)
					if err != nil {
						t.Fatal(err)
					}
					_, e = f.accounts.DisconnectAccount(ctx, ownerRequest(f.identity, &pb.DisconnectAccountRequest{Mutation: acctMutation(resourceForTest(account), domain.NewID())}))
				} else {
					action := pb.SessionAction_SESSION_ACTION_STOP
					if scenario == "claimed-archive" {
						action = pb.SessionAction_SESSION_ACTION_ARCHIVE
					}
					_, e = client.ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(resourceForTest(f.refresh(t)), domain.NewID()), Action: action}))
				}
				if e != nil {
					t.Fatal("claimed cancellation", e)
				}
			}
			result := publicCompactionResult(input, claim.ID, scenario == "failed-compact-outer-success")
			if harness == domain.Codex {
				result = domain.SessionCompactionResult{Version: 2, Harness: domain.Codex, ActionID: input.ActionID, ExecutionID: input.Assignment.ExecutionID, Outcome: domain.CompactionSucceeded, CleanupVerified: true, Checkpoint: result.Checkpoint, Codex: &domain.CodexCompactionResult{NativeThreadID: input.Completion.NativeThreadID, SourceNativeTurnID: input.Completion.NativeTurnID, NativeTurnID: domain.NativeIdentity(domain.NewID()), LiveItemID: "original-live-context", HistoryItemID: "item-0", HistoryDigest: strings.Repeat("ef", 32), Actions: 1, Acknowledged: true, LifecycleCompleted: true, ResponseUsages: []domain.NativeResponseUsage{}}}
			}
			if harness == domain.OpenCode {
				result = domain.SessionCompactionResult{Version: 3, Harness: domain.OpenCode, ActionID: input.ActionID, ExecutionID: input.Assignment.ExecutionID, Outcome: domain.CompactionSucceeded, CleanupVerified: true, Checkpoint: result.Checkpoint, OpenCode: &domain.OpenCodeCompactionResult{NativeSessionID: input.Completion.NativeThreadID, SourceNativeInputID: input.Completion.NativeTurnID, UserID: "msg_01960dcbe1fcABCDEFGHIJKLMN", PartID: "prt_01960dcbe1fcABCDEFGHIJKLMN", SummaryID: "msg_01960dcbe1fdABCDEFGHIJKLMN", CompletedEventID: "evt_01960dcbe1fdABCDEFGHIJKLMN", HistoryDigest: strings.Repeat("ef", 32), Actions: 1, Acknowledged: true, LifecycleCompleted: true, Usages: []domain.OpenCodeUsageObservation{}}}
			}
			if scenario == "foreign-result" {
				result.Checkpoint.JobID = domain.NewID()
			}
			output, _ := json.Marshal(result)
			report := &pb.ReportWorkRequest{Mutation: acctMutation(resourceForTest(claim), domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: output}
			if scenario == "lost-report" {
				report.OutputJson = nil
				report.Problem = &pb.ErrorDetail{Code: string(domain.RecoveryRequired)}
			}
			response, e := f.workerClient.ReportWork(ctx, ownerRequest(f.workerIdentity, report))
			if e != nil {
				t.Fatal(e)
			}
			repeated, e := f.workerClient.ReportWork(ctx, ownerRequest(f.workerIdentity, report))
			if e != nil || !repeated.Msg.Replayed || repeated.Msg.Job.Id != response.Msg.Job.Id {
				t.Fatal("report replay changed original action", e)
			}
			if lease, e := f.service.executionAuthority.Acquire(ctx, token); e == nil {
				lease.Release()
				t.Fatal("settled/uncertain action retained relay authority")
			}
			after := f.refresh(t)
			state, _ := store.Decode[domain.Session](after)
			if state.Outcome != prior.Outcome || state.ExecutionSelection() != prior.ExecutionSelection() || !reflect.DeepEqual(state.Execution, prior.Execution) || state.PendingInputs != 1 {
				t.Fatal("action changed conversation/queue history")
			}
			if scenario == "lost-report" || scenario == "foreign-result" || canceled {
				if state.Recovery != domain.NeedsRecovery || state.Dispatch != domain.DispatchPaused || state.CompactionJobID != claim.ID {
					t.Fatal("uncertainty regained send authority")
				}
				if canceled {
					jr, err := f.service.Store.Get(ctx, domain.JobKind, claim.ID)
					job, decodeErr := store.Decode[domain.Job](jr)
					if err != nil || decodeErr != nil || job.State != domain.JobUncertain || job.Problem == nil || job.Problem.Code != domain.RecoveryRequired || string(job.Output) != string(output) || !reflect.DeepEqual(state.Compaction, prior.Compaction) {
						t.Fatal("canceled report released ownership or lost observed evidence", err, decodeErr)
					}
					if scenario == "claimed-archive" && state.Archive != domain.Archived {
						t.Fatal("canceled report finalized Archive")
					}
				}
				// Recovery of the old conversation must not release a distinct
				// uncertain action's workspace or permit another command.
				_, recoveryErr := client.RecoverSessionExecution(ctx, ownerRequest(f.identity, &pb.RecoverSessionExecutionRequest{Mutation: acctMutation(resourceForTest(after), domain.NewID()), ExpectedExecutionId: string(prior.ExecutionSelection().ID)}))
				if connect.CodeOf(recoveryErr) != connect.CodeFailedPrecondition {
					t.Fatal("conversation recovery released action ownership", recoveryErr)
				}
				return
			}
			if state.CompactionJobID != "" || state.Compaction == nil {
				t.Fatal("settled result lost checkpoint")
			}
			if scenario == "failed-compact-outer-success" {
				if state.Dispatch != domain.DispatchPaused || state.NextExecutionIntent != "" {
					t.Fatal("outer success resumed failed action")
				}
				if e := f.service.dispatchExecution(ctx, after); e == nil {
					t.Fatal("failed compact advanced FIFO")
				}
				_, e := client.ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(resourceForTest(f.refresh(t)), domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_RESUME}))
				if e != nil {
					t.Fatal("explicit Resume", e)
				}
			} else {
				if state.Dispatch != domain.DispatchReady {
					t.Fatal("successful compaction lost original ready intent")
				}
				if e := f.service.dispatchExecution(ctx, after); e != nil {
					t.Fatal(e)
				}
			}
			current := f.refresh(t)
			advanced, _ := store.Decode[domain.Session](current)
			// The selected execution advances only after the original action has settled.
			if advanced.ExecutionSelection().ID == prior.ExecutionSelection().ID || advanced.ActiveExecutionID == "" {
				t.Fatal("continuation did not select later input")
			}
		})
	}
}

func resourceForTest(r store.Record) *pb.Resource {
	return &pb.Resource{Id: string(r.ID), Revision: r.Revision}
}

func TestCompactionContextAuthorizationAndUnavailableCounts(t *testing.T) {
	f, pf := publicCompactionFixture(t)
	client := sessionClient(f.accountFixture)
	ctx := context.Background()
	reply, e := client.GetSessionContext(ctx, ownerRequest(f.identity, &pb.GetSessionContextRequest{SessionId: string(pf.input.SessionID)}))
	if e != nil {
		t.Fatal(e)
	}
	var view sessionContextView
	if json.Unmarshal(reply.Msg.DocumentJson, &view) != nil || view.CurrentTokens != nil || view.ManualAction != nil || len(reply.Msg.Capabilities) != 2 {
		t.Fatal("missing context became measured utilization", string(reply.Msg.DocumentJson))
	}
	if _, e := client.GetSessionContext(ctx, ownerRequest(security.Identity{Token: f.workerIdentity.Token}, &pb.GetSessionContextRequest{SessionId: string(pf.input.SessionID)})); e != nil {
		t.Fatal("registered Worker could not read context", e)
	}
	sr := f.refresh(t)
	_, e = client.CompactSession(ctx, ownerRequest(f.identity, &pb.CompactSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(sr.ID), ExpectedRevision: sr.Revision - 1}}))
	if connect.CodeOf(e) != connect.CodeAborted {
		t.Fatal("stale session revision accepted", e)
	}
	// Active/unsupported/unsettled native state is never a numeric zero nor an
	// advertised manual-action capability.
	_, e = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.unsettled-context", nil, func(tx *store.Tx) (any, error) {
		r, s, e := sessionRecord(tx, sr.ID)
		if e != nil {
			return nil, e
		}
		s.Execution.UnconfirmedResponses = 1
		return tx.Put(domain.SessionKind, r.ID, r.Revision, r.ID, r.ProjectID, s)
	})
	if e != nil {
		t.Fatal(e)
	}
	reply, e = client.GetSessionContext(ctx, ownerRequest(f.identity, &pb.GetSessionContextRequest{SessionId: string(sr.ID)}))
	if e != nil || len(reply.Msg.Capabilities) != 1 {
		t.Fatal("unsettled native state gained manual action", e)
	}
}

func TestCompactionOwnershipBlocksOtherArchiveCompletion(t *testing.T) {
	f, _ := publicCompactionFixture(t)
	ctx := context.Background()
	before := f.refresh(t)
	client := sessionClient(f.accountFixture)
	if _, e := client.CompactSession(ctx, ownerRequest(f.identity, &pb.CompactSessionRequest{Mutation: acctMutation(resourceForTest(before), domain.NewID())})); e != nil {
		t.Fatal(e)
	}
	_, e := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.late-other-archive", nil, func(tx *store.Tx) (any, error) {
		r, s, e := sessionRecord(tx, before.ID)
		if e != nil {
			return nil, e
		}
		s.Archive = domain.Archived
		return tx.Put(domain.SessionKind, r.ID, r.Revision, r.ID, r.ProjectID, s)
	})
	if e != nil {
		t.Fatal(e)
	}
	held := f.refresh(t)
	s, _ := store.Decode[domain.Session](held)
	if s.Archive != domain.Archived || s.CompactionJobID == "" {
		t.Fatal("another completion archived over action ownership")
	}
	_, e = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.forward-cleanup", nil, func(tx *store.Tx) (any, error) { return nil, finishForwardArchive(tx, before.ID) })
	if e != nil || f.refresh(t).Revision != held.Revision {
		t.Fatal("forward cleanup released action workspace", e)
	}
}

func TestWorkerRevocationPreservesCompactionDispatchBoundary(t *testing.T) {
	for _, claimed := range []bool{false, true} {
		t.Run(map[bool]string{false: "queued", true: "claimed"}[claimed], func(t *testing.T) {
			ctx := context.Background()
			f, pf := publicCompactionFixture(t)
			client := sessionClient(f.accountFixture)
			before := f.refresh(t)
			prior, err := store.Decode[domain.Session](before)
			if err != nil {
				t.Fatal(err)
			}
			accepted, err := client.CompactSession(ctx, ownerRequest(f.identity, &pb.CompactSessionRequest{Mutation: acctMutation(resourceForTest(before), domain.NewID())}))
			if err != nil {
				t.Fatal(err)
			}
			id := domain.ID(accepted.Msg.Job.Id)
			raw, _ := json.Marshal(domain.SessionInput{Prompt: "Later queued input", Mode: domain.ExecuteMode})
			if _, err := client.EnqueueInput(ctx, ownerRequest(f.identity, &pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: string(before.ID), DocumentJson: raw})); err != nil {
				t.Fatal(err)
			}
			if claimed {
				_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.claim-compaction-for-revocation", id, func(tx *store.Tx) (any, error) {
					r, err := tx.Get(domain.JobKind, id)
					if err != nil {
						return nil, err
					}
					job, err := store.Decode[domain.Job](r)
					if err != nil {
						return nil, err
					}
					job.State, job.InstanceID, job.AssignedDeviceID = domain.JobClaimed, pf.instance, pf.device
					return tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			device, err := f.service.Store.Get(ctx, domain.DeviceKind, pf.device)
			if err != nil {
				t.Fatal(err)
			}
			devices := delidevv1connect.NewDeviceServiceClient(http.DefaultClient, f.endpoint.URL)
			revoke := &pb.RevokeDeviceRequest{Mutation: acctMutation(resourceForTest(device), domain.NewID())}
			if _, err := devices.RevokeDevice(ctx, ownerRequest(f.identity, revoke)); err != nil {
				t.Fatal(err)
			}
			replay, err := devices.RevokeDevice(ctx, ownerRequest(f.identity, revoke))
			if err != nil || !replay.Msg.Replayed {
				t.Fatal("revocation receipt did not replay", err)
			}
			row := f.refresh(t)
			state, err := store.Decode[domain.Session](row)
			if err != nil {
				t.Fatal(err)
			}
			jr, err := f.service.Store.Get(ctx, domain.JobKind, id)
			if err != nil {
				t.Fatal(err)
			}
			job, err := store.Decode[domain.Job](jr)
			if err != nil {
				t.Fatal(err)
			}
			if state.Dispatch != domain.DispatchPaused || state.NextExecutionIntent != "" || state.LastCompactionJobID != id || state.Outcome != prior.Outcome || !reflect.DeepEqual(state.Execution, prior.Execution) || state.ExecutionSelection() != prior.ExecutionSelection() || state.PendingInputs != 1 {
				t.Fatal("revocation changed the preceding execution, action history or FIFO")
			}
			if claimed {
				if job.State != domain.JobUncertain || job.FinishedAt != nil || state.CompactionJobID != id || state.Recovery != domain.NeedsRecovery {
					t.Fatal("claimed compaction lost native uncertainty")
				}
			} else if job.State != domain.JobCanceled || job.FinishedAt == nil || state.CompactionJobID != "" || state.Recovery != domain.NoRecovery {
				t.Fatal("undispatched compaction retained native ownership")
			}
			for _, action := range []pb.SessionAction{pb.SessionAction_SESSION_ACTION_STOP, pb.SessionAction_SESSION_ACTION_ARCHIVE} {
				if _, err := client.ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(resourceForTest(f.refresh(t)), domain.NewID()), Action: action})); err != nil {
					t.Fatal("revoked action prevented explicit control", err)
				}
			}
			final, err := store.Decode[domain.Session](f.refresh(t))
			if err != nil {
				t.Fatal(err)
			}
			if claimed {
				if final.Archive != domain.Archived || final.CompactionJobID != id || final.Recovery != domain.NeedsRecovery {
					t.Fatal("Stop/Archive released claimed compaction ownership")
				}
			} else if final.Archive != domain.Archived || final.CompactionJobID != "" || final.Recovery != domain.NoRecovery {
				t.Fatal("undispatched compaction wedged Stop/Archive")
			}
		})
	}
}

func TestCompactionSessionDeletionRetainsOriginalActionOwnership(t *testing.T) {
	ctx := context.Background()
	f, pf := publicCompactionFixture(t)
	client := sessionClient(f.accountFixture)
	before := f.refresh(t)
	accepted, err := client.CompactSession(ctx, ownerRequest(f.identity, &pb.CompactSessionRequest{Mutation: acctMutation(resourceForTest(before), domain.NewID())}))
	if err != nil {
		t.Fatal(err)
	}
	id := domain.ID(accepted.Msg.Job.Id)
	var input domain.SessionCompactionInput
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.claim-compaction-for-deletion", id, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.JobKind, id)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](r)
		if err != nil || domain.Decode(job.Input, &input) != nil {
			return nil, domain.CompactionUncertain()
		}
		job.State, job.InstanceID, job.AssignedDeviceID = domain.JobClaimed, pf.instance, pf.device
		return tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job)
	})
	if err != nil {
		t.Fatal(err)
	}
	current := f.refresh(t)
	request := &pb.DeleteSessionRequest{Mutation: acctMutation(resourceForTest(current), domain.NewID())}
	deleted, err := client.DeleteSession(ctx, ownerRequest(f.identity, request))
	if err != nil || deleted.Msg.Job.State == pb.SessionDeletionState_SESSION_DELETION_STATE_SUCCEEDED {
		t.Fatal("deletion must await original Worker cleanup", err)
	}
	intent, err := f.service.Store.GetSessionDeletion(ctx, before.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, worker := range intent.Workers {
		if worker.Work.Validate() != nil {
			t.Fatal("invalid deletion ownership plan")
		}
		for _, copy := range worker.Work.Copies {
			if copy.JobID == id {
				found = copy.Type == domain.CompactSessionJob && copy.ActionID == input.ActionID && copy.ExecutionID == "" && worker.Work.DeviceID == pf.device && copy.InstanceID == pf.instance
			}
		}
	}
	if !found {
		t.Fatal("deletion omitted the original action runtime and checkpoint")
	}
	err = f.service.Store.Read(ctx, func(tx *store.Tx) error {
		canceled, err := tx.JobCancellationRequested(id)
		if err == nil && !canceled {
			t.Error("deletion retained live compaction authority")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CompactSession(ctx, ownerRequest(f.identity, &pb.CompactSessionRequest{Mutation: acctMutation(resourceForTest(f.refresh(t)), domain.NewID())})); err == nil {
		t.Fatal("deletion admitted another compaction action")
	}
}

func TestCompactionReceiptIsBoundToOriginalActor(t *testing.T) {
	ctx := context.Background()
	f, _ := publicCompactionFixture(t)
	devices := delidevv1connect.NewDeviceServiceClient(http.DefaultClient, f.endpoint.URL)
	code, token := randomCode(), randomCode()
	codeDigest, tokenDigest := sha256.Sum256([]byte(code)), sha256.Sum256([]byte(token))
	grant, err := devices.CreatePairing(ctx, ownerRequest(f.identity, &pb.CreatePairingRequest{RequestId: string(domain.NewID()), Name: "compaction client", Type: pb.DeviceType_DEVICE_TYPE_CLIENT, CodeDigest: codeDigest[:]}))
	if err != nil {
		t.Fatal(err)
	}
	pairedReply, err := devices.PairDevice(ctx, connect.NewRequest(&pb.PairDeviceRequest{RequestId: string(domain.NewID()), PairingId: grant.Msg.Pairing.Id, Code: code, DeviceId: string(domain.NewID()), CredentialDigest: tokenDigest[:]}))
	if err != nil {
		t.Fatal(err)
	}
	paired, device := security.Identity{Token: token}, pairedReply.Msg.Device
	client := sessionClient(f.accountFixture)
	before := f.refresh(t)
	request := &pb.CompactSessionRequest{Mutation: acctMutation(resourceForTest(before), domain.NewID())}
	accepted, err := client.CompactSession(ctx, ownerRequest(f.identity, request))
	if err != nil {
		t.Fatal(err)
	}
	held := f.refresh(t)
	if _, err := client.CompactSession(ctx, ownerRequest(paired, request)); err != nil {
		t.Fatal("another actor replayed the original compaction receipt", err)
	}
	replay, err := client.CompactSession(ctx, ownerRequest(f.identity, request))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Job.Id != accepted.Msg.Job.Id || f.refresh(t).Revision != held.Revision {
		t.Fatal("original actor replay changed the accepted action", err)
	}
	if _, err := devices.RevokeDevice(ctx, ownerRequest(f.identity, &pb.RevokeDeviceRequest{Mutation: acctMutation(device, domain.NewID())})); err != nil {
		t.Fatal(err)
	}
	stale := domain.WithPrincipal(ctx, domain.Principal{Type: domain.ClientDevice, DeviceID: domain.ID(device.Id)})
	if _, err := f.service.CompactSession(stale, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("revoked actor reached receipt replay", err)
	}
}

func TestCompactionRejectsFailedConversationEligibleForExplicitResume(t *testing.T) {
	f, _ := publicCompactionOutcomeFixture(t, domain.ExecutionFailed)
	ctx := context.Background()
	before := f.refresh(t)
	prior, err := store.Decode[domain.Session](before)
	if err != nil {
		t.Fatal(err)
	}
	if prior.Outcome != domain.ExecutionFailed || prior.Recovery != domain.NoRecovery || !prior.Execution.CleanupVerified || !prior.Execution.ClaudeContinuationBoundary(prior.Execution.InputID) {
		t.Fatal("fixture did not retain a verified failed Resume boundary")
	}
	client := sessionClient(f.accountFixture)
	observation, err := client.GetSessionContext(ctx, ownerRequest(f.identity, &pb.GetSessionContextRequest{SessionId: string(before.ID)}))
	if err != nil || len(observation.Msg.Capabilities) != 1 {
		t.Fatal("failed conversation gained manual compaction capability", err)
	}
	_, err = client.CompactSession(ctx, ownerRequest(f.identity, &pb.CompactSessionRequest{Mutation: acctMutation(resourceForTest(before), domain.NewID())}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("failed Resume eligibility authorized manual compaction", err)
	}
	after := f.refresh(t)
	state, err := store.Decode[domain.Session](after)
	if err != nil || after.Revision != before.Revision || !reflect.DeepEqual(state, prior) {
		t.Fatal("rejected compaction changed the failed predecessor", err)
	}
}

func TestCodexCompactionRejectsIneligibleBoundaryBeforeMutation(t *testing.T) {
	testCompactionRejectsIneligibleBoundary(t, domain.Codex)
}
func TestOpenCodeCompactionRejectsIneligibleBoundaryBeforeMutation(t *testing.T) {
	testCompactionRejectsIneligibleBoundary(t, domain.OpenCode)
}
func testCompactionRejectsIneligibleBoundary(t *testing.T, harness domain.Harness) {
	for _, scenario := range []string{"missing-worker-profile", "paused", "queued-input", "pending-question", "pending-approval", "unfinished-observation", "child-history", "unconfirmed-response"} {
		t.Run(scenario, func(t *testing.T) {
			f := newContinuationFixtureProfile(t, domain.ExecutionSucceeded, harness)
			f.workerStream.Close()
			if _, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.compaction-rejection", scenario, func(tx *store.Tx) (any, error) {
				r, session, err := sessionRecord(tx, domain.ID(f.change.Session.Id))
				if err != nil {
					return nil, err
				}
				switch scenario {
				case "paused":
					session.Dispatch = domain.DispatchPaused
				case "queued-input":
					session.PendingInputs = 1
					session.PendingInputBytes = 1
				case "pending-question":
					session.Execution.Waiting.UserInput = true
				case "pending-approval":
					session.Execution.Waiting.Approval = true
				case "unfinished-observation":
					session.Execution.NativeCompactions = domain.NativeCompactionState{"original-context": domain.NativeCompactionStarted}
				case "unconfirmed-response":
					session.Execution.UnconfirmedResponses = 1
				case "child-history":
					session.Execution.Subagents = domain.SubagentState{"original-child": {}}
				}
				if scenario != "missing-worker-profile" {
					mr, m, err := activeMachine(tx, f.selection.MachineID)
					if err != nil {
						return nil, err
					}
					nativeCapability := domain.CodexSessionCompactionV1
					if harness == domain.OpenCode {
						nativeCapability = domain.OpenCodeSessionCompactionV1
					}
					m.WorkerCapabilities = append(m.WorkerCapabilities, domain.NativeSessionCompactionV1, nativeCapability)
					if _, err := tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", m); err != nil {
						return nil, err
					}
				}
				return tx.Put(domain.SessionKind, r.ID, r.Revision, r.ID, r.ProjectID, session)
			}); err != nil {
				t.Fatal(err)
			}
			before := f.refresh(t)
			_, err := sessionClient(f.accountFixture).CompactSession(context.Background(), ownerRequest(f.identity, &pb.CompactSessionRequest{Mutation: acctMutation(resourceForTest(before), domain.NewID())}))
			if err == nil {
				t.Fatal("ineligible Codex boundary accepted")
			}
			if after := f.refresh(t); after.Revision != before.Revision {
				t.Fatal("rejected action changed original state")
			}
		})
	}
}
