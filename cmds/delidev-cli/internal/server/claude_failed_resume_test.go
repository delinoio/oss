package server

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// Use public readiness, dispatch and authenticated publication APIs. The v2
// digest models independent Worker proof; it does not execute an installed CLI.
func TestClaudeFailedResumeClaimsNextFIFOInputOnce(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) {
			ctx := context.Background()
			f := &continuationFixture{firstDispatchFixture: newFirstDispatchFixtureForHarness(t, domain.ClaudeCode, mode)}
			// The full original turn and Resume share this bounded stream, rather
			// than the setup fixture's short account/discovery deadline.
			streamCtx, cancel := context.WithTimeout(ctx, time.Minute)
			t.Cleanup(cancel)
			stream, err := f.workerClient.WatchWork(streamCtx, ownerRequest(f.workerIdentity, &pb.WatchWorkRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance}))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { stream.Close() })
			f.workerStream = stream
			if err := f.service.dispatchExecution(ctx, f.refresh(t)); err != nil {
				t.Fatal(err)
			}
			f.claim(t)
			original := f.input
			event := domain.ExecutionEvent{Version: 1, ExecutionID: original.ExecutionID, NativeThreadID: string(original.SessionID)}
			publish := func(e domain.ExecutionEvent) {
				t.Helper()
				raw, _ := json.Marshal(e)
				_, err := f.workerClient.PublishExecution(ctx, ownerRequest(f.workerIdentity, &pb.PublishExecutionRequest{Mutation: acctMutation(f.job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, EventJson: raw}))
				if err != nil {
					t.Fatal("original publication failed", e.Kind, err)
				}
			}
			permission, _ := original.Configuration.ClaudeAPIInputPermission(mode)
			event.Kind, event.Sequence = domain.ExecutionThreadBound, 1
			event.Observed = &domain.ObservedExecutionSettings{Model: original.Configuration.NativeModel, Permission: domain.PermissionDefault, ClaudePermission: permission}
			publish(event)
			event.Kind, event.Sequence, event.Observed, event.NativeTurnID = domain.ExecutionInputAccepted, 2, nil, string(f.turn)
			publish(event)
			event.Message = &domain.ExecutionMessageUpdate{ID: domain.NewID(), NativeID: string(original.InputID), Role: domain.UserMessage, InputID: original.InputID, Text: original.Input.Prompt}
			for i, kind := range []domain.ExecutionEventKind{domain.ExecutionMessageStarted, domain.ExecutionMessageCompleted} {
				event.Kind, event.Sequence = kind, uint64(i+3)
				publish(event)
			}
			event.Message, event.Kind, event.Sequence = nil, domain.ExecutionTurnFinished, 5
			event = publishClaudeRootContent(t, event, original.Configuration.NativeModel, publish)
			result := string(domain.NewID())
			usage := event
			usage.Kind, usage.ObservationID, usage.ClaudeUsage = domain.ExecutionClaudeUsageObserved, domain.NewID(), &domain.ClaudeUsageObservation{Source: domain.ClaudeInputResultUsage, NativeEventID: result, Result: &domain.ClaudeResultUsage{}}
			publish(usage)
			event.Sequence++
			event.Outcome = domain.ExecutionFailed
			event.ClaudeTerminal = &domain.ClaudeTerminalObservation{InputID: original.InputID, ResultNativeID: result, CommandNativeID: string(domain.NewID()), IdleNativeID: string(domain.NewID()), Kind: domain.ClaudeResultMaxTurns, Reason: domain.ClaudeMaxTurns, Error: true, Command: domain.ClaudeCommandCompleted}
			publish(event)
			completion := domain.ExecutionCompletion{Version: 2, ExecutionID: original.ExecutionID, InputID: original.InputID, NativeThreadID: domain.NativeIdentity(original.SessionID), NativeTurnID: domain.NativeIdentity(f.turn), LastSequence: event.Sequence, Outcome: domain.ExecutionFailed, CleanupVerified: true, NativeCheckpointDigest: strings.Repeat("ab", 32)}
			raw, _ := json.Marshal(completion)
			if _, err := f.workerClient.ReportWork(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(f.job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: raw})); err != nil {
				t.Fatal(err)
			}
			state, err := store.Decode[domain.Session](f.refresh(t))
			if err != nil || state.Outcome != domain.ExecutionFailed || !state.Execution.CleanupVerified || state.Recovery != domain.NoRecovery || state.Dispatch != domain.DispatchPaused || state.NextExecutionIntent != "" {
				t.Fatal("verified failure did not remain paused", err)
			}
			firstSnapshot, _ := json.Marshal(state.InitialExecution)
			second := f.enqueue(t, "Explicitly selected new input", mode)
			f.enqueue(t, "Later FIFO input", mode)
			before := f.refresh(t)
			if err := f.service.dispatchExecution(ctx, before); err == nil {
				t.Fatal("failure automatically dispatched queued input")
			}
			if inputBody(t, currentCatalogResource(t, f.accountFixture, second)).Delivery != domain.InputQueued {
				t.Fatal("automatic dispatch consumed a failed session's next input")
			}
			resume := f.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
			after := f.refresh(t)
			replay, err := sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, resume))
			if err != nil || !replay.Msg.Change.Replayed || f.refresh(t).Revision != after.Revision {
				t.Fatal("Resume receipt created another claim", err)
			}
			f.claim(t)
			c := f.input.Continuation
			if c == nil || c.Intent != domain.ContinueExplicitly || c.Completion != completion || c.Previous.Outcome != domain.ExecutionFailed || f.input.InputID != domain.ID(second.Id) || f.input.ExecutionID == original.ExecutionID || f.input.ThreadRequestID == original.ThreadRequestID || f.input.TurnRequestID == original.TurnRequestID || f.input.AccountID != original.AccountID || f.input.ConnectionID != original.ConnectionID {
				t.Fatal("explicit Resume lost failure, FIFO or fresh execution ownership")
			}
			c.Intent = domain.ContinueAutomatically
			if f.input.Validate() == nil {
				t.Fatal("failed predecessor accepted automatic intent")
			}
			c.Intent = domain.ContinueExplicitly
			state, _ = store.Decode[domain.Session](f.refresh(t))
			retained, _ := json.Marshal(state.InitialExecution)
			if !bytes.Equal(firstSnapshot, retained) || state.PendingInputs != 2 || state.CurrentExecution.ID != f.input.ExecutionID {
				t.Fatal("Resume rewrote the snapshot or consumed unaccepted queue capacity")
			}
			prior := inputBody(t, currentCatalogResource(t, f.accountFixture, f.change.Input))
			if prior.Delivery != domain.InputAccepted || prior.ExecutionID != original.ExecutionID {
				t.Fatal("Resume reclaimed the failed input")
			}
		})
	}
}

func TestClaudeLostFailedReportRecoveryPreservesOriginalFailure(t *testing.T) {
	f, _ := claudeRootOutcomeCompletionFixture(t, domain.ExecutionFailed, func(f *publicationFixture) { f.registerGrant(t) })
	row, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	before, decodeErr := store.Decode[domain.Session](row)
	if err != nil || decodeErr != nil || before.Problem == nil {
		t.Fatal("original failure is unavailable", err, decodeErr)
	}
	problem, _ := json.Marshal(before.Problem)
	replaceRecoveryFixtureWorker(t, f)
	request, change := acceptRecovery(t, f)
	completeRecovery(t, f, change.ExecutionRecoveryJob)
	client := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
	replay, err := client.RecoverSessionExecution(context.Background(), ownerRequest(f.service.Identity, request))
	if err != nil || !replay.Msg.Change.Replayed || replay.Msg.Change.ExecutionRecoveryJob.Id != change.ExecutionRecoveryJob.Id {
		t.Fatal("failed recovery replay created new work", err)
	}
	var session domain.Session
	var original domain.Job
	var completion domain.ExecutionCompletion
	if domain.Decode(replay.Msg.Change.Session.DocumentJson, &session) != nil || domain.Decode(replay.Msg.Change.ExecutionJob.DocumentJson, &original) != nil || domain.Decode(original.Output, &completion) != nil || original.State != domain.JobFailed || completion.Outcome != domain.ExecutionFailed || session.Outcome != domain.ExecutionFailed || session.Recovery != domain.NoRecovery || session.Dispatch != domain.DispatchPaused || session.NextExecutionIntent != "" || !session.Execution.CleanupVerified || session.ActiveExecutionID != "" {
		t.Fatal("lost-report reconciliation erased failure or resumed input")
	}
	retained, _ := json.Marshal(session.Problem)
	if !bytes.Equal(problem, retained) {
		t.Fatal("recovery replaced the original failure classification")
	}
	jobProblem, _ := json.Marshal(original.Problem)
	if !bytes.Equal(problem, jobProblem) {
		t.Fatal("recovery erased the failed execution job diagnostic")
	}
}
