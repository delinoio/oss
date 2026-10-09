// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/proto"
	"reflect"
	"strings"
	"testing"
)

func publicRevertFixture(t *testing.T, outcome domain.ExecutionOutcome) (*continuationFixture, domain.ID) {
	f := newContinuationFixture(t, outcome)
	f.workerStream.Close()
	message := domain.NewID()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.revert-original-turn", nil, func(tx *store.Tx) (any, error) {
		r, m, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.CodexSessionRevertV1)
		if _, err = tx.Put(r.Kind, r.ID, r.Revision, "", "", m); err != nil {
			return nil, err
		}
		return tx.Put(domain.MessageKind, message, 0, f.input.SessionID, "", domain.ExecutionMessage{ExecutionID: f.input.ExecutionID, InputID: f.input.InputID, Role: domain.UserMessage, State: domain.MessageComplete, NativeThreadID: string(f.thread), NativeTurnID: string(f.turn), NativeID: string(domain.NewID()), Text: f.input.Input.Prompt, FirstSequence: 2, LastSequence: 2})
	})
	if err != nil {
		t.Fatal(err)
	}
	return f, message
}
func TestRevertPublicReceiptExactTargetHistoryAndContextRevision(t *testing.T) {
	for _, outcome := range []domain.ExecutionOutcome{domain.ExecutionSucceeded, domain.ExecutionFailed, domain.ExecutionStopped} {
		t.Run(string(outcome), func(t *testing.T) {
			f, message := publicRevertFixture(t, outcome)
			ctx := context.Background()
			before := f.refresh(t)
			prior, _ := store.Decode[domain.Session](before)
			request := &pb.RevertSessionRequest{Mutation: acctMutation(resourceForTest(before), domain.NewID()), MessageId: string(message), BeforeTurnId: string(f.turn), ExpectedContextRevision: 0}
			client := sessionClient(f.accountFixture)
			accepted, err := client.RevertSession(ctx, ownerRequest(f.identity, request))
			if err != nil {
				t.Fatal(err)
			}
			replay, err := client.RevertSession(ctx, ownerRequest(f.identity, request))
			if err != nil || !replay.Msg.Replayed || replay.Msg.Job.Id != accepted.Msg.Job.Id {
				t.Fatal("original mutation receipt changed", err)
			}
			changed := proto.Clone(request).(*pb.RevertSessionRequest)
			changed.BeforeTurnId = string(domain.NewID())
			if _, err = client.RevertSession(ctx, ownerRequest(f.identity, changed)); err == nil {
				t.Fatal("receipt replay changed native target")
			}
			var job domain.Job
			var input domain.SessionCompactionInput
			if domain.Decode(accepted.Msg.Job.DocumentJson, &job) != nil || domain.DecodeCompactionInput(job.Input, &input) != nil || input.Validate() != nil || input.Version != 4 || !reflect.DeepEqual(input.Assignment, f.input) || input.Revert.Prompt.Prompt != f.input.Input.Prompt {
				t.Fatal("Revert replaced original execution or prompt")
			}
			result := domain.SessionCompactionResult{Version: 4, Harness: domain.Codex, ActionID: input.ActionID, ExecutionID: input.Assignment.ExecutionID, Outcome: domain.CompactionSucceeded, CleanupVerified: true, Checkpoint: domain.SessionCompactionRef{Revert: true, ContextRevision: 1, JobID: domain.ID(accepted.Msg.Job.Id), ActionID: input.ActionID, ExecutionID: input.Assignment.ExecutionID, CheckpointDigest: strings.Repeat("a", 64), NativeDigest: strings.Repeat("b", 64)}, Revert: &domain.SessionRevertResult{Target: *input.Revert, NativeThreadID: input.Completion.NativeThreadID, RetainedTurnIDs: []domain.NativeIdentity{}, HistoryDigest: strings.Repeat("c", 64), ContextRevision: 1}}
			raw, _ := json.Marshal(result)
			_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.revert-result", nil, func(tx *store.Tx) (any, error) {
				r, err := tx.Get(domain.JobKind, domain.ID(accepted.Msg.Job.Id))
				if err != nil {
					return nil, err
				}
				return finishSessionCompaction(tx, r, job, r.Revision, raw, nil)
			})
			if err != nil {
				t.Fatal(err)
			}
			after, _ := store.Decode[domain.Session](f.refresh(t))
			if after.ContextRevision != 1 || after.Revert == nil || after.CompactionJobID != "" || after.Dispatch != domain.DispatchPaused || after.Execution.ExecutionID != prior.Execution.ExecutionID || after.Outcome != prior.Outcome {
				t.Fatal("history/outcome/input ownership changed")
			}
			oldMessage, err := f.service.Store.Get(ctx, domain.MessageKind, message)
			if err != nil || oldMessage.Revision != 1 {
				t.Fatal("prior transcript rewritten", err)
			}
			fresh := proto.Clone(request).(*pb.RevertSessionRequest)
			fresh.Mutation = acctMutation(resourceForTest(f.refresh(t)), domain.NewID())
			fresh.ExpectedContextRevision = 1
			if _, err := client.RevertSession(ctx, ownerRequest(f.identity, fresh)); err == nil {
				t.Fatal("removed target was accepted again")
			}
			queued := f.enqueue(t, "edited prompt", domain.ExecuteMode)
			f.control(t, pb.SessionAction_SESSION_ACTION_RESUME)

			jobs, err := f.service.Store.List(ctx, store.Filter{Kind: domain.JobKind, SessionID: f.input.SessionID, Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			found := 0
			for _, row := range jobs {
				var next domain.Job
				var nextInput domain.ExecutionJobInput
				if domain.Decode(row.Data, &next) == nil && next.Type == domain.ExecuteSessionJob && domain.Decode(next.Input, &nextInput) == nil && nextInput.InputID == domain.ID(queued.Id) {
					found++
					if nextInput.ContextRevision != 1 || nextInput.Continuation == nil || nextInput.Continuation.Compaction == nil || nextInput.Continuation.Compaction.ActionID != input.ActionID || nextInput.Input.Prompt != "edited prompt" {
						t.Fatal("edited input lost original Revert checkpoint")
					}
				}
			}
			if found != 1 {
				t.Fatal("edited input was not assigned exactly once", found)
			}
		})
	}
}
func TestRevertPublicRejectsActiveSteerForeignPromptAndMissingCapability(t *testing.T) {
	for _, scenario := range []string{"steer", "foreign-prompt", "missing-capability", "stale-context", "inherited"} {
		t.Run(scenario, func(t *testing.T) {
			f, message := publicRevertFixture(t, domain.ExecutionSucceeded)
			ctx := context.Background()
			_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.revert-rejection", nil, func(tx *store.Tx) (any, error) {
				switch scenario {
				case "steer":
					r, s, e := sessionRecord(tx, f.input.SessionID)
					if e != nil {
						return nil, e
					}
					s.PendingSteerID = domain.NewID()
					return tx.Put(r.Kind, r.ID, r.Revision, r.ID, r.ProjectID, s)
				case "missing-capability":
					r, m, e := activeMachine(tx, f.input.MachineID)
					if e != nil {
						return nil, e
					}
					m.WorkerCapabilities = nil
					return tx.Put(r.Kind, r.ID, r.Revision, "", "", m)
				case "foreign-prompt", "inherited":
					r, e := tx.Get(domain.MessageKind, message)
					if e != nil {
						return nil, e
					}
					m, e := store.Decode[domain.ExecutionMessage](r)
					if e != nil {
						return nil, e
					}
					if scenario == "foreign-prompt" {
						m.Text = "changed prompt"
					} else {
						m.Role = domain.AssistantMessage
					}
					return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, m)
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			request := &pb.RevertSessionRequest{Mutation: acctMutation(resourceForTest(f.refresh(t)), domain.NewID()), MessageId: string(message), BeforeTurnId: string(f.turn)}
			if scenario == "stale-context" {
				request.ExpectedContextRevision = 1
			}
			if _, err := sessionClient(f.accountFixture).RevertSession(ctx, ownerRequest(f.identity, request)); err == nil {
				t.Fatal("unowned revert admitted")
			}
		})
	}
}

func TestRevertOriginalRecoveryPublishesOnlyExactFrozenAction(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "original", true: "changed-target"}[changed], func(t *testing.T) {
			f, message := publicRevertFixture(t, domain.ExecutionSucceeded)
			ctx := context.Background()
			client := sessionClient(f.accountFixture)
			accepted, err := client.RevertSession(ctx, ownerRequest(f.identity, &pb.RevertSessionRequest{Mutation: acctMutation(resourceForTest(f.refresh(t)), domain.NewID()), MessageId: string(message), BeforeTurnId: string(f.turn)}))
			if err != nil {
				t.Fatal(err)
			}
			var input domain.SessionCompactionInput
			var original domain.Job
			domain.Decode(accepted.Msg.Job.DocumentJson, &original)
			domain.DecodeCompactionInput(original.Input, &input)
			_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.revert-uncertain-original", nil, func(tx *store.Tx) (any, error) {
				row, e := tx.Get(domain.JobKind, domain.ID(accepted.Msg.Job.Id))
				if e != nil {
					return nil, e
				}
				original.State = domain.JobClaimed
				original.InstanceID = domain.ID(f.workerInstance)
				original.AssignedDeviceID = f.workerDevice
				if _, e = tx.PutJob(row.ID, row.Revision, row.SessionID, row.ProjectID, original); e != nil {
					return nil, e
				}
				row, e = tx.Get(domain.JobKind, row.ID)
				if e != nil {
					return nil, e
				}
				return finishSessionCompaction(tx, row, original, row.Revision, nil, domain.CompactionUncertain())
			})
			if err != nil {
				t.Fatal(err)
			}
			recovered, err := client.RecoverSessionExecution(ctx, ownerRequest(f.identity, &pb.RecoverSessionExecutionRequest{Mutation: acctMutation(resourceForTest(f.refresh(t)), domain.NewID()), ExpectedExecutionId: string(f.input.ExecutionID)}))
			if err != nil {
				t.Fatal(err)
			}
			state, _ := store.Decode[domain.Session](store.Record{Data: recovered.Msg.Change.Session.DocumentJson})
			row, err := f.service.Store.Get(ctx, domain.JobKind, state.ExecutionRecoveryJobID)
			if err != nil {
				t.Fatal(err)
			}
			job, _ := store.Decode[domain.Job](row)
			var request domain.ExecutionRecoveryRequest
			if domain.Decode(job.Input, &request) != nil || request.Validate() != nil || request.Revert == nil || request.JobID != domain.ID(accepted.Msg.Job.Id) {
				t.Fatal("recovery changed original identity")
			}
			result := domain.SessionCompactionResult{Version: 4, Harness: domain.Codex, ActionID: input.ActionID, ExecutionID: input.Assignment.ExecutionID, Outcome: domain.CompactionSucceeded, CleanupVerified: true, Checkpoint: domain.SessionCompactionRef{Revert: true, ContextRevision: 1, JobID: request.JobID, ActionID: input.ActionID, ExecutionID: input.Assignment.ExecutionID, CheckpointDigest: strings.Repeat("a", 64), NativeDigest: strings.Repeat("b", 64)}, Revert: &domain.SessionRevertResult{Target: *input.Revert, NativeThreadID: input.Completion.NativeThreadID, RetainedTurnIDs: []domain.NativeIdentity{}, HistoryDigest: strings.Repeat("c", 64), ContextRevision: 1}}
			if changed {
				result.Revert.Target.MessageID = domain.NewID()
			}
			evidence := domain.ExecutionRecoveryEvidence{Version: 2, JobID: request.JobID, ReportID: domain.NewID(), Revert: &result}
			raw, _ := json.Marshal(evidence)
			_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.revert-observed-recovery", nil, func(tx *store.Tx) (any, error) {
				if e := validateExecutionRecoveryResult(tx, row, job, raw); e != nil {
					return nil, e
				}
				job.State = domain.JobSucceeded
				job.Output = raw
				return nil, finishExecutionRecovery(tx, row, job)
			})
			if changed {
				if err == nil {
					t.Fatal("recovery adopted changed target")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			latest, _ := store.Decode[domain.Session](f.refresh(t))
			if latest.Recovery != domain.NoRecovery || latest.ContextRevision != 1 || latest.CompactionJobID != "" || latest.ExecutionRecoveryJobID != "" || latest.Execution.ExecutionID != f.input.ExecutionID || latest.Dispatch != domain.DispatchPaused {
				t.Fatal("recovery changed execution or released without original checkpoint")
			}
		})
	}
}
