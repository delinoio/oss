package server

import (
	"context"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func claudeTerminalPublicationFixture(t *testing.T, originalInput bool) (*publicationFixture, domain.ExecutionEvent) {
	t.Helper()
	f := newClaudePublicationFixture(t, domain.ExecuteMode)
	return f, publishClaudeTerminalFixture(t, f, originalInput)
}

func publishClaudeTerminalFixture(t *testing.T, f *publicationFixture, originalInput bool) domain.ExecutionEvent {
	t.Helper()
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	sequence := uint64(3)
	if originalInput {
		m := &domain.ExecutionMessageUpdate{ID: domain.NewID(), NativeID: string(f.input.InputID), Role: domain.UserMessage, InputID: f.input.InputID, Text: f.input.Input.Prompt}
		for _, kind := range []domain.ExecutionEventKind{domain.ExecutionMessageStarted, domain.ExecutionMessageCompleted} {
			e := f.event(kind, sequence)
			e.Message = m
			f.publish(t, e)
			sequence++
		}
	}
	result := string(domain.NewID())
	e := f.event(domain.ExecutionClaudeUsageObserved, sequence)
	e.ObservationID, e.ClaudeUsage = domain.NewID(), &domain.ClaudeUsageObservation{Source: domain.ClaudeInputResultUsage, NativeEventID: result, Result: &domain.ClaudeResultUsage{}}
	f.publish(t, e)
	e = f.event(domain.ExecutionTurnFinished, sequence+1)
	e.Outcome = domain.ExecutionSucceeded
	e.ClaudeTerminal = &domain.ClaudeTerminalObservation{InputID: f.input.InputID, ResultNativeID: result, CommandNativeID: string(domain.NewID()), IdleNativeID: string(domain.NewID()), Kind: domain.ClaudeResultSuccess, Reason: domain.ClaudeCompleted, Command: domain.ClaudeCommandCompleted}
	return e
}

func TestClaudeTerminalAndCleanupRemainSeparateWithoutUnprovedContinuation(t *testing.T) {
	for _, scenario := range []string{"ordinary", "permission-changed", "prior-recovery", "unproved-checkpoint"} {
		t.Run(scenario, func(t *testing.T) {
			f, e := claudeTerminalPublicationFixture(t, true)
			if scenario == "permission-changed" || scenario == "prior-recovery" {
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.prior-state", nil, func(tx *store.Tx) (any, error) {
					r, s, err := sessionRecord(tx, f.input.SessionID)
					if err != nil {
						return nil, err
					}
					if scenario == "permission-changed" {
						mode := domain.ClaudePermissionPlan
						s.Execution.ClaudeProgress = &domain.ClaudeProgressState{NativeTurnID: e.NativeTurnID, Permission: &mode, PermissionChanged: true}
					} else {
						s.Recovery, s.Dispatch = domain.NeedsRecovery, domain.DispatchPaused
						s.Problem = domain.Fail(domain.RecoveryRequired, "Original uncertainty.", "Retain it.")
					}
					return tx.Put(domain.SessionKind, r.ID, r.Revision, r.ID, r.ProjectID, s)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			receipt := f.publish(t, e)
			if replay, err := f.call(receipt); err != nil || !replay.Msg.Replayed {
				t.Fatal("terminal receipt not retained", err)
			}
			row, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			s, err := store.Decode[domain.Session](row)
			if err != nil || s.Execution.Outcome != domain.ExecutionSucceeded || s.Execution.CleanupVerified || s.Execution.ClaudeTerminal == nil {
				t.Fatal("terminal claimed cleanup", err)
			}
			completion := f.completion()
			completion.LastSequence = e.Sequence
			if scenario == "unproved-checkpoint" {
				completion.Version, completion.NativeCheckpointDigest = 2, strings.Repeat("ab", 32)
			}
			f.reportCompletion(t, completion)
			row, _ = f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			s, err = store.Decode[domain.Session](row)
			if err != nil || s.Dispatch != map[bool]domain.DispatchState{true: domain.DispatchReady, false: domain.DispatchPaused}[scenario == "unproved-checkpoint"] || s.NextExecutionIntent != map[bool]domain.ExecutionIntent{true: domain.ContinueAutomatically, false: ""}[scenario == "unproved-checkpoint"] {
				t.Fatal("completion enabled unsupported continuation", err)
			}
			if scenario == "unproved-checkpoint" {
				if s.Execution.CleanupVerified || s.Recovery != domain.NeedsRecovery {
					t.Fatal("forged checkpoint acquired authority")
				}
			} else if !s.Execution.CleanupVerified {
				t.Fatal("original cleanup report lost")
			}
			if scenario == "prior-recovery" && (s.Recovery != domain.NeedsRecovery || s.Problem == nil || s.Problem.Message != "Original uncertainty.") {
				t.Fatal("cleanup erased prior recovery")
			}
		})
	}
}

func TestClaudeTerminalRejectsForeignMissingAndUnsettledPublicationAtomically(t *testing.T) {
	for _, scenario := range []string{"missing-input", "missing-terminal", "foreign-result", "foreign-input", "init-reuse", "wrong-outcome", "pending-message", "pending-compaction", "reused-progress", "unconfirmed"} {
		t.Run(scenario, func(t *testing.T) {
			f, e := claudeTerminalPublicationFixture(t, scenario != "missing-input")
			switch scenario {
			case "missing-terminal":
				e.ClaudeTerminal = nil
			case "foreign-result":
				e.ClaudeTerminal.ResultNativeID = string(domain.NewID())
			case "foreign-input":
				e.ClaudeTerminal.InputID = domain.NewID()
			case "init-reuse":
				e.ClaudeTerminal.IdleNativeID = e.NativeTurnID
			case "wrong-outcome":
				e.Outcome = domain.ExecutionFailed
			case "pending-message":
				pending := f.event(domain.ExecutionClaudeMessageObserved, e.Sequence)
				pending.ClaudeMessage = &domain.ClaudeMessageUpdate{ID: domain.NewID(), NativeID: "msg_unfinished", Model: f.input.Configuration.NativeModel, Mutation: domain.ClaudeMessageStart}
				f.publish(t, pending)
				e.Sequence++
			case "reused-progress":
				status := claudeProgressEvent(f, e.Sequence, true)
				status.ClaudeProgress.Observation.NativeEventID = e.ClaudeTerminal.IdleNativeID
				f.publish(t, status)
				e.Sequence++
			case "pending-compaction":
				boundary := claudeProgressEvent(f, e.Sequence, true)
				boundary.ClaudeProgress.Observation.Kind = domain.ClaudeCompactionProgress
				boundary.ClaudeProgress.Observation.Status = nil
				boundary.ClaudeProgress.Observation.Compaction = &domain.ClaudeCompactionBoundary{Trigger: domain.ClaudeAutomaticCompaction, Before: "100", Messages: &domain.ClaudePreservedMessages{Anchor: string(domain.NewID()), IDs: []string{string(domain.NewID())}}}
				f.publish(t, boundary)
				e.Sequence++
			case "unconfirmed":
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.unconfirmed", nil, func(tx *store.Tx) (any, error) {
					r, s, err := sessionRecord(tx, f.input.SessionID)
					if err != nil {
						return nil, err
					}
					s.Execution.UnconfirmedResponses = 1
					return tx.Put(domain.SessionKind, r.ID, r.Revision, r.ID, r.ProjectID, s)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			before, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if _, err := f.call(f.requestEvent(t, e)); err == nil {
				t.Fatal("unproved terminal accepted")
			}
			after, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if before.Revision != after.Revision {
				t.Fatal("rejected terminal changed outcome")
			}
		})
	}
}
