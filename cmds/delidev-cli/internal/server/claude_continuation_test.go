package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func claudeRootCompletionFixture(t *testing.T) (*publicationFixture, domain.ExecutionCompletion) {
	t.Helper()
	f, terminal := claudeTerminalPublicationFixture(t, true)
	u := domain.ClaudeMessageUpdate{ID: domain.NewID(), NativeID: "msg_original_completed", Model: f.input.Configuration.NativeModel}
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
		e := f.event(domain.ExecutionClaudeMessageObserved, terminal.Sequence)
		e.ClaudeMessage = &u
		f.publish(t, e)
		terminal.Sequence++
	}
	f.publish(t, terminal)
	completion := f.completion()
	completion.Version, completion.NativeCheckpointDigest, completion.LastSequence = 2, strings.Repeat("ab", 32), terminal.Sequence
	return f, completion
}

func TestClaudeContinuationRequiresOriginalSuccessPermissionAndReport(t *testing.T) {
	for _, scenario := range []string{"ordinary", "permission-changed", "permission-mismatch", "interruption", "denial", "stop", "multiple-inputs", "unconfirmed", "prior-recovery"} {
		t.Run(scenario, func(t *testing.T) {
			f, completion := claudeRootCompletionFixture(t)
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.continuation-boundary", nil, func(tx *store.Tx) (any, error) {
				r, s, err := sessionRecord(tx, f.input.SessionID)
				if err != nil {
					return nil, err
				}
				switch scenario {
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
				if !s.Execution.CleanupVerified || s.Recovery != domain.NoRecovery || s.Dispatch != domain.DispatchReady || s.NextExecutionIntent != domain.ContinueAutomatically {
					t.Fatal("original v2 completion lost readiness")
				}
				input := f.input
				input.Version, input.ExecutionID, input.InputID, input.ThreadRequestID, input.TurnRequestID = 2, domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
				raw, _ := json.Marshal(f.input)
				input.Continuation = &domain.ExecutionContinuation{HistoryExecutionID: f.input.ExecutionID, HistoryRequestID: domain.NewID(), Previous: *s.Execution, Completion: completion, AssignmentInputDigest: continuationDigest(raw), InputMode: f.input.Input.Mode, PromptDigest: continuationDigest([]byte(f.input.Input.Prompt)), Intent: domain.ContinueAutomatically}
				if input.Validate() != nil || len(executionAPIOperations(input, domain.AnthropicMessages)) != 1 {
					t.Fatal("checked predecessor did not retain exact relay authority", input.Validate())
				}
				input.Input.Mode = domain.PlanMode
				if input.Validate() == nil || len(executionAPIOperations(input, domain.AnthropicMessages)) != 0 {
					t.Fatal("changed permission acquired continuation relay")
				}
			} else {
				if s.Dispatch != domain.DispatchPaused || s.Recovery != domain.NeedsRecovery || s.NextExecutionIntent != "" {
					t.Fatal("unproved boundary granted FIFO")
				}
				if scenario != "prior-recovery" && s.Execution.CleanupVerified {
					t.Fatal("unsupported checkpoint report was accepted")
				}
			}
		})
	}
}
