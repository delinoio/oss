// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"strings"
	"testing"
	"time"
)

func publishedOpenCodeRecoveryFork(t *testing.T) (*continuationFixture, *pb.Resource, domain.ForkJobInput) {
	t.Helper()
	f := newContinuationFixtureProfile(t, domain.ExecutionSucceeded, domain.OpenCode)
	ctx := context.Background()
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.opencode-fork-profile", nil, func(tx *store.Tx) (any, error) {
		row, err := tx.Get(domain.MachineKind, domain.ID(f.machine.Id))
		if err != nil {
			return nil, err
		}
		machine, err := store.Decode[domain.Machine](row)
		if err != nil {
			return nil, err
		}
		machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.OpenCodeGeneralChatForkV1)
		if _, err = tx.Put(row.Kind, row.ID, row.Revision, row.SessionID, row.ProjectID, machine); err != nil {
			return nil, err
		}
		return tx.Put(domain.MessageKind, domain.NewID(), 0, f.input.SessionID, "", domain.ExecutionMessage{ExecutionID: f.input.ExecutionID, NativeThreadID: string(f.thread), NativeTurnID: string(f.turn), NativeID: "prt_01960dcbe1fcabcdefghijklmn", NativeParentID: "msg_01960dcbe1fcABCDEFGHIJKLMN", Role: domain.AssistantMessage, Text: "Original completed assistant.", State: domain.MessageComplete, FirstSequence: 5, LastSequence: 6})
	})
	if err != nil {
		t.Fatal(err)
	}
	row := f.refresh(t)
	request := &pb.ForkSessionRequest{Mutation: acctMutation(resourceForTest(row), domain.NewID()), ExpectedTurnId: string(f.turn), Name: "Independent recovery child", Workspace: pb.ForkWorkspace_FORK_WORKSPACE_GENERAL_CHAT}
	accepted, err := sessionClient(f.accountFixture).ForkSession(ctx, ownerRequest(f.identity, request))
	if err != nil {
		t.Fatal(err)
	}
	job, input := forkClaimFixture(t, f, accepted.Msg.Job.Id)
	result := forkResultFixture(t, input)
	result.Version = 2
	result.NativeThreadID = "ses_01960dcbe1fdabcdefghijklmn"
	result.NativeTurnID = "msg_01960dcbe1fdABCDEFGHIJKLMN"
	result.OpenCodeMappings = []domain.OpenCodeForkMessageMapping{
		{Source: input.Completion.NativeTurnID, Child: result.NativeTurnID, Parts: []domain.OpenCodeForkPartMapping{{Source: "prt_01960dcbe1fbabcdefghijklmn", Child: "prt_01960dcbe1fdabcdefghijklmn"}}},
		{Source: "msg_01960dcbe1fcABCDEFGHIJKLMN", Child: "msg_01960dcbe1feABCDEFGHIJKLMN", Parts: []domain.OpenCodeForkPartMapping{{Source: "prt_01960dcbe1fcabcdefghijklmn", Child: "prt_01960dcbe1feabcdefghijklmn"}}},
	}
	var preparation workspace.PrepareRequest
	var manifest workspace.Manifest
	_ = domain.Decode(result.Preparation, &preparation)
	_ = domain.Decode(result.Manifest, &manifest)
	preparation.ForkProfile = workspace.OpenCodeGeneralChatForkV1
	result.Preparation, _ = json.Marshal(preparation)
	manifest.InputDigest = forkInputDigest(result.Preparation)
	result.Manifest, _ = json.Marshal(manifest)
	raw, _ := json.Marshal(result)
	if _, err = f.workerClient.ReportWork(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: raw})); err != nil {
		t.Fatal(err)
	}
	final, err := sessionClient(f.accountFixture).GetSessionFork(ctx, ownerRequest(f.identity, &pb.GetSessionForkRequest{JobId: job.Id}))
	if err != nil || final.Msg.Session == nil {
		t.Fatal("fork publication", err)
	}
	var child domain.Session
	_ = domain.Decode(final.Msg.Session.DocumentJson, &child)
	if child.Fork.OpenCodeCreationRequestID != input.OpenCode.Fork {
		t.Fatal("published child lost original creation marker")
	}
	return f, final.Msg.Session, input
}

func finishOpenCodeRecoveryTurn(t *testing.T, f *continuationFixture, report bool) {
	t.Helper()
	ctx := context.Background()
	f.publish(t, domain.ExecutionThreadBound, 1, "")
	f.publish(t, domain.ExecutionInputAccepted, 2, "")
	pf := &publicationFixture{authorityFixture: &authorityFixture{service: f.service, client: f.workerClient, workerToken: f.workerIdentity.Token, job: domain.ID(f.job.Id), device: f.workerDevice, instance: domain.ID(f.workerInstance), input: f.input}, revision: f.job.Revision, thread: f.thread, turn: f.turn}
	u := originalOpenCodeUsage()
	u.Source, u.NativeID = domain.OpenCodeMessageUsage, u.NativeParentID
	e := pf.event(domain.ExecutionOpenCodeUsageObserved, 3)
	e.ObservationID, e.OpenCodeUsage = domain.NewID(), &u
	pf.publish(t, e)
	message := domain.ExecutionMessageUpdate{ID: domain.NewID(), NativeID: "prt_01960dcbe1fbabcdefghijklmn", NativeParentID: string(f.turn), InputID: f.input.InputID, Role: domain.UserMessage, Text: f.input.Input.Prompt}
	for n, kind := range []domain.ExecutionEventKind{domain.ExecutionMessageStarted, domain.ExecutionMessageCompleted} {
		e = pf.event(kind, uint64(4+n))
		e.Message = &message
		pf.publish(t, e)
	}
	f.publish(t, domain.ExecutionTurnFinished, 6, domain.ExecutionSucceeded)
	if report {
		completion := domain.ExecutionCompletion{Version: 2, ExecutionID: f.input.ExecutionID, InputID: f.input.InputID, NativeThreadID: domain.NativeIdentity(f.thread), NativeTurnID: domain.NativeIdentity(f.turn), LastSequence: 6, Outcome: domain.ExecutionSucceeded, CleanupVerified: true, NativeCheckpointDigest: strings.Repeat("ab", 32)}
		raw, _ := json.Marshal(completion)
		if _, err := f.workerClient.ReportWork(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(f.job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: raw})); err != nil {
			t.Fatal(err)
		}
	}
}

func deleteOpenCodeForkParent(t *testing.T, f *continuationFixture) {
	t.Helper()
	ctx := context.Background()
	source := f.refresh(t)
	deletion, err := sessionClient(f.accountFixture).DeleteSession(ctx, ownerRequest(f.identity, &pb.DeleteSessionRequest{Mutation: acctMutation(resourceForTest(source), domain.NewID())}))
	if err != nil {
		t.Fatal(err)
	}
	work, err := f.workerClient.ListSessionDeletionWork(ctx, ownerRequest(f.workerIdentity, &pb.ListSessionDeletionWorkRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance}))
	if err != nil || len(work.Msg.WorkJson) != 1 {
		t.Fatal(err)
	}
	var w domain.SessionDeletionWork
	_ = domain.Decode(work.Msg.WorkJson[0], &w)
	if _, err = f.workerClient.ReportSessionDeletion(ctx, ownerRequest(f.workerIdentity, &pb.ReportSessionDeletionRequest{RequestId: string(domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, SessionId: string(source.ID), DeletionId: deletion.Msg.Job.Id, WorkDigest: w.Digest()})); err != nil {
		t.Fatal(err)
	}
	owner := domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice})
	if _, err = f.service.Store.PurgeDeletedSession(owner, source.ID); err != nil {
		t.Fatal(err)
	}
}

func TestOpenCodeForkChildLostReportKeepsIndependentCreation(t *testing.T) {
	for _, turns := range []int{1, 2, 3} {
		for _, deleteParent := range []bool{false, true} {
			t.Run(fmt.Sprintf("turn-%d-parent-deleted-%t", turns, deleteParent), func(t *testing.T) {
				f, child, seed := publishedOpenCodeRecoveryFork(t)
				if deleteParent {
					deleteOpenCodeForkParent(t, f)
				}
				f.change.Session = child
				for n := 1; n <= turns; n++ {
					f.enqueue(t, "Ordinary child input.", domain.ExecuteMode)
					if n == 1 {
						f.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
					} else {
						if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err != nil {
							t.Fatal(err)
						}
					}
					f.claim(t)
					f.thread = "ses_01960dcbe1fdabcdefghijklmn"
					f.turn = domain.ID(fmt.Sprintf("msg_01960dcbe1f%dABCDEFGHIJKLMN", n))
					f.grant(t)
					finishOpenCodeRecoveryTurn(t, f, n < turns)
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				if err := f.workerStream.Close(); err != nil {
					t.Fatal(err)
				}
				if _, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.fork-worker-stale", nil, func(tx *store.Tx) (any, error) {
					return nil, tx.SetWorkerInstance(domain.ID(f.machine.Id), domain.ID(f.workerInstance), time.Now().UTC().Add(-2*time.Minute))
				}); err != nil {
					t.Fatal(err)
				}
				f.workerInstance = string(domain.NewID())
				if _, err := f.workerClient.AttachWorker(ctx, ownerRequest(f.workerIdentity, &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, Version: rpc.Version})); err != nil {
					t.Fatal(err)
				}
				row := f.refresh(t)
				req := &pb.RecoverSessionExecutionRequest{Mutation: acctMutation(resourceForTest(row), domain.NewID()), ExpectedExecutionId: string(f.input.ExecutionID)}
				response, err := sessionClient(f.accountFixture).RecoverSessionExecution(ctx, ownerRequest(f.identity, req))
				if err != nil || response.Msg.Change.ExecutionRecoveryJob == nil {
					t.Fatal("lost child report recovery", err)
				}
				var job domain.Job
				var recovery domain.ExecutionRecoveryRequest
				_ = domain.Decode(response.Msg.Change.ExecutionRecoveryJob.DocumentJson, &job)
				_ = domain.Decode(job.Input, &recovery)
				if recovery.Validate() != nil || recovery.OpenCode == nil || recovery.OpenCode.ClaimVersion != 2 || recovery.OpenCode.CreationRequestID != seed.OpenCode.Fork || recovery.OpenCode.BindingRequestID != f.input.ThreadRequestID || recovery.OpenCode.InputRequestID != f.input.TurnRequestID || recovery.HistoryExecutionID != seed.RuntimeID {
					t.Fatal("recovery substituted original/current identities")
				}
				replay, err := sessionClient(f.accountFixture).RecoverSessionExecution(ctx, ownerRequest(f.identity, req))
				if err != nil || !replay.Msg.Change.Replayed || replay.Msg.Change.ExecutionRecoveryJob.Id != response.Msg.Change.ExecutionRecoveryJob.Id {
					t.Fatal("recovery replay replaced job", err)
				}
				stream, err := f.workerClient.WatchWork(ctx, ownerRequest(f.workerIdentity, &pb.WatchWorkRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance}))
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				var claimed *pb.Resource
				for stream.Receive() {
					if stream.Msg().Job != nil {
						claimed = stream.Msg().Job
						break
					}
				}
				if claimed == nil || claimed.Id != response.Msg.Change.ExecutionRecoveryJob.Id {
					t.Fatal("recovery claim", stream.Err())
				}
				completion := recovery.Completion
				completion.Version, completion.NativeCheckpointDigest = 2, strings.Repeat("ab", 32)
				evidence, _ := json.Marshal(domain.ExecutionRecoveryEvidence{Version: 1, JobID: recovery.JobID, ReportID: domain.NewID(), Completion: completion})
				if _, err = f.workerClient.ReportWork(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(claimed, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: evidence})); err != nil {
					t.Fatal("original completion reconciliation", err)
				}
				settled, err := store.Decode[domain.Session](f.refresh(t))
				if err != nil || settled.Recovery != domain.NoRecovery || settled.Dispatch != domain.DispatchPaused || settled.Outcome != domain.ExecutionSucceeded || !settled.Execution.CleanupVerified || settled.ActiveExecutionID != "" {
					t.Fatal("recovery lost settled pause", err)
				}
			})
		}
	}
}

func TestOpenCodeForkLegacyCreationRequiresExactOriginalSeed(t *testing.T) {
	for _, change := range []string{"valid", "missing-job", "digest", "runtime", "child", "selection", "device", "marker", "valid-foreign-marker", "checkpoint", "output"} {
		t.Run(change, func(t *testing.T) {
			f, child, seed := publishedOpenCodeRecoveryFork(t)
			f.change.Session = child
			f.enqueue(t, "Original child input.", domain.ExecuteMode)
			f.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
			f.claim(t)
			f.thread = "ses_01960dcbe1fdabcdefghijklmn"
			f.turn = "msg_01960dcbe1ffABCDEFGHIJKLMN"
			f.publish(t, domain.ExecutionThreadBound, 1, "")
			f.publish(t, domain.ExecutionInputAccepted, 2, "")
			row := f.refresh(t)
			session, err := store.Decode[domain.Session](row)
			if err != nil {
				t.Fatal(err)
			}
			session.Fork.OpenCodeCreationRequestID = ""
			session.Fork.OpenCodeCreationProof = nil
			_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.legacy-fork-proof", nil, func(tx *store.Tx) (any, error) {
				original, err := tx.Get(domain.JobKind, session.Fork.JobID)
				if err != nil {
					return nil, err
				}
				job, err := store.Decode[domain.Job](original)
				if err != nil {
					return nil, err
				}
				switch change {
				case "missing-job":
					if err = tx.Delete(original.Kind, original.ID, original.Revision); err != nil {
						return nil, err
					}
				case "digest":
					session.Fork.JobInputDigest = strings.Repeat("cd", 32)
				case "runtime":
					session.Fork.RuntimeID = domain.NewID()
				case "child":
					var old domain.ForkJobInput
					_ = domain.Decode(job.Input, &old)
					old.ChildSessionID = domain.NewID()
					job.Input, _ = json.Marshal(old)
					session.Fork.JobInputDigest = forkInputDigest(job.Input)
					if _, err = tx.Put(original.Kind, original.ID, original.Revision, original.SessionID, original.ProjectID, job); err != nil {
						return nil, err
					}
				case "selection":
					session.Fork.Snapshot.ConnectionID = domain.NewID()
				case "device":
					session.Fork.WorkerDeviceID = domain.NewID()
				case "checkpoint":
					session.Fork.CheckpointDigest = strings.Repeat("ef", 32)
				case "output":
					job.Output = json.RawMessage(`{}`)
					if _, err = tx.Put(original.Kind, original.ID, original.Revision, original.SessionID, original.ProjectID, job); err != nil {
						return nil, err
					}
				case "valid-foreign-marker":
					session.Fork.OpenCodeCreationRequestID = domain.NewID()
				case "marker":
					session.Fork.OpenCodeCreationRequestID = "invalid-original-marker"
				}
				creation, proofErr := openCodeForkRecoveryCreation(tx, row, session, f.input, seed.RuntimeID, f.workerDevice)
				if change == "valid" {
					if proofErr != nil || creation != seed.OpenCode.Fork {
						t.Fatal("exact legacy original seed not resolved", proofErr)
					}
				} else if proofErr == nil || creation != "" {
					t.Fatal("foreign or absent legacy ownership adopted")
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeRecoveryClaimVersionDescribesOperation(t *testing.T) {
	for _, version := range []uint32{1, 2, 3, 4} {
		input := domain.ExecutionJobInput{Version: version}
		if nativeRecoveryClaimVersion(input) != 1 {
			t.Fatal("first claim version changed")
		}
		input.Continuation = &domain.ExecutionContinuation{}
		if nativeRecoveryClaimVersion(input) != 2 {
			t.Fatal("continuation assignment version became native claim")
		}
		input.Continuation = nil
		input.Fork = &domain.ForkExecution{}
		if nativeRecoveryClaimVersion(input) != 2 {
			t.Fatal("Fork assignment version became native claim")
		}
	}
}

func TestOpenCodeRootRecoveryKeepsOriginalCreationMarker(t *testing.T) {
	for _, turns := range []int{1, 2} {
		t.Run(fmt.Sprint(turns), func(t *testing.T) {
			f := newContinuationFixtureProfile(t, domain.ExecutionSucceeded, domain.OpenCode)
			creation, history := f.input.ThreadRequestID, f.input.ExecutionID
			if turns == 2 {
				f.enqueue(t, "Ordinary root continuation.", domain.ExecuteMode)
				if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err != nil {
					t.Fatal(err)
				}
				f.claim(t)
				f.turn = "msg_01960dcbe1ffABCDEFGHIJKLMN"
				f.grant(t)
				f.completeOpenCode(t, domain.ExecutionSucceeded)
			}
			row := f.refresh(t)
			session, err := store.Decode[domain.Session](row)
			if err != nil {
				t.Fatal(err)
			}
			session.Recovery = domain.NeedsRecovery
			if err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
				request, err := executionRecoveryRequest(tx, f.service.Identity.ServerID, row, session)
				if err != nil {
					return err
				}
				claimVersion := uint32(1)
				if turns == 2 {
					claimVersion = 2
				}
				if request.OpenCode == nil || request.OpenCode.CreationRequestID != creation || request.OpenCode.ClaimVersion != claimVersion || request.HistoryExecutionID != history || request.OpenCode.BindingRequestID != f.input.ThreadRequestID || request.OpenCode.InputRequestID != f.input.TurnRequestID {
					t.Fatal("ordinary root recovery changed original/current identity")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOpenCodeForkForeignCreationRejectedBeforeAdmission(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(fmt.Sprintf("parent-deleted-%t", deleted), func(t *testing.T) {
			f, child, _ := publishedOpenCodeRecoveryFork(t)
			if deleted {
				deleteOpenCodeForkParent(t, f)
			}
			f.change.Session = child
			f.enqueue(t, "Original child input.", domain.ExecuteMode)
			f.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
			f.claim(t)
			f.thread, f.turn = "ses_01960dcbe1fdabcdefghijklmn", "msg_01960dcbe1ffABCDEFGHIJKLMN"
			f.grant(t)
			finishOpenCodeRecoveryTurn(t, f, false)
			ctx := context.Background()
			if err := f.workerStream.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.fork-worker-stale", nil, func(tx *store.Tx) (any, error) {
				return nil, tx.SetWorkerInstance(domain.ID(f.machine.Id), domain.ID(f.workerInstance), time.Now().UTC().Add(-2*time.Minute))
			}); err != nil {
				t.Fatal(err)
			}
			f.workerInstance = string(domain.NewID())
			if _, err := f.workerClient.AttachWorker(ctx, ownerRequest(f.workerIdentity, &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, Version: rpc.Version})); err != nil {
				t.Fatal(err)
			}
			row := f.refresh(t)
			session, err := store.Decode[domain.Session](row)
			if err != nil || session.Fork.OpenCodeCreationProof == nil {
				t.Fatal("missing original publication proof", err)
			}
			// Change only the marker; all original publication and current execution
			// ownership remains intact, including the immutable expected marker.
			session.Fork.OpenCodeCreationRequestID = domain.NewID()
			changed, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.foreign-marker", nil, func(tx *store.Tx) (any, error) {
				return tx.Put(row.Kind, row.ID, row.Revision, row.SessionID, row.ProjectID, session)
			})
			if err != nil {
				t.Fatal(err)
			}
			_ = changed
			before, err := f.service.Store.Get(ctx, row.Kind, row.ID)
			if err != nil {
				t.Fatal(err)
			}
			jobsBefore, err := f.service.Store.List(ctx, store.Filter{Kind: domain.JobKind, SessionID: row.ID, Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			_, err = sessionClient(f.accountFixture).RecoverSessionExecution(ctx, ownerRequest(f.identity, &pb.RecoverSessionExecutionRequest{Mutation: acctMutation(resourceForTest(before), domain.NewID()), ExpectedExecutionId: string(f.input.ExecutionID)}))
			if err == nil {
				t.Fatal("valid foreign creation UUID accepted")
			}
			after, err := f.service.Store.Get(ctx, row.Kind, row.ID)
			jobsAfter, listErr := f.service.Store.List(ctx, store.Filter{Kind: domain.JobKind, SessionID: row.ID, Limit: 100})
			if err != nil || listErr != nil || after.Revision != before.Revision || string(after.Data) != string(before.Data) || len(jobsAfter) != len(jobsBefore) {
				t.Fatal("rejected foreign marker admitted recovery work or mutated original session", err, listErr)
			}
		})
	}
}
