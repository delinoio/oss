package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func claudeRootCompletionFixture(t *testing.T, beforeTerminal ...func(*publicationFixture)) (*publicationFixture, domain.ExecutionCompletion) {
	return claudeRootOutcomeCompletionFixture(t, domain.ExecutionSucceeded, beforeTerminal...)
}

func claudeRootOutcomeCompletionFixture(t *testing.T, outcome domain.ExecutionOutcome, beforeTerminal ...func(*publicationFixture)) (*publicationFixture, domain.ExecutionCompletion) {
	t.Helper()
	f, terminal := claudeTerminalPublicationFixture(t, true)
	for _, setup := range beforeTerminal {
		setup(f)
	}
	terminal = publishClaudeRootContent(t, terminal, f.input.Configuration.NativeModel, func(e domain.ExecutionEvent) { f.publish(t, e) })
	if outcome == domain.ExecutionFailed {
		terminal.ClaudeTerminal.Reason, terminal.ClaudeTerminal.Error, terminal.ClaudeTerminal.Command = domain.ClaudeAPIError, true, domain.ClaudeCommandCancelled
	}
	terminal.Outcome = outcome
	f.publish(t, terminal)
	completion := f.completion()
	completion.Version, completion.NativeCheckpointDigest, completion.LastSequence, completion.Outcome = 2, strings.Repeat("ab", 32), terminal.Sequence, outcome
	return f, completion
}

// Manual-action fixtures use an already selected successful predecessor. Reuse
// the common content publisher without changing settled-failure fixture semantics.
func publishClaudeRootCompletionFixture(t *testing.T, f *publicationFixture, terminal domain.ExecutionEvent) domain.ExecutionCompletion {
	t.Helper()
	terminal = publishClaudeRootContent(t, terminal, f.input.Configuration.NativeModel, func(e domain.ExecutionEvent) { f.publish(t, e) })
	f.publish(t, terminal)
	completion := f.completion()
	completion.Version, completion.NativeCheckpointDigest, completion.LastSequence = 2, strings.Repeat("ab", 32), terminal.Sequence
	return completion
}

func publishClaudeRootContent(t *testing.T, terminal domain.ExecutionEvent, model string, publish func(domain.ExecutionEvent)) domain.ExecutionEvent {
	t.Helper()
	u := domain.ClaudeMessageUpdate{ID: domain.NewID(), NativeID: "msg_original_completed", Model: model}
	index, text := uint32(0), "Original retained content"
	for _, kind := range []domain.ClaudeMessageMutation{domain.ClaudeMessageStart, domain.ClaudeBlockStart, domain.ClaudeBlockAppend, domain.ClaudeBlockComplete, domain.ClaudeBlockStop, domain.ClaudeMessageStop} {
		u.Mutation, u.Index, u.Block, u.Delta = kind, nil, nil, nil
		if kind != domain.ClaudeMessageStart && kind != domain.ClaudeMessageStop {
			u.Index = &index
		}
		if kind == domain.ClaudeBlockStart {
			u.Block = &domain.ClaudeTextBlock{Kind: domain.ClaudeText}
		}
		if kind == domain.ClaudeBlockAppend {
			u.Delta = &text
		}
		if kind == domain.ClaudeBlockComplete {
			u.Block = &domain.ClaudeTextBlock{Kind: domain.ClaudeText, Text: text}
		}
		e := domain.ExecutionEvent{Version: 1, ExecutionID: terminal.ExecutionID, NativeThreadID: terminal.NativeThreadID, NativeTurnID: terminal.NativeTurnID, Kind: domain.ExecutionClaudeMessageObserved, Sequence: terminal.Sequence}
		e.ClaudeMessage = &u
		publish(e)
		terminal.Sequence++
	}
	return terminal
}

func TestClaudeContinuationRequiresOriginalSettledPermissionAndReport(t *testing.T) {
	for _, outcome := range []domain.ExecutionOutcome{domain.ExecutionSucceeded, domain.ExecutionFailed} {
		scenarios := []string{"ordinary", "permission-changed", "permission-mismatch", "interruption", "denial", "stop", "multiple-inputs", "unconfirmed", "prior-recovery"}
		if outcome == domain.ExecutionFailed {
			scenarios = append(scenarios, "background-requested")
		}
		for _, scenario := range scenarios {
			t.Run(string(outcome)+"/"+scenario, func(t *testing.T) {
				f, completion := claudeRootOutcomeCompletionFixture(t, outcome)
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.continuation-boundary", nil, func(tx *store.Tx) (any, error) {
					r, s, err := sessionRecord(tx, f.input.SessionID)
					if err != nil {
						return nil, err
					}
					switch scenario {
					case "background-requested":
						s.Execution.ClaudeTerminal.Reason, s.Execution.ClaudeTerminal.Command = domain.ClaudeBackgroundRequested, domain.ClaudeCommandCompleted
						if s.Execution.ClaudeTerminal.Validate() != nil || !s.Execution.ClaudeTasks.InlineBashHistoryReady() {
							return nil, domain.Fail(domain.Internal, "Invalid background-requested fixture.", "Retain a valid terminal without a tracked task.")
						}
					case "permission-changed":
						s.Execution.ClaudeProgress = &domain.ClaudeProgressState{NativeTurnID: s.Execution.NativeTurnID, PermissionChanged: true}
					case "permission-mismatch":
						mode := domain.ClaudePermissionPlan
						s.Execution.ClaudeProgress = &domain.ClaudeProgressState{NativeTurnID: s.Execution.NativeTurnID, Permission: &mode}
					case "interruption":
						s.Execution.ClaudeInterruption = &domain.ClaudeInterruptionProgress{}
					case "denial":
						s.Execution.ClaudeDenial = &domain.ClaudeDenialCompletion{}
					case "stop":
						s.Execution.ClaudeStop = &domain.ClaudeStopObservation{}
					case "multiple-inputs":
						s.Execution.AcceptedInputs = append(s.Execution.AcceptedInputs, domain.BindExecutionInput(domain.NewID(), "Extra input"))
					case "unconfirmed":
						s.Execution.UnconfirmedResponses = 1
					case "prior-recovery":
						s.Recovery, s.Dispatch = domain.NeedsRecovery, domain.DispatchPaused
					}
					return tx.Put(domain.SessionKind, r.ID, r.Revision, r.ID, r.ProjectID, s)
				})
				if err != nil {
					t.Fatal(err)
				}
				f.reportCompletion(t, completion)
				row, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
				s, err := store.Decode[domain.Session](row)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "ordinary" {
					dispatch, nextIntent, continuationIntent := domain.DispatchReady, domain.ContinueAutomatically, domain.ContinueAutomatically
					if outcome == domain.ExecutionFailed {
						dispatch, nextIntent, continuationIntent = domain.DispatchPaused, "", domain.ContinueExplicitly
					}
					if !s.Execution.CleanupVerified || s.Recovery != domain.NoRecovery || s.Dispatch != dispatch || s.NextExecutionIntent != nextIntent {
						t.Fatal("original v2 completion lost readiness")
					}
					input := f.input
					input.Version, input.ExecutionID, input.InputID, input.ThreadRequestID, input.TurnRequestID = 2, domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
					raw, _ := json.Marshal(f.input)
					input.Continuation = &domain.ExecutionContinuation{HistoryExecutionID: f.input.ExecutionID, HistoryRequestID: domain.NewID(), Previous: *s.Execution, Completion: completion, AssignmentInputDigest: continuationDigest(raw), InputMode: f.input.Input.Mode, PromptDigest: continuationDigest([]byte(f.input.Input.Prompt)), Intent: continuationIntent}
					if input.Validate() != nil || len(executionAPIOperations(input, domain.AnthropicMessages)) != 1 {
						t.Fatal("checked predecessor did not retain exact relay authority", input.Validate())
					}
					input.Input.Mode = domain.PlanMode
					if input.Validate() == nil || len(executionAPIOperations(input, domain.AnthropicMessages)) != 0 {
						t.Fatal("changed permission acquired continuation relay")
					}
				} else {
					dispatch := domain.DispatchPaused
					if outcome == domain.ExecutionSucceeded && scenario != "prior-recovery" {
						dispatch = domain.DispatchReady
					}
					intent := domain.ExecutionIntent("")
					if dispatch == domain.DispatchReady {
						intent = domain.ContinueAutomatically
					}
					if s.Dispatch != dispatch || s.Recovery != domain.NeedsRecovery || s.NextExecutionIntent != intent {
						t.Fatal("unproved boundary granted FIFO")
					}
					if scenario != "prior-recovery" && s.Execution.CleanupVerified {
						t.Fatal("unsupported checkpoint report was accepted")
					}
				}
			})
		}
	}
}
